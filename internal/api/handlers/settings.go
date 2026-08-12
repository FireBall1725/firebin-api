// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/firelabsca/firebin-api/internal/api/respond"
)

// providerSettings is the non-secret view of one enrichment provider's config.
type providerSettings struct {
	Provider   string `json:"provider"`
	Label      string `json:"label"`
	Configured bool   `json:"configured"`
	Enabled    bool   `json:"enabled"`
	ClientID   string `json:"client_id"` // masked
	SecretSet  bool   `json:"secret_set"`
	FromEnv    bool   `json:"from_env"`
	Scope      string `json:"scope,omitempty"` // nexar only
	// KeyOnly marks a provider that authenticates with a single API key and has
	// no client id, so the client renders one field instead of two.
	KeyOnly bool `json:"key_only,omitempty"` // mouser only
}

// enricherEnabled reports whether a provider participates in the default lookup
// chain. Default on; the user can disable it in settings.
func (h *Handler) enricherEnabled(ctx context.Context, name string) bool {
	v, _ := h.Settings.Get(ctx, name+".enabled")
	return v != "false"
}

// GetEnrichmentSettings reports each provider's configuration without exposing
// secrets (only whether they are set, and a masked client-id hint).
// @Summary     Get enrichment settings
// @Description Report each enrichment provider's configuration without exposing secrets.
// @Tags        settings
// @Security    BearerAuth
// @Produce     json
// @Success     200  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Router      /settings/enrichment  [get]
func (h *Handler) GetEnrichmentSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := make([]providerSettings, 0, len(h.Enrichers))
	for _, e := range h.Enrichers {
		out = append(out, h.providerSettings(ctx, e.Name(), e.Label(), e.Configured(ctx)))
	}
	respond.JSON(w, http.StatusOK, map[string]any{"providers": out, "currency": h.enrichmentCurrency(ctx)})
}

// enrichmentCurrency is the preferred pricing currency (Digi-Key locale currency and
// the default for manual price breaks). Stored in settings, falling back to config.
func (h *Handler) enrichmentCurrency(ctx context.Context) string {
	cur, _ := h.Settings.Get(ctx, "enrichment.currency")
	if cur == "" {
		cur = h.Cfg.DigiKeyCurrency
	}
	if cur == "" {
		cur = "USD"
	}
	return cur
}

func (h *Handler) providerSettings(ctx context.Context, name, label string, configured bool) providerSettings {
	clientID, _ := h.Settings.Get(ctx, name+".client_id")
	secret, _ := h.Settings.Get(ctx, name+".client_secret")

	ps := providerSettings{
		Provider:   name,
		Label:      label,
		Configured: configured,
		Enabled:    h.enricherEnabled(ctx, name),
		ClientID:   maskID(clientID),
	}
	switch name {
	case "nexar":
		ps.SecretSet = secret != "" || h.Cfg.NexarClientSecret != ""
		ps.FromEnv = clientID == "" && h.Cfg.NexarClientID != ""
		scope, _ := h.Settings.Get(ctx, "nexar.scope")
		if scope == "" {
			scope = h.Cfg.NexarScope
		}
		ps.Scope = scope
	case "digikey":
		ps.SecretSet = secret != "" || h.Cfg.DigiKeyClientSecret != ""
		ps.FromEnv = clientID == "" && h.Cfg.DigiKeyClientID != ""
	case "mouser":
		// Mouser authenticates with one API key and has no client id. It is
		// stored in the secret slot so the existing settings shape carries it;
		// KeyOnly tells the client to show a single "API key" field.
		ps.SecretSet = secret != "" || h.Cfg.MouserAPIKey != ""
		ps.FromEnv = secret == "" && h.Cfg.MouserAPIKey != ""
		ps.KeyOnly = true
	}
	return ps
}

type enrichmentSettingsRequest struct {
	Provider     string  `json:"provider"`
	ClientID     *string `json:"client_id"`
	ClientSecret *string `json:"client_secret"`
	Scope        *string `json:"scope"`
	Currency     *string `json:"currency"` // global preferred currency (provider-independent)
	Enabled      *bool   `json:"enabled"`  // toggle a provider in/out of the default chain
}

