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

// A reference part never appears on the reorder list, and the dashboard count
// agrees with the list it heads.
//
// Zero used to mean two different things, "I ran out" and "I never owned one",
// and the reorder query read both as an alarm. That buried the parts that
// genuinely needed ordering under every part saved for a future design, which
// is the whole reason reference_only exists.
//
// Needs a disposable Postgres via DATABASE_URL and TRUNCATEs parts, so it skips
// when that is unset. CI provides one; do not point it at real data.
func TestLowStockExcludesReferenceParts(t *testing.T) {
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

	// Both are at zero stock with the same reorder point. The only difference
	// is whether the user ever owned one, which is the difference that decides
	// whether it is worth an alert.
	if _, err := pool.Exec(ctx, `
		INSERT INTO parts (name, minimum_stock, reference_only) VALUES
			('Owned and empty', 10, false),
			('Never owned',     10, true)`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	parts := repository.NewPartRepo(pool)
	low, err := parts.ListLowStock(ctx)
	if err != nil {
		t.Fatalf("ListLowStock: %v", err)
	}
	names := make([]string, 0, len(low))
	for _, p := range low {
		names = append(names, p.Name)
	}
	if len(names) != 1 || names[0] != "Owned and empty" {
		t.Errorf("low stock = %v, want only the part that was owned", names)
	}

	// The dashboard headline is a separate query, so it can drift from the list
	// it sits above. A count of 2 over a list of 1 is worse than either alone.
	stats, err := repository.NewStatsRepo(pool).Get(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.LowStockCount != 1 {
		t.Errorf("dashboard low count = %d, want 1 to match the list", stats.LowStockCount)
	}
}

// Receiving stock turns a reference part into a part you own, and running out
// again does not undo that.
//
// reference_only asks "have you ever had one of these". Booking one in answers
// it, and leaving the flag set after a receipt produces a state nothing in the
// app can describe: every view says reference while the shelf holds three.
//
// Needs a disposable Postgres via DATABASE_URL and TRUNCATEs parts, so it skips
// when that is unset. CI provides one; do not point it at real data.
func TestReceivingStockPromotesAReferencePart(t *testing.T) {
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

	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO parts (name, reference_only) VALUES ('Never owned', true) RETURNING id`).
		Scan(&id); err != nil {
		t.Fatalf("seed: %v", err)
	}
	stock := repository.NewStockRepo(pool)
	isReference := func() bool {
		t.Helper()
		var v bool
		if err := pool.QueryRow(ctx, `SELECT reference_only FROM parts WHERE id = $1`, id).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}

	if _, err := stock.Adjust(ctx, repository.AdjustParams{PartID: id, Kind: "add", Quantity: 3}); err != nil {
		t.Fatalf("Adjust add: %v", err)
	}
	if isReference() {
		t.Error("still marked reference after three were booked in")
	}

	// Removing them all leaves it owned and empty. "I ran out" is a different
	// fact from "I never had one", and only the user can say the second.
	if _, err := stock.Adjust(ctx, repository.AdjustParams{PartID: id, Kind: "remove", Quantity: 3}); err != nil {
		t.Fatalf("Adjust remove: %v", err)
	}
	if isReference() {
		t.Error("running out put the part back to reference; that is the user's call, not the app's")
	}

	// A removal against a part that is reference must not promote it either.
	var other uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO parts (name, reference_only) VALUES ('Also never owned', true) RETURNING id`).
		Scan(&other); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := stock.Adjust(ctx, repository.AdjustParams{PartID: other, Kind: "remove", Quantity: 1}); err != nil {
		t.Fatalf("Adjust remove: %v", err)
	}
	var stillRef bool
	if err := pool.QueryRow(ctx, `SELECT reference_only FROM parts WHERE id = $1`, other).Scan(&stillRef); err != nil {
		t.Fatal(err)
	}
	if !stillRef {
		t.Error("removing from a reference part promoted it")
	}
}
