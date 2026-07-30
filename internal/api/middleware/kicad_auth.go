// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/firelabsca/firebin-api/internal/api/respond"
	"github.com/firelabsca/firebin-api/internal/auth"
	"github.com/firelabsca/firebin-api/internal/repository"
)

// KicadAuthenticator guards the KiCad HTTP library routes.
//
// Separate from Authenticator because KiCad speaks a different scheme and a
// different credential store. It sends `Authorization: Token <t>`, not Bearer,
// and the tokens resolve against kicad_library_tokens rather than api_tokens. No
// user is loaded and nothing is put in the request context: these routes read
// the catalogue and have no notion of a caller.
//
// Note this middleware may legitimately answer non-200, unlike the handlers
// behind it. Those must always answer 200 because KiCad discards the whole
// library otherwise; "the feature is off" and "your credential is wrong" are the
// two cases where making it discard the library is the correct outcome.
type KicadAuthenticator struct {
	tokens *repository.KicadLibraryTokenRepo
	// enabled is read per request so toggling the feature in Settings takes
	// effect immediately, with no restart and no cached flag to go stale.
	enabled func(ctx context.Context) bool
}

func NewKicadAuthenticator(tokens *repository.KicadLibraryTokenRepo, enabled func(ctx context.Context) bool) *KicadAuthenticator {
	return &KicadAuthenticator{tokens: tokens, enabled: enabled}
}

// Require rejects anything without a live KiCad workstation token.
func (a *KicadAuthenticator) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Checked before the credential, on purpose. Someone with a stale config
		// then learns the feature is switched off, which is the diagnostic they
		// need, instead of being sent to debug a token that is perfectly fine.
		//
		// 503 rather than 404: the endpoint exists and an operator turned it off,
		// so no amount of fixing root_url or reissuing tokens will help. It also
		// matches how the settings handlers already report a disabled provider.
		if !a.enabled(r.Context()) {
			respond.Error(w, http.StatusServiceUnavailable, "the KiCad library server is disabled")
			return
		}

		raw := schemeToken(r)
		if raw == "" || !auth.IsKicadToken(raw) {
			a.unauthorized(w)
			return
		}

		// Unknown and revoked are answered identically. Distinguishing them would
		// let anyone holding a revoked token probe which others exist, and the
		// admin UI already shows which tokens are revoked to the only people who
		// need to know.
		_, ok, err := a.tokens.Lookup(r.Context(), auth.HashToken(raw))
		if err != nil || !ok {
			a.unauthorized(w)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (a *KicadAuthenticator) unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Token")
	respond.Error(w, http.StatusUnauthorized, "missing or invalid authorization header")
}

// schemeToken reads an `Authorization: Token <t>` header.
//
// The sibling of bearerToken in auth.go, and separate rather than parameterised
// so neither scheme can start accepting the other by accident.
func schemeToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Token") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}
