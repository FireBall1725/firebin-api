// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import (
	"context"
	"errors"

	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrCategoryNotEmpty is returned when deleting a category that still has parts.
var ErrCategoryNotEmpty = errors.New("category is not empty")

type CategoryRepo struct{ pool *pgxpool.Pool }

func NewCategoryRepo(pool *pgxpool.Pool) *CategoryRepo { return &CategoryRepo{pool: pool} }

const categoryCols = `id, parent_id, name, description, created_at, updated_at`

func scanCategory(row pgx.Row) (*models.Category, error) {
	var c models.Category
	if err := row.Scan(&c.ID, &c.ParentID, &c.Name, &c.Description, &c.CreatedAt, &c.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &c, nil
}

func (r *CategoryRepo) List(ctx context.Context) ([]models.Category, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT c.id, c.parent_id, c.name, c.description, c.created_at, c.updated_at,
		       (SELECT count(*) FROM parts p WHERE p.category_id = c.id) AS part_count
		FROM categories c ORDER BY c.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Category{}
	for rows.Next() {
		var c models.Category
		if err := rows.Scan(&c.ID, &c.ParentID, &c.Name, &c.Description, &c.CreatedAt, &c.UpdatedAt, &c.PartCount); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *CategoryRepo) Get(ctx context.Context, id uuid.UUID) (*models.Category, error) {
	return scanCategory(r.pool.QueryRow(ctx, `SELECT `+categoryCols+` FROM categories WHERE id = $1`, id))
}

// Create returns the category with this name (case-insensitive, same parent) if
// one already exists, otherwise inserts it. Making this get-or-create keeps a
// stale client cache or a re-scan from spawning duplicate "Schottky Diodes"
// categories that then split a part's assignment.
func (r *CategoryRepo) Create(ctx context.Context, c *models.Category) error {
	err := r.pool.QueryRow(ctx, `
		SELECT id, name, created_at, updated_at FROM categories
		WHERE lower(name) = lower($1) AND parent_id IS NOT DISTINCT FROM $2
		ORDER BY created_at LIMIT 1`,
		c.Name, c.ParentID,
	).Scan(&c.ID, &c.Name, &c.CreatedAt, &c.UpdatedAt)
	if err == nil {
		return nil // reuse the existing category
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	return r.pool.QueryRow(ctx, `
		INSERT INTO categories (parent_id, name, description)
		VALUES ($1, $2, $3)
		RETURNING id, created_at, updated_at`,
		c.ParentID, c.Name, c.Description,
	).Scan(&c.ID, &c.CreatedAt, &c.UpdatedAt)
}

func (r *CategoryRepo) Update(ctx context.Context, c *models.Category) error {
	ct, err := r.pool.Exec(ctx, `
		UPDATE categories SET parent_id = $2, name = $3, description = $4
		WHERE id = $1`,
		c.ID, c.ParentID, c.Name, c.Description)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *CategoryRepo) Delete(ctx context.Context, id uuid.UUID) error {
	// Only empty categories may be deleted, so parts are never silently orphaned.
	var n int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM parts WHERE category_id = $1`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrCategoryNotEmpty
	}
	ct, err := r.pool.Exec(ctx, `DELETE FROM categories WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
