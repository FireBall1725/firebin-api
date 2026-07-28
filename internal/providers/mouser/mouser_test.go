// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package mouser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Mouser sends price as a formatted display string rather than a number, and
// the formatting follows the account locale. Getting this wrong turns $1,234.56
// into 1.23 or drops the break entirely, so every shape gets a case.
func TestParsePrice(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"$0.42", 0.42, true},
		{"0.42", 0.42, true},
		{"US$ 1.23", 1.23, true},
		{"$1,234.56", 1234.56, true},  // en grouping
		{"1.234,56 €", 1234.56, true}, // de grouping
		{"0,42 €", 0.42, true},        // comma as decimal
		{"1,234", 1234, true},         // comma as grouping: 3 digits after
		{"$12", 12, true},
		{"", 0, false},
		{"N/A", 0, false},
		{"Quote", 0, false},
	}
	for _, c := range cases {
		got, ok := parsePrice(c.in)
		if ok != c.ok {
			t.Errorf("parsePrice(%q) ok = %v, want %v", c.in, ok, c.ok)
			continue
		}
		if ok && got != c.want {
			t.Errorf("parsePrice(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestPickMatchPrefersExactMPN(t *testing.T) {
	parts := []msPart{
		{ManufacturerPartNumber: "SOMETHING-ELSE"},
		{ManufacturerPartNumber: "moc-3063s"}, // case and punctuation differ
	}
	got := pickMatch("MOC3063S", parts)
	if got == nil || got.ManufacturerPartNumber != "moc-3063s" {
		t.Errorf("expected the normalized MPN match, got %+v", got)
	}

	// No exact match falls back to the first result rather than nothing.
	got = pickMatch("NOT-PRESENT", parts)
	if got == nil || got.ManufacturerPartNumber != "SOMETHING-ELSE" {
		t.Errorf("expected the first result as fallback, got %+v", got)
	}

	if pickMatch("X", nil) != nil {
		t.Error("expected nil for no parts")
	}
}

func TestMapPart(t *testing.T) {
	p := msPart{
		MouserPartNumber:       "512-MOC3063S",
		ManufacturerPartNumber: "MOC3063S",
		Manufacturer:           "Lite-On",
		Description:            "Optoisolator Triac",
		DataSheetURL:           "https://example.test/ds.pdf",
		ImagePath:              "https://example.test/img.jpg",
		Category:               "Optoisolators",
		ProductDetailURL:       "https://example.test/p",
	}
	p.PriceBreaks = []struct {
		Quantity int    `json:"Quantity"`
		Price    string `json:"Price"`
		Currency string `json:"Currency"`
	}{
		{Quantity: 1, Price: "$0.79", Currency: "CAD"},
		{Quantity: 100, Price: "$0.61", Currency: ""}, // falls back to configured
		{Quantity: 500, Price: "Quote"},               // unparseable, must be dropped
	}
	p.ProductAttributes = []struct {
		AttributeName  string `json:"AttributeName"`
		AttributeValue string `json:"AttributeValue"`
	}{
		{AttributeName: "Package / Case", AttributeValue: "6-SMD"},
		{AttributeName: "Mounting Style", AttributeValue: "SMD/SMT"},
		{AttributeName: "Blank", AttributeValue: "-"}, // filler, must be dropped
	}

	out := mapPart(p, "CAD")

	if out.MPN != "MOC3063S" || out.Manufacturer != "Lite-On" || out.Source != "mouser" {
		t.Errorf("scalar mapping wrong: %+v", out)
	}
	if out.Package != "6-SMD" {
		t.Errorf("Package = %q, want 6-SMD (Mounting Style must not win)", out.Package)
	}
	if len(out.Parameters) != 2 {
		t.Errorf("expected the placeholder '-' attribute dropped, got %d params", len(out.Parameters))
	}
	if len(out.Suppliers) != 1 {
		t.Fatalf("expected 1 supplier, got %d", len(out.Suppliers))
	}
	prices := out.Suppliers[0].Prices
	if len(prices) != 2 {
		t.Fatalf("expected the unparseable break dropped, got %d breaks", len(prices))
	}
	if prices[0].Price != 0.79 || prices[0].Currency != "CAD" {
		t.Errorf("break 0 = %+v", prices[0])
	}
	if prices[1].Currency != "CAD" {
		t.Errorf("empty currency should fall back to the configured one, got %q", prices[1].Currency)
	}
}

// A bad key comes back as HTTP 200 with an Errors array, so status alone is not
// enough to treat a response as success.
func TestErrorsInsideTwoHundred(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Errors":        []map[string]string{{"Code": "Invalid", "Message": "Invalid unique identifier."}},
			"SearchResults": map[string]any{},
		})
	}))
	defer srv.Close()

	p := New(func(context.Context) Credentials {
		return Credentials{APIKey: "bad", BaseURL: srv.URL}
	})
	_, err := p.Enrich(context.Background(), "MOC3063S")
	if err == nil {
		t.Fatal("expected an error for an Errors array inside a 200")
	}
}

func TestEnrichNoMatchIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Errors":        []any{},
			"SearchResults": map[string]any{"NumberOfResult": 0, "Parts": []any{}},
		})
	}))
	defer srv.Close()

	p := New(func(context.Context) Credentials {
		return Credentials{APIKey: "k", BaseURL: srv.URL}
	})
	got, err := p.Enrich(context.Background(), "NOPE")
	if err != nil {
		t.Fatalf("no match should not error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for no match, got %+v", got)
	}
}

func TestEnrichSendsKeyAndExactOption(t *testing.T) {
	var gotKey, gotOption, gotTerm string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.URL.Query().Get("apiKey")
		var body struct {
			SearchByPartRequest struct {
				MouserPartNumber  string `json:"mouserPartNumber"`
				PartSearchOptions string `json:"partSearchOptions"`
			} `json:"SearchByPartRequest"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotTerm = body.SearchByPartRequest.MouserPartNumber
		gotOption = body.SearchByPartRequest.PartSearchOptions
		_ = json.NewEncoder(w).Encode(map[string]any{
			"SearchResults": map[string]any{"Parts": []msPart{{ManufacturerPartNumber: "MOC3063S"}}},
		})
	}))
	defer srv.Close()

	p := New(func(context.Context) Credentials {
		return Credentials{APIKey: "secret-key", BaseURL: srv.URL}
	})
	if _, err := p.Enrich(context.Background(), "MOC3063S"); err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if gotKey != "secret-key" {
		t.Errorf("apiKey query param = %q", gotKey)
	}
	if gotTerm != "MOC3063S" || gotOption != "Exact" {
		t.Errorf("request body term=%q option=%q", gotTerm, gotOption)
	}
}

func TestNotConfigured(t *testing.T) {
	p := New(func(context.Context) Credentials { return Credentials{} })
	if p.Configured(context.Background()) {
		t.Error("expected not configured with an empty key")
	}
	if _, err := p.Enrich(context.Background(), "X"); err != ErrNotConfigured {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
	if err := p.Ping(context.Background()); err != ErrNotConfigured {
		t.Errorf("Ping expected ErrNotConfigured, got %v", err)
	}
}
