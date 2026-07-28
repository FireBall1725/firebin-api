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

// TestSetMinimumStock covers the bulk reorder-point write: that it touches only
// the parts named, that a zero clears the threshold rather than meaning "reorder
// at zero", and that the returned count distinguishes ids that applied from ids
// that were stale. The count is what the UI reports back to the user, so a wrong
// one tells them their edit landed when it did not.
//
// Needs a disposable Postgres via DATABASE_URL and TRUNCATEs parts, so it skips
// when that is unset. CI provides one; do not point it at real data.
func TestSetMinimumStock(t *testing.T) {
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
		partA = "bbbbbbbb-0000-0000-0000-00000000000a"
		partB = "bbbbbbbb-0000-0000-0000-00000000000b"
		partC = "bbbbbbbb-0000-0000-0000-00000000000c"
	)
	mustExec(t, pool, ctx, `INSERT INTO parts (id, name, minimum_stock) VALUES ($1, 'A', 0)`, partA)
	mustExec(t, pool, ctx, `INSERT INTO parts (id, name, minimum_stock) VALUES ($1, 'B', 0)`, partB)
	mustExec(t, pool, ctx, `INSERT INTO parts (id, name, minimum_stock) VALUES ($1, 'C', 7)`, partC)

	repo := repository.NewPartRepo(pool)

	readMin := func(id string) float64 {
		t.Helper()
		var v float64
		if err := pool.QueryRow(ctx, `SELECT minimum_stock::float8 FROM parts WHERE id = $1`, id).Scan(&v); err != nil {
			t.Fatalf("read minimum_stock for %s: %v", id, err)
		}
		return v
	}

	// Setting two of three parts must leave the third alone.
	n, err := repo.SetMinimumStock(ctx, []uuid.UUID{uuid.MustParse(partA), uuid.MustParse(partB)}, 25)
	if err != nil {
		t.Fatalf("SetMinimumStock: %v", err)
	}
	if n != 2 {
		t.Errorf("updated = %d, want 2", n)
	}
	if got := readMin(partA); got != 25 {
		t.Errorf("part A minimum_stock = %v, want 25", got)
	}
	if got := readMin(partB); got != 25 {
		t.Errorf("part B minimum_stock = %v, want 25", got)
	}
	if got := readMin(partC); got != 7 {
		t.Errorf("part C was not in the id list but changed to %v, want 7", got)
	}

	// Zero is a clear, not a threshold of zero. ListLowStock filters on
	// minimum_stock > 0, so this is what takes a part off the reorder list.
	if _, err := repo.SetMinimumStock(ctx, []uuid.UUID{uuid.MustParse(partC)}, 0); err != nil {
		t.Fatalf("SetMinimumStock(0): %v", err)
	}
	if got := readMin(partC); got != 0 {
		t.Errorf("part C minimum_stock = %v, want 0", got)
	}

	// A fractional threshold has to survive: minimum_stock is NUMERIC(18,4),
	// and reels get measured in things other than whole units.
	if _, err := repo.SetMinimumStock(ctx, []uuid.UUID{uuid.MustParse(partA)}, 2.5); err != nil {
		t.Fatalf("SetMinimumStock(2.5): %v", err)
	}
	if got := readMin(partA); got != 2.5 {
		t.Errorf("part A minimum_stock = %v, want 2.5", got)
	}

	// An id that does not exist contributes nothing to the count, which is how
	// the handler reports "some of your selection was stale".
	n, err = repo.SetMinimumStock(ctx, []uuid.UUID{uuid.MustParse(partA), uuid.New()}, 10)
	if err != nil {
		t.Fatalf("SetMinimumStock(missing id): %v", err)
	}
	if n != 1 {
		t.Errorf("updated = %d for one real and one missing id, want 1", n)
	}
}
