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

	// TaskRetention: finished background tasks (and their logs) older than this
	// are pruned by the job service. 0 keeps them forever.
	TaskRetention time.Duration

	// AttachmentStoragePath is the BYO filesystem root for datasheets, part
	// images, and STEP models. The DB stores path references, not blobs.
	AttachmentStoragePath string

	// AppBaseURL is the public URL of the web app, used to build QR deep-links on
	// printed labels (e.g. <base>/parts/{id}).
	AppBaseURL string

	// Nexar (Octopart) enrichment credentials. Client-credentials OAuth app
	// from nexar.com. Empty = enrichment disabled.
	NexarClientID     string
	NexarClientSecret string
	NexarScope        string

	// Digi-Key Product Information V4 enrichment credentials. 2-legged
	// (client-credentials) OAuth app from developer.digikey.com; free, no credit
	// account needed for lookups. Locale controls the catalogue/currency.
	DigiKeyClientID     string
	DigiKeyClientSecret string
	DigiKeyBaseURL      string // empty → production (sandbox-api.digikey.com for sandbox)
	DigiKeySite         string // locale site + ship-to country, e.g. "CA"
	DigiKeyLanguage     string // e.g. "en"
	DigiKeyCurrency     string // e.g. "CAD"

	// Mouser Search API credentials. A single key from mouser.com/api-search,
	// no OAuth step. Published limits are 30 calls/minute and 1000/day, so this
	// sits behind Digi-Key in the default chain.
	MouserAPIKey  string
	MouserBaseURL string // empty → production
}

func Load() *Config {
	return &Config{
		Host:          getEnv("HOST", "0.0.0.0"),
		Port:          getEnv("PORT", "8080"),
		DatabaseURL:   getEnv("DATABASE_URL", "postgres://firebin:firebin@localhost:5432/firebin?sslmode=disable"),
		JWTSecret:     getEnv("JWT_SECRET", ""),
		JWTAccessTTL:  parseDuration(getEnv("JWT_ACCESS_TTL", "30m")),
		JWTRefreshTTL: parseDuration(getEnv("JWT_REFRESH_TTL", "720h")),
		// Off by default: the first user still bootstraps the instance admin, and
		// admins add everyone else. Set REGISTRATION_ENABLED=true to allow open
		// self-signup.
		RegistrationEnabled:   getEnv("REGISTRATION_ENABLED", "false") == "true",
		TaskRetention:         parseDuration(getEnv("TASK_RETENTION", "720h")), // 30 days
		LogLevel:              parseLogLevel(getEnv("LOG_LEVEL", "info")),
		AttachmentStoragePath: getEnv("ATTACHMENT_STORAGE_PATH", "./data/attachments"),
		AppBaseURL:            strings.TrimRight(getEnv("APP_BASE_URL", "http://localhost:5173"), "/"),
		NexarClientID:         getEnv("NEXAR_CLIENT_ID", ""),
		NexarClientSecret:     getEnv("NEXAR_CLIENT_SECRET", ""),
		NexarScope:            getEnv("NEXAR_SCOPE", "supply.domain"),
		DigiKeyClientID:       getEnv("DIGIKEY_CLIENT_ID", ""),
		DigiKeyClientSecret:   getEnv("DIGIKEY_CLIENT_SECRET", ""),
		DigiKeyBaseURL:        getEnv("DIGIKEY_BASE_URL", ""),
		DigiKeySite:           getEnv("DIGIKEY_SITE", "CA"),
		DigiKeyLanguage:       getEnv("DIGIKEY_LANGUAGE", "en"),
		DigiKeyCurrency:       getEnv("DIGIKEY_CURRENCY", "CAD"),
		MouserAPIKey:          getEnv("MOUSER_API_KEY", ""),
		MouserBaseURL:         getEnv("MOUSER_BASE_URL", ""),
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
