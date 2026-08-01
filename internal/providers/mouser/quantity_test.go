// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package mouser

import "testing"

// Mouser reports its minimum order quantity as a display string, so this is the
// difference between supplier_parts.moq being populated and being quietly empty
// the way it was for every row before it was read at all.
func TestParseQuantity(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"1", 1, true},
		{"10", 10, true},
		{"1,000", 1000, true}, // thousands separated
		{"2 500", 2500, true}, // space separated
		{"2 500", 2500, true}, // non-breaking space, which is what a locale formatter emits
		{"  25  ", 25, true},
		{"", 0, false},   // absent
		{"0", 0, false},  // no minimum is not a minimum of zero
		{"-5", 0, false}, // nonsense
		{"N/A", 0, false},
		{"1.5", 1.5, true}, // not expected, but a number is a number
	}
	for _, c := range cases {
		got, ok := parseQuantity(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parseQuantity(%q) = (%v, %v), want (%v, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}
