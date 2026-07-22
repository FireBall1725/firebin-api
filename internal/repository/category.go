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
	rows, err := r.pool.Query(ctx, `SELECT `+categoryCols+` FROM categories ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Category{}
	for rows.Next() {
		c, err := scanCategory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (r *CategoryRepo) Get(ctx context.Context, id uuid.UUID) (*models.Category, error) {
	return scanCategory(r.pool.QueryRow(ctx, `SELECT `+categoryCols+` FROM categories WHERE id = $1`, id))
}

func (r *CategoryRepo) Create(ctx context.Context, c *models.Category) error {
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
	ct, err := r.pool.Exec(ctx, `DELETE FROM categories WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
