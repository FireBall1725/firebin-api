// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"unicode"

	"github.com/firelabsca/firebin-api/internal/api/respond"
	"github.com/firelabsca/firebin-api/internal/models"
)

// junkParams are noisy attributes (customs/compliance codes) not worth storing
// as part parameters.
var junkParams = map[string]bool{
	"schedule b":             true,
	"htsus code":             true,
	"hts":                    true,
	"eccn":                   true,
	"harmonized tariff code": true,
	"package description":    true,
	"factory lead time":      true,
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
	low := strings.ToLower(s)
	switch {
	case strings.HasSuffix(low, "ies"): // Batteries → Battery
		return s[:len(s)-3] + "y"
	case strings.HasSuffix(low, "ches"), strings.HasSuffix(low, "shes"),
		strings.HasSuffix(low, "sses"), strings.HasSuffix(low, "xes"),
		strings.HasSuffix(low, "zes"): // Switches → Switch, Boxes → Box
		return s[:len(s)-2]
	case strings.HasSuffix(low, "s") && !strings.HasSuffix(low, "ss"): // Resistors → Resistor
		return s[:len(s)-1]
	}
	return s
}

// deriveName produces a clean, human part name from enrichment data:
//   - resistors/capacitors/inductors → "<value> Resistor" ("22 Ω Resistor")
//   - everything else → the last clause of the description, which is where the
//     provider puts the type name ("…, Dc Power Jack Connector")
//   - fall back to a cleaned category, then the raw description.
//
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
				// The unit may have been split into its own column ("100" + "nF");
				// reattach it so the name reads "100 nF Capacitor", not "100 …".
				val = strings.TrimSpace(param.Value)
				if u := strings.TrimSpace(param.Units); u != "" {
					val += " " + u
				}
			}
		}
	}
	if val != "" && base != "" {
		return val + " " + base
	}
	// A part without a headline value (an IC, isolator, connector, module) is
	// best named by its manufacturer part number: a chip is "MOC3063S", not the
	// bare category word "Isolator". The user can rename it. Description clause
	// and category are last-resort only when no MPN was returned.
	if p.MPN != "" {
		return p.MPN
	}
	if i := strings.LastIndex(p.Description, ","); i >= 0 {
		if t := strings.TrimSpace(p.Description[i+1:]); looksLikeType(t) {
			return fixAcronyms(t)
		}
	}
	if p.Category != "" {
		return fixAcronyms(singularType(p.Category))
	}
	return p.Description
}

// looksLikeType reports whether a description clause reads like a part-type name
// ("Power Jack Connector") rather than a stray spec value ("105 C", "3.00mm",
// "50 C"). A type name is alphabetic and never starts with a digit; a spec value
// is numeric-led. It also requires a real word (3+ letters) so scraps like
// "No Mt" or "9T" don't win.
func looksLikeType(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if unicode.IsDigit([]rune(s)[0]) {
		return false // "105 C", "3.00mm" — a measurement, not a type
	}
	run := 0
	for _, c := range s {
		if unicode.IsLetter(c) {
			if run++; run >= 3 {
				return true
			}
		} else {
			run = 0
		}
	}
	return false
}

// ensureSlices replaces nil slices with empty ones so JSON encodes them as []
// rather than null (Go marshals a nil slice as null, which crashes JS consumers
// that call .length / .map on the result).
func ensureSlices(p *models.EnrichedPart) {
	if p == nil {
		return
	}
	if p.Parameters == nil {
		p.Parameters = []models.EnrichedParameter{}
	}
	if p.Suppliers == nil {
		p.Suppliers = []models.EnrichedSupplier{}
	}
	if p.Alternatives == nil {
		p.Alternatives = []models.EnrichedAlt{}
	}
	for i := range p.Suppliers {
		if p.Suppliers[i].Prices == nil {
			p.Suppliers[i].Prices = []models.PriceBreak{}
		}
	}
}

// mojibake maps the CJK characters that result when a symbol's UTF-8 bytes get
// decoded as GBK upstream back to the intended symbol. E.g. "±" is UTF-8 C2 B1,
// which GBK reads as "卤"; distributor data (often sourced through Chinese
// systems) arrives already mangled this way.
var mojibake = map[rune]rune{
	'卤': '±', '掳': '°', '碌': 'µ', '脳': '×', '梅': '÷',
	'虏': '²', '鲁': '³', '路': '·', '惟': 'Ω', '螖': 'Δ',
}

