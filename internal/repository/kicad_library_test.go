// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository_test

import (
	"context"
	"errors"
	"testing"

	"github.com/firelabsca/firebin-api/internal/db"
	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/firelabsca/firebin-api/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Importing a library never deletes, and never silently replaces.
//
// Finishing a scan used to drop every item it did not carry, so importing a
// downloaded folder replaced the whole index with its contents. Worse when the
// scan came up short: a folder that parsed to three footprints and no symbols
// took every symbol in the index with it.
//
// Needs a disposable Postgres via DATABASE_URL and TRUNCATEs its own tables, so
// it skips when that is unset. CI provides one; do not point it at real data.
func TestImportKeepsWhatIsAlreadyThere(t *testing.T) {
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
		if _, err := pool.Exec(ctx, `TRUNCATE kicad_library_items, kicad_library_index_meta`); err != nil {
			t.Fatalf("truncate: %v", err)
		}
	}
	truncate()
	t.Cleanup(truncate)

	repo := repository.NewKicadLibraryRepo(pool)
	item := func(kind, lib, name, src string) models.KicadLibraryUpload {
		return models.KicadLibraryUpload{Kind: kind, Lib: lib, Name: name, Source: src}
	}
	sourceOf := func(kind, lib, name string) string {
		t.Helper()
		b, err := repo.Source(ctx, kind, lib, name)
		if err != nil {
			t.Fatalf("Source: %v", err)
		}
		return string(b)
	}
	count := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM kicad_library_items`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// An established index.
	first := uuid.New()
	if _, _, err := repo.UpsertBatch(ctx, first, []models.KicadLibraryUpload{
		item("symbol", "Device", "R", "(symbol R original)"),
		item("footprint", "Resistor_SMD", "R_0603", "(module R_0603 original)"),
	}, false); err != nil {
		t.Fatalf("UpsertBatch: %v", err)
	}
	if _, err := repo.FinishScan(ctx, first, "workstation", "8.0"); err != nil {
		t.Fatalf("FinishScan: %v", err)
	}

	t.Run("a new library is added and the old one survives", func(t *testing.T) {
		scan := uuid.New()
		written, skipped, err := repo.UpsertBatch(ctx, scan, []models.KicadLibraryUpload{
			item("footprint", "MyDownload", "XVF3800", "(module XVF3800)"),
		}, false)
		if err != nil {
			t.Fatalf("UpsertBatch: %v", err)
		}
		if written != 1 || skipped != 0 {
			t.Errorf("written=%d skipped=%d, want 1 and 0", written, skipped)
		}
		if _, err := repo.FinishScan(ctx, scan, "downloads", ""); err != nil {
			t.Fatalf("FinishScan: %v", err)
		}
		if count() != 3 {
			t.Errorf("index holds %d items, want the original two plus the new one", count())
		}
	})

	// The point of the default: importing does not overwrite something already
	// curated here, and says how many it left alone.
	t.Run("an existing name is skipped, not replaced", func(t *testing.T) {
		scan := uuid.New()
		written, skipped, err := repo.UpsertBatch(ctx, scan, []models.KicadLibraryUpload{
			item("symbol", "Device", "R", "(symbol R FROM THE IMPORT)"),
			item("symbol", "Device", "C", "(symbol C new)"),
		}, false)
		if err != nil {
			t.Fatalf("UpsertBatch: %v", err)
		}
		if written != 1 || skipped != 1 {
			t.Errorf("written=%d skipped=%d, want 1 written and 1 skipped", written, skipped)
		}
		if got := sourceOf("symbol", "Device", "R"); got != "(symbol R original)" {
			t.Errorf("the stored symbol was replaced: %q", got)
		}
		if got := sourceOf("symbol", "Device", "C"); got != "(symbol C new)" {
			t.Errorf("the new symbol was not stored: %q", got)
		}
	})

	t.Run("overwrite replaces it deliberately", func(t *testing.T) {
		scan := uuid.New()
		written, skipped, err := repo.UpsertBatch(ctx, scan, []models.KicadLibraryUpload{
			item("symbol", "Device", "R", "(symbol R UPDATED)"),
		}, true)
		if err != nil {
			t.Fatalf("UpsertBatch: %v", err)
		}
		if written != 1 || skipped != 0 {
			t.Errorf("written=%d skipped=%d, want the overwrite counted as written", written, skipped)
		}
		if got := sourceOf("symbol", "Device", "R"); got != "(symbol R UPDATED)" {
			t.Errorf("overwrite did not take: %q", got)
		}
	})

	// Nothing an import does removes an item. There is no path through this
	// that empties the index, which is what made the old behaviour dangerous.
	t.Run("finishing never deletes", func(t *testing.T) {
		before := count()
		scan := uuid.New()
		if _, _, err := repo.UpsertBatch(ctx, scan, []models.KicadLibraryUpload{
			item("symbol", "Unrelated", "X", "(symbol X)"),
		}, false); err != nil {
			t.Fatalf("UpsertBatch: %v", err)
		}
		if _, err := repo.FinishScan(ctx, scan, "somewhere else", ""); err != nil {
			t.Fatalf("FinishScan: %v", err)
		}
		if got := count(); got != before+1 {
			t.Errorf("index went from %d to %d; a scan removed something", before, got)
		}
	})
}

// A library can be renamed and deleted.
//
// The name a library lands under is the filename it was imported from, so a
// vendor download arrives as something like 2026-08-01_09-13-46. That name is
// what KiCad matches on and what you have to recognise in the chooser, and
// nothing else in the app can change it.
func TestRenameAndDeleteLibrary(t *testing.T) {
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
		if _, err := pool.Exec(ctx, `TRUNCATE kicad_library_items, kicad_library_index_meta`); err != nil {
			t.Fatalf("truncate: %v", err)
		}
	}
	truncate()
	t.Cleanup(truncate)

	repo := repository.NewKicadLibraryRepo(pool)
	item := func(kind, lib, name, src string) models.KicadLibraryUpload {
		return models.KicadLibraryUpload{Kind: kind, Lib: lib, Name: name, Source: src}
	}
	names := func(kind, lib string) []string {
		t.Helper()
		items, err := repo.Items(ctx, kind, lib)
		if err != nil {
			t.Fatalf("Items: %v", err)
		}
		out := make([]string, 0, len(items))
		for _, it := range items {
			out = append(out, it.Name)
		}
		return out
	}

	scan := uuid.New()
	if _, _, err := repo.UpsertBatch(ctx, scan, []models.KicadLibraryUpload{
		item("symbol", "2026-08-01_09-13-46", "XVF3800", "(symbol XVF3800)"),
		item("symbol", "2026-08-01_09-13-46", "R", "(symbol R downloaded)"),
		item("symbol", "Device", "R", "(symbol R stock)"),
		// Same library name under the other kind. Renaming symbols must leave it
		// alone, or fixing one name quietly renames a library you did not touch.
		item("footprint", "2026-08-01_09-13-46", "QFN60", "(module QFN60)"),
	}, false); err != nil {
		t.Fatalf("UpsertBatch: %v", err)
	}

	// Renaming into a free name moves everything.
	t.Run("rename moves every item of that kind", func(t *testing.T) {
		moved, err := repo.RenameLibrary(ctx, "symbol", "2026-08-01_09-13-46", "XMOS")
		if err != nil {
			t.Fatalf("RenameLibrary: %v", err)
		}
		if moved != 2 {
			t.Errorf("moved %d, want 2", moved)
		}
		if got := names("symbol", "XMOS"); len(got) != 2 {
			t.Errorf("XMOS holds %v, want both symbols", got)
		}
		if got := names("footprint", "2026-08-01_09-13-46"); len(got) != 1 {
			t.Errorf("the footprint library was touched: %v", got)
		}
	})

	// Merging is allowed and is sometimes the point. What must not happen is the
	// whole rename failing, or an item landing on top of one already there.
	t.Run("merging keeps the item already at the destination", func(t *testing.T) {
		moved, err := repo.RenameLibrary(ctx, "symbol", "XMOS", "Device")
		if err != nil {
			t.Fatalf("RenameLibrary: %v", err)
		}
		if moved != 1 {
			t.Errorf("moved %d, want only XVF3800 to move", moved)
		}
		// Device:R was already there and stays as it was.
		src, err := repo.Source(ctx, "symbol", "Device", "R")
		if err != nil {
			t.Fatalf("Source: %v", err)
		}
		if string(src) != "(symbol R stock)" {
			t.Errorf("the destination item was replaced: %q", src)
		}
		// The one that could not move is still reachable under its old name
		// rather than lost.
		if got := names("symbol", "XMOS"); len(got) != 1 || got[0] != "R" {
			t.Errorf("XMOS holds %v, want the R that could not move", got)
		}
	})

	t.Run("delete one item", func(t *testing.T) {
		if err := repo.DeleteItem(ctx, "symbol", "Device", "XVF3800"); err != nil {
			t.Fatalf("DeleteItem: %v", err)
		}
		if got := names("symbol", "Device"); len(got) != 1 || got[0] != "R" {
			t.Errorf("Device holds %v, want just R", got)
		}
	})

	t.Run("deleting something that is not there is not found", func(t *testing.T) {
		err := repo.DeleteItem(ctx, "symbol", "Device", "NoSuchSymbol")
		if !errors.Is(err, repository.ErrNotFound) {
			t.Errorf("got %v, want ErrNotFound", err)
		}
	})

	t.Run("delete a whole library, of one kind only", func(t *testing.T) {
		n, err := repo.DeleteLibrary(ctx, "footprint", "2026-08-01_09-13-46")
		if err != nil {
			t.Fatalf("DeleteLibrary: %v", err)
		}
		if n != 1 {
			t.Errorf("deleted %d, want 1", n)
		}
		if got := names("symbol", "Device"); len(got) != 1 {
			t.Errorf("deleting footprints took a symbol with it: %v", got)
		}
	})
}
