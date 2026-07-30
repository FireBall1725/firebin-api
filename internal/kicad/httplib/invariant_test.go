// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package httplib

import (
	"go/parser"
	"go/token"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// These two tests exist because this package broke the surrounding repo's most
// basic convention on purpose, and the breakage is invisible at a glance.
//
// Everywhere else in the API, reporting a problem with a proper 4xx/5xx is
// correct. Here it is a bug: KiCad abandons the whole library on any non-200,
// so a "not found" that looks entirely reasonable in review empties a user's
// Symbol Chooser with no diagnostic. A comment does not survive contact with a
// contributor who has not read it. These do.

// TestPackageDoesNotImportRespond keeps the API's response helpers out of reach.
//
// respond.Error is the idiomatic way to answer in every sibling package, and
// this is the one place it must not be used. Removing the temptation entirely is
// more reliable than asking people to remember the exception, so this fails the
// moment the import appears.
func TestPackageDoesNotImportRespond(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range f.Imports {
			if strings.Contains(imp.Path.Value, "api/respond") {
				t.Errorf("%s imports %s.\n\n"+
					"This package answers KiCad, which discards the entire library on any\n"+
					"non-200. Use the local writeJSON helper. If you genuinely need a new\n"+
					"non-200, add it to the allowlist in TestReadsAreAlways200 first and say\n"+
					"why there.", name, imp.Path.Value)
			}
		}
	}
}

// TestReadsAreAlways200 walks every data route against a source that is empty,
// that errors, and that is asked for ids it does not hold, and asserts none of
// it produces a non-200.
//
// The allowlist is the point: the two deliberate non-200s live here explicitly,
// so adding a third means editing this test and reading this comment.
func TestReadsAreAlways200(t *testing.T) {
	cases := []struct {
		name string
		src  Source
	}{
		{"populated", defaultStub()},
		{"empty catalogue", stubSource{}},
		{"source erroring", stubSource{err: errNoSuchPart}},
	}

	// Every route, including ids that cannot resolve in any of the sources.
	paths := []string{
		"/v1/",
		"/v1/categories.json",
		"/v1/parts/category/cat-resistors.json",
		"/v1/parts/category/does-not-exist.json",
		"/v1/parts/category/uncategorized.json",
		"/v1/parts/" + sampleResistor().ID + ".json",
		"/v1/parts/00000000-0000-0000-0000-000000000000.json",
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Built without WarmUp for the erroring and empty sources, so the
			// cold-snapshot path is covered too.
			cache := NewCache(tc.src, "(no symbol) ", time.Minute, slog.New(slog.DiscardHandler))
			lib := NewServer(cache, testToken, slog.New(slog.DiscardHandler))
			_ = lib.WarmUp(t.Context()) // may fail; that is one of the cases
			mux := http.NewServeMux()
			lib.Routes(mux)

			for _, p := range paths {
				rec := do(t, mux, p, "Token "+testToken)
				if rec.Code != http.StatusOK {
					t.Errorf("GET %s returned %d, want 200.\n\n"+
						"KiCad discards the whole library on a non-200. The only sanctioned\n"+
						"non-200s are 401 for a bad credential and 503 for the feature being\n"+
						"switched off; both are decided before these handlers run.",
						p, rec.Code)
				}
			}
		})
	}
}
