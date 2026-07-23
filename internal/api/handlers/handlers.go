// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package handlers holds the HTTP handlers for the FireBin API.
package handlers

import (
	"context"

	"github.com/firelabsca/firebin-api/internal/auth"
	"github.com/firelabsca/firebin-api/internal/config"
	"github.com/firelabsca/firebin-api/internal/events"
	"github.com/firelabsca/firebin-api/internal/providers/nexar"
	"github.com/firelabsca/firebin-api/internal/repository"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Handler bundles the dependencies shared by every endpoint.
type Handler struct {
	Cfg    *config.Config
	JWT    *auth.JWTService
	Users  *repository.UserRepo
	Tokens *repository.TokenRepo

	Categories *repository.CategoryRepo
	Parts      *repository.PartRepo
	Projects   *repository.ProjectRepo
	Locations  *repository.LocationRepo
	Stock      *repository.StockRepo
	Stats      *repository.StatsRepo
	Catalog     *repository.CatalogRepo
	Settings    *repository.SettingsRepo
	EnrichCache *repository.EnrichmentCacheRepo
	Bus         *events.Broker
	Enricher    *nexar.Provider
}

// New builds the handler and all its repositories from the connection pool.
func New(cfg *config.Config, pool *pgxpool.Pool, jwt *auth.JWTService) *Handler {
	settings := repository.NewSettingsRepo(pool)

	// Enrichment credentials resolve fresh per call: DB settings first (entered
	// in the UI), then env fallback — so the user can add keys without a restart.
	creds := func(ctx context.Context) nexar.Credentials {
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

	return &Handler{
		Cfg:        cfg,
		JWT:        jwt,
		Users:      repository.NewUserRepo(pool),
		Tokens:     repository.NewTokenRepo(pool),
		Categories: repository.NewCategoryRepo(pool),
		Parts:      repository.NewPartRepo(pool),
		Projects:   repository.NewProjectRepo(pool),
		Locations:  repository.NewLocationRepo(pool),
		Stock:      repository.NewStockRepo(pool),
		Stats:      repository.NewStatsRepo(pool),
		Catalog:     repository.NewCatalogRepo(pool),
		Settings:    settings,
		EnrichCache: repository.NewEnrichmentCacheRepo(pool),
		Bus:         events.NewBroker(),
		Enricher:    nexar.New(creds),
	}
}
