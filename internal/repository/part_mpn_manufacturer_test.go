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

// TestPrimaryMPNSurvivesAMissingManufacturer pins the join in List and Get that
// resolves a part's primary MPN.
//
// manufacturer_parts.manufacturer_id is nullable and rows are created with it
// null: the assistant's create-reference-part path attaches an MPN with no brand
// because ReferencePartInput carries none. Both queries used to inner-join
// manufacturers, so a null brand produced no lateral row at all and the part
// number vanished from the parts list alongside it. The part stayed findable by
// MPN the whole time, because the search predicate reads manufacturer_parts
// directly, which is what made it look like the data was missing when it wasn't.
//
// Needs a disposable Postgres via DATABASE_URL and TRUNCATEs parts, so it skips
// when that is unset. CI provides one; do not point it at real data.
func TestPrimaryMPNSurvivesAMissingManufacturer(t *testing.T) {
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

	const (
		brandlessID = "dddddddd-0000-0000-0000-00000000000a"
		brandedID   = "dddddddd-0000-0000-0000-00000000000b"
	)

	mustExec(t, pool, ctx,
		`INSERT INTO parts (id, name) VALUES ($1, '220 Ohm Resistor')`, brandlessID)
	mustExec(t, pool, ctx,
		`INSERT INTO manufacturer_parts (part_id, manufacturer_id, mpn)
		 VALUES ($1, NULL, 'RMCF0603JT220R')`, brandlessID)

	// Control: an ordinary part with a brand must keep reporting both, so the
	// fix cannot pass by dropping the manufacturer everywhere.
	mustExec(t, pool, ctx,
		`INSERT INTO parts (id, name) VALUES ($1, '120 Ohm Resistor')`, brandedID)
	mustExec(t, pool, ctx,
		`INSERT INTO manufacturers (name) VALUES ('Stackpole Electronics')
		 ON CONFLICT (name) DO NOTHING`)
	mustExec(t, pool, ctx,
		`INSERT INTO manufacturer_parts (part_id, manufacturer_id, mpn)
		 VALUES ($1, (SELECT id FROM manufacturers WHERE name = 'Stackpole Electronics'), 'RMCF0603FT120R')`,
		brandedID)

	repo := repository.NewPartRepo(pool)

	parts, err := repo.List(ctx, repository.ListOptions{TopLevel: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	byID := map[string]struct{ mpn, mfr string }{}
	for _, p := range parts {
		byID[p.ID.String()] = struct{ mpn, mfr string }{p.PrimaryMPN, p.PrimaryManufacturer}
	}

	got, ok := byID[brandlessID]
	if !ok {
		t.Fatalf("List did not return the brandless part at all; got %d parts", len(parts))
	}
	if got.mpn != "RMCF0603JT220R" {
		t.Errorf("List primary MPN = %q, want it present despite a null manufacturer", got.mpn)
	}
	if got.mfr != "" {
		t.Errorf("List manufacturer = %q, want empty; there is no brand to report", got.mfr)
	}

	if ctl := byID[brandedID]; ctl.mpn != "RMCF0603FT120R" || ctl.mfr != "Stackpole Electronics" {
		t.Errorf("List lost the branded part's fields: mpn=%q mfr=%q", ctl.mpn, ctl.mfr)
	}

	// Get runs its own copy of the same lateral, so it can regress on its own.
	one, err := repo.Get(ctx, uuid.MustParse(brandlessID))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if one.PrimaryMPN != "RMCF0603JT220R" {
		t.Errorf("Get primary MPN = %q, want it present despite a null manufacturer", one.PrimaryMPN)
	}
	if one.PrimaryManufacturer != "" {
		t.Errorf("Get manufacturer = %q, want empty", one.PrimaryManufacturer)
	}
}
