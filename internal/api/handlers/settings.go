// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"context"
	"net/http"
	"strings"

	"github.com/firelabsca/firebin-api/internal/api/respond"
)

// GetEnrichmentSettings reports the enrichment configuration without exposing
// secrets (only whether they are set, and a masked hint).
func (h *Handler) GetEnrichmentSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	clientID, _ := h.Settings.Get(ctx, "nexar.client_id")
	scope, _ := h.Settings.Get(ctx, "nexar.scope")
	if scope == "" {
		scope = h.Cfg.NexarScope
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"provider":    "nexar",
		"configured":  h.Enricher.Configured(ctx),
		"client_id":   maskID(clientID),
		"secret_set":  secretSet(ctx, h),
		"scope":       scope,
		"from_env":    clientID == "" && h.Cfg.NexarClientID != "",
	})
}

type enrichmentSettingsRequest struct {
	ClientID     *string `json:"client_id"`
	ClientSecret *string `json:"client_secret"`
	Scope        *string `json:"scope"`
}

// UpdateEnrichmentSettings stores Nexar credentials. Only non-nil fields are
// written; an empty client_secret is ignored so the UI can save other fields
// without re-entering the secret.
func (h *Handler) UpdateEnrichmentSettings(w http.ResponseWriter, r *http.Request) {
	var req enrichmentSettingsRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	ctx := r.Context()
	if req.ClientID != nil {
		_ = h.Settings.Set(ctx, "nexar.client_id", strings.TrimSpace(*req.ClientID))
	}
	if req.ClientSecret != nil && strings.TrimSpace(*req.ClientSecret) != "" {
		_ = h.Settings.Set(ctx, "nexar.client_secret", strings.TrimSpace(*req.ClientSecret))
	}
	if req.Scope != nil {
		_ = h.Settings.Set(ctx, "nexar.scope", strings.TrimSpace(*req.Scope))
	}
	h.GetEnrichmentSettings(w, r)
}

// TestEnrichment validates the credentials by minting a token. This does NOT
// spend a query against the Nexar quota — it only checks auth.
func (h *Handler) TestEnrichment(w http.ResponseWriter, r *http.Request) {
	if !h.Enricher.Configured(r.Context()) {
		respond.Error(w, http.StatusServiceUnavailable, "no credentials set")
		return
	}
	if err := h.Enricher.Ping(r.Context()); err != nil {
		respond.Error(w, http.StatusBadGateway, err.Error())
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"ok": true})
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

func secretSet(ctx context.Context, h *Handler) bool {
	v, _ := h.Settings.Get(ctx, "nexar.client_secret")
	return v != "" || h.Cfg.NexarClientSecret != ""
}
