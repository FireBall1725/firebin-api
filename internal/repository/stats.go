// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import (
	"context"

	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

type StatsRepo struct{ pool *pgxpool.Pool }

func NewStatsRepo(pool *pgxpool.Pool) *StatsRepo { return &StatsRepo{pool: pool} }

// Get returns the dashboard summary in a single round trip.
func (r *StatsRepo) Get(ctx context.Context) (*models.Stats, error) {
	var s models.Stats
	err := r.pool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM parts WHERE variant_of IS NULL),
			(SELECT COUNT(*) FROM parts WHERE variant_of IS NOT NULL),
			(SELECT COUNT(*) FROM storage_locations),
			(SELECT COUNT(*) FROM parts p
				WHERE p.minimum_stock > 0
				  AND NOT EXISTS (SELECT 1 FROM parts v WHERE v.variant_of = p.id)
				  AND COALESCE((SELECT SUM(quantity) FROM stock_items s WHERE s.part_id = p.id), 0) <= p.minimum_stock),
			COALESCE((SELECT SUM(quantity) FROM stock_items), 0)::float8,
			COALESCE((SELECT SUM(quantity * purchase_price) FROM stock_items WHERE purchase_price IS NOT NULL), 0)::float8
	`).Scan(&s.PartsCount, &s.VariantsCount, &s.LocationsCount, &s.LowStockCount, &s.TotalUnits, &s.InventoryValue)
	if err != nil {
		return nil, err
	}
	return &s, nil
}
