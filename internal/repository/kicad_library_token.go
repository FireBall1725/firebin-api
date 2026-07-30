// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/firelabsca/firebin-api/internal/models"
)

// KicadLibraryTokenRepo stores the per-workstation tokens the KiCad HTTP library
// routes accept. Deliberately separate from TokenRepo: these credentials are
// only ever resolved by the KiCad route group, which is read-only, and keeping
// the two stores apart is what makes that guarantee structural.
type KicadLibraryTokenRepo struct {
	pool *pgxpool.Pool
}

func NewKicadLibraryTokenRepo(pool *pgxpool.Pool) *KicadLibraryTokenRepo {
	return &KicadLibraryTokenRepo{pool: pool}
}

const kicadTokenCols = `id, name, token_suffix, created_by, last_used_at, revoked_at, created_at`

func scanKicadToken(row pgx.Row) (models.KicadLibraryToken, error) {
	var t models.KicadLibraryToken
	err := row.Scan(&t.ID, &t.Name, &t.TokenSuffix, &t.CreatedBy, &t.LastUsedAt, &t.RevokedAt, &t.CreatedAt)
	return t, err
}

// Create records a new workstation token. The caller mints the secret and keeps
// the only copy; this side sees the hash and the display suffix.
func (r *KicadLibraryTokenRepo) Create(ctx context.Context, name, tokenHash, suffix string, createdBy *uuid.UUID) (models.KicadLibraryToken, error) {
	t, err := scanKicadToken(r.pool.QueryRow(ctx, `
		INSERT INTO kicad_library_tokens (name, token_hash, token_suffix, created_by)
		VALUES ($1, $2, $3, $4)
		RETURNING `+kicadTokenCols,
		name, tokenHash, suffix, createdBy))
	if isUniqueViolation(err) {
		return t, ErrConflict
	}
	return t, err
}

// List returns every token, live and revoked, newest first. Revoked rows are
// kept visible so the UI can show that a machine was deliberately cut off
// rather than silently omitting it.
func (r *KicadLibraryTokenRepo) List(ctx context.Context) ([]models.KicadLibraryToken, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+kicadTokenCols+` FROM kicad_library_tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Never nil: a nil slice marshals to JSON null and the web client indexes
	// into it.
	out := []models.KicadLibraryToken{}
	for rows.Next() {
		t, err := scanKicadToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Revoke marks a token dead. Idempotent, and returns ErrNotFound for an id that
// does not exist so the handler can answer 404 rather than a silent success.
func (r *KicadLibraryTokenRepo) Revoke(ctx context.Context, id uuid.UUID) error {
	ct, err := r.pool.Exec(ctx, `
		UPDATE kicad_library_tokens SET revoked_at = NOW()
		WHERE id = $1 AND revoked_at IS NULL`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		// Either unknown or already revoked. Distinguish, so revoking twice is
		// not reported as a missing token.
		var exists bool
		if err := r.pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM kicad_library_tokens WHERE id = $1)`, id).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
	}
	return nil
}

// Lookup resolves a presented token, returning false when it is unknown,
// revoked, or simply not a KiCad token.
//
// last_used_at is stamped at most once a minute rather than on every call. KiCad
// issues one request per category every time the Symbol Chooser opens, so the
// unconditional UPDATE ... RETURNING that TokenRepo.LookupPAT uses would turn a
// read-only feature into a steady write load for information nobody needs to the
// second.
func (r *KicadLibraryTokenRepo) Lookup(ctx context.Context, tokenHash string) (uuid.UUID, bool, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `
		UPDATE kicad_library_tokens
		SET last_used_at = NOW()
		WHERE token_hash = $1
		  AND revoked_at IS NULL
		  AND (last_used_at IS NULL OR last_used_at < NOW() - INTERVAL '1 minute')
		RETURNING id`, tokenHash).Scan(&id)
	if err == nil {
		return id, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, err
	}

	// No row updated: either the token is live but was used within the last
	// minute, or it is not valid at all. A plain read settles it.
	err = r.pool.QueryRow(ctx, `
		SELECT id FROM kicad_library_tokens
		WHERE token_hash = $1 AND revoked_at IS NULL`, tokenHash).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, nil
		}
		return uuid.Nil, false, err
	}
	return id, true, nil
}
