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

// Fetching one BOM line works, and keeps its lib_id.
//
// This is the third scan mismatch in this codebase and the worst-presenting
// one. lib_id was added to both BOM line queries; only the list query grew a
// destination for it, so GetBOMLine read sixteen columns into fifteen. pgx
// returned an error, the handler for PATCH /lines/{id} treated any error as a
// missing row, and every attempt to match a BOM line to a part answered "line
// not found" about a line sitting on the screen.
//
// The list query is what the page renders from, so the board looked fine and
// only editing failed. Checking one field is not enough here: a mismatch
// shifts every column after it, so the test reads a value from each side of
// the one that was skipped.
//
// Needs a disposable Postgres via DATABASE_URL and TRUNCATEs projects, so it
// skips when that is unset. CI provides one; do not point it at real data.
func TestGetBOMLineReadsEveryColumn(t *testing.T) {
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
		if _, err := pool.Exec(ctx, `TRUNCATE projects, parts CASCADE`); err != nil {
			t.Fatalf("truncate: %v", err)
		}
	}
	truncate()
	t.Cleanup(truncate)

	var projectID, boardID, partID, lineID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name) VALUES ('Alarm Beeper') RETURNING id`).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO project_boards (project_id, name) VALUES ($1, 'Main') RETURNING id`,
		projectID).Scan(&boardID); err != nil {
		t.Fatalf("seed board: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO parts (name) VALUES ('220 Ω Resistor') RETURNING id`).Scan(&partID); err != nil {
		t.Fatalf("seed part: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO board_bom_lines
			(board_id, refs, quantity, value, footprint, lib_id, mpn, manufacturer,
			 supplier_sku, ipn, description, part_id, match_kind, position)
		VALUES ($1, 'R7', 1, '220Ω (1%)', 'R_0603_1608Metric', 'Device:R', 'RMCF0603JT220R',
			'Stackpole', '708-RMCF0603JT220R', 'FB-SF2WNG4H', 'RES 220 OHM', $2, 'manual', 7)
		RETURNING id`, boardID, partID).Scan(&lineID); err != nil {
		t.Fatalf("seed line: %v", err)
	}

	repo := repository.NewProjectRepo(pool)
	line, err := repo.GetBOMLine(ctx, lineID)
	if err != nil {
		t.Fatalf("GetBOMLine: %v", err)
	}
	if line == nil {
		t.Fatal("GetBOMLine returned no line for a row that exists")
	}

	// Either side of the column that was skipped, plus the last one, because a
	// mismatch shifts everything after the gap rather than blanking one field.
	for _, c := range []struct{ field, got, want string }{
		{"Footprint", line.Footprint, "R_0603_1608Metric"},
		{"LibID", line.LibID, "Device:R"},
		{"MPN", line.MPN, "RMCF0603JT220R"},
		{"Manufacturer", line.Manufacturer, "Stackpole"},
		{"IPN", line.IPN, "FB-SF2WNG4H"},
		{"PartName", line.PartName, "220 Ω Resistor"},
		{"MatchKind", line.MatchKind, "manual"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.field, c.got, c.want)
		}
	}
	if line.Position != 7 {
		t.Errorf("Position = %d, want 7", line.Position)
	}

	// A genuinely absent line is still nil rather than an error, which is what
	// lets the handler tell a missing row from a broken query.
	missing, err := repo.GetBOMLine(ctx, uuid.New())
	if err != nil {
		t.Fatalf("GetBOMLine(unknown): %v", err)
	}
	if missing != nil {
		t.Error("an unknown id returned a line")
	}
}
