// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type EnrichmentCacheRepo struct{ pool *pgxpool.Pool }

func NewEnrichmentCacheRepo(pool *pgxpool.Pool) *EnrichmentCacheRepo {
	return &EnrichmentCacheRepo{pool: pool}
}

// Get returns a cached enrichment result if present and fresher than 30 days.
func (r *EnrichmentCacheRepo) Get(ctx context.Context, mpn string) (*models.EnrichedPart, bool, error) {
	var raw []byte
	err := r.pool.QueryRow(ctx, `
		SELECT data FROM enrichment_cache
		WHERE mpn = $1 AND fetched_at > NOW() - INTERVAL '30 days'`, mpn).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var p models.EnrichedPart
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, false, err
	}
	return &p, true, nil
}

// Set upserts a cached enrichment result.
func (r *EnrichmentCacheRepo) Set(ctx context.Context, mpn string, p *models.EnrichedPart) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO enrichment_cache (mpn, data, source, fetched_at) VALUES ($1, $2, $3, NOW())
		ON CONFLICT (mpn) DO UPDATE SET data = EXCLUDED.data, source = EXCLUDED.source, fetched_at = NOW()`,
		mpn, data, p.Source)
	return err
}
