// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import (
	"context"
	"errors"

	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrConflict is returned when a create would duplicate an existing row.
var ErrConflict = errors.New("conflict")

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

type LabelMediaRepo struct{ pool *pgxpool.Pool }

func NewLabelMediaRepo(pool *pgxpool.Pool) *LabelMediaRepo { return &LabelMediaRepo{pool: pool} }

const labelMediaCols = `id, brand, code, name, page_w, page_h, label_w, label_h,
	corner_radius, cols, rows, x0, y0, pitch_x, pitch_y, cut_guides, kind, builtin`

func scanLabelMedia(row pgx.Row) (models.LabelMedia, error) {
	var m models.LabelMedia
	err := row.Scan(&m.ID, &m.Brand, &m.Code, &m.Name, &m.PageW, &m.PageH,
		&m.LabelW, &m.LabelH, &m.CornerRadius, &m.Cols, &m.Rows,
		&m.X0, &m.Y0, &m.PitchX, &m.PitchY, &m.CutGuides, &m.Kind, &m.Builtin)
	return m, err
}

// List returns all label media, built-ins first, then by brand and code.
func (r *LabelMediaRepo) List(ctx context.Context) ([]models.LabelMedia, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+labelMediaCols+`
		FROM label_media ORDER BY builtin DESC, brand, code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]models.LabelMedia, 0)
	for rows.Next() {
		m, err := scanLabelMedia(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Get returns one media by id.
func (r *LabelMediaRepo) Get(ctx context.Context, id uuid.UUID) (models.LabelMedia, error) {
	m, err := scanLabelMedia(r.pool.QueryRow(ctx, `SELECT `+labelMediaCols+` FROM label_media WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

// Create inserts a user-added media (imported from the catalogue or custom) and
// returns it. Duplicate (brand, code) yields ErrConflict.
func (r *LabelMediaRepo) Create(ctx context.Context, m models.LabelMedia) (models.LabelMedia, error) {
	if m.Kind == "" {
		m.Kind = "sheet"
	}
	out, err := scanLabelMedia(r.pool.QueryRow(ctx, `
		INSERT INTO label_media
			(brand, code, name, page_w, page_h, label_w, label_h, corner_radius,
			 cols, rows, x0, y0, pitch_x, pitch_y, cut_guides, kind, builtin)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16, FALSE)
		RETURNING `+labelMediaCols,
		m.Brand, m.Code, m.Name, m.PageW, m.PageH, m.LabelW, m.LabelH, m.CornerRadius,
		m.Cols, m.Rows, m.X0, m.Y0, m.PitchX, m.PitchY, m.CutGuides, m.Kind))
	if isUniqueViolation(err) {
		return out, ErrConflict
	}
	return out, err
}

// Delete removes a media from the user's list.
func (r *LabelMediaRepo) Delete(ctx context.Context, id uuid.UUID) error {
	ct, err := r.pool.Exec(ctx, `DELETE FROM label_media WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
