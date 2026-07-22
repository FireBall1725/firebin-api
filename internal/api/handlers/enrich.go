// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/firelabsca/firebin-api/internal/api/respond"
	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/firelabsca/firebin-api/internal/providers/nexar"
)

// junkParams are noisy attributes (customs/compliance codes) not worth storing
// as part parameters.
var junkParams = map[string]bool{
	"schedule b":              true,
	"htsus code":              true,
	"hts":                     true,
	"eccn":                    true,
	"harmonized tariff code":  true,
	"package description":     true,
	"factory lead time":       true,
}

// cleanParameters drops empty, pathologically long, and junk parameters so the
// part gets a tidy spec sheet. Applied to every enrichment result (fresh or
// cached) before it reaches the client.
func cleanParameters(p *models.EnrichedPart) {
	if p == nil {
		return
	}
	out := p.Parameters[:0]
	for _, param := range p.Parameters {
		v := strings.TrimSpace(param.Value)
		if v == "" || len(v) > 60 {
			continue
		}
		if junkParams[strings.ToLower(strings.TrimSpace(param.Name))] {
			continue
		}
		out = append(out, param)
	}
	p.Parameters = out
}

// Enrich looks up an MPN via the configured parts-data provider (Nexar/Octopart)
// and returns normalized part data to prefill the scan create-flow.
func (h *Handler) Enrich(w http.ResponseWriter, r *http.Request) {
	mpn := strings.TrimSpace(r.URL.Query().Get("mpn"))
	if mpn == "" {
		respond.Error(w, http.StatusBadRequest, "mpn query param is required")
		return
	}
	// Serve from cache first so re-scans/retries never spend a provider query.
	if cached, ok, _ := h.EnrichCache.Get(r.Context(), mpn); ok {
		cleanParameters(cached)
		respond.JSON(w, http.StatusOK, map[string]any{"found": true, "part": cached, "cached": true})
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
	_ = h.EnrichCache.Set(r.Context(), mpn, part) // cache the full hit
	cleanParameters(part)
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
