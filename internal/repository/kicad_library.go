// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// KicadLibraryRepo owns the uploaded copy of the user's KiCad libraries: the
// identifier index behind typeahead, and the source behind previews.
type KicadLibraryRepo struct{ pool *pgxpool.Pool }

func NewKicadLibraryRepo(pool *pgxpool.Pool) *KicadLibraryRepo {
	return &KicadLibraryRepo{pool: pool}
}

// searchLimit caps a typeahead response. A stock install indexes about 38,000
// items, so an uncapped prefix like "R" would return thousands of rows nobody
// scrolls.
const searchLimit = 50

// maxSourceBytes rejects an implausible upload before it reaches the database.
// The largest thing here is a whole .kicad_mod, which runs to tens of KB.
const maxSourceBytes = 4 << 20

// Search returns items of one kind whose "Lib:Name" contains every
// whitespace-separated term in q, so "0603 res" finds
// "Resistor_SMD:R_0603_1608Metric" regardless of term order.
//
// Shorter identifiers sort first: someone typing "Device:R" wants "Device:R",
// not "Device:R_Potentiometer_Trim".
func (r *KicadLibraryRepo) Search(ctx context.Context, kind, q string) ([]models.KicadLibraryItem, error) {
	args := []any{kind}
	var where strings.Builder
	where.WriteString(`WHERE kind = $1`)
	for _, term := range strings.Fields(q) {
		args = append(args, "%"+term+"%")
		where.WriteString(fmt.Sprintf(` AND (lib || ':' || name) ILIKE $%d`, len(args)))
	}

	rows, err := r.pool.Query(ctx, `
		SELECT kind, lib, name, source IS NOT NULL
		FROM kicad_library_items `+where.String()+`
		ORDER BY length(lib) + length(name), lib, name
		LIMIT `+fmt.Sprint(searchLimit), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.KicadLibraryItem{}
	for rows.Next() {
		var it models.KicadLibraryItem
		if err := rows.Scan(&it.Kind, &it.Lib, &it.Name, &it.HasSource); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// Libraries lists the distinct library nicknames with their item counts, for
// the viewer's left-hand list.
func (r *KicadLibraryRepo) Libraries(ctx context.Context, kind string) ([]models.KicadLibrarySummary, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT kind, lib, count(*)::int, count(source)::int
		FROM kicad_library_items
		WHERE ($1 = '' OR kind = $1)
		GROUP BY kind, lib
		ORDER BY lib`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.KicadLibrarySummary{}
	for rows.Next() {
		var s models.KicadLibrarySummary
		if err := rows.Scan(&s.Kind, &s.Lib, &s.Count, &s.WithSource); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Items lists one library's contents.
func (r *KicadLibraryRepo) Items(ctx context.Context, kind, lib string) ([]models.KicadLibraryItem, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT kind, lib, name, source IS NOT NULL
		FROM kicad_library_items
		WHERE kind = $1 AND lib = $2
		ORDER BY name`, kind, lib)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.KicadLibraryItem{}
	for rows.Next() {
		var it models.KicadLibraryItem
		if err := rows.Scan(&it.Kind, &it.Lib, &it.Name, &it.HasSource); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// Source returns the decompressed S-expression for one item.
func (r *KicadLibraryRepo) Source(ctx context.Context, kind, lib, name string) ([]byte, error) {
	var gz []byte
	err := r.pool.QueryRow(ctx,
		`SELECT source FROM kicad_library_items WHERE kind=$1 AND lib=$2 AND name=$3`,
		kind, lib, name).Scan(&gz)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if len(gz) == 0 {
		return nil, ErrNotFound
	}
	return gunzip(gz)
}

// Drawing returns the cached render data for one item, or nil when it has not
// been computed yet.
func (r *KicadLibraryRepo) Drawing(ctx context.Context, kind, lib, name string) (json.RawMessage, error) {
	var raw []byte
	err := r.pool.QueryRow(ctx,
		`SELECT drawing FROM kicad_library_items WHERE kind=$1 AND lib=$2 AND name=$3`,
		kind, lib, name).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// SaveDrawing caches render data so the parse happens once per item, not once
// per page view.
func (r *KicadLibraryRepo) SaveDrawing(ctx context.Context, kind, lib, name string, drawing json.RawMessage) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE kicad_library_items SET drawing=$4 WHERE kind=$1 AND lib=$2 AND name=$3`,
		kind, lib, name, drawing)
	return err
}

// UpsertBatch writes one chunk of a scan. Called repeatedly; the scan is not
// visible as "current" until FinishScan runs.
func (r *KicadLibraryRepo) UpsertBatch(ctx context.Context, scanID uuid.UUID, items []models.KicadLibraryUpload) (int64, error) {
	if len(items) == 0 {
		return 0, nil
	}
	batch := &pgx.Batch{}
	for _, it := range items {
		var gz []byte
		if it.Source != "" {
			if len(it.Source) > maxSourceBytes {
				return 0, fmt.Errorf("%s:%s source is %d bytes, over the %d limit", it.Lib, it.Name, len(it.Source), maxSourceBytes)
			}
			var err error
			if gz, err = gzipBytes([]byte(it.Source)); err != nil {
				return 0, err
			}
		}
		// Re-uploading an item clears its cached drawing: the source may have
		// changed, and a stale drawing would render the old geometry forever.
		batch.Queue(`
			INSERT INTO kicad_library_items (scan_id, kind, lib, name, source)
			VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (kind, lib, name) DO UPDATE
			SET scan_id = EXCLUDED.scan_id,
			    source  = COALESCE(EXCLUDED.source, kicad_library_items.source),
			    drawing = CASE WHEN EXCLUDED.source IS DISTINCT FROM kicad_library_items.source
			                   THEN NULL ELSE kicad_library_items.drawing END`,
			scanID, it.Kind, it.Lib, it.Name, gz)
	}
	res := r.pool.SendBatch(ctx, batch)
	defer res.Close()

	var n int64
	for range items {
		tag, err := res.Exec()
		if err != nil {
			return n, err
		}
		n += tag.RowsAffected()
	}
	return n, nil
}

// FinishScan drops everything not carried by this scan and records provenance.
// This is the point at which a library the user uninstalled disappears.
func (r *KicadLibraryRepo) FinishScan(ctx context.Context, scanID uuid.UUID, source, kicadVersion string) (*models.KicadIndexMeta, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM kicad_library_items WHERE scan_id <> $1`, scanID); err != nil {
		return nil, err
	}

	var meta models.KicadIndexMeta
	err = tx.QueryRow(ctx, `
		WITH counts AS (
			SELECT
				count(*) FILTER (WHERE kind='symbol')::int    AS symbols,
				count(*) FILTER (WHERE kind='footprint')::int AS footprints,
				COALESCE(sum(length(source)), 0)::bigint      AS bytes
			FROM kicad_library_items
		)
		INSERT INTO kicad_library_index_meta
			(id, scan_id, source, kicad_version, scanned_at, symbol_count, footprint_count, bytes_stored)
		SELECT 1, $1, $2, NULLIF($3,''), now(), symbols, footprints, bytes FROM counts
		ON CONFLICT (id) DO UPDATE SET
			scan_id = EXCLUDED.scan_id, source = EXCLUDED.source,
			kicad_version = EXCLUDED.kicad_version, scanned_at = EXCLUDED.scanned_at,
			symbol_count = EXCLUDED.symbol_count, footprint_count = EXCLUDED.footprint_count,
			bytes_stored = EXCLUDED.bytes_stored
		RETURNING source, COALESCE(kicad_version,''), scanned_at, symbol_count, footprint_count, bytes_stored`,
		scanID, source, kicadVersion).
		Scan(&meta.Source, &meta.KicadVersion, &meta.ScannedAt, &meta.SymbolCount, &meta.FootprintCount, &meta.BytesStored)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &meta, nil
}

// Meta returns index provenance, or nil when nothing has been scanned yet.
func (r *KicadLibraryRepo) Meta(ctx context.Context) (*models.KicadIndexMeta, error) {
	var m models.KicadIndexMeta
	err := r.pool.QueryRow(ctx, `
		SELECT source, COALESCE(kicad_version,''), scanned_at, symbol_count, footprint_count, bytes_stored
		FROM kicad_library_index_meta WHERE id = 1`).
		Scan(&m.Source, &m.KicadVersion, &m.ScannedAt, &m.SymbolCount, &m.FootprintCount, &m.BytesStored)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func gzipBytes(b []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(b); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func gunzip(b []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(io.LimitReader(zr, maxSourceBytes))
}
