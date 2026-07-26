// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package labels

import (
	_ "embed"
	"encoding/xml"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// The catalogue is the full, browseable list of known label products (hundreds
// of Avery sizes for US Letter and A4). It is bundled from the open gLabels
// template database (template files MIT-licensed; the dimensional facts are not
// copyrightable). Users pick from it to build their own short "sheets I use"
// list (the label_media table); the catalogue itself is read-only reference data
// and stays out of the DB.
//
//go:embed catalog/avery-us-templates.xml
var averyUS []byte

//go:embed catalog/avery-iso-templates.xml
var averyISO []byte

// CatalogEntry is one label product's geometry, all lengths in PDF points.
type CatalogEntry struct {
	Brand        string  `json:"brand"`
	Code         string  `json:"code"`
	Name         string  `json:"name"`
	PageSize     string  `json:"page_size"`
	PageW        float64 `json:"page_w"`
	PageH        float64 `json:"page_h"`
	LabelW       float64 `json:"label_w"`
	LabelH       float64 `json:"label_h"`
	CornerRadius float64 `json:"corner_radius"`
	Cols         int     `json:"cols"`
	Rows         int     `json:"rows"`
	X0           float64 `json:"x0"`
	Y0           float64 `json:"y0"`
	PitchX       float64 `json:"pitch_x"`
	PitchY       float64 `json:"pitch_y"`
}

// ── gLabels XML shapes (only the rectangular-label fields we use) ─────────────

type glTemplates struct {
	Templates []glTemplate `xml:"Template"`
}

type glTemplate struct {
	Brand string  `xml:"brand,attr"`
	Part  string  `xml:"part,attr"`
	Size  string  `xml:"size,attr"`
	Equiv string  `xml:"equiv,attr"`
	Desc  string  `xml:"_description,attr"`
	Rect  *glRect `xml:"Label-rectangle"`
}

type glRect struct {
	Width  string   `xml:"width,attr"`
	Height string   `xml:"height,attr"`
	Round  string   `xml:"round,attr"`
	Layout glLayout `xml:"Layout"`
}

type glLayout struct {
	Nx string `xml:"nx,attr"`
	Ny string `xml:"ny,attr"`
	X0 string `xml:"x0,attr"`
	Y0 string `xml:"y0,attr"`
	Dx string `xml:"dx,attr"`
	Dy string `xml:"dy,attr"`
}

// Page dimensions in points for the sizes the Avery templates use. Entries with
// an unknown page size are skipped rather than guessed.
var pageSizes = map[string][2]float64{
	"US-Letter":    {612, 792},
	"US-Legal":     {612, 1008},
	"A4":           {595.276, 841.890},
	"A5":           {419.528, 595.276},
	"A3":           {841.890, 1190.551},
	"B5-ISO":       {498.898, 708.661},
	"US-Index-3x5": {216, 360},
	"US-Index-4x6": {288, 432},
	"US-Index-5x8": {360, 576},
}

var (
	catalogOnce sync.Once
	catalog     []CatalogEntry
)

// Catalog returns the full label catalogue, parsed once and sorted by brand+code.
func Catalog() []CatalogEntry {
	catalogOnce.Do(buildCatalog)
	return catalog
}

// SearchCatalog returns catalogue entries matching q (in code, name, or brand),
// ranked exact-code → code-prefix → code-substring → name/brand, capped at
// limit. An empty query returns the first `limit` entries.
func SearchCatalog(q string, limit int) []CatalogEntry {
	all := Catalog()
	if limit <= 0 {
		limit = 50
	}
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		if len(all) > limit {
			return all[:limit]
		}
		return all
	}

	type ranked struct {
		e CatalogEntry
		r int
	}
	var hits []ranked
	for _, e := range all {
		code := strings.ToLower(e.Code)
		r := -1
		switch {
		case code == q:
			r = 0
		case strings.HasPrefix(code, q):
			r = 1
		case strings.Contains(code, q):
			r = 2
		case strings.Contains(strings.ToLower(e.Name), q) || strings.Contains(strings.ToLower(e.Brand), q):
			r = 3
		}
		if r >= 0 {
			hits = append(hits, ranked{e, r})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].r != hits[j].r {
			return hits[i].r < hits[j].r
		}
		return hits[i].e.Code < hits[j].e.Code
	})

	out := make([]CatalogEntry, 0, limit)
	for _, h := range hits {
		out = append(out, h.e)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func buildCatalog() {
	// First pass: parse every rectangular template into a base map keyed by part
	// number. Second pass: resolve equiv/alias entries to their base geometry.
	bases := map[string]CatalogEntry{}
	var aliases []glTemplate

	for _, data := range [][]byte{averyUS, averyISO} {
		var doc glTemplates
		if err := xml.Unmarshal(data, &doc); err != nil {
			continue
		}
		for _, t := range doc.Templates {
			if t.Equiv != "" {
				aliases = append(aliases, t)
				continue
			}
			if t.Rect == nil {
				continue // round / CD / ellipse — not a rectangular sheet label
			}
			if e, ok := entryFromTemplate(t); ok {
				bases[t.Part] = e
			}
		}
	}

	seen := map[string]bool{}
	for _, e := range bases {
		catalog = append(catalog, e)
		seen[e.Brand+"|"+e.Code] = true
	}
	for _, a := range aliases {
		base, ok := bases[a.Equiv]
		if !ok {
			continue
		}
		e := base
		e.Code = a.Part
		if a.Brand != "" {
			e.Brand = a.Brand
		}
		if seen[e.Brand+"|"+e.Code] {
			continue
		}
		seen[e.Brand+"|"+e.Code] = true
		catalog = append(catalog, e)
	}

	sort.Slice(catalog, func(i, j int) bool {
		if catalog[i].Brand != catalog[j].Brand {
			return catalog[i].Brand < catalog[j].Brand
		}
		return catalog[i].Code < catalog[j].Code
	})
}

func entryFromTemplate(t glTemplate) (CatalogEntry, bool) {
	page, ok := pageSizes[t.Size]
	if !ok {
		return CatalogEntry{}, false
	}
	r := t.Rect
	cols := atoi(r.Layout.Nx)
	rows := atoi(r.Layout.Ny)
	lw := toPt(r.Width)
	lh := toPt(r.Height)
	if cols < 1 || rows < 1 || lw <= 0 || lh <= 0 {
		return CatalogEntry{}, false
	}
	return CatalogEntry{
		Brand:        t.Brand,
		Code:         t.Part,
		Name:         strings.TrimSpace(t.Desc),
		PageSize:     t.Size,
		PageW:        page[0],
		PageH:        page[1],
		LabelW:       lw,
		LabelH:       lh,
		CornerRadius: toPt(r.Round),
		Cols:         cols,
		Rows:         rows,
		X0:           toPt(r.Layout.X0),
		Y0:           toPt(r.Layout.Y0),
		PitchX:       toPt(r.Layout.Dx),
		PitchY:       toPt(r.Layout.Dy),
	}, true
}

// toPt parses a gLabels length ("2.5in", "189pt", "36mm") into PDF points. A
// bare number is treated as points.
func toPt(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	i := 0
	for i < len(s) {
		c := s[i]
		if (c >= '0' && c <= '9') || c == '.' || c == '-' || c == '+' {
			i++
			continue
		}
		break
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s[:i]), 64)
	if err != nil {
		return 0
	}
	switch strings.ToLower(strings.TrimSpace(s[i:])) {
	case "in":
		return v * 72
	case "mm":
		return v * 72 / 25.4
	case "cm":
		return v * 72 / 2.54
	case "pc":
		return v * 12
	default: // "pt" or bare
		return v
	}
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
