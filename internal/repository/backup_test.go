// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/firelabsca/firebin-api/internal/db"
	"github.com/firelabsca/firebin-api/internal/repository"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestImportAll_NonSuperuser is the regression guard for the import that silently
// restored nothing on managed Postgres. The importer used to defer foreign keys
// with `SET session_replication_role = replica`, which is superuser-only; the app
// role that CloudNativePG (and RDS, Cloud SQL, etc.) create is not a superuser, so
// the import failed. This test runs the whole import as a deliberately
// non-superuser role and asserts the data lands, including self-referential rows
// fed in child-before-parent order.
//
// It needs a disposable Postgres via DATABASE_URL and TRUNCATEs parts/categories,
// so it skips when that is unset. CI provides one; do not point it at real data.
func TestImportAll_NonSuperuser(t *testing.T) {
	url := dbURL(t)
	ctx := context.Background()

	if err := db.Migrate(url); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	// Registered first so it runs last (cleanups are LIFO); later cleanups still
	// need this pool open.
	t.Cleanup(admin.Close)

	// Seed two categories (parent + child) and two parts (a base part and a variant
	// of it) in valid order, so defaults fill every NOT NULL column.
	const (
		parentCat = "22222222-2222-2222-2222-222222222222"
		childCat  = "11111111-1111-1111-1111-111111111111"
		basePart  = "aaaaaaaa-0000-0000-0000-000000000002"
		varPart   = "aaaaaaaa-0000-0000-0000-000000000001"
	)
	truncate := func() {
		if _, err := admin.Exec(ctx, `TRUNCATE parts, categories CASCADE`); err != nil {
			t.Fatalf("truncate: %v", err)
		}
	}
	truncate()
	t.Cleanup(truncate)

	mustExec(t, admin, ctx, `INSERT INTO categories (id, name) VALUES ($1, 'Parent')`, parentCat)
	mustExec(t, admin, ctx, `INSERT INTO categories (id, name, parent_id) VALUES ($1, 'Child', $2)`, childCat, parentCat)
	mustExec(t, admin, ctx, `INSERT INTO parts (id, name, category_id) VALUES ($1, '1k resistor', $2)`, basePart, childCat)
	mustExec(t, admin, ctx, `INSERT INTO parts (id, name, category_id, variant_of) VALUES ($1, 'R-0603 1k', $2, $3)`, varPart, childCat, basePart)

	// Export the full, faithful rows, then keep only the two self-referential tables
	// and reverse each so the child/variant lands before its parent/base. Immediate
	// FK checks would reject that order; deferred checks accept it.
	full, err := repository.NewBackupRepo(admin).ExportAll(ctx)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	tables := map[string]json.RawMessage{
		"categories": reverseJSONArray(t, full["categories"]),
		"parts":      reverseJSONArray(t, full["parts"]),
	}

	// A non-superuser role, exactly what a managed Postgres hands the app.
	// DROP OWNED BY clears any privileges a prior interrupted run left behind, so
	// the role can actually be dropped.
	dropRoleSQL := `DO $$ BEGIN
		IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='fbimport_test') THEN
			EXECUTE 'DROP OWNED BY fbimport_test';
			EXECUTE 'DROP ROLE fbimport_test';
		END IF;
	END $$;`
	mustExec(t, admin, ctx, dropRoleSQL)
	mustExec(t, admin, ctx, `CREATE ROLE fbimport_test LOGIN PASSWORD 'test' NOSUPERUSER`)
	mustExec(t, admin, ctx, `GRANT USAGE ON SCHEMA public TO fbimport_test`)
	mustExec(t, admin, ctx, `GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO fbimport_test`)
	mustExec(t, admin, ctx, `GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO fbimport_test`)

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	cfg.ConnConfig.User = "fbimport_test"
	cfg.ConnConfig.Password = "test"
	appPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("app pool: %v", err)
	}
	t.Cleanup(func() {
		appPool.Close()
		_, _ = admin.Exec(ctx, dropRoleSQL)
	})

	// Clear the seed and restore it through the importer as the non-superuser role.
	truncate()
	counts, err := repository.NewBackupRepo(appPool).ImportAll(ctx, tables)
	if err != nil {
		t.Fatalf("import as non-superuser failed (the bug): %v", err)
	}
	if counts["categories"] != 2 || counts["parts"] != 2 {
		t.Fatalf("expected 2 categories and 2 parts, got %v", counts)
	}

	// The relationships must survive: the variant points at the base part, and the
	// child category at the parent.
	var variantOf, childParent string
	if err := admin.QueryRow(ctx, `SELECT variant_of FROM parts WHERE id=$1`, varPart).Scan(&variantOf); err != nil {
		t.Fatalf("read variant_of: %v", err)
	}
	if variantOf != basePart {
		t.Fatalf("variant_of = %q, want %q", variantOf, basePart)
	}
	if err := admin.QueryRow(ctx, `SELECT parent_id FROM categories WHERE id=$1`, childCat).Scan(&childParent); err != nil {
		t.Fatalf("read parent_id: %v", err)
	}
	if childParent != parentCat {
		t.Fatalf("parent_id = %q, want %q", childParent, parentCat)
	}
}
