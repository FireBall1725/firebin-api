// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/firelabsca/firebin-api/internal/auth"
)

// These cover the paths that short-circuit before any database access: the
// feature being off, and a credential that is not a KiCad token at all. The
// token repository is nil on purpose — if a change makes either path reach the
// store, these panic rather than quietly passing.

func kicadAuth(enabled bool) *KicadAuthenticator {
	return NewKicadAuthenticator(nil, func(context.Context) bool { return enabled })
}

func serve(t *testing.T, a *KicadAuthenticator, authorization string) *httptest.ResponseRecorder {
	t.Helper()
	reached := false
	h := a.Require(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))

	req := httptest.NewRequest(http.MethodGet, "/api/kicad-lib/v1/categories.json", nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK && reached {
		t.Error("handler ran despite a rejected request")
	}
	return rec
}

// Disabled is reported before the credential is even looked at, so a stale
// workstation config produces "the server is off" rather than sending its owner
// off to debug a token that is perfectly good.
func TestDisabledIsReportedBeforeAuth(t *testing.T) {
	valid, _, _, err := auth.GenerateKicadToken()
	if err != nil {
		t.Fatalf("GenerateKicadToken: %v", err)
	}

	for _, authz := range []string{"", "Token " + valid, "Token garbage"} {
		rec := serve(t, kicadAuth(false), authz)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("Authorization %q: got %d, want 503", authz, rec.Code)
		}
	}
}

func TestOnlyTheTokenSchemeAndOnlyKicadTokens(t *testing.T) {
	kicadToken, _, _, err := auth.GenerateKicadToken()
	if err != nil {
		t.Fatalf("GenerateKicadToken: %v", err)
	}
	pat, _, _, err := auth.GeneratePAT()
	if err != nil {
		t.Fatalf("GeneratePAT: %v", err)
	}

	cases := []struct {
		name  string
		authz string
	}{
		{"no header", ""},
		// KiCad sends Token. Accepting Bearer as well would mean a PAT could
		// reach these routes through the same door.
		{"right token, wrong scheme", "Bearer " + kicadToken},
		// The important one: a full-authority PAT presented under the KiCad
		// scheme is refused on its prefix, before any lookup.
		{"PAT under the Token scheme", "Token " + pat},
		{"not a token at all", "Token hello"},
		{"scheme only", "Token "},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := serve(t, kicadAuth(true), c.authz)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("got %d, want 401", rec.Code)
			}
			if got := rec.Header().Get("WWW-Authenticate"); got != "Token" {
				t.Errorf("WWW-Authenticate = %q, want %q", got, "Token")
			}
		})
	}
}
