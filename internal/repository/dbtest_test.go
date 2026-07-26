// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository_test

import (
	"context"
	"encoding/json"
	"os"
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
