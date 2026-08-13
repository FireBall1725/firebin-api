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

type DatasheetRepo struct{ pool *pgxpool.Pool }

func NewDatasheetRepo(pool *pgxpool.Pool) *DatasheetRepo { return &DatasheetRepo{pool: pool} }

// datasheetColList is the column order scanDatasheet reads. One constant so a
// new column cannot be added to one query and forgotten in another.
//
// Two forms because RETURNING has no table alias to qualify with, while the
// SELECTs join and need one. Derived from a single list rather than written
// twice, so they cannot drift apart.
const datasheetColList = `id, sha256, filename, title, mime, size_bytes, page_count,
	source_url, origin, language, text_status, extracted_at, category_id, created_at, updated_at`

// datasheetCols is the same list qualified with the `d` alias used by the
// SELECT queries below.
var datasheetCols = qualify(datasheetColList, "d")

// qualify prefixes every comma-separated column with a table alias.
func qualify(cols, alias string) string {
	parts := strings.Split(cols, ",")
	for i, c := range parts {
		parts[i] = alias + "." + strings.TrimSpace(c)
	}
	return strings.Join(parts, ", ")
}

func scanDatasheet(row pgx.Row) (*models.Datasheet, error) {
	var d models.Datasheet
	if err := row.Scan(
		&d.ID, &d.SHA256, &d.Filename, &d.Title, &d.Mime, &d.SizeBytes, &d.PageCount,
		&d.SourceURL, &d.Origin, &d.Language, &d.TextStatus, &d.ExtractedAt, &d.CategoryID,
		&d.CreatedAt, &d.UpdatedAt,
	); err != nil {
		return nil, err
	}
	d.Parts = []models.DatasheetPartLink{}
	return &d, nil
}

// NewDatasheet is the input to Create.
type NewDatasheet struct {
	SHA256    string
	Filename  string
	Title     *string
	Mime      string
	SizeBytes int64
	SourceURL *string
	Origin    string
	CreatedBy *uuid.UUID
}

// Create records a stored file, or returns the existing row when the same bytes
// are already held.
//
// The ON CONFLICT is the point, not a safety net. One PDF routinely covers a
// whole product family, so mirroring six MPNs of the same series fetches six
// times and stores one row and one file. The caller then links the part, which
// is what actually differs between those six calls.
func (r *DatasheetRepo) Create(ctx context.Context, in NewDatasheet) (*models.Datasheet, error) {
	if in.Mime == "" {
		in.Mime = "application/pdf"
	}
	if in.Origin == "" {
		in.Origin = models.OriginUpload
	}
	// DO UPDATE rather than DO NOTHING so RETURNING always yields a row; DO
	// NOTHING returns nothing on conflict and would need a second SELECT.
	// filename is refreshed because a later, better-named copy of identical bytes
	// is worth keeping.
	row := r.pool.QueryRow(ctx, `
		INSERT INTO datasheets (sha256, filename, title, mime, size_bytes, source_url, origin, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (sha256) DO UPDATE SET
			filename = EXCLUDED.filename,
			title = COALESCE(datasheets.title, EXCLUDED.title),
			source_url = COALESCE(datasheets.source_url, EXCLUDED.source_url),
			updated_at = NOW()
		RETURNING `+datasheetColList,
		in.SHA256, in.Filename, in.Title, in.Mime, in.SizeBytes, in.SourceURL, in.Origin, in.CreatedBy)
	return scanDatasheet(row)
}