// fixMojibake repairs the GBK-of-UTF-8 mangling above. It only runs on
// mostly-ASCII strings (these spec descriptions are English) so a genuinely
// Chinese value is left alone — the replacement chars include common Han
// characters like 路 that we must not touch in real Chinese text.
func fixMojibake(s string) string {
	if s == "" {
		return s
	}
	ascii, total := 0, 0
	for _, r := range s {
		total++
		if r < 128 {
			ascii++
		}
	}
	if total == 0 || ascii*100/total < 60 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if rep, ok := mojibake[r]; ok {
			b.WriteRune(rep)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ohmsRepl normalizes Digi-Key's spelled-out "Ohms"/"Ohm" (e.g. "10 kOhms")
// to the Ω symbol other providers use, so resistance values read consistently
// and the unit-splitter can peel "kΩ" into its own column. Longest first so
// "kOhms" beats "Ohms".
var ohmsRepl = strings.NewReplacer(
	"kOhms", "kΩ", "MOhms", "MΩ", "mOhms", "mΩ", "µOhms", "µΩ", "GOhms", "GΩ",
	"kOhm", "kΩ", "MOhm", "MΩ", "mOhm", "mΩ", "µOhm", "µΩ", "GOhm", "GΩ",
	"Ohms", "Ω", "Ohm", "Ω",
)

// scrubEnriched repairs mojibake and normalizes unit text across every field of
// an enriched part. Applied on both the fresh and cached paths so re-scans and
// re-reads self-heal with no provider query.
func scrubEnriched(p *models.EnrichedPart) {
	if p == nil {
		return
	}
	clean := func(s string) string { return ohmsRepl.Replace(fixMojibake(s)) }
	p.Description = clean(p.Description)
	p.Manufacturer = fixMojibake(p.Manufacturer)
	p.Category = fixMojibake(p.Category)
	p.Name = clean(p.Name)
	for i := range p.Parameters {
		p.Parameters[i].Name = fixMojibake(p.Parameters[i].Name)
		p.Parameters[i].Value = clean(p.Parameters[i].Value)
	}
}

// paramUnits are the unit suffixes we peel off a "100 nF" style value into its
// own column, longest/most-specific first so a fuller unit's extra letter can't
// be shadowed by its single-char base ("nF" before "F", "MHz" before "Hz"). The
// per-part units column (part_parameters.units) means each part keeps its own
// prefix, so splitting SI-prefixed families here is safe.
var paramUnits = []string{
	"°C", "°F",
	"GHz", "MHz", "kHz", "Hz",
	"mΩ", "kΩ", "MΩ", "GΩ", "Ω",
	"pF", "nF", "µF", "uF", "mF", "F",
	"pH", "nH", "µH", "uH", "mH", "H",
	"mV", "kV", "µV", "uV", "nV", "V",
	"mAh", "Ah", "mA", "µA", "uA", "nA", "kA", "A",
	"mW", "kW", "µW", "uW", "W", "VA",
	"mm", "cm", "nm", "µm", "um", "mil",
	"dB", "ppm", "%",
}

// numHead matches a pure measurement magnitude: an optional sign then digits
// with decimals/commas/ranges. It gates unit-splitting so a value like
// "Surface Mount" or "Production" is never mistaken for "<number> <unit>".
var numHead = regexp.MustCompile(`^[±+\-]?\d[\d.,\s±\-+]*$`)

// splitUnit peels a trailing unit off a value: "100 nF" → ("100", "nF"),
// "1.25 mm" → ("1.25", "mm"), "10 %" → ("10", "%"). Returns the value unchanged
// with an empty unit when nothing looks like a unit.
func splitUnit(value string) (string, string) {
	v := strings.TrimSpace(value)
	for _, u := range paramUnits {
		if strings.HasSuffix(v, u) {
			head := strings.TrimSpace(v[:len(v)-len(u)])
			if head != "" && numHead.MatchString(head) {
				return head, u
			}
		}
	}
	return v, ""
}

// cleanParameters drops empty, pathologically long, and junk parameters, and
// splits a trailing unit into its own column, so the part gets a tidy spec
// sheet. Applied to every enrichment result (fresh or cached) before it reaches
// the client.
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
		// Only infer a unit when the provider didn't already give one.
		if strings.TrimSpace(param.Units) == "" {
			if head, unit := splitUnit(v); unit != "" {
				param.Value = head
				param.Units = unit
			} else {
				param.Value = v
			}
		}
		out = append(out, param)
	}
	p.Parameters = out
}