// UpdateEnrichmentSettings stores one provider's credentials. Only non-nil
// fields are written; an empty client_secret is ignored so the UI can save
// other fields without re-entering the secret.
// @Summary     Update enrichment settings
// @Description Store one provider's credentials and enrichment preferences.
// @Tags        settings
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     200      {object}  map[string]interface{}
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Router      /settings/enrichment  [put]
func (h *Handler) UpdateEnrichmentSettings(w http.ResponseWriter, r *http.Request) {
	var req enrichmentSettingsRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	ctx := r.Context()
	// Currency is global; it can be saved on its own (no provider needed).
	if req.Currency != nil {
		_ = h.Settings.Set(ctx, "enrichment.currency", strings.ToUpper(strings.TrimSpace(*req.Currency)))
	}
	name := strings.TrimSpace(req.Provider)
	if name == "" {
		h.GetEnrichmentSettings(w, r)
		return
	}
	if _, ok := h.EnricherBy[name]; !ok {
		respond.Error(w, http.StatusBadRequest, "unknown provider")
		return
	}
	if req.ClientID != nil {
		_ = h.Settings.Set(ctx, name+".client_id", strings.TrimSpace(*req.ClientID))
	}
	if req.ClientSecret != nil && strings.TrimSpace(*req.ClientSecret) != "" {
		_ = h.Settings.Set(ctx, name+".client_secret", strings.TrimSpace(*req.ClientSecret))
	}
	if req.Scope != nil && name == "nexar" {
		_ = h.Settings.Set(ctx, "nexar.scope", strings.TrimSpace(*req.Scope))
	}
	if req.Enabled != nil {
		v := "true"
		if !*req.Enabled {
			v = "false"
		}
		_ = h.Settings.Set(ctx, name+".enabled", v)
	}
	h.GetEnrichmentSettings(w, r)
}

type testEnrichmentRequest struct {
	Provider string `json:"provider"`
}

// TestEnrichment validates one provider's credentials by minting a token. This
// does NOT spend a metered lookup — it only checks auth.
// @Summary     Test enrichment provider
// @Description Validate one provider's credentials by minting a token without spending a lookup.
// @Tags        settings
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     200      {object}  map[string]interface{}
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Router      /settings/enrichment/test  [post]
func (h *Handler) TestEnrichment(w http.ResponseWriter, r *http.Request) {
	var req testEnrichmentRequest
	// Body is optional; default to the first provider if omitted.
	_ = respond.DecodeAllowEmpty(w, r, &req)
	name := strings.TrimSpace(req.Provider)

	e, ok := h.EnricherBy[name]
	if !ok {
		if len(h.Enrichers) == 0 {
			respond.Error(w, http.StatusServiceUnavailable, "no providers")
			return
		}
		e = h.Enrichers[0]
	}
	if !e.Configured(r.Context()) {
		respond.Error(w, http.StatusServiceUnavailable, "no credentials set")
		return
	}
	if err := e.Ping(r.Context()); err != nil {
		respond.Error(w, http.StatusBadGateway, err.Error())
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"ok": true, "provider": e.Name()})
}

// deleteEmptyLotsEnabled reports whether zero-quantity, non-barcoded lots may be
// purged. Off by default: a lot at zero stock is not a lot the user wants gone
// (they may reorder into it), so cleanup is opt-in and never automatic.
func (h *Handler) deleteEmptyLotsEnabled(ctx context.Context) bool {
	v, _ := h.Settings.Get(ctx, "stock.delete_empty_lots")
	return v == "true"
}

// GetStockSettings returns the empty-lot cleanup toggle and how many lots the
// cleanup would remove right now.
// @Summary     Get stock settings
// @Description Return the empty-lot cleanup toggle and the current count of purgeable lots.
// @Tags        settings
// @Security    BearerAuth
// @Produce     json
// @Success     200  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Router      /settings/stock  [get]
func (h *Handler) GetStockSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	n, err := h.Stock.CountEmptyLots(ctx)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"delete_empty_lots": h.deleteEmptyLotsEnabled(ctx),
		"empty_lot_count":   n,
	})
}

type stockSettingsRequest struct {
	DeleteEmptyLots *bool `json:"delete_empty_lots"`
}

