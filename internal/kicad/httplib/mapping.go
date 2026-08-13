// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package httplib

import (
	"strconv"
	"strings"

	"github.com/firelabsca/firebin-api/internal/kicad/httplib/source"
)

// valueParameterByCategory maps a category-name substring to the spec parameter
// that stands in for a symbol's Value. A resistor's value is its resistance, not
// its part name, and KiCad puts Value on the schematic next to the symbol.
//
// Keyed on category rather than "first parameter that matches", because
// enrichment attaches specs that have nothing to do with a part's value: a
// TCA9548A I2C mux in this inventory carries a Resistance of 70 Ω, and a
// parameter-first rule would put "70" on the chip. Category is the only signal
// that says which spec is *the* value. Anything unmatched falls back to the part
// name, which is what actives want anyway.
var valueParameterByCategory = []struct{ categorySubstring, parameter string }{
	{"resistor", "Resistance"},
	{"capacitor", "Capacitance"},
	{"inductor", "Inductance"},
}

// baseUnits are the unit symbols stripped before fusing an SI prefix onto a
// value. Ω appears as both U+03A9 (Greek capital omega) and U+2126 (ohm sign).
var baseUnits = []string{"Ω", "Ω", "ohms", "ohm", "Ohms", "Ohm", "F", "H"}

// MapPart projects a FireBin part onto a KiCad part detail object.
//
// The part must have been fetched from the per-part endpoint: the list
// endpoint omits Parameters and ManufacturerParts entirely, and a part mapped
// from a list row would silently lose its value, MPN and datasheet.
//
// categoryName decides which spec parameter becomes the symbol's Value; see
// valueParameterByCategory.
func MapPart(p source.Part, categoryName, unmappedMarker string) Part {
	symbol := strings.TrimSpace(source.Deref(p.KicadSymbol))
	footprint := strings.TrimSpace(source.Deref(p.KicadFootprint))

	name := p.Name
	if symbol == "" {
		// No symbol mapping: KiCad will render a flagged placeholder. Mark the
		// name too, so the reason is legible in the chooser instead of looking
		// like a broken library.
		name = unmappedMarker + name
	}

	out := Part{
		ID:          p.ID,
		Name:        name,
		SymbolIDStr: symbol,
		Description: strings.TrimSpace(source.Deref(p.Description)),
		Keywords:    keywordsFor(p),
		Fields:      map[string]Field{},
	}

	// value is left visible: it is the one field that belongs on the drawing.
	out.Fields["value"] = Field{Value: valueFor(p, categoryName)}

	if footprint != "" {
		// KiCad splits this on ";" and tabs/newlines, takes the first token as
		// the Footprint field, and folds every token into the footprint filter
		// list. A single identifier is the ordinary case.
		out.Fields["footprint"] = Field{Value: footprint, Visible: "False"}
	}

	if ds := datasheetFor(p); ds != "" {
		out.Fields["datasheet"] = Field{Value: ds, Visible: "False"}
	}

	// MPN and fbpn are deliberately named to match what FireBin's own BOM
	// parser reads back out of a .kicad_sch (see internal/kicad/bom.go in the
	// API: it looks for MPN, Manufacturer, and fbpn/firebin for the IPN). That
	// closes the loop — place a part from here, export the BOM, upload it to a
	// FireBin project, and it matches back to this exact inventory row.
	addField(out.Fields, "MPN", mpnFor(p))
	addField(out.Fields, "Manufacturer", manufacturerFor(p))
	addField(out.Fields, "fbpn", strings.TrimSpace(source.Deref(p.IPN)))
	addField(out.Fields, "Package", strings.TrimSpace(source.Deref(p.Package)))

	// Stock is why this exists: seeing it as a chooser column is the whole
	// point of designing against inventory. Every emitted field becomes a
	// column (the HTTP schema has no visible_in_chooser knob), so the field
	// list above is kept deliberately short.
	out.Fields["Stock"] = Field{Value: formatQty(p.TotalStock), Visible: "False"}

	return out
}

// addField sets a hidden field when the value is non-empty. Empty fields would
// still become chooser columns, so they are omitted rather than blanked.
func addField(m map[string]Field, name, value string) {
	if value == "" {
		return
	}
	m[name] = Field{Value: value, Visible: "False"}
}

// valueFor picks the symbol's Value: the spec parameter appropriate to the
// part's category, else the part name.
func valueFor(p source.Part, categoryName string) string {
	cat := strings.ToLower(categoryName)
	for _, rule := range valueParameterByCategory {
		if !strings.Contains(cat, rule.categorySubstring) {
			continue
		}
		for _, param := range p.Parameters {
			if !strings.EqualFold(strings.TrimSpace(param.TemplateName), rule.parameter) {
				continue
			}
			if v := strings.TrimSpace(param.Value); v != "" {
				return fuseSIPrefix(v, source.Deref(param.Units))
			}
		}
	}
	return p.Name
}

