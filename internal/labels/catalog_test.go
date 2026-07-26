// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package labels

import (
	"math"
	"testing"
)

func find(t *testing.T, code string) CatalogEntry {
	t.Helper()
	for _, e := range Catalog() {
		if e.Code == code {
			return e
		}
	}
	t.Fatalf("catalog entry %q not found", code)
	return CatalogEntry{}
}

func approx(a, b float64) bool { return math.Abs(a-b) < 0.5 }

func TestCatalogParses(t *testing.T) {
	all := Catalog()
	if len(all) < 200 {
		t.Fatalf("expected a few hundred catalogue entries, got %d", len(all))
	}

	// Base rectangular template with known 2"x4" geometry.
	e := find(t, "5163")
	if !approx(e.LabelW, 288) || !approx(e.LabelH, 144) || e.Cols != 2 || e.Rows != 5 {
		t.Errorf("5163 geometry wrong: %+v", e)
	}
	if !approx(e.PageW, 612) || !approx(e.PageH, 792) {
		t.Errorf("5163 page size wrong: %+v", e)
	}

	// Alias (5260 equiv 5160) must resolve to the base 1"x2-5/8" geometry.
	a := find(t, "5260")
	if !approx(a.LabelW, 189) || !approx(a.LabelH, 72) || a.Cols != 3 || a.Rows != 10 {
		t.Errorf("5260 alias geometry wrong: %+v", a)
	}

	// A4 entry present with A4 page dimensions.
	l7160 := find(t, "7160")
	if !approx(l7160.PageW, 595.28) {
		t.Errorf("7160 should be A4, got page_w %.2f", l7160.PageW)
	}
}

func TestSearchCatalog(t *testing.T) {
	if got := SearchCatalog("5163", 10); len(got) == 0 || got[0].Code != "5163" {
		t.Errorf("search 5163 failed: %+v", got)
	}
	if got := SearchCatalog("", 5); len(got) != 5 {
		t.Errorf("empty search should cap at limit, got %d", len(got))
	}
}
