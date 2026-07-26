// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package providers defines the common contract for MPN enrichment sources
// (Nexar/Octopart, Digi-Key, …) so the handler can try several in order.
package providers

import (
	"context"

	"github.com/firelabsca/firebin-api/internal/models"
)

// Enricher is a parts-data source that resolves an MPN to normalized part data.
// Implementations resolve their own credentials on demand (from DB settings,
// falling back to env) so keys can be entered in the UI without a restart.
type Enricher interface {
	// Name is the stable provider id ("nexar", "digikey").
	Name() string
	// Label is the human-facing provider name shown in the UI.
	Label() string
	// Configured reports whether credentials are currently present.
	Configured(ctx context.Context) bool
	// Ping validates the credentials cheaply (mint a token); it must NOT spend a
	// metered lookup against the provider's quota.
	Ping(ctx context.Context) error
	// Enrich returns normalized part data, or (nil, nil) for no match.
	Enrich(ctx context.Context, mpn string) (*models.EnrichedPart, error)
}
