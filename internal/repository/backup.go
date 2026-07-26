// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// BackupRepo exports and imports the whole instance as portable JSON — an
// application-level backup, separate from a Postgres-level dump. Table order lists
// parents before children; import disables FK checks anyway so a partial or
// out-of-order file still restores.
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

// ImportAll restores an export. It runs in a transaction with foreign-key checks
// disabled (session_replication_role = replica, superuser only), inserting each
// table's rows and skipping any that already exist by primary key. Returns the
// number of rows inserted per table.
func (r *BackupRepo) ImportAll(ctx context.Context, tables map[string]json.RawMessage) (map[string]int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SET session_replication_role = replica`); err != nil {
		return nil, fmt.Errorf("import needs a superuser database role (to defer foreign keys): %w", err)
	}

	counts := make(map[string]int64)
	for _, t := range backupTables {
		raw, ok := tables[t]
		if !ok || len(raw) == 0 {
			continue
		}
		// jsonb_populate_recordset expands the JSON array into typed rows of the
		// table; ON CONFLICT DO NOTHING makes re-import idempotent.
		tag, err := tx.Exec(ctx, `INSERT INTO `+t+` SELECT * FROM jsonb_populate_recordset(NULL::`+t+`, $1) ON CONFLICT DO NOTHING`, raw)
		if err != nil {
			return nil, fmt.Errorf("import %s: %w", t, err)
		}
		counts[t] = tag.RowsAffected()
	}

	if _, err := tx.Exec(ctx, `SET session_replication_role = DEFAULT`); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return counts, nil
}
