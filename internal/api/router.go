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
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewRouter builds the top-level HTTP handler with all routes and middleware.
func NewRouter(pool *pgxpool.Pool, cfg *config.Config) http.Handler {
	jwtSvc := auth.NewJWTService(cfg.JWTSecret, cfg.JWTAccessTTL)
	h := handlers.New(cfg, pool, jwtSvc)
	authn := middleware.NewAuthenticator(jwtSvc, h.Tokens, h.Users)

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

	// Categories
	protected("GET /api/v1/categories", h.ListCategories)
	protected("POST /api/v1/categories", h.CreateCategory)
	protected("PATCH /api/v1/categories/{id}", h.UpdateCategory)
	protected("DELETE /api/v1/categories/{id}", h.DeleteCategory)

	// Parts
	protected("GET /api/v1/parts", h.ListParts)
	protected("POST /api/v1/parts", h.CreatePart)
	protected("GET /api/v1/parts/{id}", h.GetPart)
	protected("PATCH /api/v1/parts/{id}", h.UpdatePart)
	protected("DELETE /api/v1/parts/{id}", h.DeletePart)

	// Stock (scoped to a part)
	protected("GET /api/v1/parts/{id}/stock", h.ListPartStock)
	protected("GET /api/v1/parts/{id}/stock/history", h.ListPartStockHistory)
	protected("POST /api/v1/parts/{id}/stock/adjust", h.AdjustPartStock)
	protected("POST /api/v1/stock/move", h.MoveStock)

	// Locations
	protected("GET /api/v1/locations", h.ListLocations)
	protected("GET /api/v1/locations/scan", h.ScanLocation)
	protected("POST /api/v1/locations", h.CreateLocation)
	protected("GET /api/v1/locations/{id}", h.GetLocation)
	protected("PATCH /api/v1/locations/{id}", h.UpdateLocation)
	protected("DELETE /api/v1/locations/{id}", h.DeleteLocation)

	// Global middleware chain: security headers → request logging.
	return middleware.Chain(mux, middleware.SecurityHeaders, middleware.Logger)
}
