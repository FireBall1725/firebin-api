// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package httplib

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/firelabsca/firebin-api/internal/kicad/httplib/source"
)

func ptr(s string) *string { return &s }

func sampleResistor() source.Part {
	return source.Part{
		ID:             "11111111-1111-1111-1111-111111111111",
		CategoryID:     ptr("cat-resistors"),
		Name:           "10k Resistor",
		Description:    ptr("RES 10K OHM 1% 1/10W 0603"),
		IPN:            ptr("FB-R-0603-10K"),
		Package:        ptr("0603"),
		KicadSymbol:    ptr("Device:R"),
		KicadFootprint: ptr("Resistor_SMD:R_0603_1608Metric"),
		Keywords:       ptr("resistor smd"),
		TotalStock:     250,
		Parameters: []source.Parameter{
			{TemplateName: "Tolerance", Value: "1"},
			{TemplateName: "Resistance", Units: ptr("kΩ"), Value: "10"},
		},
		ManufacturerParts: []source.ManufacturerPart{{
			ManufacturerName: ptr("Yageo"),
			MPN:              "RC0603FR-0710KL",
			DatasheetURL:     ptr("https://example.invalid/rc0603.pdf"),
			SupplierParts: []source.SupplierPart{
				{SupplierName: "LCSC", SKU: "C25804"},
			},
		}},
	}
}

// TestEveryJSONValueIsAString guards the single hardest rule in KiCad's HTTP
// library contract: every value must be a JSON string. An int, a float or a
// real JSON boolean anywhere in this payload makes KiCad reject the response
// and silently drop the library. footprint_filters is the one permitted array.
func TestEveryJSONValueIsAString(t *testing.T) {
	raw, err := json.Marshal(MapPart(sampleResistor(), "Resistors", "(no symbol) "))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch t2 := v.(type) {
		case map[string]any:
			for k, child := range t2 {
				walk(path+"."+k, child)
			}
		case []any:
			for _, child := range t2 {
				walk(path+"[]", child)
			}
		case string:
			// ok
		default:
			t.Errorf("%s is %T, must be a JSON string (KiCad rejects non-strings)", path, v)
		}
	}
	walk("part", tree)
}

// TestStockIsAStringNotANumber is the specific case the rule above exists for:
// FireBin stores quantity as numeric and unmarshals it into a float64, so an
// unconverted stock value is exactly the kind of thing that slips through.
func TestStockIsAStringNotANumber(t *testing.T) {
	got := MapPart(sampleResistor(), "Resistors", "")
	if got.Fields["Stock"].Value != "250" {
		t.Errorf("Stock = %q, want %q", got.Fields["Stock"].Value, "250")
	}
	frac := sampleResistor()
	frac.TotalStock = 12.5
	if v := MapPart(frac, "Resistors", "").Fields["Stock"].Value; v != "12.5" {
		t.Errorf("fractional Stock = %q, want %q", v, "12.5")
	}
}

// TestValueComesFromSpecParameter checks a resistor gets "10k" rather than its
// part name, and that units are not glued on.
func TestValueComesFromSpecParameter(t *testing.T) {
	if v := MapPart(sampleResistor(), "Resistors", "").Fields["value"].Value; v != "10k" {
		t.Errorf("value = %q, want %q", v, "10k")
	}
}

// TestValueFallsBackToName covers actives, which have no single spec standing
// in for a value.
func TestValueFallsBackToName(t *testing.T) {
	p := source.Part{ID: "x", Name: "ATmega328P", KicadSymbol: ptr("MCU_Microchip_ATmega:ATmega328P-PU")}
	if v := MapPart(p, "Resistors", "").Fields["value"].Value; v != "ATmega328P" {
		t.Errorf("value = %q, want %q", v, "ATmega328P")
	}
}

// TestKeywordsCarrySearchIdentifiers is why keyword stuffing exists: KiCad's
// Symbol Chooser search does not look at custom fields, so an MPN or a
// distributor SKU is unfindable unless it lands in keywords.
func TestKeywordsCarrySearchIdentifiers(t *testing.T) {
	kw := MapPart(sampleResistor(), "Resistors", "").Keywords
	for _, want := range []string{"resistor", "smd", "0603", "FB-R-0603-10K", "RC0603FR-0710KL", "Yageo", "C25804"} {
		if !strings.Contains(kw, want) {
			t.Errorf("keywords %q missing %q", kw, want)
		}
	}
}

// TestKeywordsDeduplicate keeps the term list from ballooning when the same
// token arrives from several sources.
func TestKeywordsDeduplicate(t *testing.T) {
	p := sampleResistor()
	p.Keywords = ptr("0603 resistor 0603")
	p.PrimaryMPN = "RC0603FR-0710KL"
	kw := MapPart(p, "Resistors", "").Keywords
	if n := strings.Count(kw, "0603 "); n > 1 {
		t.Errorf("keywords %q repeats 0603", kw)
	}
	if n := strings.Count(kw, "RC0603FR-0710KL"); n != 1 {
		t.Errorf("keywords %q has MPN %d times, want 1", kw, n)
	}
}

// TestUnmappedPartIsMarkedAndHasNoSymbol covers the deliberate choice to show
// unmapped parts rather than hide them. KiCad renders a flagged placeholder
// when symbolIdStr is absent; the name marker explains why.
func TestUnmappedPartIsMarkedAndHasNoSymbol(t *testing.T) {
	p := sampleResistor()
	p.KicadSymbol = nil
	got := MapPart(p, "Resistors", "(no symbol) ")
	if got.SymbolIDStr != "" {
		t.Errorf("symbolIdStr = %q, want empty so KiCad flags it", got.SymbolIDStr)
	}
	if !strings.HasPrefix(got.Name, "(no symbol) ") {
		t.Errorf("name = %q, want the unmapped marker prefix", got.Name)
	}
}