// UpdateStockSettings stores the empty-lot cleanup toggle.
// @Summary     Update stock settings
// @Description Store the empty-lot cleanup toggle.
// @Tags        settings
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     200      {object}  map[string]interface{}
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Router      /settings/stock  [put]
func (h *Handler) UpdateStockSettings(w http.ResponseWriter, r *http.Request) {
	var req stockSettingsRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	if req.DeleteEmptyLots != nil {
		v := "false"
		if *req.DeleteEmptyLots {
			v = "true"
		}
		_ = h.Settings.Set(r.Context(), "stock.delete_empty_lots", v)
	}
	h.GetStockSettings(w, r)
}

// GetDatasheetSettings returns the datasheet storage toggles plus the library
// totals, so the settings card can show what the choice is costing on disk.
// @Summary     Get datasheet settings
// @Description Return the auto-mirror and text-extraction toggles, the upload size cap, and library totals.
// @Tags        settings
// @Security    BearerAuth
// @Produce     json
// @Success     200  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Router      /settings/datasheets  [get]
func (h *Handler) GetDatasheetSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	stats, err := h.Datasheets.Stats(ctx)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	maxBytes := h.maxDatasheetBytes(ctx)
	extract := true // default on; only an explicit "false" turns it off
	if v, _ := h.Settings.Get(ctx, "datasheets.extract_text"); v == "false" {
		extract = false
	}
	autoMirror := false
	if v, _ := h.Settings.Get(ctx, "datasheets.auto_mirror"); v == "true" {
		autoMirror = true
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"auto_mirror":       autoMirror,
		"extract_text":      extract,
		"max_bytes":         maxBytes,
		"storage_path":      h.DatasheetFiles.Root(),
		"count":             stats.Count,
		"total_bytes":       stats.TotalBytes,
		"unlinked":          stats.Unlinked,
		"mirror_candidates": stats.MirrorCandidates,
	})
}

type datasheetSettingsRequest struct {
	AutoMirror  *bool  `json:"auto_mirror"`
	ExtractText *bool  `json:"extract_text"`
	MaxBytes    *int64 `json:"max_bytes"`
}

// UpdateDatasheetSettings stores the datasheet storage toggles.
// @Summary     Update datasheet settings
// @Description Store the auto-mirror and text-extraction toggles and the upload size cap.
// @Tags        settings
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     200      {object}  map[string]interface{}
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Router      /settings/datasheets  [put]
func (h *Handler) UpdateDatasheetSettings(w http.ResponseWriter, r *http.Request) {
	var req datasheetSettingsRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	ctx := r.Context()
	if req.AutoMirror != nil {
		_ = h.Settings.Set(ctx, "datasheets.auto_mirror", boolSetting(*req.AutoMirror))
	}
	if req.ExtractText != nil {
		_ = h.Settings.Set(ctx, "datasheets.extract_text", boolSetting(*req.ExtractText))
	}
	if req.MaxBytes != nil {
		if *req.MaxBytes <= 0 {
			respond.Error(w, http.StatusBadRequest, "max_bytes must be greater than zero")
			return
		}
		_ = h.Settings.Set(ctx, "datasheets.max_bytes", strconv.FormatInt(*req.MaxBytes, 10))
	}
	h.GetDatasheetSettings(w, r)
}

func boolSetting(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// CleanupEmptyLots purges zero-quantity, non-barcoded lots, but only when the
// admin has turned the toggle on. With it off the request is a no-op, so the
// default install never deletes stock history.
// @Summary     Cleanup empty lots
// @Description Purge zero-quantity, non-barcoded lots when the cleanup toggle is on.
// @Tags        settings
// @Security    BearerAuth
// @Produce     json
// @Success     200  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Router      /stock/cleanup-empty  [post]
func (h *Handler) CleanupEmptyLots(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !h.deleteEmptyLotsEnabled(ctx) {
		respond.JSON(w, http.StatusOK, map[string]any{"enabled": false, "deleted": 0})
		return
	}
	n, err := h.Stock.DeleteEmptyLots(ctx)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"enabled": true, "deleted": n})
}

func maskID(id string) string {
	if id == "" {
		return ""
	}
	if len(id) <= 6 {
		return "••••"
	}
	return id[:4] + "…" + id[len(id)-2:]
}
