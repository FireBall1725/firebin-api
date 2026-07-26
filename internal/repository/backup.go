// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// BackupRepo exports and imports the whole instance as portable JSON — an
// application-level backup, separate from a Postgres-level dump. Import defers
// foreign-key checks to COMMIT (SET CONSTRAINTS ALL DEFERRED), so a partial or
// out-of-order file still restores without needing a superuser role.
type BackupRepo struct{ pool *pgxpool.Pool }

func NewBackupRepo(pool *pgxpool.Pool) *BackupRepo { return &BackupRepo{pool: pool} }

// backupTables are the durable tables included in an export, in dependency order.
// Volatile/session tables (job queue, refresh/revoked tokens, enrichment cache)
// are deliberately excluded.
var backupTables = []string{
	"categories", "parameter_templates", "manufacturers", "suppliers", "storage_locations",
	"users", "parts", "part_parameters", "manufacturer_parts", "supplier_parts",
	"supplier_part_pricing", "stock_items", "stock_transactions", "part_images", "attachments",
	"label_media", "label_templates", "projects", "project_boards", "board_bom_lines",
	"project_matches", "project_assets", "api_tokens", "instance_settings",
}

// ExportAll returns each table as a JSON array (to_jsonb keeps types faithful:
// uuids, timestamps, numerics, bytea, jsonb all round-trip).
func (r *BackupRepo) ExportAll(ctx context.Context) (map[string]json.RawMessage, error) {
	out := make(map[string]json.RawMessage, len(backupTables))
	for _, t := range backupTables {
		var b []byte
		// t is from a fixed allow-list, never user input.
		if err := r.pool.QueryRow(ctx, `SELECT coalesce(jsonb_agg(to_jsonb(x)), '[]'::jsonb) FROM `+t+` x`).Scan(&b); err != nil {
			return nil, fmt.Errorf("export %s: %w", t, err)
		}
		out[t] = b
	}
	return out, nil
}

// ImportAll restores an export inside a single transaction. Foreign-key checks
// are deferred to COMMIT with SET CONSTRAINTS ALL DEFERRED, so tables can be
// inserted in any order (including self-referential rows such as a child category
// before its parent). Every FK is DEFERRABLE as of migration 000023, and
// SET CONSTRAINTS needs no special privilege, so this works as the non-superuser
// app role that managed Postgres (CloudNativePG, RDS, etc.) provides.
//
// When replace is false (merge), rows that collide are skipped (ON CONFLICT DO
// NOTHING). Merge only reliably fills gaps in a matching instance: because the
// schema seeds rows (parameter_templates, label_media, suppliers) and the first
// account bootstraps an admin, all with fresh UUIDs, an export from another
// instance carries different ids for the same unique names/usernames. Those rows
// get skipped, orphaning their children and failing the commit. So a cross-instance
// restore must use replace=true, which first truncates every durable table and
// loads the export exactly as-is (its own ids, no collisions). Replace is
// destructive: it also clears sessions, so the caller signs in again afterward with
// the credentials carried in the export. Returns the number of rows inserted per
// table.
func (r *BackupRepo) ImportAll(ctx context.Context, tables map[string]json.RawMessage, replace bool) (map[string]int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SET CONSTRAINTS ALL DEFERRED`); err != nil {
		return nil, fmt.Errorf("deferring constraints for import: %w", err)
	}

	if replace {
		// Empty every durable table first so the export loads with no collisions.
		// CASCADE also clears volatile tables that reference these (sessions, jobs).
		if _, err := tx.Exec(ctx, `TRUNCATE `+strings.Join(backupTables, ", ")+` RESTART IDENTITY CASCADE`); err != nil {
			return nil, fmt.Errorf("clearing data for replace import: %w", err)
		}
	}

	counts := make(map[string]int64)
	for _, t := range backupTables {
		raw, ok := tables[t]
		if !ok || len(raw) == 0 {
			continue
		}
		// jsonb_populate_recordset expands the JSON array into typed rows of the
		// table; ON CONFLICT DO NOTHING keeps a merge import idempotent (and is a
		// no-op after a truncate, where nothing conflicts).
		tag, err := tx.Exec(ctx, `INSERT INTO `+t+` SELECT * FROM jsonb_populate_recordset(NULL::`+t+`, $1) ON CONFLICT DO NOTHING`, raw)
		if err != nil {
			return nil, fmt.Errorf("import %s: %w", t, err)
		}
		counts[t] = tag.RowsAffected()
	}

	// Deferred FK checks fire here; a genuinely inconsistent export fails the commit.
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit import (foreign keys checked here): %w", err)
	}
	return counts, nil
}