// TestMappedPartIsNotMarked is the other half: a whitespace-only mapping is
// treated as absent, and a real one is left alone.
func TestMappedPartIsNotMarked(t *testing.T) {
	got := MapPart(sampleResistor(), "Resistors", "(no symbol) ")
	if got.Name != "10k Resistor" {
		t.Errorf("name = %q, want it unmarked", got.Name)
	}
	if got.SymbolIDStr != "Device:R" {
		t.Errorf("symbolIdStr = %q, want %q", got.SymbolIDStr, "Device:R")
	}

	blank := sampleResistor()
	blank.KicadSymbol = ptr("   ")
	if MapPart(blank, "Resistors", "M ").SymbolIDStr != "" {
		t.Error("whitespace-only symbol should be treated as unmapped")
	}
}

// TestBOMRoundTripFieldNames pins the field names FireBin's own .kicad_sch BOM
// parser reads back (MPN, Manufacturer, fbpn). Renaming these silently breaks
// place-here → export BOM → match back to this inventory row.
func TestBOMRoundTripFieldNames(t *testing.T) {
	f := MapPart(sampleResistor(), "Resistors", "").Fields
	for name, want := range map[string]string{
		"MPN":          "RC0603FR-0710KL",
		"Manufacturer": "Yageo",
		"fbpn":         "FB-R-0603-10K",
	} {
		if f[name].Value != want {
			t.Errorf("field %s = %q, want %q", name, f[name].Value, want)
		}
	}
}

// TestEmptyFieldsAreOmitted keeps the chooser clean: the HTTP schema has no
// visible_in_chooser flag, so every emitted field becomes a column whether it
// holds anything or not.
func TestEmptyFieldsAreOmitted(t *testing.T) {
	p := source.Part{ID: "x", Name: "Mystery", KicadSymbol: ptr("Device:R")}
	f := MapPart(p, "Resistors", "").Fields
	for _, name := range []string{"MPN", "Manufacturer", "fbpn", "Package", "datasheet"} {
		if _, ok := f[name]; ok {
			t.Errorf("field %s present with no source value; should be omitted", name)
		}
	}
	if _, ok := f["value"]; !ok {
		t.Error("value should always be present")
	}
}

// The cases below come from the real inventory export, not invented fixtures.
// FireBin stores the number and the prefixed unit in separate columns, so
// "100 kΩ" arrives as value "100" with units "kΩ". An earlier version of the
// mapper dropped the units and put "100" on every 100 kΩ resistor — a wrong
// value that looks entirely reasonable on a schematic.
func TestValueFusesSIPrefixFromUnits(t *testing.T) {
	cases := []struct{ category, param, value, units, want string }{
		{"Resistors", "Resistance", "100", "kΩ", "100k"},
		{"Resistors", "Resistance", "10", "kΩ", "10k"},
		{"Resistors", "Resistance", "100", "Ω", "100"},
		{"Resistors", "Resistance", "0", "Ω", "0"},
		{"Resistors", "Resistance", "4.7", "Ω", "4.7"},
		{"Resistors", "Resistance", "49.9", "Ω", "49.9"},
		{"Resistors", "Resistance", "5.11", "kΩ", "5.11k"},
		{"Capacitors", "Capacitance", "0.1", "µF", "0.1u"},
		{"Capacitors", "Capacitance", "10", "µF", "10u"},
		{"Capacitors", "Capacitance", "22", "pF", "22p"},
		{"Inductors", "Inductance", "2.2", "µH", "2.2u"},
	}
	for _, c := range cases {
		p := source.Part{
			ID: "x", Name: c.value + " " + c.units + " part",
			Parameters: []source.Parameter{{TemplateName: c.param, Units: ptr(c.units), Value: c.value}},
		}
		if got := MapPart(p, c.category, "").Fields["value"].Value; got != c.want {
			t.Errorf("%s %s %s -> %q, want %q", c.category, c.value, c.units, got, c.want)
		}
	}
}

// TestStraySpecDoesNotBecomeValue is the other half of the real-data lesson.
// Enrichment gave the TCA9548A I2C mux a Resistance of 70 Ω. A rule that took
// the first matching parameter would label that chip "70" on the schematic.
func TestStraySpecDoesNotBecomeValue(t *testing.T) {
	mux := source.Part{
		ID: "x", Name: "TCA9548APWR",
		Parameters: []source.Parameter{{TemplateName: "Resistance", Units: ptr("Ω"), Value: "70"}},
	}
	if got := MapPart(mux, "Integrated Circuits (ICs)", "").Fields["value"].Value; got != "TCA9548APWR" {
		t.Errorf("value = %q, want the part name; a stray spec must not become the value", got)
	}
}

// TestValueAlreadyPrefixedIsLeftAlone covers a hand-entered "10k" that also
// carries units, so the prefix is not doubled into "10kk".
func TestValueAlreadyPrefixedIsLeftAlone(t *testing.T) {
	p := source.Part{
		ID: "x", Name: "R",
		Parameters: []source.Parameter{{TemplateName: "Resistance", Units: ptr("kΩ"), Value: "10k"}},
	}
	if got := MapPart(p, "Resistors", "").Fields["value"].Value; got != "10k" {
		t.Errorf("value = %q, want %q", got, "10k")
	}
}
