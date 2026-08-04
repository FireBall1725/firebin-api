// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository_test

import (
	"context"
	"testing"

	"github.com/firelabsca/firebin-api/internal/db"
	"github.com/firelabsca/firebin-api/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Board readiness sums a part across its lines before comparing, and counts
// unmatched lines separately.
//
// One part often appears on several lines of the same board (two 100 nF on the
// rails, one more by the regulator). Comparing each line against stock on its
// own calls a board buildable that is short overall, which is the failure mode
// worth guarding: it is wrong in the reassuring direction.
//
// Unmatched lines are their own number because no stock comparison can see
// them. A board with nothing short and four unmatched lines is not ready, and
// only the second figure says so.
//
// Needs a disposable Postgres via DATABASE_URL and TRUNCATEs projects, so it
// skips when that is unset. CI provides one; do not point it at real data.
func TestBoardFillSumsAPartAcrossItsLines(t *testing.T) {
	url := dbURL(t)
	ctx := context.Background()
	if err := db.Migrate(url); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)

	truncate := func() {
		if _, err := pool.Exec(ctx, `TRUNCATE projects, parts, storage_locations CASCADE`); err != nil {
			t.Fatalf("truncate: %v", err)
		}
	}
	truncate()
	t.Cleanup(truncate)

	var projectID, boardID, partID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO projects (name) VALUES ('P') RETURNING id`).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO project_boards (project_id, name) VALUES ($1, 'Board') RETURNING id`,
		projectID).Scan(&boardID); err != nil {
		t.Fatalf("seed board: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO parts (name) VALUES ('100 nF Capacitor') RETURNING id`).Scan(&partID); err != nil {
		t.Fatalf("seed part: %v", err)
	}
	// Six on the shelf, seven needed across two lines. Neither line alone
	// exceeds six, so a per-line check would call this buildable.
	if _, err := pool.Exec(ctx,
		`INSERT INTO stock_items (part_id, quantity) VALUES ($1, 6)`, partID); err != nil {
		t.Fatalf("seed stock: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO board_bom_lines (board_id, refs, quantity, part_id, match_kind, position) VALUES
			($1, 'C1,C2,C3,C4', 4, $2, 'manual', 1),
			($1, 'C5,C6,C7',    3, $2, 'manual', 2)`, boardID, partID); err != nil {
		t.Fatalf("seed matched lines: %v", err)
	}
	// Two lines that resolve to nothing at all.
	if _, err := pool.Exec(ctx, `
		INSERT INTO board_bom_lines (board_id, refs, quantity, match_kind, position) VALUES
			($1, 'U1', 1, 'none', 3),
			($1, 'J1', 1, 'none', 4)`, boardID); err != nil {
		t.Fatalf("seed unmatched lines: %v", err)
	}

	fills, err := repository.NewStatsRepo(pool).Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(fills.Boards) != 1 {
		t.Fatalf("got %d boards, want 1", len(fills.Boards))
	}
	b := fills.Boards[0]
	if b.Lines != 4 {
		t.Errorf("Lines = %d, want 4", b.Lines)
	}
	if b.Short != 1 {
		t.Errorf("Short = %d, want 1; 7 are needed and 6 are on the shelf, "+
			"which only shows up once the two lines are summed", b.Short)
	}
	if b.Unmatched != 2 {
		t.Errorf("Unmatched = %d, want 2", b.Unmatched)
	}
	// And the headline count agrees with the per-board detail.
	if fills.UnmatchedBOMLines != 2 {
		t.Errorf("UnmatchedBOMLines = %d, want 2", fills.UnmatchedBOMLines)
	}
	// The movement series is evenly spaced whether or not anything moved.
	if len(fills.Movement) != 30 {
		t.Errorf("movement has %d days, want 30 including empty ones", len(fills.Movement))
	}
}
