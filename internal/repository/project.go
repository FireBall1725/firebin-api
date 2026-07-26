// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import (
	"context"
	"errors"
	"strings"

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
		SELECT p.id, p.name, COALESCE(p.description, ''), p.tags, p.created_at, p.updated_at,
		       (SELECT COUNT(*) FROM project_boards b WHERE b.project_id = p.id),
		       cov.id, cov.kind
		FROM projects p `+coverJoin+`
		ORDER BY p.updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Project{}
	for rows.Next() {
		var p models.Project
		var kind *string
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.Tags, &p.CreatedAt, &p.UpdatedAt, &p.BoardCount, &p.CoverAssetID, &kind); err != nil {
			return nil, err
		}
		if kind != nil {
			p.CoverAssetKind = *kind
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// coverJoin resolves a project's cover thumbnail: its uploaded cover image, else
// the first board's render (a real iBOM preferred over the generated one).
const coverJoin = `
	LEFT JOIN LATERAL (
		SELECT a.id, a.kind FROM project_assets a
		WHERE a.id = p.cover_image_id
		   OR (p.cover_image_id IS NULL AND a.project_id = p.id AND a.kind IN ('ibom', 'pcbrender'))
		ORDER BY (a.id = p.cover_image_id) DESC, (a.kind = 'ibom') DESC, a.created_at
		LIMIT 1
	) cov ON true`

func (r *ProjectRepo) Create(ctx context.Context, name, description string, tags []string) (*models.Project, error) {
	var p models.Project
	err := r.pool.QueryRow(ctx, `
		INSERT INTO projects (name, description, tags) VALUES ($1, NULLIF($2, ''), $3)
		RETURNING id, name, COALESCE(description, ''), tags, created_at, updated_at`,
		name, description, normTags(tags)).Scan(&p.ID, &p.Name, &p.Description, &p.Tags, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// Get returns a project with its boards (each carrying a line count).
func (r *ProjectRepo) Get(ctx context.Context, id uuid.UUID) (*models.Project, error) {
	var p models.Project
	var kind *string
	err := r.pool.QueryRow(ctx, `
		SELECT p.id, p.name, COALESCE(p.description, ''), p.tags, p.created_at, p.updated_at, cov.id, cov.kind
		FROM projects p `+coverJoin+`
		WHERE p.id = $1`, id).
		Scan(&p.ID, &p.Name, &p.Description, &p.Tags, &p.CreatedAt, &p.UpdatedAt, &p.CoverAssetID, &kind)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if kind != nil {
		p.CoverAssetKind = *kind
	}
	boards, err := r.ListBoards(ctx, id)
	if err != nil {
		return nil, err
	}
	p.Boards = boards
	p.BoardCount = len(boards)
	return &p, nil
}

func (r *ProjectRepo) Update(ctx context.Context, id uuid.UUID, name, description string, tags []string) (*models.Project, error) {
	var p models.Project
	err := r.pool.QueryRow(ctx, `
		UPDATE projects SET name = $2, description = NULLIF($3, ''), tags = $4 WHERE id = $1
		RETURNING id, name, COALESCE(description, ''), tags, created_at, updated_at`,
		id, name, description, normTags(tags)).Scan(&p.ID, &p.Name, &p.Description, &p.Tags, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

// normTags trims, drops blanks, dedupes (case-insensitive), and never returns nil
// (so it stores as an empty array, not NULL).
func normTags(tags []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, t := range tags {
		t = strings.TrimSpace(t)
		k := strings.ToLower(t)
		if t == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, t)
	}
	return out
}

func (r *ProjectRepo) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, id)
	return err
}

// CoverImageID returns a project's uploaded cover image asset, if any.
func (r *ProjectRepo) CoverImageID(ctx context.Context, projectID uuid.UUID) (*uuid.UUID, error) {
	var id *uuid.UUID
	err := r.pool.QueryRow(ctx, `SELECT cover_image_id FROM projects WHERE id = $1`, projectID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return id, err
}

// SetProjectCover points a project's cover at an asset (nil clears it).
func (r *ProjectRepo) SetProjectCover(ctx context.Context, projectID uuid.UUID, assetID *uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE projects SET cover_image_id = $2 WHERE id = $1`, projectID, assetID)
	return err
}

// ── Boards ───────────────────────────────────────────────────────────────────

func (r *ProjectRepo) ListBoards(ctx context.Context, projectID uuid.UUID) ([]models.Board, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT b.id, b.project_id, b.name, COALESCE(b.description, ''), b.revision,
		       COALESCE(b.source_filename, ''), b.source_format, b.kind, b.copies, b.position,
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
		if err := rows.Scan(&b.ID, &b.ProjectID, &b.Name, &b.Description, &b.Revision,
			&b.SourceFilename, &b.SourceFormat, &b.Kind, &b.Copies, &b.Position,
			&b.CreatedAt, &b.UpdatedAt, &b.LineCount); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (r *ProjectRepo) CreateBoard(ctx context.Context, b *models.Board) error {
	kind := b.Kind
	if kind == "" {
		kind = "board"
	}
	copies := b.Copies
	if copies < 1 {
		copies = 1
	}
	return r.pool.QueryRow(ctx, `
		INSERT INTO project_boards (project_id, name, description, revision, source_filename, source_format, kind, copies, position)
		VALUES ($1, $2, NULLIF($3, ''), $4, NULLIF($5, ''), $6, $7, $8,
		        COALESCE((SELECT MAX(position)+1 FROM project_boards WHERE project_id = $1), 0))
		RETURNING id, project_id, name, COALESCE(description, ''), revision,
		          COALESCE(source_filename, ''), source_format, kind, copies, position, created_at, updated_at`,
		b.ProjectID, b.Name, b.Description, b.Revision, b.SourceFilename, b.SourceFormat, kind, copies).
		Scan(&b.ID, &b.ProjectID, &b.Name, &b.Description, &b.Revision, &b.SourceFilename,
			&b.SourceFormat, &b.Kind, &b.Copies, &b.Position, &b.CreatedAt, &b.UpdatedAt)
}

// UpdateBoard changes a board's name, revision, and copy count (the panel N-up).
func (r *ProjectRepo) UpdateBoard(ctx context.Context, id uuid.UUID, name, revision string, copies int) (*models.Board, error) {
	if copies < 1 {
		copies = 1
	}
	var b models.Board
	err := r.pool.QueryRow(ctx, `
		UPDATE project_boards SET name = COALESCE(NULLIF($2, ''), name), revision = $3, copies = $4 WHERE id = $1
		RETURNING id, project_id, name, COALESCE(description, ''), revision,
		          COALESCE(source_filename, ''), source_format, kind, copies, position, created_at, updated_at`,
		id, name, revision, copies).Scan(&b.ID, &b.ProjectID, &b.Name, &b.Description, &b.Revision, &b.SourceFilename,
		&b.SourceFormat, &b.Kind, &b.Copies, &b.Position, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &b, nil
}

// GetBoard returns a board with its BOM lines (part name joined when matched).
func (r *ProjectRepo) GetBoard(ctx context.Context, id uuid.UUID) (*models.Board, error) {
	var b models.Board
	err := r.pool.QueryRow(ctx, `
		SELECT id, project_id, name, COALESCE(description, ''), revision,
		       COALESCE(source_filename, ''), source_format, kind, copies, position, created_at, updated_at
		FROM project_boards WHERE id = $1`, id).
		Scan(&b.ID, &b.ProjectID, &b.Name, &b.Description, &b.Revision, &b.SourceFilename,
			&b.SourceFormat, &b.Kind, &b.Copies, &b.Position, &b.CreatedAt, &b.UpdatedAt)
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
		       l.manufacturer, l.supplier_sku, l.ipn, l.description, l.part_id, COALESCE(p.name, ''), l.match_kind, l.position
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
			&l.MPN, &l.Manufacturer, &l.SupplierSKU, &l.IPN, &l.Description, &l.PartID, &l.PartName, &l.MatchKind, &l.Position); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// CreateBOMLine appends one manually-entered BOM line to a board.
func (r *ProjectRepo) CreateBOMLine(ctx context.Context, l *models.BOMLine) error {
	return r.pool.QueryRow(ctx, `
		INSERT INTO board_bom_lines
		  (board_id, refs, quantity, value, footprint, mpn, manufacturer, supplier_sku, ipn, description, part_id, match_kind, position)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,
		        COALESCE((SELECT MAX(position)+1 FROM board_bom_lines WHERE board_id = $1), 0))
		RETURNING id, position`,
		l.BoardID, l.Refs, l.Quantity, l.Value, l.Footprint, l.MPN, l.Manufacturer, l.SupplierSKU, l.IPN, l.Description, l.PartID, l.MatchKind).
		Scan(&l.ID, &l.Position)
}

// UpdateBOMLine edits one BOM line's fields (and its resolved match).
func (r *ProjectRepo) UpdateBOMLine(ctx context.Context, l *models.BOMLine) (uuid.UUID, error) {
	var boardID uuid.UUID
	err := r.pool.QueryRow(ctx, `
		UPDATE board_bom_lines
		SET refs=$2, quantity=$3, value=$4, footprint=$5, mpn=$6, manufacturer=$7, supplier_sku=$8, ipn=$9, description=$10, part_id=$11, match_kind=$12
		WHERE id=$1 RETURNING board_id`,
		l.ID, l.Refs, l.Quantity, l.Value, l.Footprint, l.MPN, l.Manufacturer, l.SupplierSKU, l.IPN, l.Description, l.PartID, l.MatchKind).
		Scan(&boardID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, nil
		}
		return uuid.Nil, err
	}
	return boardID, nil
}

// GetBOMLine loads one line (with resolved part name) for returning after edits.
func (r *ProjectRepo) GetBOMLine(ctx context.Context, id uuid.UUID) (*models.BOMLine, error) {
	var l models.BOMLine
	err := r.pool.QueryRow(ctx, `
		SELECT l.id, l.board_id, l.refs, l.quantity, l.value, l.footprint, l.mpn,
		       l.manufacturer, l.supplier_sku, l.ipn, l.description, l.part_id, COALESCE(p.name, ''), l.match_kind, l.position
		FROM board_bom_lines l LEFT JOIN parts p ON p.id = l.part_id
		WHERE l.id = $1`, id).
		Scan(&l.ID, &l.BoardID, &l.Refs, &l.Quantity, &l.Value, &l.Footprint, &l.MPN,
			&l.Manufacturer, &l.SupplierSKU, &l.IPN, &l.Description, &l.PartID, &l.PartName, &l.MatchKind, &l.Position)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &l, nil
}

func (r *ProjectRepo) DeleteBOMLine(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	var boardID uuid.UUID
	err := r.pool.QueryRow(ctx, `DELETE FROM board_bom_lines WHERE id = $1 RETURNING board_id`, id).Scan(&boardID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, nil
		}
		return uuid.Nil, err
	}
	return boardID, nil
}

// ReplaceBOMLines swaps a board's BOM for a freshly-parsed set, in a transaction.
func (r *ProjectRepo) ReplaceBOMLines(ctx context.Context, boardID uuid.UUID, lines []models.BOMLine) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM board_bom_lines WHERE board_id = $1`, boardID); err != nil {
		return err
	}
	for i, l := range lines {
		if _, err := tx.Exec(ctx, `
			INSERT INTO board_bom_lines
			  (board_id, refs, quantity, value, footprint, mpn, manufacturer, supplier_sku, ipn, description, part_id, match_kind, position)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
			boardID, l.Refs, l.Quantity, l.Value, l.Footprint, l.MPN,
			l.Manufacturer, l.SupplierSKU, l.IPN, l.Description, l.PartID, l.MatchKind, i); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ── Project match memory ─────────────────────────────────────────────────────

// UpsertProjectMatch records that a BOM identity (match_key) maps to a part for
// this project, so the choice propagates across all its boards.
func (r *ProjectRepo) UpsertProjectMatch(ctx context.Context, projectID uuid.UUID, key string, partID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO project_matches (project_id, match_key, part_id) VALUES ($1, $2, $3)
		ON CONFLICT (project_id, match_key) DO UPDATE SET part_id = EXCLUDED.part_id`,
		projectID, key, partID)
	return err
}

func (r *ProjectRepo) DeleteProjectMatch(ctx context.Context, projectID uuid.UUID, key string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM project_matches WHERE project_id = $1 AND match_key = $2`, projectID, key)
	return err
}

// ProjectMatch resolves a match key to a part within a project.
func (r *ProjectRepo) ProjectMatch(ctx context.Context, projectID uuid.UUID, key string) (uuid.UUID, bool, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `SELECT part_id FROM project_matches WHERE project_id = $1 AND match_key = $2`, projectID, key).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, nil
		}
		return uuid.Nil, false, err
	}
	return id, true, nil
}

// ProjectIDForBoard returns the project a board belongs to.
func (r *ProjectRepo) ProjectIDForBoard(ctx context.Context, boardID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `SELECT project_id FROM project_boards WHERE id = $1`, boardID).Scan(&id)
	return id, err
}

// ProjectBoardIDs returns every board id in a project (for re-matching).
func (r *ProjectRepo) ProjectBoardIDs(ctx context.Context, projectID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := r.pool.Query(ctx, `SELECT id FROM project_boards WHERE project_id = $1`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// LinesForBoard returns a board's BOM lines (for re-matching); alias of the
// internal list used by GetBoard.
func (r *ProjectRepo) LinesForBoard(ctx context.Context, boardID uuid.UUID) ([]models.BOMLine, error) {
	return r.listLines(ctx, boardID)
}

// SetLineMatch updates only a line's resolved part + match kind.
func (r *ProjectRepo) SetLineMatch(ctx context.Context, lineID uuid.UUID, partID *uuid.UUID, kind string) error {
	_, err := r.pool.Exec(ctx, `UPDATE board_bom_lines SET part_id = $2, match_kind = $3 WHERE id = $1`, lineID, partID, kind)
	return err
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

// DeleteBoardAssetsOfKind removes all of a board's assets of one kind (e.g. its
// iBOM when replacing or clearing it). Returns how many were removed.
func (r *ProjectRepo) DeleteBoardAssetsOfKind(ctx context.Context, boardID uuid.UUID, kind string) (int64, error) {
	ct, err := r.pool.Exec(ctx, `DELETE FROM project_assets WHERE board_id = $1 AND kind = $2`, boardID, kind)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
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
