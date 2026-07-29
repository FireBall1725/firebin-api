// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package kicad

import (
	"encoding/csv"
	"sort"
	"strconv"
	"strings"
)

// Component is a single placed schematic symbol that belongs in the BOM.
type Component struct {
	Reference string
	Value     string
	Footprint string
	// LibID is the KiCad symbol the designer placed, e.g. "Device:R". Already
	// read for the power-symbol filter; kept because it is the authoritative
	// symbol mapping for the part this line matches.
	LibID        string
	MPN          string
	Manufacturer string
	SupplierSKU  string
	IPN          string
	Description  string
}

// BOMLine is a grouped BOM row (components sharing value+footprint+MPN).
type BOMLine struct {
	Refs      []string
	Quantity  int
	Value     string
	Footprint string
	// LibID is the KiCad symbol the grouped components were drawn with. Only
	// set by the schematic parser: a .kicad_pcb has footprints but no symbols,
	// and a CSV/xlsx BOM carries neither.
	LibID        string
	MPN          string
	Manufacturer string
	SupplierSKU  string // supplier/distributor SKU (LCSC, Digi-Key…), for matching
	IPN          string // FireBin internal part number, if the BOM carries one
	Description  string
}

// ParseSchematic reads a .kicad_sch file and returns its grouped BOM.
func ParseSchematic(data []byte) ([]BOMLine, error) {
	comps, err := componentsFromSchematic(data)
	if err != nil {
		return nil, err
	}
	return GroupComponents(comps), nil
}

// componentsFromSchematic reads the placed symbols (top-level `(symbol …)`
// entries — not the `lib_symbols` definitions), skipping power/no-connect
// symbols and anything flagged out of BOM or DNP.
func componentsFromSchematic(data []byte) ([]Component, error) {
	root, err := parseSexpr(data)
	if err != nil {
		return nil, err
	}
	var comps []Component
	for _, child := range root.Children {
		if child.head() != "symbol" {
			continue
		}
		if c, ok := componentFromSymbol(child); ok {
			comps = append(comps, c)
		}
	}
	return comps, nil
}

// ParsePCB reads a .kicad_pcb and returns a grouped BOM from its placed
// footprints. The schematic is the richer BOM source, but a bare PCB carries
// each footprint's reference, value, and footprint name, which is enough for a
// usable BOM. Footprints flagged exclude_from_bom / dnp / board_only and power
// symbols are skipped.
func ParsePCB(data []byte) ([]BOMLine, error) {
	comps, err := componentsFromPCB(data)
	if err != nil {
		return nil, err
	}
	return GroupComponents(comps), nil
}

// ParsePanelBoardBOM returns the single-board BOM of a panelized .kicad_pcb. A
// panel repeats every reference designator once per copy, so deduplicating
// components by reference recovers exactly one board's parts (the panel frame's
// KiKit_* parts are already skipped by componentFromPCBFootprint's `#`/attr
// filtering, and carry no BOM refs). Use with DetectPanelPCB's copy count.
func ParsePanelBoardBOM(data []byte) ([]BOMLine, error) {
	comps, err := componentsFromPCB(data)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	uniq := make([]Component, 0, len(comps))
	for _, c := range comps {
		if c.Reference == "" || seen[c.Reference] {
			continue
		}
		seen[c.Reference] = true
		uniq = append(uniq, c)
	}
	return GroupComponents(uniq), nil
}

func componentsFromPCB(data []byte) ([]Component, error) {
	root, err := parseSexpr(data)
	if err != nil {
		return nil, err
	}
	var comps []Component
	for _, child := range root.Children {
		if child.head() != "footprint" && child.head() != "module" {
			continue
		}
		if c, ok := componentFromPCBFootprint(child); ok {
			comps = append(comps, c)
		}
	}
	return comps, nil
}

