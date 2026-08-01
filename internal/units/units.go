// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package units reduces an engineering quantity to a number you can compare.
//
// FireBin stores a parameter's magnitude and its prefixed unit in separate
// columns: 100 kΩ is value "100" with units "kΩ", and 33 Ω is value "33" with
// units "Ω". Those two live in the same column and differ by a factor of a
// thousand, so comparing the value alone finds a 100 kΩ resistor when asked for
// 100 Ω. Every search over parameters has to come through here first.
package units

import (
	"strconv"
	"strings"
)

// Quantity is a magnitude reduced to its base unit: ohms, farads, henries and
// so on, never kilohms or picofarads.
type Quantity struct {
	Value float64
	Base  string // canonical base symbol, "" when the parameter is unitless
	// Shown is the magnitude as a person reads it: 22 for "22 pF", 100 for
	// "100 kΩ". Kept alongside the normalised value because a search box gets
	// both kinds of question. "220 ohm" is a claim about a real quantity and
	// must compare in base units; a bare "220" is someone reading a list and
	// meaning the number they can see, which in base units would be 2.2e-10.
	Shown float64
	// Bare records that the query named neither a prefix nor a unit, which is
	// what makes it the loose kind of question.
	Bare bool
}

// baseUnits maps every spelling of a base unit onto one canonical symbol, so
// "ohm", "Ohms" and both Unicode omegas compare equal. U+03A9 is the Greek
// capital omega and U+2126 is the ohm sign; enrichment produces both.
var baseUnits = map[string]string{
	// Escaped rather than literal: U+03A9 and U+2126 render identically, so as
	// literals they look like a duplicate key and an editor or a copy-paste will
	// quietly collapse them into one. Enrichment emits both.
	"\u03a9": "\u03a9", "\u2126": "\u03a9", "ohm": "\u03a9", "ohms": "\u03a9", "r": "\u03a9",
	"f": "F", "farad": "F", "farads": "F",
	"h": "H", "henry": "H", "henries": "H",
	"v": "V", "volt": "V", "volts": "V",
	"a": "A", "amp": "A", "amps": "A", "ampere": "A", "amperes": "A",
	"w": "W", "watt": "W", "watts": "W",
	"hz": "Hz", "hertz": "Hz",
}

// prefixes are SI multipliers. Case matters and is not a typo: lowercase m is
// milli and uppercase M is mega, a factor of a billion apart. Uppercase K is
// accepted as kilo because people type it constantly, which is safe only
// because no SI prefix uses that letter for anything else.
var prefixes = map[rune]float64{
	'p': 1e-12, 'n': 1e-9, 'u': 1e-6, 'µ': 1e-6, 'μ': 1e-6,
	'm': 1e-3, 'c': 1e-2, 'd': 1e-1,
	'k': 1e3, 'K': 1e3, 'M': 1e6, 'G': 1e9, 'T': 1e12,
}

// Parse reduces a stored value and unit to a comparable quantity.
//
// The unit may carry a prefix ("kΩ", "µF") or be a bare base ("Ω"). A value that
// already carries its own prefix ("10k") is handled too, since hand-entered
// parameters do that and enrichment does not always normalise them.
func Parse(value, unit string) (Quantity, bool) {
	v, suffix, ok := splitNumber(strings.TrimSpace(value))
	if !ok {
		return Quantity{}, false
	}

	// A prefix stuck to the number wins over the unit column, because that is
	// where the person who typed "10k" put it.
	unit = strings.TrimSpace(unit)
	if suffix != "" {
		if mult, base, ok := splitUnit(suffix); ok {
			if base == "" {
				_, base, _ = splitUnit(unit) // "10k" + "Ω"
			}
			return Quantity{Value: v * mult, Base: base, Shown: v}, true
		}
		return Quantity{}, false
	}

	mult, base, ok := splitUnit(unit)
	if !ok {
		return Quantity{}, false
	}
	return Quantity{Value: v * mult, Base: base, Shown: v}, true
}

// ParseQuery reads what someone types into a search box: "220", "220 ohm",
// "4.7uF", "10k", "100nF".
func ParseQuery(s string) (Quantity, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Quantity{}, false
	}
	v, suffix, ok := splitNumber(s)
	if !ok {
		return Quantity{}, false
	}
	if suffix == "" {
		return Quantity{Value: v, Shown: v, Bare: true}, true
	}
	mult, base, ok := splitUnit(suffix)
	if !ok {
		return Quantity{}, false
	}
	return Quantity{Value: v * mult, Base: base, Shown: v}, true
}

// Matches reports whether a stored quantity satisfies a query.
//
// A query with no unit ("220") matches on magnitude alone, which is what makes
// "220" find a 220 Ω resistor without the user spelling out the unit. A query
// that names a unit must agree on it, so "220 ohm" never matches 220 pF.
//
// The tolerance is relative and deliberately tight. Component values are
// discrete, so the nearest neighbours are percent apart, and floating point is
// the only reason not to compare exactly.
func Matches(stored, query Quantity) bool {
	// A bare number means the magnitude on the label, so "22" finds both a 22 pF
	// capacitor and a 22 Ω resistor. Comparing base units here would find
	// neither, since 22 pF is 2.2e-11 of anything.
	if query.Bare {
		return near(stored.Shown, query.Shown)
	}
	if query.Base != "" && stored.Base != query.Base {
		return false
	}
	return near(stored.Value, query.Value)
}

// near compares magnitudes with a relative tolerance. Component values are
// discrete and their neighbours are percent apart, so this is only here because
// 100 x 1e-9 is not bit-identical to 1e-7.
func near(a, b float64) bool {
	if b == 0 {
		return a == 0
	}
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff/b < 1e-9
}

// splitNumber peels a leading number off a string, returning it with whatever
// followed. "4.7uF" gives 4.7 and "uF"; "220" gives 220 and "".
func splitNumber(s string) (float64, string, bool) {
	i := 0
	for i < len(s) {
		c := s[i]
		if (c >= '0' && c <= '9') || c == '.' || (i == 0 && (c == '-' || c == '+')) {
			i++
			continue
		}
		break
	}
	if i == 0 {
		return 0, "", false
	}
	v, err := strconv.ParseFloat(s[:i], 64)
	if err != nil {
		return 0, "", false
	}
	return v, strings.TrimSpace(s[i:]), true
}

// splitUnit separates an SI prefix from a base unit, returning the multiplier
// and the canonical base. An empty unit is unitless with multiplier 1.
func splitUnit(u string) (float64, string, bool) {
	u = strings.TrimSpace(u)
	if u == "" {
		return 1, "", true
	}
	if base, ok := lookupBase(u); ok {
		return 1, base, true // whole thing is a base unit, no prefix
	}

	r := []rune(u)
	mult, isPrefix := prefixes[r[0]]
	if !isPrefix {
		return 0, "", false
	}
	rest := strings.TrimSpace(string(r[1:]))
	if rest == "" {
		return mult, "", true // a bare prefix, as in "10k"
	}
	base, ok := lookupBase(rest)
	if !ok {
		return 0, "", false
	}
	return mult, base, true
}

// lookupBase resolves a base unit, tolerating case for the spelled-out forms.
//
// The exact match has to come first. Lowercasing Ω (U+03A9) yields ω (U+03C9), a
// different character that is not a unit, so folding case before looking up
// silently broke every ohm value — which is most of the inventory.
func lookupBase(u string) (string, bool) {
	if base, ok := baseUnits[u]; ok {
		return base, true
	}
	base, ok := baseUnits[strings.ToLower(u)]
	return base, ok
}