// Get returns one datasheet with its part links.
func (r *DatasheetRepo) Get(ctx context.Context, id uuid.UUID) (*models.Datasheet, error) {
	d, err := scanDatasheet(r.pool.QueryRow(ctx,
		`SELECT `+datasheetCols+` FROM datasheets d WHERE d.id = $1`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	links, err := r.linksFor(ctx, []uuid.UUID{id})
	if err != nil {
		return nil, err
	}
	d.Parts = links[id]
	if d.Parts == nil {
		d.Parts = []models.DatasheetPartLink{}
	}
	return d, nil
}

// GetBySHA looks a datasheet up by content hash. Used by the mirror job to skip
// work when the bytes are already held.
func (r *DatasheetRepo) GetBySHA(ctx context.Context, sha string) (*models.Datasheet, error) {
	d, err := scanDatasheet(r.pool.QueryRow(ctx,
		`SELECT `+datasheetCols+` FROM datasheets d WHERE d.sha256 = $1`, sha))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return d, nil
}

// DatasheetListOptions filters the library view.
type DatasheetListOptions struct {
	Search     string
	CategoryID *uuid.UUID
	PartID     *uuid.UUID
	// Unlinked restricts to documents with no part links: the "saw a part
	// online, saved it for later" pile. It is deliberately reachable as a filter
	// rather than a separate list.
	Unlinked bool
	Limit    int
}

// List returns datasheets with their part links attached.
func (r *DatasheetRepo) List(ctx context.Context, opts DatasheetListOptions) ([]models.Datasheet, error) {
	q := strings.Builder{}
	q.WriteString(`SELECT ` + datasheetCols + ` FROM datasheets d WHERE 1=1`)
	args := []any{}

	if opts.Unlinked {
		q.WriteString(` AND NOT EXISTS (SELECT 1 FROM datasheet_parts dp WHERE dp.datasheet_id = d.id)`)
	}
	if opts.PartID != nil {
		args = append(args, *opts.PartID)
		q.WriteString(` AND EXISTS (SELECT 1 FROM datasheet_parts dp
			WHERE dp.datasheet_id = d.id AND dp.part_id = $` + itoa(len(args)) + `)`)
	}
	if opts.CategoryID != nil {
		// Either the document's own category or one borrowed from a linked part.
		// The borrowed form came first and is still right for a mirrored
		// datasheet; the OR is what lets a loose upload be sorted at all, since
		// it has no parts to borrow from.
		args = append(args, *opts.CategoryID)
		n := itoa(len(args))
		q.WriteString(` AND (d.category_id = $` + n + ` OR EXISTS (SELECT 1 FROM datasheet_parts dp
			JOIN parts p ON p.id = dp.part_id
			WHERE dp.datasheet_id = d.id AND p.category_id = $` + n + `))`)
	}
	if s := strings.TrimSpace(opts.Search); s != "" {
		args = append(args, "%"+s+"%")
		n := itoa(len(args))
		// Title, filename, and any linked MPN. Searching the extracted text would
		// mean a global index, which is deliberately not built: the sidecar is
		// scanned per-document by the assistant instead.
		q.WriteString(` AND (d.title ILIKE $` + n + ` OR d.filename ILIKE $` + n +
			` OR EXISTS (SELECT 1 FROM datasheet_parts dp
				JOIN manufacturer_parts mp ON mp.part_id = dp.part_id
				WHERE dp.datasheet_id = d.id AND mp.mpn ILIKE $` + n + `)
			 OR EXISTS (SELECT 1 FROM datasheet_parts dp
				JOIN parts p ON p.id = dp.part_id
				WHERE dp.datasheet_id = d.id AND p.name ILIKE $` + n + `))`)
	}

	limit := opts.Limit
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	args = append(args, limit)
	q.WriteString(` ORDER BY d.created_at DESC LIMIT $` + itoa(len(args)))

	rows, err := r.pool.Query(ctx, q.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.Datasheet{}
	ids := []uuid.UUID{}
	for rows.Next() {
		d, err := scanDatasheet(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
		ids = append(ids, d.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	links, err := r.linksFor(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if l, ok := links[out[i].ID]; ok {
			out[i].Parts = l
		}
	}
	return out, nil
}

// linksFor fetches part links for a set of datasheets in one query.
//
// A second query rather than a json_agg in the list query: aggregating would
// make the row scan depend on JSON shape, and this repository has already been
// bitten once by a scan whose column list drifted from its Scan call.
func (r *DatasheetRepo) linksFor(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]models.DatasheetPartLink, error) {
	out := map[uuid.UUID][]models.DatasheetPartLink{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT dp.datasheet_id, dp.part_id, p.name, dp.manufacturer_part_id, mp.mpn, p.category_id, c.name
		FROM datasheet_parts dp
		JOIN parts p ON p.id = dp.part_id
		LEFT JOIN manufacturer_parts mp ON mp.id = dp.manufacturer_part_id
		LEFT JOIN categories c ON c.id = p.category_id
		WHERE dp.datasheet_id = ANY($1)
		ORDER BY p.name`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var dsID uuid.UUID
		var l models.DatasheetPartLink
		if err := rows.Scan(&dsID, &l.PartID, &l.PartName, &l.ManufacturerPartID, &l.MPN,
			&l.CategoryID, &l.CategoryName); err != nil {
			return nil, err
		}
		out[dsID] = append(out[dsID], l)
	}
	return out, rows.Err()
}

// LinkPart attaches a datasheet to a part. Idempotent.
func (r *DatasheetRepo) LinkPart(ctx context.Context, dsID, partID uuid.UUID, mpID *uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO datasheet_parts (datasheet_id, part_id, manufacturer_part_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (datasheet_id, part_id)
		DO UPDATE SET manufacturer_part_id = COALESCE(EXCLUDED.manufacturer_part_id, datasheet_parts.manufacturer_part_id)`,
		dsID, partID, mpID)
	return err
}

// UnlinkPart detaches a datasheet from a part. The document itself survives with
// no links, landing in the Unlinked bucket rather than being deleted.
func (r *DatasheetRepo) UnlinkPart(ctx context.Context, dsID, partID uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM datasheet_parts WHERE datasheet_id = $1 AND part_id = $2`, dsID, partID)
	return err
}

// SetTitle renames a datasheet. A nil title clears it, falling back to the
// filename wherever the title is displayed.
func (r *DatasheetRepo) SetTitle(ctx context.Context, id uuid.UUID, title *string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE datasheets SET title = $2, updated_at = NOW() WHERE id = $1`, id, title)
	return err
}

// SetTitleIfUnset fills in a title only where there is none.
//
// Used by extraction, which reads the title the PDF declares about itself. That
// is a guess and a person's is not, so it must never overwrite one: the WHERE
// clause is the whole point of this being separate from SetTitle.
func (r *DatasheetRepo) SetTitleIfUnset(ctx context.Context, id uuid.UUID, title string) error {
	if strings.TrimSpace(title) == "" {
		return nil
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE datasheets SET title = $2, updated_at = NOW()
		WHERE id = $1 AND (title IS NULL OR title = '')`, id, title)
	return err
}

// SetCategory files a datasheet under a category of its own. A nil id clears it,
// leaving the document sorted by whatever parts it is linked to.
func (r *DatasheetRepo) SetCategory(ctx context.Context, id uuid.UUID, categoryID *uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE datasheets SET category_id = $2, updated_at = NOW() WHERE id = $1`, id, categoryID)
	return err
}

// SetExtraction records the outcome of a text-extraction run.
func (r *DatasheetRepo) SetExtraction(ctx context.Context, id uuid.UUID, pages *int, language *string, status string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE datasheets
		SET page_count = COALESCE($2, page_count), language = COALESCE($3, language),
		    text_status = $4, extracted_at = NOW()
		WHERE id = $1`, id, pages, language, status)
	return err
}

// Delete removes the row and returns its hash so the caller can drop the file.
// Returns "" when the row was already gone.
func (r *DatasheetRepo) Delete(ctx context.Context, id uuid.UUID) (string, error) {
	var sha string
	err := r.pool.QueryRow(ctx, `DELETE FROM datasheets WHERE id = $1 RETURNING sha256`, id).Scan(&sha)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return sha, nil
}

// MirrorCandidate is one MPN carrying a datasheet URL with no local copy yet.
type MirrorCandidate struct {
	ManufacturerPartID uuid.UUID
	PartID             uuid.UUID
	MPN                string
	DatasheetURL       string
}

// MirrorCandidates lists MPNs whose datasheet_url has not been mirrored.
//
// "Not mirrored" is judged per PART, not per MPN: once any datasheet is linked
// to the part, its siblings are almost always the same family PDF, and fetching
// each one to discover they hash identically wastes the user's bandwidth on a
// backfill that could run to hundreds of files.
func (r *DatasheetRepo) MirrorCandidates(ctx context.Context, limit int) ([]MirrorCandidate, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT ON (mp.part_id) mp.id, mp.part_id, mp.mpn, mp.datasheet_url
		FROM manufacturer_parts mp
		WHERE mp.datasheet_url IS NOT NULL AND mp.datasheet_url <> ''
		  AND NOT EXISTS (SELECT 1 FROM datasheet_parts dp WHERE dp.part_id = mp.part_id)
		ORDER BY mp.part_id, mp.created_at
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MirrorCandidate{}
	for rows.Next() {
		var c MirrorCandidate
		if err := rows.Scan(&c.ManufacturerPartID, &c.PartID, &c.MPN, &c.DatasheetURL); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CountMirrorCandidates is the number behind the "Mirror missing (N)" button.
func (r *DatasheetRepo) CountMirrorCandidates(ctx context.Context) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `
		SELECT COUNT(DISTINCT mp.part_id) FROM manufacturer_parts mp
		WHERE mp.datasheet_url IS NOT NULL AND mp.datasheet_url <> ''
		  AND NOT EXISTS (SELECT 1 FROM datasheet_parts dp WHERE dp.part_id = mp.part_id)`).Scan(&n)
	return n, err
}

// CountUnlinked is the Unlinked badge in the library rail.
func (r *DatasheetRepo) CountUnlinked(ctx context.Context) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM datasheets d
		WHERE NOT EXISTS (SELECT 1 FROM datasheet_parts dp WHERE dp.datasheet_id = d.id)`).Scan(&n)
	return n, err
}

// Stats is the header line on the library page.
type DatasheetStats struct {
	Count            int   `json:"count"`
	TotalBytes       int64 `json:"total_bytes"`
	Unlinked         int   `json:"unlinked"`
	MirrorCandidates int   `json:"mirror_candidates"`
}

// Stats returns the library totals.
func (r *DatasheetRepo) Stats(ctx context.Context) (*DatasheetStats, error) {
	var s DatasheetStats
	if err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*), COALESCE(SUM(size_bytes), 0) FROM datasheets`).Scan(&s.Count, &s.TotalBytes); err != nil {
		return nil, err
	}
	var err error
	if s.Unlinked, err = r.CountUnlinked(ctx); err != nil {
		return nil, err
	}
	if s.MirrorCandidates, err = r.CountMirrorCandidates(ctx); err != nil {
		return nil, err
	}
	return &s, nil
}

// PendingExtraction lists datasheets whose text has not been pulled yet, for the
// extraction job to pick up.
func (r *DatasheetRepo) PendingExtraction(ctx context.Context, limit int) ([]models.Datasheet, error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+datasheetCols+` FROM datasheets d WHERE d.text_status = $1 ORDER BY d.created_at LIMIT $2`,
		models.TextPending, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Datasheet{}
	for rows.Next() {
		d, err := scanDatasheet(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}
