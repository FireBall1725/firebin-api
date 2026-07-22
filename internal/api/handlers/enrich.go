// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/firelabsca/firebin-api/internal/api/respond"
	"github.com/firelabsca/firebin-api/internal/providers/nexar"
)

// Enrich looks up an MPN via the configured parts-data provider (Nexar/Octopart)
// and returns normalized part data to prefill the scan create-flow.
func (h *Handler) Enrich(w http.ResponseWriter, r *http.Request) {
	mpn := strings.TrimSpace(r.URL.Query().Get("mpn"))
	if mpn == "" {
		respond.Error(w, http.StatusBadRequest, "mpn query param is required")
		return
	}
	if !h.Enricher.Configured(r.Context()) {
		respond.Error(w, http.StatusServiceUnavailable, "enrichment not configured — add Nexar credentials in settings")
		return
	}

	part, err := h.Enricher.Enrich(r.Context(), mpn)
	if errors.Is(err, nexar.ErrNotConfigured) {
		respond.Error(w, http.StatusServiceUnavailable, "enrichment not configured")
		return
	}
	if err != nil {
		respond.Error(w, http.StatusBadGateway, "enrichment lookup failed: "+err.Error())
		return
	}
	if part == nil {
		respond.JSON(w, http.StatusOK, map[string]any{"found": false})
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"found": true, "part": part})
}

// EnrichmentStatus reports whether enrichment is configured (for the UI to show
// the right affordance without exposing secrets).
func (h *Handler) EnrichmentStatus(w http.ResponseWriter, r *http.Request) {
	respond.JSON(w, http.StatusOK, map[string]any{
		"configured": h.Enricher.Configured(r.Context()),
		"provider":   "nexar",
	})
}
