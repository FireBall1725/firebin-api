// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository_test

import (
	"context"
	"testing"

	"github.com/firelabsca/firebin-api/internal/db"
	"github.com/firelabsca/firebin-api/internal/models"
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

	if got.HasDatasheet {
		t.Error("HasDatasheet = true for a part with no datasheet linked")
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

// TestListReportsLinkedDatasheet covers the has_datasheet flag on both list
// paths.
//
// SearchParametric has its own inline scan, separate from List's, so the flag
// can be right in one and wrong in the other. The palette reads List and the
// parts page switches to SearchParametric the moment a package or value filter
// is typed, which would make the PDF badge blink out of existence for no reason
// the user could see.
func TestListReportsLinkedDatasheet(t *testing.T) {
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
		if _, err := pool.Exec(ctx, `TRUNCATE parts, datasheets CASCADE`); err != nil {
			t.Fatalf("truncate: %v", err)
		}
	}
	truncate()
	t.Cleanup(truncate)

	const withID = "cccccccc-0000-0000-0000-0000000000d1"
	const withoutID = "cccccccc-0000-0000-0000-0000000000d2"
	mustExec(t, pool, ctx,
		`INSERT INTO parts (id, name, package) VALUES ($1, 'ESP32-C6-MINI-1', 'MINI-1'), ($2, 'CH340C', 'SOP-16')`,
		withID, withoutID)

	dsRepo := repository.NewDatasheetRepo(pool)
	d, err := dsRepo.Create(ctx, repository.NewDatasheet{
		SHA256:   "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Filename: "esp32-c6_datasheet_en.pdf", SizeBytes: 4200000,
	})
	if err != nil {
		t.Fatalf("Create datasheet: %v", err)
	}
	if err := dsRepo.LinkPart(ctx, d.ID, uuid.MustParse(withID), nil); err != nil {
		t.Fatalf("LinkPart: %v", err)
	}

	repo := repository.NewPartRepo(pool)

	byName := func(parts []models.Part) map[string]bool {
		m := map[string]bool{}
		for _, p := range parts {
			m[p.Name] = p.HasDatasheet
		}
		return m
	}

	list, err := repo.List(ctx, repository.ListOptions{TopLevel: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := byName(list)
	if !got["ESP32-C6-MINI-1"] {
		t.Error("List: linked part reported HasDatasheet = false")
	}
	if got["CH340C"] {
		t.Error("List: unlinked part reported HasDatasheet = true")
	}

	matches, err := repo.SearchParametric(ctx, repository.ParametricOptions{})
	if err != nil {
		t.Fatalf("SearchParametric: %v", err)
	}
	parts := make([]models.Part, 0, len(matches))
	for _, m := range matches {
		parts = append(parts, m.Part)
	}
	got = byName(parts)
	if !got["ESP32-C6-MINI-1"] {
		t.Error("SearchParametric: linked part reported HasDatasheet = false")
	}
	if got["CH340C"] {
		t.Error("SearchParametric: unlinked part reported HasDatasheet = true")
	}
}