func componentFromPCBFootprint(fp *node) (Component, bool) {
	var c Component
	c.Footprint = fp.atom(1) // "Lib:Name"
	for _, ch := range fp.Children {
		switch ch.head() {
		case "attr":
			for _, a := range ch.Children[1:] {
				switch strings.ToLower(a.Value) {
				case "exclude_from_bom", "dnp", "board_only":
					return c, false
				}
			}
		case "property", "fp_text":
			// fp_text uses (fp_text reference "R1" …); property uses (property "Reference" "R1" …)
			key := ch.atom(1)
			val := ch.atom(2)
			switch strings.ToLower(key) {
			case "reference":
				c.Reference = val
			case "value":
				c.Value = val
			case "mpn", "manufacturer part number", "manufacturer_part_number", "part number":
				if c.MPN == "" {
					c.MPN = val
				}
			case "manufacturer", "mfr", "mfg":
				if c.Manufacturer == "" {
					c.Manufacturer = val
				}
			}
		}
	}
	if strings.HasPrefix(c.Reference, "#") {
		return c, false
	}
	if c.Reference == "" && c.Value == "" {
		return c, false
	}
	return c, true
}

// componentFromSymbol extracts a Component, returning ok=false for symbols that
// don't belong in the BOM (power, no-connect, in_bom=no, dnp=yes).
func componentFromSymbol(sym *node) (Component, bool) {
	var c Component
	libID := ""
	inBom, onBoard, dnp := true, true, false

	for _, ch := range sym.Children {
		switch ch.head() {
		case "lib_id":
			libID = ch.atom(1)
			c.LibID = libID
		case "in_bom":
			inBom = ch.atom(1) != "no"
		case "on_board":
			onBoard = ch.atom(1) != "no"
		case "dnp":
			dnp = ch.atom(1) == "yes"
		case "property":
			name := ch.atom(1)
			val := ch.atom(2)
			switch strings.ToLower(name) {
			case "reference":
				c.Reference = val
			case "value":
				c.Value = val
			case "footprint":
				c.Footprint = val
			case "mpn", "manufacturer part number", "manufacturer_part_number", "manufacturer part no", "part number", "part number (mpn)":
				if c.MPN == "" {
					c.MPN = val
				}
			case "manufacturer", "mfr", "mfg":
				if c.Manufacturer == "" {
					c.Manufacturer = val
				}
			case "fbpn", "firebin", "firebinpn", "firebin part number", "ipn", "internal part number":
				if c.IPN == "" {
					c.IPN = val
				}
			case "lcsc", "lcsc part", "lcsc part number", "lcsc#", "supplier part", "supplier part number", "sku":
				if c.SupplierSKU == "" {
					c.SupplierSKU = val
				}
			case "description", "desc":
				if c.Description == "" {
					c.Description = val
				}
			}
		}
	}

	if !inBom || !onBoard || dnp {
		return c, false
	}
	// Power symbols, net labels, and no-connects aren't real parts.
	if strings.HasPrefix(c.Reference, "#") || strings.HasPrefix(strings.ToLower(libID), "power:") {
		return c, false
	}
	if c.Reference == "" && c.Value == "" {
		return c, false
	}
	return c, true
}

// GroupComponents collapses components sharing (value, footprint, MPN) into one
// BOM line, tallying quantity and collecting reference designators.
func GroupComponents(comps []Component) []BOMLine {
	type key struct{ value, footprint, mpn string }
	order := []key{}
	groups := map[key]*BOMLine{}

	for _, c := range comps {
		k := key{c.Value, c.Footprint, c.MPN}
		g, ok := groups[k]
		if !ok {
			g = &BOMLine{
				Value:        c.Value,
				Footprint:    c.Footprint,
				LibID:        c.LibID,
				MPN:          c.MPN,
				Manufacturer: c.Manufacturer,
				SupplierSKU:  c.SupplierSKU,
				IPN:          c.IPN,
				Description:  c.Description,
			}
			groups[k] = g
			order = append(order, k)
		}
		if c.Reference != "" {
			g.Refs = append(g.Refs, c.Reference)
		}
		g.Quantity++
		if g.Manufacturer == "" {
			g.Manufacturer = c.Manufacturer
		}
		if g.SupplierSKU == "" {
			g.SupplierSKU = c.SupplierSKU
		}
		if g.LibID == "" {
			g.LibID = c.LibID
		}
		if g.IPN == "" {
			g.IPN = c.IPN
		}
		if g.Description == "" {
			g.Description = c.Description
		}
	}

	out := make([]BOMLine, 0, len(order))
	for _, k := range order {
		g := groups[k]
		sort.Slice(g.Refs, func(i, j int) bool { return refLess(g.Refs[i], g.Refs[j]) })
		out = append(out, *g)
	}
	// Stable, human-friendly ordering: by leading refdes letter then number.
	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i].Refs) == 0 || len(out[j].Refs) == 0 {
			return out[i].Value < out[j].Value
		}
		return refLess(out[i].Refs[0], out[j].Refs[0])
	})
	return out
}