// Enrich looks up an MPN across the enrichment providers and returns normalized
// part data to prefill the scan create-flow and the part-detail Update.
//
// Query params:
//   - mpn       (required) the manufacturer part number.
//   - providers (optional) comma-separated provider ids to query (e.g.
//     "digikey,nexar"); default = all configured. Naming a subset implies a
//     fresh lookup.
//   - refresh   (optional) "1"/"true" skips the cache and re-queries.
//
// Results from every queried provider are merged into the richest single part
// (union of parameters/suppliers, first-non-empty for scalar fields) so a scan
// gets the most complete data possible.
func (h *Handler) Enrich(w http.ResponseWriter, r *http.Request) {
	mpn := strings.TrimSpace(r.URL.Query().Get("mpn"))
	if mpn == "" {
		respond.Error(w, http.StatusBadRequest, "mpn query param is required")
		return
	}
	ctx := r.Context()
	refresh := isTrue(r.URL.Query().Get("refresh"))
	names := h.parseProviders(r.URL.Query().Get("providers"))
	// A refresh, or an explicit provider subset, always re-queries; otherwise a
	// cache hit is served so repeated scans never spend a provider query.
	forceFresh := refresh || len(names) > 0

	if !forceFresh {
		if cached, ok, _ := h.EnrichCache.Get(ctx, mpn); ok {
			h.serveEnriched(w, cached, true)
			return
		}
	}

	if !h.anyEnricherConfigured(ctx) {
		respond.Error(w, http.StatusServiceUnavailable, "enrichment not configured — add Digi-Key or Nexar credentials in settings")
		return
	}

	part, err := h.enrichAll(ctx, mpn, names)
	if err != nil {
		respond.Error(w, http.StatusBadGateway, "enrichment lookup failed: "+err.Error())
		return
	}
	if part == nil {
		respond.JSON(w, http.StatusOK, map[string]any{"found": false})
		return
	}

	_ = h.EnrichCache.Set(ctx, mpn, part) // cache the merged result
	h.serveEnriched(w, part, false)
}

// serveEnriched cleans and writes an enriched part (shared by the cached and
// fresh paths so both self-heal identically).
func (h *Handler) serveEnriched(w http.ResponseWriter, p *models.EnrichedPart, cached bool) {
	scrubEnriched(p)
	cleanParameters(p)
	p.Name = deriveName(p)
	ensureSlices(p)
	respond.JSON(w, http.StatusOK, map[string]any{"found": true, "part": p, "cached": cached})
}

// enrichAll queries the target providers (all configured when names is empty,
// otherwise the named+configured subset) and merges their results. Returns
// (nil, nil) when nothing matched, or the last error only if every provider that
// ran errored.
type enrichPartRequest struct {
	Providers []string `json:"providers"`
}

// EnrichPart refreshes ONE part from its primary MPN and applies the result
// server-side — the exact same apply path as the bulk refresh, so a single
// "Update" and a bulk refresh never diverge.
func (h *Handler) EnrichPart(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	var req enrichPartRequest
	_ = respond.DecodeAllowEmpty(w, r, &req)
	part, err := h.Parts.Get(r.Context(), id)
	if err != nil {
		respond.Error(w, http.StatusNotFound, "part not found")
		return
	}
	mps, _ := h.Catalog.ListManufacturerParts(r.Context(), id)
	if len(mps) == 0 || mps[0].MPN == "" {
		respond.Error(w, http.StatusBadRequest, "part has no MPN to look up")
		return
	}
	primary := mps[0]
	en, err := h.enrichAll(r.Context(), primary.MPN, req.Providers)
	if err != nil || en == nil {
		respond.Error(w, http.StatusBadGateway, "no data found for "+primary.MPN)
		return
	}
	h.applyEnrichment(r.Context(), part, primary, en)
	h.Bus.Publish("parts")
	respond.JSON(w, http.StatusOK, map[string]any{"source": en.Source})
}

// majorDistributorRE matches the distributors whose SKUs + pricing we import
// (kept in sync with the web client).
var majorDistributorRE = regexp.MustCompile(`(?i)digi-?key|mouser|lcsc|arrow|newark|element14|farnell|avnet|\btme\b`)

func isMajorDistributor(name string) bool { return majorDistributorRE.MatchString(name) }

// applyEnrichment writes an enrichment result onto a part: package, description,
// parameters, the primary MPN's datasheet, and each major distributor's SKU +
// pricing. This is the single source of truth so the per-part Update and the bulk
// refresh apply exactly the same thing. Returns a short summary line.
func (h *Handler) applyEnrichment(ctx context.Context, part *models.Part, primary models.ManufacturerPart, en *models.EnrichedPart) {
	scrubEnriched(en)
	cleanParameters(en)

	changed := false
	if en.Package != "" {
		pkg := en.Package
		part.Package = &pkg
		changed = true
	}
	if en.Description != "" {
		d := en.Description
		part.Description = &d
		changed = true
	}
	if changed {
		_ = h.Parts.Update(ctx, part)
	}
	for _, prm := range en.Parameters {
		var units *string
		if prm.Units != "" {
			u := prm.Units
			units = &u
		}
		_ = h.Parts.SetParameter(ctx, part.ID, prm.Name, units, prm.Value)
	}
	if en.DatasheetURL != "" {
		mfg := ""
		if primary.ManufacturerName != nil {
			mfg = *primary.ManufacturerName
		}
		_ = h.Catalog.UpdateManufacturerPart(ctx, primary.ID, mfg, primary.MPN, &en.DatasheetURL)
	}
	for _, s := range en.Suppliers {
		if !isMajorDistributor(s.Name) {
			continue
		}
		supID, err := h.Catalog.GetOrCreateSupplier(ctx, s.Name)
		if err != nil {
			continue
		}
		var url, pkg *string
		if s.URL != "" {
			url = &s.URL
		}
		if s.Packaging != "" {
			pkg = &s.Packaging
		}
		_, _ = h.Catalog.CreateSupplierPart(ctx, primary.ID, supID, s.SKU, pkg, url, nil, s.Prices)
	}
}

