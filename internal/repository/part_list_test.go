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

// TestListPartsScansEveryColumn exercises PartRepo.List end to end against a
// real database.
//
// It exists because List does not share a scanner with Get: it has its own
// inline rows.Scan covering partCols plus four joined columns. Adding a column
// to partCols without updating that inline scan compiles cleanly, passes vet,
// and then fails at runtime with "could not list parts" — a 500 on the main
// catalogue endpoint. Nothing else in the suite calls List, so that mistake
// reached a running server once already.
//
// Needs a disposable Postgres via DATABASE_URL and TRUNCATEs parts, so it skips
// when that is unset. CI provides one; do not point it at real data.
func TestListPartsScansEveryColumn(t *testing.T) {
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
		if _, err := pool.Exec(ctx, `TRUNCATE parts CASCADE`); err != nil {
			t.Fatalf("truncate: %v", err)
		}
	}
	truncate()
	t.Cleanup(truncate)

	const partID = "cccccccc-0000-0000-0000-00000000000a"
	mustExec(t, pool, ctx,
		`INSERT INTO parts (id, name, package, kicad_symbol, kicad_footprint)
		 VALUES ($1, '10k Resistor', '0603', 'Device:R', 'Resistor_SMD:R_0603_1608Metric')`,
		partID)

	repo := repository.NewPartRepo(pool)

	parts, err := repo.List(ctx, repository.ListOptions{TopLevel: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("got %d parts, want 1", len(parts))
	}

	got := parts[0]
	if got.Name != "10k Resistor" {
		t.Errorf("Name = %q, want %q", got.Name, "10k Resistor")
	}
	if got.KicadSymbol == nil || *got.KicadSymbol != "Device:R" {
		t.Errorf("KicadSymbol = %v, want Device:R", got.KicadSymbol)
	}
	if got.KicadFootprint == nil || *got.KicadFootprint != "Resistor_SMD:R_0603_1608Metric" {
		t.Errorf("KicadFootprint = %v, want Resistor_SMD:R_0603_1608Metric", got.KicadFootprint)
	}

	// Get uses a different scanner over the same column list; both have to stay
	// in step, so check the column round-trips on that path too.
	one, err := repo.Get(ctx, uuid.MustParse(partID))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if one.KicadSymbol == nil || *one.KicadSymbol != "Device:R" {
		t.Errorf("Get KicadSymbol = %v, want Device:R", one.KicadSymbol)
	}
}