// refLess orders reference designators naturally: R1 < R2 < R10, C1 < R1.
func refLess(a, b string) bool {
	ap, an := splitRef(a)
	bp, bn := splitRef(b)
	if ap != bp {
		return ap < bp
	}
	return an < bn
}

func splitRef(r string) (string, int) {
	i := 0
	for i < len(r) && (r[i] < '0' || r[i] > '9') {
		i++
	}
	n, _ := strconv.Atoi(r[i:])
	return r[:i], n
}

// ParseBOMCSV reads a KiCad-exported BOM CSV. It fuzzy-matches columns
// (references/value/footprint/quantity/mpn/manufacturer) so exports from
// different BOM plugins work. Rows are assumed already grouped; quantity comes
// from the quantity column or the reference count.
func ParseBOMCSV(data []byte) ([]BOMLine, error) {
	r := csv.NewReader(strings.NewReader(string(data)))
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	rows, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	return rowsToLines(rows), nil
}

// rowsToLines turns a header + data grid (from CSV or XLSX) into BOM lines by
// fuzzy-matching columns, so exports from KiCad, EasyEDA, JLCPCB and LCSC all
// work. Rows are assumed already grouped; quantity comes from the quantity
// column or the reference count.
func rowsToLines(rows [][]string) []BOMLine {
	// Skip any leading blank rows before the header.
	for len(rows) > 0 && rowEmpty(rows[0]) {
		rows = rows[1:]
	}
	if len(rows) < 2 {
		return nil
	}

	col := map[string]int{}
	for i, h := range rows[0] {
		col[normHeader(h)] = i
	}
	pick := func(names ...string) int {
		for _, n := range names {
			if i, ok := col[n]; ok {
				return i
			}
		}
		return -1
	}
	get := func(row []string, idx int) string {
		if idx < 0 || idx >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[idx])
	}

	iRef := pick("references", "reference", "designator", "designators", "refdes")
	iVal := pick("value", "comment")
	iFoot := pick("footprint", "package", "pattern")
	iQty := pick("quantity", "qty", "count")
	iMPN := pick("mpn", "manufacturerpartnumber", "manufacturerpart", "mfrpartnumber", "partnumber", "mfrpart", "mfgpartnumber")
	iMfr := pick("manufacturer", "mfr", "mfg")
	iSup := pick("supplierpart", "supplierpartnumber", "lcscpart", "lcscpartnumber", "lcsc", "jlcpcbpart", "distributorpart", "sku")
	iIPN := pick("fbpn", "firebinpartnumber", "firebinpn", "firebin", "ipn", "internalpartnumber")
	iDesc := pick("description", "desc")

	out := []BOMLine{}
	for _, row := range rows[1:] {
		if rowEmpty(row) {
			continue
		}
		refs := splitRefs(get(row, iRef))
		qty := 0
		if q, err := strconv.Atoi(get(row, iQty)); err == nil && q > 0 {
			qty = q
		} else {
			qty = len(refs)
		}
		if qty == 0 {
			qty = 1
		}
		line := BOMLine{
			Refs:         refs,
			Quantity:     qty,
			Value:        get(row, iVal),
			Footprint:    get(row, iFoot),
			MPN:          get(row, iMPN),
			Manufacturer: get(row, iMfr),
			SupplierSKU:  get(row, iSup),
			IPN:          get(row, iIPN),
			Description:  get(row, iDesc),
		}
		if line.Value == "" && line.MPN == "" && len(line.Refs) == 0 {
			continue
		}
		out = append(out, line)
	}
	return out
}

func rowEmpty(row []string) bool {
	for _, c := range row {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

func normHeader(h string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(h) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// splitRefs splits a designator field on commas/spaces ("R1, R2 R3").
func splitRefs(s string) []string {
	f := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ' ' || r == ';'
	})
	out := make([]string, 0, len(f))
	for _, x := range f {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}