func (h *Handler) enrichAll(ctx context.Context, mpn string, names []string) (*models.EnrichedPart, error) {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	var results []*models.EnrichedPart
	var lastErr error
	ran := false
	for _, e := range h.Enrichers {
		if len(want) > 0 && !want[e.Name()] {
			continue
		}
		// The default chain skips providers the user disabled; an explicit
		// selection (want) still runs them.
		if len(want) == 0 && !h.enricherEnabled(ctx, e.Name()) {
			continue
		}
		if !e.Configured(ctx) {
			continue
		}
		ran = true
		p, err := e.Enrich(ctx, mpn)
		if err != nil {
			lastErr = err
			continue
		}
		if p != nil {
			results = append(results, p)
		}
	}
	if len(results) == 0 {
		if ran && lastErr != nil {
			return nil, lastErr
		}
		return nil, nil
	}
	return mergeEnriched(results), nil
}

// mergeEnriched folds several provider results (in priority order) into one:
// first non-empty wins for scalar fields, slices are unioned (parameters by
// name, suppliers by name+SKU, alternatives by MPN), and Source lists every
// provider that contributed ("digikey + nexar").
func mergeEnriched(parts []*models.EnrichedPart) *models.EnrichedPart {
	out := &models.EnrichedPart{}
	set := func(dst *string, v string) {
		if *dst == "" {
			*dst = strings.TrimSpace(v)
		}
	}
	paramSeen := map[string]bool{}
	supSeen := map[string]bool{}
	altSeen := map[string]bool{}
	var sources []string
	for _, p := range parts {
		if p == nil {
			continue
		}
		if p.Source != "" {
			sources = append(sources, p.Source)
		}
		set(&out.MPN, p.MPN)
		set(&out.Description, p.Description)
		set(&out.Manufacturer, p.Manufacturer)
		set(&out.Category, p.Category)
		set(&out.Package, p.Package)
		set(&out.DatasheetURL, p.DatasheetURL)
		set(&out.ImageURL, p.ImageURL)
		for _, prm := range p.Parameters {
			k := strings.ToLower(strings.TrimSpace(prm.Name))
			if k == "" || paramSeen[k] {
				continue
			}
			paramSeen[k] = true
			out.Parameters = append(out.Parameters, prm)
		}
		for _, s := range p.Suppliers {
			k := strings.ToLower(s.Name) + "|" + strings.ToLower(s.SKU)
			if supSeen[k] {
				continue
			}
			supSeen[k] = true
			out.Suppliers = append(out.Suppliers, s)
		}
		for _, a := range p.Alternatives {
			k := strings.ToLower(strings.TrimSpace(a.MPN))
			if k == "" || altSeen[k] {
				continue
			}
			altSeen[k] = true
			out.Alternatives = append(out.Alternatives, a)
		}
	}
	out.Source = strings.Join(sources, " + ")
	return out
}

// parseProviders parses a comma-separated provider list, keeping only ids that
// map to a real provider.
func (h *Handler) parseProviders(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		n := strings.ToLower(strings.TrimSpace(part))
		if n == "" {
			continue
		}
		if _, ok := h.EnricherBy[n]; ok {
			out = append(out, n)
		}
	}
	return out
}

func isTrue(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "1" || s == "true" || s == "yes"
}

// anyEnricherConfigured reports whether at least one provider has credentials.
func (h *Handler) anyEnricherConfigured(ctx context.Context) bool {
	for _, e := range h.Enrichers {
		if e.Configured(ctx) {
			return true
		}
	}
	return false
}

// EnrichmentStatus reports which providers are configured (for the UI to show
// the right affordance without exposing secrets).
func (h *Handler) EnrichmentStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	provs := make([]map[string]any, 0, len(h.Enrichers))
	for _, e := range h.Enrichers {
		provs = append(provs, map[string]any{
			"provider":   e.Name(),
			"label":      e.Label(),
			"configured": e.Configured(ctx),
		})
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"configured": h.anyEnricherConfigured(ctx),
		"providers":  provs,
	})
}
