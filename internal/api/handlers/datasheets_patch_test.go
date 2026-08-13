// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"encoding/json"
	"testing"
)

// A partial update has to tell "field not sent" from "field sent as null". Both
// are meaningful and they are one keystroke apart in a decoded struct: without
// the distinction, the library page's category picker would wipe the title of
// every datasheet it filed, because its body carries no title key at all.
func TestDatasheetPatchDistinguishesAbsentFromNull(t *testing.T) {
	cases := []struct {
		body        string
		hasTitle    bool
		hasCategory bool
		titleNil    bool
	}{
		{`{"title":"ESP32-C6 Datasheet"}`, true, false, false},
		{`{"title":null}`, true, false, true},
		{`{"category_id":"6f1f1b1e-0000-4000-8000-000000000001"}`, false, true, false},
		{`{"category_id":null}`, false, true, false},
		{`{"title":"x","category_id":null}`, true, true, false},
		{`{}`, false, false, false},
	}
	for _, c := range cases {
		var p datasheetPatchRequest
		if err := json.Unmarshal([]byte(c.body), &p); err != nil {
			t.Fatalf("%s: %v", c.body, err)
		}
		if got := p.has("title"); got != c.hasTitle {
			t.Errorf("%s: has(title) = %v, want %v", c.body, got, c.hasTitle)
		}
		if got := p.has("category_id"); got != c.hasCategory {
			t.Errorf("%s: has(category_id) = %v, want %v", c.body, got, c.hasCategory)
		}
		if c.hasTitle && (p.Title == nil) != c.titleNil {
			t.Errorf("%s: Title nil = %v, want %v", c.body, p.Title == nil, c.titleNil)
		}
	}
}

// The alias trick inside UnmarshalJSON is the kind of thing that silently
// recurses if it is ever rewritten. A parse that returns is the assertion.
func TestDatasheetPatchDecodesValues(t *testing.T) {
	var p datasheetPatchRequest
	if err := json.Unmarshal([]byte(`{"title":"LM358","category_id":"6f1f1b1e-0000-4000-8000-000000000001"}`), &p); err != nil {
		t.Fatal(err)
	}
	if p.Title == nil || *p.Title != "LM358" {
		t.Errorf("title = %v, want LM358", p.Title)
	}
	if p.CategoryID == nil || p.CategoryID.String() != "6f1f1b1e-0000-4000-8000-000000000001" {
		t.Errorf("category_id = %v", p.CategoryID)
	}
}
