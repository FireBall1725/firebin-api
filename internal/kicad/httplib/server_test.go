// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package httplib

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/firelabsca/firebin-api/internal/kicad/httplib/source"
)

const testToken = "fbin_kicad_test"

// stubSource stands in for the repositories, offering one category with a part
// in it and one empty category. It replaces what used to be a fake HTTP
// upstream: the data now arrives in-process, so there is no wire to fake.
type stubSource struct {
	cats  []source.Category
	parts []source.Part
	err   error
}

func (s stubSource) Categories(context.Context) ([]source.Category, error) {
	return s.cats, s.err
}

func (s stubSource) Parts(context.Context) ([]source.Part, error) {
	return s.parts, s.err
}

func (s stubSource) Part(_ context.Context, id string) (*source.Part, error) {
	if s.err != nil {
		return nil, s.err
	}
	for i := range s.parts {
		if s.parts[i].ID == id {
			return &s.parts[i], nil
		}
	}
	return nil, errNoSuchPart
}

var errNoSuchPart = errors.New("no such part")

// Note the category names carry no Path: models.Category has no such field and
// GET /categories has never returned one, so testing against a Path here would
// be testing a value production never produces.
func defaultStub() stubSource {
	return stubSource{
		cats: []source.Category{
			{ID: "cat-resistors", Name: "Resistors"},
			{ID: "cat-empty", Name: "Empty"},
		},
		parts: []source.Part{sampleResistor()},
	}
}

func newTestServerFrom(t *testing.T, src Source) http.Handler {
	t.Helper()
	cache := NewCache(src, "(no symbol) ", time.Minute, slog.New(slog.DiscardHandler))
	lib := NewServer(cache, slog.New(slog.DiscardHandler))
	if err := lib.WarmUp(t.Context()); err != nil {
		t.Fatalf("warm up: %v", err)
	}
	mux := http.NewServeMux()
	lib.Routes(mux, "")
	return mux
}

func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	return newTestServerFrom(t, defaultStub())
}

func do(t *testing.T, h http.Handler, path, auth string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestValidationEndpoint covers `GET /v1/`. KiCad checks only that both keys
// are present, but their absence fails the whole library.
func TestValidationEndpoint(t *testing.T) {
	rec := do(t, newTestServer(t), "/v1/", "Token "+testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, k := range []string{"categories", "parts"} {
		if _, ok := body[k]; !ok {
			t.Errorf("validation response missing key %q", k)
		}
	}
}

// TestCategoriesOmitsEmptyOnes keeps dead-end sub-libraries out of the chooser.
func TestCategoriesOmitsEmptyOnes(t *testing.T) {
	rec := do(t, newTestServer(t), "/v1/categories.json", "Token "+testToken)
	var cats []Category
	if err := json.Unmarshal(rec.Body.Bytes(), &cats); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(cats) != 1 || cats[0].ID != "cat-resistors" {
		t.Fatalf("categories = %+v, want only cat-resistors", cats)
	}
}

// TestPartsByCategoryReturnsFullDetail is the performance contract. If these
// entries lack a populated `fields` object, KiCad 10 issues one extra request
// per part every time the Symbol Chooser opens.
func TestPartsByCategoryReturnsFullDetail(t *testing.T) {
	rec := do(t, newTestServer(t), "/v1/parts/category/cat-resistors.json", "Token "+testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var parts []Part
	if err := json.Unmarshal(rec.Body.Bytes(), &parts); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("got %d parts, want 1", len(parts))
	}
	if len(parts[0].Fields) == 0 {
		t.Error("listing returned no fields; KiCad would back-fill with a request per part")
	}
	if parts[0].Fields["value"].Value != "10k" {
		t.Errorf("value = %q, want 10k", parts[0].Fields["value"].Value)
	}
}

// TestUnknownCategoryIsEmptyNot404 matters because KiCad discards the entire
// library on any non-200, and it caches category ids for the life of the
// process.
func TestUnknownCategoryIsEmptyNot404(t *testing.T) {
	rec := do(t, newTestServer(t), "/v1/parts/category/does-not-exist.json", "Token "+testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := trimSpaceBody(t, rec.Body); got != "[]" {
		t.Errorf("body = %s, want []", got)
	}
}

// TestPartDetailEndpoint covers the per-part route, including the .json suffix
// KiCad appends to every path.
func TestPartDetailEndpoint(t *testing.T) {
	rec := do(t, newTestServer(t), "/v1/parts/"+sampleResistor().ID+".json", "Token "+testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var p Part
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.SymbolIDStr != "Device:R" {
		t.Errorf("symbolIdStr = %q, want Device:R", p.SymbolIDStr)
	}
}

func trimSpaceBody(t *testing.T, r io.Reader) string {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	for len(out) > 0 && (out[len(out)-1] == '\n' || out[len(out)-1] == ' ') {
		out = out[:len(out)-1]
	}
	return out
}
