// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package httplib

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
)

// Server exposes the KiCad HTTP library endpoints.
//
// KiCad derives every path from root_url + "/" + api_version + "/", and the
// api_version it sends is compared against a hardcoded "v1" on its side. There
// is no negotiation and no v2, so these four routes are the entire surface:
//
//	GET /v1/                          endpoint validation
//	GET /v1/categories.json           the chooser tree
//	GET /v1/parts/category/{id}.json  parts in one category, with full detail
//	GET /v1/parts/{id}.json           one part
type Server struct {
	cache *Cache
	token string
	log   *slog.Logger
}

// NewServer builds the handler set.
func NewServer(cache *Cache, token string, log *slog.Logger) *Server {
	return &Server{cache: cache, token: token, log: log}
}

// Routes registers the library endpoints on a mux. Everything under /v1 is
// gated by the inbound token.
func (s *Server) Routes(mux *http.ServeMux) {
	mux.Handle("GET /v1/{$}", s.auth(http.HandlerFunc(s.handleValidate)))
	mux.Handle("GET /v1/categories.json", s.auth(http.HandlerFunc(s.handleCategories)))
	mux.Handle("GET /v1/parts/category/{id}", s.auth(http.HandlerFunc(s.handlePartsByCategory)))
	mux.Handle("GET /v1/parts/{id}", s.auth(http.HandlerFunc(s.handlePart)))
}

// handleValidate answers the endpoint check. KiCad verifies only that both
// keys exist; the values are ignored.
func (s *Server) handleValidate(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, validation{Categories: "", Parts: ""})
}

func (s *Server) handleCategories(w http.ResponseWriter, r *http.Request) {
	snap, err := s.cache.Get(r.Context())
	if err != nil {
		s.fail(w, "listing categories", err)
		return
	}
	// Always an array, never null: KiCad's parser expects a JSON array here.
	out := snap.Categories
	if out == nil {
		out = []Category{}
	}
	writeJSON(w, http.StatusOK, out)
}

// handlePartsByCategory returns the parts in a category as FULL detail
// objects, not the minimal {id, name, description} the KiCad dev-docs suggest.
//
// That is intentional and is the difference between this being usable and not.
// In KiCad 10 the Symbol Chooser calls the vector overload of
// EnumerateSymbolLib, which back-fills any part lacking details with an
// individual request. Returning complete objects here sets that flag up front
// and collapses N+1 requests into one.
func (s *Server) handlePartsByCategory(w http.ResponseWriter, r *http.Request) {
	id := trimJSON(r.PathValue("id"))
	snap, err := s.cache.Get(r.Context())
	if err != nil {
		s.fail(w, "listing parts", err)
		return
	}
	parts, ok := snap.ByCategory[id]
	if !ok {
		// An unknown category is an empty list, not a 404: KiCad discards the
		// whole library on any non-200, and one stale category id in its
		// process-lifetime cache should not take the rest down with it.
		s.log.Warn("unknown category requested", "category_id", id)
		parts = []Part{}
	}
	writeJSON(w, http.StatusOK, parts)
}

func (s *Server) handlePart(w http.ResponseWriter, r *http.Request) {
	id := trimJSON(r.PathValue("id"))
	snap, err := s.cache.Get(r.Context())
	if err != nil {
		s.fail(w, "reading part", err)
		return
	}
	part, ok := snap.ByID[id]
	if !ok {
		// 200 with a placeholder, not 404.
		//
		// KiCad caches part ids for the lifetime of the process, so a part
		// deleted or recategorised after the chooser was opened is requested
		// again by an id we no longer hold. A 404 there makes KiCad discard the
		// *entire* library mid-design, which is a wildly disproportionate
		// response to one stale id. This is the same reasoning that makes an
		// unknown category an empty array: asking for something absent inside a
		// working library is a 200, and only "the library is off" or "your
		// credential is bad" are non-200.
		writeJSON(w, http.StatusOK, Part{
			ID:   id,
			Name: s.cache.Marker() + "unknown part",
		})
		return
	}
	writeJSON(w, http.StatusOK, part)
}

// auth enforces `Authorization: Token <token>`, which is the only scheme KiCad
// speaks — note it is not Bearer, and not the scheme FireBin's own API uses.
// The comparison is constant-time so a response cannot be timed to recover the
// token byte by byte.
func (s *Server) auth(next http.Handler) http.Handler {
	want := []byte(s.token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Token ")))
		if len(got) == 0 || subtle.ConstantTimeCompare(got, want) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"error": "missing or invalid authorization header",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// fail logs the cause and returns a generic 502. The detail stays in the logs:
// KiCad surfaces almost none of it, and behind an ingress that rewrites error
// bodies the client would not see it anyway.
func (s *Server) fail(w http.ResponseWriter, what string, err error) {
	s.log.Error("upstream failure", "op", what, "error", err)
	writeJSON(w, http.StatusBadGateway, map[string]string{"error": "upstream FireBin API unavailable"})
}

// trimJSON strips the ".json" suffix KiCad appends to every resource path.
func trimJSON(s string) string {
	return strings.TrimSuffix(s, ".json")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WarmUp does the initial catalogue load. Called before the listener starts so
// the first chooser open is served from memory.
func (s *Server) WarmUp(ctx context.Context) error {
	return s.cache.Refresh(ctx)
}
