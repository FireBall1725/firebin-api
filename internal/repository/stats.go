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
			-- Matches ListLowStock, reference exclusion included. A count that
			-- disagrees with the list it heads is worse than no count.
			(SELECT COUNT(*) FROM parts p
				WHERE p.minimum_stock > 0
				  AND NOT COALESCE(p.reference_only, false)
				  AND NOT EXISTS (SELECT 1 FROM parts v WHERE v.variant_of = p.id)
				  AND COALESCE((SELECT SUM(quantity) FROM stock_items s WHERE s.part_id = p.id), 0) <= p.minimum_stock),
			COALESCE((SELECT SUM(quantity) FROM stock_items), 0)::float8,
			(SELECT COUNT(*) FROM parts WHERE COALESCE(reference_only, false)),
			(SELECT COUNT(*) FROM board_bom_lines WHERE part_id IS NULL),
			(SELECT COUNT(*) FROM parts WHERE variant_of IS NULL
				AND COALESCE(NULLIF(kicad_symbol, ''), NULL) IS NULL),
			(SELECT COUNT(*) FROM stock_transactions WHERE created_at > now() - interval '30 days')
	`).Scan(&s.PartsCount, &s.VariantsCount, &s.LocationsCount, &s.LowStockCount, &s.TotalUnits,
		&s.NotStockedCount, &s.UnmatchedBOMLines, &s.PartsWithoutSymbol, &s.Moves30d)
	if err != nil {
		return nil, err
	}
	if s.Movement, err = r.movement(ctx); err != nil {
		return nil, err
	}
	if s.Boards, err = r.boardFill(ctx); err != nil {
		return nil, err
	}
	return &s, nil
}

// movement returns one row per day for the last 30 days, zeros included.
//
// generate_series supplies the days so a quiet stretch is a flat run rather
// than a gap the chart closes up. A sparkline that silently omits empty days
// draws a busy week and a dead one identically.
func (r *StatsRepo) movement(ctx context.Context) ([]models.DayCount, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT to_char(d.day, 'YYYY-MM-DD'), COALESCE(t.n, 0)::int
		FROM generate_series(
			(now() - interval '29 days')::date, now()::date, interval '1 day'
		) AS d(day)
		LEFT JOIN (
			SELECT created_at::date AS day, COUNT(*) AS n
			FROM stock_transactions
			WHERE created_at > now() - interval '30 days'
			GROUP BY 1
		) t ON t.day = d.day
		ORDER BY d.day`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.DayCount{}
	for rows.Next() {
		var d models.DayCount
		if err := rows.Scan(&d.Day, &d.Count); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// boardFill reports how far each board is from buildable, for one of each.
//
// Two numbers, because they fail differently. Short counts matched parts the
// shelf cannot cover. Unmatched counts lines that resolve to no part at all,
// which no stock comparison can see: those are the ones the pick list drops
// silently, and a board with none short and six unmatched is not ready.
//
// Quantities are summed per part before comparing, since one part can appear on
// several lines of the same board and checking each line alone would call a
// board buildable that is short overall.
func (r *StatsRepo) boardFill(ctx context.Context) ([]models.BoardFill, error) {
	rows, err := r.pool.Query(ctx, `
		WITH needed AS (
			SELECT l.board_id, l.part_id, SUM(l.quantity)::float8 AS need
			FROM board_bom_lines l
			WHERE l.part_id IS NOT NULL
			GROUP BY l.board_id, l.part_id
		),
		short AS (
			SELECT n.board_id, COUNT(*)::int AS short
			FROM needed n
			WHERE n.need > COALESCE(
				(SELECT SUM(s.quantity) FROM stock_items s WHERE s.part_id = n.part_id), 0)
			GROUP BY n.board_id
		)
		SELECT b.id, b.project_id, b.name,
		       COUNT(l.id)::int,
		       COALESCE(sh.short, 0),
		       COUNT(l.id) FILTER (WHERE l.part_id IS NULL)::int
		FROM project_boards b
		LEFT JOIN board_bom_lines l ON l.board_id = b.id
		LEFT JOIN short sh ON sh.board_id = b.id
		GROUP BY b.id, b.project_id, b.name, sh.short
		HAVING COUNT(l.id) > 0
		ORDER BY COALESCE(sh.short, 0) + COUNT(l.id) FILTER (WHERE l.part_id IS NULL) DESC, b.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.BoardFill{}
	for rows.Next() {
		var b models.BoardFill
		if err := rows.Scan(&b.BoardID, &b.ProjectID, &b.Name, &b.Lines, &b.Short, &b.Unmatched); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
