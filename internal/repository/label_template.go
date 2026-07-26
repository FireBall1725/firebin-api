// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type LabelTemplateRepo struct{ pool *pgxpool.Pool }

func NewLabelTemplateRepo(pool *pgxpool.Pool) *LabelTemplateRepo {
	return &LabelTemplateRepo{pool: pool}
}

const labelTemplateCols = `id, name, label_media_id, elements, created_by, created_at, updated_at`

func scanLabelTemplate(row pgx.Row) (models.LabelTemplate, error) {
	var t models.LabelTemplate
	err := row.Scan(&t.ID, &t.Name, &t.MediaID, &t.Elements, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

func (r *LabelTemplateRepo) List(ctx context.Context) ([]models.LabelTemplate, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+labelTemplateCols+` FROM label_templates ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.LabelTemplate{}
	for rows.Next() {
		t, err := scanLabelTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *LabelTemplateRepo) Get(ctx context.Context, id uuid.UUID) (models.LabelTemplate, error) {
	t, err := scanLabelTemplate(r.pool.QueryRow(ctx,
		`SELECT `+labelTemplateCols+` FROM label_templates WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

func (r *LabelTemplateRepo) Create(ctx context.Context, t *models.LabelTemplate) error {
	if len(t.Elements) == 0 {
		t.Elements = json.RawMessage(`[]`)
	}
	return r.pool.QueryRow(ctx, `
		INSERT INTO label_templates (name, label_media_id, elements, created_by)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at, updated_at`,
		t.Name, t.MediaID, t.Elements, t.CreatedBy,
	).Scan(&t.ID, &t.CreatedAt, &t.UpdatedAt)
}

func (r *LabelTemplateRepo) Update(ctx context.Context, id uuid.UUID, name string, mediaID *uuid.UUID, elements json.RawMessage) (models.LabelTemplate, error) {
	if len(elements) == 0 {
		elements = json.RawMessage(`[]`)
	}
	t, err := scanLabelTemplate(r.pool.QueryRow(ctx, `
		UPDATE label_templates SET name = $2, label_media_id = $3, elements = $4, updated_at = NOW()
		WHERE id = $1
		RETURNING `+labelTemplateCols, id, name, mediaID, elements))
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

func (r *LabelTemplateRepo) Delete(ctx context.Context, id uuid.UUID) error {
	ct, err := r.pool.Exec(ctx, `DELETE FROM label_templates WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
