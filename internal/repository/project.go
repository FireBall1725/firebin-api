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

type ProjectRepo struct{ pool *pgxpool.Pool }

func NewProjectRepo(pool *pgxpool.Pool) *ProjectRepo { return &ProjectRepo{pool: pool} }

// ── Projects ─────────────────────────────────────────────────────────────────

func (r *ProjectRepo) List(ctx context.Context) ([]models.Project, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT p.id, p.name, COALESCE(p.description, ''), p.created_at, p.updated_at,
		       (SELECT COUNT(*) FROM project_boards b WHERE b.project_id = p.id)
		FROM projects p ORDER BY p.updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Project{}
	for rows.Next() {
		var p models.Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.CreatedAt, &p.UpdatedAt, &p.BoardCount); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *ProjectRepo) Create(ctx context.Context, name, description string) (*models.Project, error) {
	var p models.Project
	err := r.pool.QueryRow(ctx, `
		INSERT INTO projects (name, description) VALUES ($1, NULLIF($2, ''))
		RETURNING id, name, COALESCE(description, ''), created_at, updated_at`,
		name, description).Scan(&p.ID, &p.Name, &p.Description, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// Get returns a project with its boards (each carrying a line count).
func (r *ProjectRepo) Get(ctx context.Context, id uuid.UUID) (*models.Project, error) {
	var p models.Project
	err := r.pool.QueryRow(ctx, `
		SELECT id, name, COALESCE(description, ''), created_at, updated_at
		FROM projects WHERE id = $1`, id).
		Scan(&p.ID, &p.Name, &p.Description, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	boards, err := r.ListBoards(ctx, id)
	if err != nil {
		return nil, err
	}
	p.Boards = boards
	p.BoardCount = len(boards)
	return &p, nil
}

func (r *ProjectRepo) Update(ctx context.Context, id uuid.UUID, name, description string) (*models.Project, error) {
	var p models.Project
	err := r.pool.QueryRow(ctx, `
		UPDATE projects SET name = $2, description = NULLIF($3, '') WHERE id = $1
		RETURNING id, name, COALESCE(description, ''), created_at, updated_at`,
		id, name, description).Scan(&p.ID, &p.Name, &p.Description, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

func (r *ProjectRepo) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, id)
	return err
}

// ── Boards ───────────────────────────────────────────────────────────────────

func (r *ProjectRepo) ListBoards(ctx context.Context, projectID uuid.UUID) ([]models.Board, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT b.id, b.project_id, b.name, COALESCE(b.description, ''),
		       COALESCE(b.source_filename, ''), b.source_format, b.position,
		       b.created_at, b.updated_at,
		       (SELECT COUNT(*) FROM board_bom_lines l WHERE l.board_id = b.id)
		FROM project_boards b WHERE b.project_id = $1
		ORDER BY b.position, b.created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Board{}
	for rows.Next() {
		var b models.Board
		if err := rows.Scan(&b.ID, &b.ProjectID, &b.Name, &b.Description,
			&b.SourceFilename, &b.SourceFormat, &b.Position,
			&b.CreatedAt, &b.UpdatedAt, &b.LineCount); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (r *ProjectRepo) CreateBoard(ctx context.Context, b *models.Board) error {
	return r.pool.QueryRow(ctx, `
		INSERT INTO project_boards (project_id, name, description, source_filename, source_format, position)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), $5,
		        COALESCE((SELECT MAX(position)+1 FROM project_boards WHERE project_id = $1), 0))
		RETURNING id, project_id, name, COALESCE(description, ''),
		          COALESCE(source_filename, ''), source_format, position, created_at, updated_at`,
		b.ProjectID, b.Name, b.Description, b.SourceFilename, b.SourceFormat).
		Scan(&b.ID, &b.ProjectID, &b.Name, &b.Description, &b.SourceFilename,
			&b.SourceFormat, &b.Position, &b.CreatedAt, &b.UpdatedAt)
}

// GetBoard returns a board with its BOM lines (part name joined when matched).
func (r *ProjectRepo) GetBoard(ctx context.Context, id uuid.UUID) (*models.Board, error) {
	var b models.Board
	err := r.pool.QueryRow(ctx, `
		SELECT id, project_id, name, COALESCE(description, ''),
		       COALESCE(source_filename, ''), source_format, position, created_at, updated_at
		FROM project_boards WHERE id = $1`, id).
		Scan(&b.ID, &b.ProjectID, &b.Name, &b.Description, &b.SourceFilename,
			&b.SourceFormat, &b.Position, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	lines, err := r.listLines(ctx, id)
	if err != nil {
		return nil, err
	}
	b.Lines = lines
	b.LineCount = len(lines)
	return &b, nil
}

func (r *ProjectRepo) DeleteBoard(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM project_boards WHERE id = $1`, id)
	return err
}

func (r *ProjectRepo) listLines(ctx context.Context, boardID uuid.UUID) ([]models.BOMLine, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT l.id, l.board_id, l.refs, l.quantity, l.value, l.footprint, l.mpn,
		       l.manufacturer, l.description, l.part_id, COALESCE(p.name, ''), l.match_kind, l.position
		FROM board_bom_lines l
		LEFT JOIN parts p ON p.id = l.part_id
		WHERE l.board_id = $1 ORDER BY l.position`, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.BOMLine{}
	for rows.Next() {
		var l models.BOMLine
		if err := rows.Scan(&l.ID, &l.BoardID, &l.Refs, &l.Quantity, &l.Value, &l.Footprint,
			&l.MPN, &l.Manufacturer, &l.Description, &l.PartID, &l.PartName, &l.MatchKind, &l.Position); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ReplaceBOMLines swaps a board's BOM for a freshly-parsed set, in a transaction.
func (r *ProjectRepo) ReplaceBOMLines(ctx context.Context, boardID uuid.UUID, lines []models.BOMLine) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM board_bom_lines WHERE board_id = $1`, boardID); err != nil {
		return err
	}
	for i, l := range lines {
		if _, err := tx.Exec(ctx, `
			INSERT INTO board_bom_lines
			  (board_id, refs, quantity, value, footprint, mpn, manufacturer, description, part_id, match_kind, position)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			boardID, l.Refs, l.Quantity, l.Value, l.Footprint, l.MPN,
			l.Manufacturer, l.Description, l.PartID, l.MatchKind, i); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ── Assets (renderable files: iBOM, images) ──────────────────────────────────

func (r *ProjectRepo) CreateAsset(ctx context.Context, a *models.ProjectAsset, content []byte) error {
	return r.pool.QueryRow(ctx, `
		INSERT INTO project_assets (project_id, board_id, name, kind, mime, size, content)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at`,
		a.ProjectID, a.BoardID, a.Name, a.Kind, a.Mime, int64(len(content)), content).
		Scan(&a.ID, &a.CreatedAt)
}

func (r *ProjectRepo) ListAssets(ctx context.Context, projectID uuid.UUID) ([]models.ProjectAsset, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, project_id, board_id, name, kind, mime, size, created_at
		FROM project_assets WHERE project_id = $1
		ORDER BY (kind = 'ibom') DESC, name`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.ProjectAsset{}
	for rows.Next() {
		var a models.ProjectAsset
		if err := rows.Scan(&a.ID, &a.ProjectID, &a.BoardID, &a.Name, &a.Kind, &a.Mime, &a.Size, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAssetContent returns an asset's bytes and mime for serving.
func (r *ProjectRepo) GetAssetContent(ctx context.Context, id uuid.UUID) (mime, name string, content []byte, found bool, err error) {
	err = r.pool.QueryRow(ctx, `SELECT mime, name, content FROM project_assets WHERE id = $1`, id).
		Scan(&mime, &name, &content)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", nil, false, nil
		}
		return "", "", nil, false, err
	}
	return mime, name, content, true, nil
}

func (r *ProjectRepo) DeleteAsset(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM project_assets WHERE id = $1`, id)
	return err
}

// FindPartByValueFootprint matches a non-template part by value and footprint
// token, for BOM lines that carry no MPN. The footprint match is loose: it
// looks for the part's package as a substring of the KiCad footprint
// ("…:R_0603_1608Metric" matches package "0603").
func (r *ProjectRepo) FindPartByValueFootprint(ctx context.Context, value, footprint string) (uuid.UUID, string, bool, error) {
	var id uuid.UUID
	var name string
	err := r.pool.QueryRow(ctx, `
		SELECT id, name FROM parts
		WHERE is_template = false
		  AND lower(name) = lower($1)
		  AND ($2 = '' OR package IS NULL OR package = '' OR position(lower(package) in lower($2)) > 0)
		ORDER BY (package IS NOT NULL AND package <> '') DESC
		LIMIT 1`, value, footprint).Scan(&id, &name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, "", false, nil
		}
		return uuid.Nil, "", false, err
	}
	return id, name, true, nil
}
