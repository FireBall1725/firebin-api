// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package units

import (
	"math"
	"testing"
)

// close compares magnitudes without demanding bit-exact floating point.
func close(a, b float64) bool {
	if b == 0 {
		return a == 0
	}
	return math.Abs(a-b)/math.Abs(b) < 1e-9
}

// The case this package exists for: these two sit in the same column in real
// data and differ by a thousand.
func TestPrefixIsNotOptional(t *testing.T) {
	kilo, ok := Parse("100", "kΩ")
	if !ok {
		t.Fatal("could not parse 100 kΩ")
	}
	ohms, ok := Parse("100", "Ω")
	if !ok {
		t.Fatal("could not parse 100 Ω")
	}
	if kilo.Value == ohms.Value {
		t.Fatal("100 kΩ and 100 Ω compared equal; the prefix was ignored")
	}
	if kilo.Value != 100_000 || ohms.Value != 100 {
		t.Errorf("got %v and %v, want 100000 and 100", kilo.Value, ohms.Value)
	}

	// And the failure this prevents: asking for 100 ohms must not find 100k.
	q, _ := ParseQuery("100 ohm")
	if Matches(kilo, q) {
		t.Error("a 100 kΩ resistor matched a search for 100 Ω")
	}
	if !Matches(ohms, q) {
		t.Error("a 100 Ω resistor did not match a search for 100 Ω")
	}
}

func TestParseStoredValues(t *testing.T) {
	// Shapes taken from real rows in the inventory.
	cases := []struct {
		value, unit string
		want        float64
		base        string
	}{
		{"100", "kΩ", 100_000, "Ω"},
		{"64.9", "kΩ", 64_900, "Ω"},
		{"33", "Ω", 33, "Ω"},
		{"12.1", "Ω", 12.1, "Ω"},
		{"0.1", "µF", 0.1e-6, "F"},
		{"22", "pF", 22e-12, "F"},
		{"4.7", "µF", 4.7e-6, "F"},
		{"2.2", "µH", 2.2e-6, "H"},
		{"0", "Ω", 0, "Ω"},
		// Both Unicode omegas appear in enriched data and must agree.
		{"470", "\u03a9", 470, "\u03a9"},
		{"470", "\u2126", 470, "\u03a9"},
		// Hand-entered, where the prefix rides on the number instead.
		{"10k", "Ω", 10_000, "Ω"},
		{"10k", "", 10_000, ""},
	}
	for _, c := range cases {
		got, ok := Parse(c.value, c.unit)
		if !ok {
			t.Errorf("Parse(%q, %q) failed", c.value, c.unit)
			continue
		}
		if !close(got.Value, c.want) || got.Base != c.base {
			t.Errorf("Parse(%q, %q) = %v %q, want %v %q",
				c.value, c.unit, got.Value, got.Base, c.want, c.base)
		}
	}
}

func TestMilliIsNotMega(t *testing.T) {
	milli, _ := Parse("10", "mΩ")
	mega, _ := Parse("10", "MΩ")
	if milli.Value != 0.01 {
		t.Errorf("10 mΩ = %v, want 0.01", milli.Value)
	}
	if mega.Value != 10_000_000 {
		t.Errorf("10 MΩ = %v, want 1e7", mega.Value)
	}
}

func TestParseQuery(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		base string
		ok   bool
	}{
		{"220", 220, "", true},       // bare number, unit unconstrained
		{"220 ohm", 220, "Ω", true},  // spelled out
		{"220Ω", 220, "Ω", true},     // symbol
		{"10k", 10_000, "", true},    // prefix, no unit
		{"4.7uF", 4.7e-6, "F", true}, // the way a datasheet writes it
		{"100nF", 100e-9, "F", true}, //
		{"1.5 kΩ", 1500, "Ω", true},  // space before the unit
		{"", 0, "", false},           // nothing
		{"resistor", 0, "", false},   // not a quantity
		{"0603", 603, "", true},      // a package code parses as a number
	}
	for _, c := range cases {
		got, ok := ParseQuery(c.in)
		if ok != c.ok {
			t.Errorf("ParseQuery(%q) ok = %v, want %v", c.in, ok, c.ok)
			continue
		}
		if ok && (!close(got.Value, c.want) || got.Base != c.base) {
			t.Errorf("ParseQuery(%q) = %v %q, want %v %q", c.in, got.Value, got.Base, c.want, c.base)
		}
	}
}

// A bare number matches on magnitude alone, so "220" finds a 220 Ω resistor
// without the user naming the unit. That is the whole point, but it means a
// bare number also matches 220 anything, which callers should know.
func TestBareNumberIgnoresUnit(t *testing.T) {
	q, _ := ParseQuery("22")
	pf, _ := Parse("22", "pF")
	ohm, _ := Parse("22", "Ω")
	if !Matches(pf, q) || !Matches(ohm, q) {
		t.Error("a bare number should match on magnitude regardless of unit")
	}
}

// "0603" parsing as the number 603 is why package must be a separate filter and
// not something a value search is asked to interpret.
func TestPackageCodeIsNotAValue(t *testing.T) {
	q, ok := ParseQuery("0603")
	if !ok || q.Base != "" {
		t.Fatalf("expected 0603 to read as a bare number, got %+v ok=%v", q, ok)
	}
	res, _ := Parse("603", "Ω")
	if !Matches(res, q) {
		t.Error("sanity: 0603 reads as 603, which is why it must not be used as a value filter")
	}
}
