// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package config

import (
	"log/slog"
	"os"
	"strings"
	"time"
)

// Config holds all runtime configuration, loaded from environment variables.
type Config struct {
	Host                string
	Port                string
	DatabaseURL         string
	JWTSecret           string
	JWTAccessTTL        time.Duration
	JWTRefreshTTL       time.Duration
	RegistrationEnabled bool
	LogLevel            slog.Level

	// AttachmentStoragePath is the BYO filesystem root for datasheets, part
	// images, and STEP models. The DB stores path references, not blobs.
	AttachmentStoragePath string
}

func Load() *Config {
	return &Config{
		Host:                  getEnv("HOST", "0.0.0.0"),
		Port:                  getEnv("PORT", "8080"),
		DatabaseURL:           getEnv("DATABASE_URL", "postgres://firebin:firebin@localhost:5432/firebin?sslmode=disable"),
		JWTSecret:             getEnv("JWT_SECRET", ""),
		JWTAccessTTL:          parseDuration(getEnv("JWT_ACCESS_TTL", "30m")),
		JWTRefreshTTL:         parseDuration(getEnv("JWT_REFRESH_TTL", "720h")),
		RegistrationEnabled:   getEnv("REGISTRATION_ENABLED", "true") != "false",
		LogLevel:              parseLogLevel(getEnv("LOG_LEVEL", "info")),
		AttachmentStoragePath: getEnv("ATTACHMENT_STORAGE_PATH", "./data/attachments"),
	}
}

func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return fallback
}

func parseDuration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0
	}
	return d
}

func parseLogLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
