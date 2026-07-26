// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// FireBin API — self-hosted electronics component inventory.
// All protected endpoints require a Bearer JWT or `fbin_pat_*` personal access
// token in the Authorization header.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	// Embed the IANA timezone database so a distroless runtime can resolve
	// zone names without a filesystem zoneinfo.
	_ "time/tzdata"

	"github.com/firelabsca/firebin-api/internal/api"
	"github.com/firelabsca/firebin-api/internal/api/handlers"
	"github.com/firelabsca/firebin-api/internal/auth"
	"github.com/firelabsca/firebin-api/internal/config"
	"github.com/firelabsca/firebin-api/internal/db"
	"github.com/firelabsca/firebin-api/internal/jobs"
	"github.com/firelabsca/firebin-api/internal/version"
)

func main() {
	cfg := config.Load()

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	})))

	// Refuse to start without a JWT signing secret. An empty secret would sign
	// every token with []byte(""), which is trivially forgeable.
	if cfg.JWTSecret == "" {
		fmt.Fprintln(os.Stderr, "FATAL: JWT_SECRET is required and must not be empty")
		os.Exit(1)
	}

	slog.Info("firebin-api starting", "version", version.Version)

	baseCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	slog.Info("running database migrations")
	if err := db.Migrate(cfg.DatabaseURL); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}

	pool, err := db.Connect(baseCtx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	slog.Info("database connected")

	// River's own schema migrations, alongside FireBin's.
	if err := jobs.Migrate(baseCtx, pool); err != nil {
		slog.Error("river migration failed", "error", err)
		os.Exit(1)
	}

	jwt := auth.NewJWTService(cfg.JWTSecret, cfg.JWTAccessTTL)
	h, err := handlers.New(cfg, pool, jwt)
	if err != nil {
		slog.Error("handler init failed", "error", err)
		os.Exit(1)
	}
	h.Jobs.SetRetention(cfg.TaskRetention)
	if err := h.Jobs.Start(baseCtx); err != nil {
		slog.Error("job workers failed to start", "error", err)
		os.Exit(1)
	}
	slog.Info("job workers started")

	addr := cfg.Host + ":" + cfg.Port
	srv := &http.Server{
		Addr:           addr,
		Handler:        api.NewRouter(h),
		ReadTimeout:    15 * time.Second,
		WriteTimeout:   15 * time.Second,
		IdleTimeout:    60 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	go func() {
		slog.Info("listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down")
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutCancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		slog.Error("forced shutdown", "error", err)
	}
	// Drain in-flight jobs before tearing down the context and pool.
	if err := h.Jobs.Stop(shutCtx); err != nil {
		slog.Warn("job workers stop", "error", err)
	}
	cancel()
	slog.Info("stopped")
}
