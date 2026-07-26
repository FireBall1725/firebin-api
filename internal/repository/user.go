// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned by repositories when a lookup matches no row.
var ErrNotFound = errors.New("not found")

type UserRepo struct {
	pool *pgxpool.Pool
}

func NewUserRepo(pool *pgxpool.Pool) *UserRepo { return &UserRepo{pool: pool} }

const userColumns = `id, username, email, password_hash, display_name, role,
	is_instance_admin, is_active, created_at, updated_at`

func scanUser(row pgx.Row) (*models.User, error) {
	var u models.User
	if err := row.Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.DisplayName, &u.Role,
		&u.IsInstanceAdmin, &u.IsActive, &u.CreatedAt, &u.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

func (r *UserRepo) Create(ctx context.Context, u *models.User) error {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO users (username, email, password_hash, display_name, role, is_instance_admin)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, is_active, created_at, updated_at`,
		u.Username, u.Email, u.PasswordHash, u.DisplayName, u.Role, u.IsInstanceAdmin,
	).Scan(&u.ID, &u.IsActive, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return fmt.Errorf("creating user: %w", err)
	}
	return nil
}

func (r *UserRepo) GetByUsername(ctx context.Context, username string) (*models.User, error) {
	return scanUser(r.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE username = $1`, username))
}

func (r *UserRepo) GetByID(ctx context.Context, id uuid.UUID) (*models.User, error) {
	return scanUser(r.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = $1`, id))
}

// Count returns the total number of users. Used to grant the first-registered
// user instance-admin.
func (r *UserRepo) Count(ctx context.Context) (int, error) {
	var n int
	if err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// CountAdmins returns how many active admins exist, so we never let the last one
// be deleted, demoted, or deactivated (which would lock everyone out).
func (r *UserRepo) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE role = 'admin' AND is_active`).Scan(&n)
	return n, err
}

// List returns all users, newest last, for the admin user-management screen.
func (r *UserRepo) List(ctx context.Context) ([]models.User, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+userColumns+` FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// Update sets a user's role, active flag, and display name. is_instance_admin is
// kept in lockstep with role='admin' so the legacy flag and the new role never
// disagree.
func (r *UserRepo) Update(ctx context.Context, id uuid.UUID, role string, isActive bool, displayName *string) (*models.User, error) {
	return scanUser(r.pool.QueryRow(ctx, `
		UPDATE users SET role = $2, is_instance_admin = ($2 = 'admin'), is_active = $3,
			display_name = $4, updated_at = NOW()
		WHERE id = $1
		RETURNING `+userColumns, id, role, isActive, displayName))
}

// SetPassword replaces a user's password hash (admin reset or self-service change).
func (r *UserRepo) SetPassword(ctx context.Context, id uuid.UUID, hash string) error {
	ct, err := r.pool.Exec(ctx, `UPDATE users SET password_hash = $2, updated_at = NOW() WHERE id = $1`, id, hash)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes a user.
func (r *UserRepo) Delete(ctx context.Context, id uuid.UUID) error {
	ct, err := r.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
