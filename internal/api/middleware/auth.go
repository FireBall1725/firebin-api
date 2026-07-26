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
	"github.com/google/uuid"
)

type ctxKey int

const (
	ctxUserID ctxKey = iota
	ctxIsAdmin
	ctxScopes
	ctxRole
)

// Authenticator validates a Bearer credential — either a JWT access token or an
// `fbin_pat_` personal access token — and injects the caller identity into the
// request context.
type Authenticator struct {
	jwt    *auth.JWTService
	tokens *repository.TokenRepo
	users  *repository.UserRepo
}

func NewAuthenticator(jwt *auth.JWTService, tokens *repository.TokenRepo, users *repository.UserRepo) *Authenticator {
	return &Authenticator{jwt: jwt, tokens: tokens, users: users}
}

// Require wraps a handler, rejecting requests without a valid credential.
func (a *Authenticator) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := bearerToken(r)
		if raw == "" {
			respond.Error(w, http.StatusUnauthorized, "missing bearer token")
			return
		}

		var (
			userID uuid.UUID
			scopes []string
		)

		if auth.IsPAT(raw) {
			uid, sc, err := a.tokens.LookupPAT(r.Context(), auth.HashToken(raw))
			if err != nil {
				respond.Error(w, http.StatusUnauthorized, "invalid or expired token")
				return
			}
			userID, scopes = uid, sc
		} else {
			claims, err := a.jwt.Validate(raw)
			if err != nil {
				respond.Error(w, http.StatusUnauthorized, "invalid or expired token")
				return
			}
			revoked, err := a.tokens.IsAccessTokenRevoked(r.Context(), claims.ID)
			if err != nil || revoked {
				respond.Error(w, http.StatusUnauthorized, "token revoked")
				return
			}
			userID = claims.UserID
		}

		// Load the user to confirm it still exists and is active, and to get
		// the current admin flag (a PAT carries no admin claim of its own).
		u, err := a.users.GetByID(r.Context(), userID)
		if err != nil || !u.IsActive {
			respond.Error(w, http.StatusUnauthorized, "account not found or disabled")
			return
		}

		// Role is authoritative (loaded fresh each request, so a demotion or
		// deactivation takes effect immediately); admin is derived from it.
		ctx := context.WithValue(r.Context(), ctxUserID, userID)
		ctx = context.WithValue(ctx, ctxRole, u.Role)
		ctx = context.WithValue(ctx, ctxIsAdmin, u.Role == "admin")
		ctx = context.WithValue(ctx, ctxScopes, scopes)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireWriter wraps Require and rejects viewers on any state-changing method,
// so read-only accounts can browse and export but never mutate. Safe methods and
// a small self-service allowlist (changing your own password) pass through.
func (a *Authenticator) RequireWriter(next http.Handler) http.Handler {
	return a.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if Role(r.Context()) == "viewer" && !strings.HasSuffix(r.URL.Path, "/users/me/password") {
			respond.Error(w, http.StatusForbidden, "read-only account")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// RequireAdmin wraps Require and additionally rejects non-admin callers.
func (a *Authenticator) RequireAdmin(next http.Handler) http.Handler {
	return a.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !IsAdmin(r.Context()) {
			respond.Error(w, http.StatusForbidden, "instance admin required")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

// UserID returns the authenticated user id from the request context.
func UserID(ctx context.Context) uuid.UUID {
	if v, ok := ctx.Value(ctxUserID).(uuid.UUID); ok {
		return v
	}
	return uuid.Nil
}

// IsAdmin reports whether the authenticated caller is an instance admin.
func IsAdmin(ctx context.Context) bool {
	v, _ := ctx.Value(ctxIsAdmin).(bool)
	return v
}

// Role returns the authenticated caller's role (admin | member | viewer), or "".
func Role(ctx context.Context) string {
	v, _ := ctx.Value(ctxRole).(string)
	return v
}
