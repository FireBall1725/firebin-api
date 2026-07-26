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
	counts, err := repository.NewBackupRepo(appPool).ImportAll(ctx, tables, false)
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

// TestImportAll_ReplaceOverSeededInstance is the regression guard for the import
// that returned 500 / restored nothing when the target already had rows the schema
// seeds (parameter_templates, etc.) or a bootstrapped admin. Those seeds get fresh
// UUIDs per instance, so an export from another instance carries the same names
// with different ids; a merge skips them on the unique-name conflict and orphans
// their children, failing the commit. Replace mode must wipe first and restore the
// export exactly. Needs a disposable Postgres (DATABASE_URL); TRUNCATEs everything.
func TestImportAll_ReplaceOverSeededInstance(t *testing.T) {
	url := dbURL(t)
	ctx := context.Background()
	if err := db.Migrate(url); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	t.Cleanup(admin.Close)

	// Migration seeds parameter_templates; grab the seeded "Resistance" id, which
	// stands in for "the target instance's id for this name".
	var seedID string
	if err := admin.QueryRow(ctx, `SELECT id FROM parameter_templates WHERE name='Resistance'`).Scan(&seedID); err != nil {
		t.Fatalf("no seeded Resistance template (schema changed?): %v", err)
	}

	// A little user data that references the seeded template.
	const (
		cat  = "cccccccc-0000-0000-0000-000000000001"
		part = "dddddddd-0000-0000-0000-000000000001"
	)
	mustExec(t, admin, ctx, `TRUNCATE parts, categories CASCADE`) // clean slate for re-runs
	mustExec(t, admin, ctx, `INSERT INTO categories (id, name) VALUES ($1, 'Passives')`, cat)
	mustExec(t, admin, ctx, `INSERT INTO parts (id, name, category_id) VALUES ($1, '1k resistor', $2)`, part, cat)
	mustExec(t, admin, ctx, `INSERT INTO part_parameters (part_id, template_id, value) VALUES ($1, $2, '1k')`, part, seedID)
	t.Cleanup(func() { _, _ = admin.Exec(ctx, `TRUNCATE parts, categories CASCADE`) })

	// Export, then rewrite the seeded template's id to a new value in both the
	// template row and the part_parameter that points at it. Now the export names
	// "Resistance" with an id the target does not have — exactly the cross-instance
	// case. Every other seeded template keeps the target's id.
	full, err := repository.NewBackupRepo(admin).ExportAll(ctx)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	// A new id for "Resistance", guaranteed different from whatever the target has
	// now (keeps the test idempotent against a persistent local DB, not just CI's
	// fresh one).
	newID := "abcdabcd-0000-0000-0000-000000000009"
	if seedID == newID {
		newID = "abcdabcd-0000-0000-0000-00000000000a"
	}
	tables := map[string]json.RawMessage{
		"categories":          full["categories"],
		"parts":               full["parts"],
		"parameter_templates": swapID(full["parameter_templates"], seedID, newID),
		"part_parameters":     swapID(full["part_parameters"], seedID, newID),
	}

	// Clear the user data so the target looks like a different, freshly-seeded
	// instance: seeds present (Resistance still = seedID), no parts/params yet.
	mustExec(t, admin, ctx, `TRUNCATE parts, categories CASCADE`)

	// Merge must fail: "Resistance" (newID) collides on name with the seed, is
	// skipped, and the part_parameter referencing newID is orphaned at commit.
	if _, err := repository.NewBackupRepo(admin).ImportAll(ctx, tables, false); err == nil {
		t.Fatal("merge import unexpectedly succeeded; expected an orphaned-FK failure")
	}

	// Replace must succeed: wipe first, then load the export exactly.
	if _, err := repository.NewBackupRepo(admin).ImportAll(ctx, tables, true); err != nil {
		t.Fatalf("replace import failed: %v", err)
	}
	var gotID string
	if err := admin.QueryRow(ctx, `SELECT id FROM parameter_templates WHERE name='Resistance'`).Scan(&gotID); err != nil {
		t.Fatalf("read Resistance after replace: %v", err)
	}
	if gotID != newID {
		t.Fatalf("after replace, Resistance id = %q, want the export's %q", gotID, newID)
	}
	var ppCount int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM part_parameters WHERE template_id=$1`, newID).Scan(&ppCount); err != nil {
		t.Fatalf("count part_parameters: %v", err)
	}
	if ppCount != 1 {
		t.Fatalf("expected 1 part_parameter on the restored template, got %d", ppCount)
	}
}
