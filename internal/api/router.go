// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package api wires the HTTP routes for the FireBin API.
package api

import (
	"net/http"

	"github.com/firelabsca/firebin-api/internal/api/handlers"
	"github.com/firelabsca/firebin-api/internal/api/middleware"
	"github.com/firelabsca/firebin-api/internal/auth"
	"github.com/firelabsca/firebin-api/internal/config"
	"github.com/firelabsca/firebin-api/internal/repository"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewRouter builds the top-level HTTP handler with all routes and middleware.
func NewRouter(pool *pgxpool.Pool, cfg *config.Config) http.Handler {
	jwtSvc := auth.NewJWTService(cfg.JWTSecret, cfg.JWTAccessTTL)
	userRepo := repository.NewUserRepo(pool)
	tokenRepo := repository.NewTokenRepo(pool)

	h := handlers.New(cfg, jwtSvc, userRepo, tokenRepo)
	authn := middleware.NewAuthenticator(jwtSvc, tokenRepo, userRepo)

	mux := http.NewServeMux()

	// ── Public ────────────────────────────────────────────────────────────────
	mux.HandleFunc("GET /api/v1/health", h.Health)
	mux.HandleFunc("POST /api/v1/auth/register", h.Register)
	mux.HandleFunc("POST /api/v1/auth/login", h.Login)
	mux.HandleFunc("POST /api/v1/auth/refresh", h.Refresh)
	mux.HandleFunc("POST /api/v1/auth/logout", h.Logout)

	// ── Authenticated ───────────────────────────────────────────────────────
	protected := func(pattern string, fn http.HandlerFunc) {
		mux.Handle(pattern, authn.Require(fn))
	}
	protected("GET /api/v1/me", h.Me)
	protected("POST /api/v1/tokens", h.CreatePAT)
	protected("GET /api/v1/tokens", h.ListPATs)
	protected("DELETE /api/v1/tokens/{id}", h.RevokePAT)

	// Global middleware chain: security headers → request logging.
	return middleware.Chain(mux, middleware.SecurityHeaders, middleware.Logger)
}
