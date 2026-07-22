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

type LocationRepo struct{ pool *pgxpool.Pool }

func NewLocationRepo(pool *pgxpool.Pool) *LocationRepo { return &LocationRepo{pool: pool} }

const locationCols = `id, parent_id, name, description, barcode, created_at, updated_at`

func scanLocation(row pgx.Row) (*models.StorageLocation, error) {
	var l models.StorageLocation
	if err := row.Scan(&l.ID, &l.ParentID, &l.Name, &l.Description, &l.Barcode, &l.CreatedAt, &l.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &l, nil
}

func (r *LocationRepo) List(ctx context.Context) ([]models.StorageLocation, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+locationCols+` FROM storage_locations ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.StorageLocation{}
	for rows.Next() {
		l, err := scanLocation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}

func (r *LocationRepo) Get(ctx context.Context, id uuid.UUID) (*models.StorageLocation, error) {
	return scanLocation(r.pool.QueryRow(ctx, `SELECT `+locationCols+` FROM storage_locations WHERE id = $1`, id))
}

// GetByBarcode resolves a location by its scannable barcode — the "scan a bin"
// entry point.
func (r *LocationRepo) GetByBarcode(ctx context.Context, barcode string) (*models.StorageLocation, error) {
	return scanLocation(r.pool.QueryRow(ctx, `SELECT `+locationCols+` FROM storage_locations WHERE barcode = $1`, barcode))
}

func (r *LocationRepo) Create(ctx context.Context, l *models.StorageLocation) error {
	return r.pool.QueryRow(ctx, `
		INSERT INTO storage_locations (parent_id, name, description, barcode)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at, updated_at`,
		l.ParentID, l.Name, l.Description, l.Barcode,
	).Scan(&l.ID, &l.CreatedAt, &l.UpdatedAt)
}

func (r *LocationRepo) Update(ctx context.Context, l *models.StorageLocation) error {
	ct, err := r.pool.Exec(ctx, `
		UPDATE storage_locations SET parent_id = $2, name = $3, description = $4, barcode = $5
		WHERE id = $1`,
		l.ID, l.ParentID, l.Name, l.Description, l.Barcode)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *LocationRepo) Delete(ctx context.Context, id uuid.UUID) error {
	ct, err := r.pool.Exec(ctx, `DELETE FROM storage_locations WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
