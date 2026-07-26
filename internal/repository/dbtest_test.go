// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// dbURL returns the test database URL, or skips the test when none is set.
// CI sets DATABASE_URL to a disposable Postgres; local runs skip unless you point
// it at a throwaway database (these tests TRUNCATE tables).
func dbURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping database-backed test")
	}
	return url
}

// mustExec runs a statement and fails the test on error.
func mustExec(t *testing.T, pool *pgxpool.Pool, ctx context.Context, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// swapID replaces every occurrence of oldID with newID in a JSON blob. Used to
// give an exported row an id the target does not have, simulating an export from a
// different instance whose per-instance seed UUIDs differ.
func swapID(raw json.RawMessage, oldID, newID string) json.RawMessage {
	return json.RawMessage(strings.ReplaceAll(string(raw), oldID, newID))
}

// reverseJSONArray reverses a JSON array of objects, used to feed rows in
// child-before-parent order so the test exercises deferred foreign-key checks.
func reverseJSONArray(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		t.Fatalf("unmarshal array: %v", err)
	}
	for i, j := 0, len(arr)-1; i < j; i, j = i+1, j-1 {
		arr[i], arr[j] = arr[j], arr[i]
	}
	out, err := json.Marshal(arr)
	if err != nil {
		t.Fatalf("marshal array: %v", err)
	}
	return out
}
