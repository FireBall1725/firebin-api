// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import (
	"context"
	"errors"
	"time"

	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TokenRepo struct {
	pool *pgxpool.Pool
}

func NewTokenRepo(pool *pgxpool.Pool) *TokenRepo { return &TokenRepo{pool: pool} }

// ── Refresh tokens ───────────────────────────────────────────────────────────

func (r *TokenRepo) CreateRefreshToken(ctx context.Context, userID uuid.UUID, tokenHash string, expiresAt time.Time) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at) VALUES ($1, $2, $3)`,
		userID, tokenHash, expiresAt)
	return err
}

// LookupRefreshToken returns the owning user id for an active (non-revoked,
// non-expired) refresh token hash.
func (r *TokenRepo) LookupRefreshToken(ctx context.Context, tokenHash string) (uuid.UUID, error) {
	var userID uuid.UUID
	err := r.pool.QueryRow(ctx, `
		SELECT user_id FROM refresh_tokens
		WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > NOW()`,
		tokenHash).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	return userID, err
}

func (r *TokenRepo) RevokeRefreshToken(ctx context.Context, tokenHash string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = NOW() WHERE token_hash = $1 AND revoked_at IS NULL`,
		tokenHash)
	return err
}

// ── Access-token revocation ──────────────────────────────────────────────────

func (r *TokenRepo) RevokeAccessToken(ctx context.Context, jti string, expiresAt time.Time) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO revoked_access_tokens (jti, expires_at) VALUES ($1, $2)
		 ON CONFLICT (jti) DO NOTHING`, jti, expiresAt)
	return err
}

func (r *TokenRepo) IsAccessTokenRevoked(ctx context.Context, jti string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM revoked_access_tokens WHERE jti = $1)`, jti).Scan(&exists)
	return exists, err
}

// ── Personal access tokens ───────────────────────────────────────────────────

func (r *TokenRepo) CreatePAT(ctx context.Context, t *models.APIToken, tokenHash string) error {
	return r.pool.QueryRow(ctx, `
		INSERT INTO api_tokens (user_id, name, token_hash, token_suffix, scopes, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at`,
		t.UserID, t.Name, tokenHash, t.TokenSuffix, t.Scopes, t.ExpiresAt,
	).Scan(&t.ID, &t.CreatedAt)
}

// LookupPAT resolves an active PAT by its hash, returning the owner and scopes.
// It also stamps last_used_at.
func (r *TokenRepo) LookupPAT(ctx context.Context, tokenHash string) (userID uuid.UUID, scopes []string, err error) {
	err = r.pool.QueryRow(ctx, `
		UPDATE api_tokens SET last_used_at = NOW()
		WHERE token_hash = $1 AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > NOW())
		RETURNING user_id, scopes`,
		tokenHash).Scan(&userID, &scopes)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil, ErrNotFound
	}
	return userID, scopes, err
}

func (r *TokenRepo) ListPATs(ctx context.Context, userID uuid.UUID) ([]models.APIToken, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, name, token_suffix, scopes, last_used_at, expires_at, revoked_at, created_at
		FROM api_tokens WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.APIToken
	for rows.Next() {
		var t models.APIToken
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &t.TokenSuffix, &t.Scopes,
			&t.LastUsedAt, &t.ExpiresAt, &t.RevokedAt, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *TokenRepo) RevokePAT(ctx context.Context, userID, tokenID uuid.UUID) error {
	ct, err := r.pool.Exec(ctx,
		`UPDATE api_tokens SET revoked_at = NOW()
		 WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`, tokenID, userID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
