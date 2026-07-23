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

// acronyms that Nexar title-cases wrong ("Dc" → "DC").
var acronyms = map[string]bool{
	"dc": true, "ac": true, "ic": true, "led": true, "usb": true, "rf": true,
	"smd": true, "smt": true, "lcd": true, "pcb": true, "emi": true, "esd": true,
	"mlcc": true, "ir": true, "uv": true, "io": true, "pmic": true, "mcu": true,
}

func fixAcronyms(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		if acronyms[strings.ToLower(w)] {
			words[i] = strings.ToUpper(w)
		}
	}
	return strings.Join(words, " ")
}

func singularType(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "ies") {
		return strings.TrimSuffix(s, "ies") + "y"
	}
	if strings.HasSuffix(s, "s") && !strings.HasSuffix(s, "ss") {
		return strings.TrimSuffix(s, "s")
	}
	return s
}

// deriveName produces a clean, human part name from enrichment data:
//   - resistors/capacitors/inductors → "<value> Resistor" ("22 Ω Resistor")
//   - everything else → the last clause of the description, which is where the
//     provider puts the type name ("…, Dc Power Jack Connector")
//   - fall back to a cleaned category, then the raw description.
// It re-derives from the cached fields so naming changes apply with no query.
func deriveName(p *models.EnrichedPart) string {
	var val, base string
	catLow := strings.ToLower(p.Category)
	switch {
	case strings.Contains(catLow, "resistor"):
		base = "Resistor"
	case strings.Contains(catLow, "capacitor"):
		base = "Capacitor"
	case strings.Contains(catLow, "inductor"):
		base = "Inductor"
	case strings.Contains(catLow, "ferrite"):
		base = "Ferrite Bead"
	}
	for _, param := range p.Parameters {
		switch strings.ToLower(param.Name) {
		case "resistance", "capacitance", "inductance":
			if val == "" {
				val = strings.TrimSpace(param.Value)
			}
		}
	}
	if val != "" && base != "" {
		return val + " " + base
	}
	// Non-value part: the provider's description ends with the type name.
	if i := strings.LastIndex(p.Description, ","); i >= 0 {
		if t := strings.TrimSpace(p.Description[i+1:]); t != "" {
			return fixAcronyms(t)
		}
	}
	if p.Category != "" {
		return fixAcronyms(singularType(p.Category))
	}
	return p.Description
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
		cached.Name = deriveName(cached)
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
	part.Name = deriveName(part)
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