// fuseSIPrefix turns FireBin's split representation into KiCad's fused one.
//
// FireBin stores the number and the prefixed unit in separate columns:
// value "100" with units "kΩ". KiCad expects "100k" on the schematic. Dropping
// the units, as an earlier version did, silently turned every 100 kΩ resistor
// into a 100 Ω one — wrong in a way that looks entirely plausible on a drawing.
//
// So strip the base unit symbol and append whatever prefix is left. µ is
// normalised to "u" because that survives CSV BOMs and netlists that are not
// UTF-8 clean, and K to "k" to match KiCad's own libraries.
func fuseSIPrefix(value, units string) string {
	u := strings.TrimSpace(units)
	if u == "" {
		return value
	}
	// A value that already carries its own prefix ("10k") is left alone.
	if last := value[len(value)-1]; last < '0' || last > '9' {
		return value
	}
	for _, base := range baseUnits {
		if strings.HasSuffix(u, base) {
			u = strings.TrimSuffix(u, base)
			break
		}
	}
	switch strings.TrimSpace(u) {
	case "":
		return value // plain Ω, F, H — no prefix to fuse
	case "µ", "μ", "u", "U":
		return value + "u"
	case "k", "K":
		return value + "k"
	case "m", "M", "n", "p", "f", "G", "T", "c", "d":
		return value + strings.TrimSpace(u)
	}
	// Something unrecognised. Better a bare number than an invented unit.
	return value
}

// keywordsFor builds the searchable term list.
//
// This is load-bearing, not decoration. KiCad's Symbol Chooser search only
// looks at the library nickname, symbol name, LIB_ID, keywords, description
// and footprint — custom fields are NOT searched. Typing an MPN or an LCSC SKU
// into the chooser matches nothing unless it also appears here. Keywords are
// whitespace-tokenized and weighted heavily, so folding the identifiers in is
// what makes "search my inventory" actually work.
func keywordsFor(p source.Part) string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		for _, tok := range strings.Fields(s) {
			tok = strings.TrimSpace(tok)
			if tok == "" {
				continue
			}
			k := strings.ToLower(tok)
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, tok)
		}
	}

	add(source.Deref(p.Keywords))
	add(source.Deref(p.Package))
	add(source.Deref(p.IPN))
	add(p.PrimaryMPN)
	// Tags, so the chooser answers to the same words the app does. A two-word
	// tag splits into two tokens here, which is right for this surface: KiCad
	// weights keywords per token, so "STEMMA QT" makes the part findable by
	// typing either half.
	for _, t := range p.Tags {
		add(t)
	}
	for _, mp := range p.ManufacturerParts {
		add(mp.MPN)
		add(source.Deref(mp.ManufacturerName))
		for _, sp := range mp.SupplierParts {
			add(sp.SKU)
		}
	}

	return strings.Join(out, " ")
}

func mpnFor(p source.Part) string {
	if len(p.ManufacturerParts) > 0 && strings.TrimSpace(p.ManufacturerParts[0].MPN) != "" {
		return strings.TrimSpace(p.ManufacturerParts[0].MPN)
	}
	return strings.TrimSpace(p.PrimaryMPN)
}

func manufacturerFor(p source.Part) string {
	if len(p.ManufacturerParts) > 0 {
		if v := strings.TrimSpace(source.Deref(p.ManufacturerParts[0].ManufacturerName)); v != "" {
			return v
		}
	}
	return strings.TrimSpace(p.PrimaryManufacturer)
}

func datasheetFor(p source.Part) string {
	for _, mp := range p.ManufacturerParts {
		if ds := strings.TrimSpace(source.Deref(mp.DatasheetURL)); ds != "" {
			return ds
		}
	}
	return ""
}

// formatQty renders a stock quantity without a trailing ".0" — FireBin stores
// numeric(18,4) so whole counts arrive as 25.0, and "25" is what a human wants
// to read in a chooser column.
func formatQty(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// MapCategory projects a FireBin category. Path is preferred over Name because
// FireBin stores it already "/"-joined for nested categories, and KiCad turns
// a "/" in a category name into chooser tree nesting.
func MapCategory(c source.Category) Category {
	name := strings.TrimSpace(c.Path)
	if name == "" {
		name = c.Name
	}
	return Category{ID: c.ID, Name: name}
}
