// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package handlers holds the HTTP handlers for the FireBin API.
package handlers

import (
	"context"
	"log/slog"

	"github.com/firelabsca/firebin-api/internal/ai"
	"github.com/firelabsca/firebin-api/internal/auth"
	"github.com/firelabsca/firebin-api/internal/config"
	"github.com/firelabsca/firebin-api/internal/datasheets"
	"github.com/firelabsca/firebin-api/internal/events"
	"github.com/firelabsca/firebin-api/internal/jobs"
	"github.com/firelabsca/firebin-api/internal/kicad/httplib"
	"github.com/firelabsca/firebin-api/internal/providers"
	"github.com/firelabsca/firebin-api/internal/providers/digikey"
	"github.com/firelabsca/firebin-api/internal/providers/mouser"
	"github.com/firelabsca/firebin-api/internal/providers/nexar"
	"github.com/firelabsca/firebin-api/internal/repository"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

// Handler bundles the dependencies shared by every endpoint.
type Handler struct {
	Cfg    *config.Config
	JWT    *auth.JWTService
	Users  *repository.UserRepo
	Tokens *repository.TokenRepo

	Categories     *repository.CategoryRepo
	Parts          *repository.PartRepo
	Projects       *repository.ProjectRepo
	Locations      *repository.LocationRepo
	Stock          *repository.StockRepo
	Stats          *repository.StatsRepo
	Catalog        *repository.CatalogRepo
	Settings       *repository.SettingsRepo
	EnrichCache    *repository.EnrichmentCacheRepo
	LabelMedia     *repository.LabelMediaRepo
	LabelTemplates *repository.LabelTemplateRepo
	Backup         *repository.BackupRepo
	KicadLib       *repository.KicadLibraryRepo
	Bus            *events.Broker

	// Datasheets is the metadata; DatasheetFiles is the content-addressed store
	// on disk under Cfg.AttachmentStoragePath. Split because the PDFs are too
	// large to sit in Postgres without breaking the JSON backup path.
	Datasheets     *repository.DatasheetRepo
	DatasheetFiles *datasheets.Store

	// The KiCad HTTP library: per-workstation credentials, the catalogue snapshot
	// it is served from, and the handler set. The cache is exposed so main can
	// warm it and run its refresh ticker.
	KicadHTTPTokens *repository.KicadLibraryTokenRepo
	KicadHTTPCache  *httplib.Cache
	KicadHTTP       *httplib.Server

	// Enrichers are the MPN-lookup providers, tried in order (Digi-Key first,
	// then Nexar). EnricherBy indexes them by provider id for settings/test.
	Enrichers  []providers.Enricher
	EnricherBy map[string]providers.Enricher

	// Jobs is the background job service (River). Started and stopped by main.
	Jobs *jobs.Service

	// Assistant stores conversations, their messages, and what each turn cost.
	Assistant *repository.AssistantRepo

	// AI owns the chat providers and their configuration. Nil is a valid state
	// and means the assistant is not available on this instance; every AI
	// handler checks for it rather than assuming.
	AI *ai.Service
}

// New builds the handler and all its repositories from the connection pool, and
// wires the background job service (workers registered, not yet started).
func New(cfg *config.Config, pool *pgxpool.Pool, jwt *auth.JWTService) (*Handler, error) {
	settings := repository.NewSettingsRepo(pool)

	// Chat providers. Registration order is the order the settings page lists
	// them: the two hosted ones first, then the two that run on your own
	// hardware. None is active until an admin picks one.
	aiRegistry := ai.NewRegistry()
	aiRegistry.Register(ai.NewAnthropicProvider())
	aiRegistry.Register(ai.NewOpenAIProvider())
	aiRegistry.Register(ai.NewOllamaProvider())
	aiRegistry.Register(ai.NewOsaurusProvider())
	aiService := ai.NewService(aiRegistry, settings, repository.ErrNotFound)

	// Enrichment credentials resolve fresh per call: DB settings first (entered
	// in the UI), then env fallback — so the user can add keys without a restart.
	nexarCreds := func(ctx context.Context) nexar.Credentials {
		id, _ := settings.Get(ctx, "nexar.client_id")
		secret, _ := settings.Get(ctx, "nexar.client_secret")
		scope, _ := settings.Get(ctx, "nexar.scope")
		if id == "" {
			id = cfg.NexarClientID
		}
		if secret == "" {
			secret = cfg.NexarClientSecret
		}
		if scope == "" {
			scope = cfg.NexarScope
		}
		return nexar.Credentials{ClientID: id, ClientSecret: secret, Scope: scope}
	}

	digikeyCreds := func(ctx context.Context) digikey.Credentials {
		id, _ := settings.Get(ctx, "digikey.client_id")
		secret, _ := settings.Get(ctx, "digikey.client_secret")
		if id == "" {
			id = cfg.DigiKeyClientID
		}
		if secret == "" {
			secret = cfg.DigiKeyClientSecret
		}
		currency, _ := settings.Get(ctx, "enrichment.currency")
		if currency == "" {
			currency = cfg.DigiKeyCurrency
		}
		return digikey.Credentials{
			ClientID:     id,
			ClientSecret: secret,
			BaseURL:      cfg.DigiKeyBaseURL,
			Site:         cfg.DigiKeySite,
			Language:     cfg.DigiKeyLanguage,
			Currency:     currency,
		}
	}

	mouserCreds := func(ctx context.Context) mouser.Credentials {
		// Mouser has no client id, so the key lives in the secret slot the
		// settings UI already writes.
		key, _ := settings.Get(ctx, "mouser.client_secret")
		if key == "" {
			key = cfg.MouserAPIKey
		}
		currency, _ := settings.Get(ctx, "enrichment.currency")
		if currency == "" {
			currency = cfg.DigiKeyCurrency
		}
		return mouser.Credentials{APIKey: key, BaseURL: cfg.MouserBaseURL, Currency: currency}
	}

	// Order matters: Digi-Key first (free, own catalogue, richest parametrics),
	// then Mouser (free but capped at 1000 lookups a day), Nexar as fallback.
	enrichers := []providers.Enricher{
		digikey.New(digikeyCreds),
		mouser.New(mouserCreds),
		nexar.New(nexarCreds),
	}
	enricherBy := make(map[string]providers.Enricher, len(enrichers))
	for _, e := range enrichers {
		enricherBy[e.Name()] = e
	}

	h := &Handler{
		Cfg:            cfg,
		JWT:            jwt,
		Users:          repository.NewUserRepo(pool),
		Tokens:         repository.NewTokenRepo(pool),
		Categories:     repository.NewCategoryRepo(pool),
		Parts:          repository.NewPartRepo(pool),
		Projects:       repository.NewProjectRepo(pool),
		Locations:      repository.NewLocationRepo(pool),
		Stock:          repository.NewStockRepo(pool),
		Stats:          repository.NewStatsRepo(pool),
		Catalog:        repository.NewCatalogRepo(pool),
		Settings:       settings,
		EnrichCache:    repository.NewEnrichmentCacheRepo(pool),
		LabelMedia:     repository.NewLabelMediaRepo(pool),
		LabelTemplates: repository.NewLabelTemplateRepo(pool),
		Backup:         repository.NewBackupRepo(pool),
		KicadLib:       repository.NewKicadLibraryRepo(pool),
		Bus:            events.NewBroker(),
		Datasheets:     repository.NewDatasheetRepo(pool),
		DatasheetFiles: datasheets.New(cfg.AttachmentStoragePath),
		Enrichers:      enrichers,
		EnricherBy:     enricherBy,
		AI:             aiService,
		Assistant:      repository.NewAssistantRepo(pool),

		KicadHTTPTokens: repository.NewKicadLibraryTokenRepo(pool),
	}

	// The KiCad library server. The snapshot is built off the request path: a
	// rebuild is one detail composition per part, and the HTTP server's 15s
	// WriteTimeout would truncate a response that tried to do it inline.
	//
	// Built unconditionally even though the feature ships disabled, because the
	// toggle is read per request and flipping it in Settings has to work without a
	// restart. The snapshot is therefore warmed and refreshed on every instance,
	// enabled or not, which costs one catalogue read every five minutes and buys a
	// library that is already populated the moment someone turns it on. Nothing is
	// served until the middleware sees it enabled.
	kicadLog := slog.Default().With("component", "kicad-library")
	h.KicadHTTPCache = httplib.NewCache(
		kicadSource{categories: h.Categories, parts: h.Parts, catalog: h.Catalog},
		kicadUnmappedMarker,
		kicadSnapshotTTL,
		kicadLog,
	)
	h.KicadHTTP = httplib.NewServer(h.KicadHTTPCache, kicadLog)

	// Wire the job service: register workers (which reference h), then build the
	// River client. Not started here; main owns the lifecycle.
	store := jobs.NewStore(pool)
	deps := jobs.NewDeps(store, h.Bus)
	workers := river.NewWorkers()
	river.AddWorker(workers, &bulkEnrichWorker{h: h, deps: deps})
	river.AddWorker(workers, &datasheetMirrorWorker{h: h, deps: deps})
	river.AddWorker(workers, &datasheetExtractWorker{h: h, deps: deps})
	svc, err := jobs.New(pool, store, deps, workers)
	if err != nil {
		return nil, err
	}
	h.Jobs = svc
	return h, nil
}
