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

type PartRepo struct{ pool *pgxpool.Pool }

func NewPartRepo(pool *pgxpool.Pool) *PartRepo { return &PartRepo{pool: pool} }

// partCols selects the base part columns, casting numeric to float8 so pgx can
// scan straight into float64.
const partCols = `id, category_id, variant_of, name, description, ipn, package, keywords,
	barcode, image_path, is_template, is_component, is_assembly, is_purchaseable,
	is_trackable, minimum_stock::float8, default_location_id, created_at, updated_at`

func scanPart(row pgx.Row) (*models.Part, error) {
	var p models.Part
	if err := row.Scan(
		&p.ID, &p.CategoryID, &p.VariantOf, &p.Name, &p.Description, &p.IPN, &p.Package, &p.Keywords,
		&p.Barcode, &p.ImagePath, &p.IsTemplate, &p.IsComponent, &p.IsAssembly, &p.IsPurchaseable,
		&p.IsTrackable, &p.MinimumStock, &p.DefaultLocationID, &p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}

// ListOptions filters and shapes a part listing.
type ListOptions struct {
	CategoryID *uuid.UUID
	Search     string
	TopLevel   bool // only parts with no parent (templates + standalone parts)
}

// List returns parts matching the options, each annotated with total stock and
// (for templates) a variant count. Uses a correlated subquery for the stock
// sum so parts with no stock still appear.
func (r *PartRepo) List(ctx context.Context, opts ListOptions) ([]models.Part, error) {
	q := strings.Builder{}
	// total_stock rolls up a template's variants: stock held directly on the
	// part plus stock on any of its variants (the model is one level deep).
	q.WriteString(`SELECT ` + partCols + `,
		COALESCE((SELECT SUM(quantity) FROM stock_items s
			WHERE s.part_id = parts.id
			   OR s.part_id IN (SELECT id FROM parts v WHERE v.variant_of = parts.id)), 0)::float8 AS total_stock,
		(SELECT COUNT(*) FROM parts v WHERE v.variant_of = parts.id)::int AS variant_count
		FROM parts WHERE 1=1`)
	args := []any{}
	if opts.TopLevel {
		q.WriteString(` AND variant_of IS NULL`)
	}
	if opts.CategoryID != nil {
		args = append(args, *opts.CategoryID)
		q.WriteString(` AND category_id = $` + itoa(len(args)))
	}
	if s := strings.TrimSpace(opts.Search); s != "" {
		args = append(args, "%"+s+"%")
		n := itoa(len(args))
		// Match name/keywords, or any linked manufacturer part number.
		q.WriteString(` AND (name ILIKE $` + n + ` OR keywords ILIKE $` + n +
			` OR EXISTS (SELECT 1 FROM manufacturer_parts mp WHERE mp.part_id = parts.id AND mp.mpn ILIKE $` + n + `))`)
	}
	q.WriteString(` ORDER BY name LIMIT 500`)

	rows, err := r.pool.Query(ctx, q.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.Part{}
	for rows.Next() {
		p, err := scanPartWithTotals(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// scanPartWithTotals scans the base part columns plus the trailing total_stock
// and variant_count computed columns (as selected by List and Get's variant
// query).
func scanPartWithTotals(row pgx.Row) (*models.Part, error) {
	var p models.Part
	if err := row.Scan(
		&p.ID, &p.CategoryID, &p.VariantOf, &p.Name, &p.Description, &p.IPN, &p.Package, &p.Keywords,
		&p.Barcode, &p.ImagePath, &p.IsTemplate, &p.IsComponent, &p.IsAssembly, &p.IsPurchaseable,
		&p.IsTrackable, &p.MinimumStock, &p.DefaultLocationID, &p.CreatedAt, &p.UpdatedAt,
		&p.TotalStock, &p.VariantCount,
	); err != nil {
		return nil, err
	}
	return &p, nil
}

// ListLowStock returns leaf parts (no variants) whose stock is at or below a
// positive minimum, most-depleted first.
func (r *PartRepo) ListLowStock(ctx context.Context) ([]models.Part, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+partCols+`,
		COALESCE((SELECT SUM(quantity) FROM stock_items s WHERE s.part_id = parts.id), 0)::float8 AS total_stock,
		0::int AS variant_count
		FROM parts
		WHERE minimum_stock > 0
		  AND NOT EXISTS (SELECT 1 FROM parts v WHERE v.variant_of = parts.id)
		  AND COALESCE((SELECT SUM(quantity) FROM stock_items s WHERE s.part_id = parts.id), 0) <= minimum_stock
		ORDER BY (COALESCE((SELECT SUM(quantity) FROM stock_items s WHERE s.part_id = parts.id), 0) - minimum_stock) ASC
		LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Part{}
	for rows.Next() {
		p, err := scanPartWithTotals(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// Get returns one part with its parameters, total stock, and (for templates)
// its variants.
func (r *PartRepo) Get(ctx context.Context, id uuid.UUID) (*models.Part, error) {
	p, err := scanPart(r.pool.QueryRow(ctx, `SELECT `+partCols+` FROM parts WHERE id = $1`, id))
	if err != nil {
		return nil, err
	}
	if err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(quantity), 0)::float8 FROM stock_items
		WHERE part_id = $1 OR part_id IN (SELECT id FROM parts WHERE variant_of = $1)`, id,
	).Scan(&p.TotalStock); err != nil {
		return nil, err
	}
	params, err := r.GetParameters(ctx, id)
	if err != nil {
		return nil, err
	}
	p.Parameters = params

	// Nest variants for templates, each annotated with its own total stock and
	// (nested) variant count — same shape as List rows.
	rows, err := r.pool.Query(ctx, `SELECT `+partCols+`,
		COALESCE((SELECT SUM(quantity) FROM stock_items s WHERE s.part_id = parts.id), 0)::float8 AS total_stock,
		(SELECT COUNT(*) FROM parts vv WHERE vv.variant_of = parts.id)::int AS variant_count
		FROM parts WHERE variant_of = $1 ORDER BY name`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		v, err := scanPartWithTotals(rows)
		if err != nil {
			return nil, err
		}
		p.Variants = append(p.Variants, *v)
	}
	return p, rows.Err()
}

func (r *PartRepo) Create(ctx context.Context, p *models.Part) error {
	return r.pool.QueryRow(ctx, `
		INSERT INTO parts (category_id, variant_of, name, description, ipn, package, keywords,
			barcode, image_path, is_template, is_component, is_assembly, is_purchaseable,
			is_trackable, minimum_stock, default_location_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		RETURNING id, created_at, updated_at`,
		p.CategoryID, p.VariantOf, p.Name, p.Description, p.IPN, p.Package, p.Keywords,
		p.Barcode, p.ImagePath, p.IsTemplate, p.IsComponent, p.IsAssembly, p.IsPurchaseable,
		p.IsTrackable, p.MinimumStock, p.DefaultLocationID,
	).Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
}

func (r *PartRepo) Update(ctx context.Context, p *models.Part) error {
	ct, err := r.pool.Exec(ctx, `
		UPDATE parts SET category_id=$2, variant_of=$3, name=$4, description=$5, ipn=$6, package=$7,
			keywords=$8, barcode=$9, image_path=$10, is_template=$11, is_component=$12,
			is_assembly=$13, is_purchaseable=$14, is_trackable=$15, minimum_stock=$16,
			default_location_id=$17
		WHERE id=$1`,
		p.ID, p.CategoryID, p.VariantOf, p.Name, p.Description, p.IPN, p.Package,
		p.Keywords, p.Barcode, p.ImagePath, p.IsTemplate, p.IsComponent,
		p.IsAssembly, p.IsPurchaseable, p.IsTrackable, p.MinimumStock,
		p.DefaultLocationID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *PartRepo) Delete(ctx context.Context, id uuid.UUID) error {
	ct, err := r.pool.Exec(ctx, `DELETE FROM parts WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ── Parameters ───────────────────────────────────────────────────────────────

func (r *PartRepo) GetParameters(ctx context.Context, partID uuid.UUID) ([]models.PartParameter, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT pp.id, pp.template_id, pt.name, pt.units, pp.value
		FROM part_parameters pp
		JOIN parameter_templates pt ON pt.id = pp.template_id
		WHERE pp.part_id = $1
		ORDER BY pt.name`, partID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.PartParameter{}
	for rows.Next() {
		var p models.PartParameter
		if err := rows.Scan(&p.ID, &p.TemplateID, &p.TemplateName, &p.Units, &p.Value); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListParameterTemplates returns every known parameter name (+ default units),
// alphabetical. Feeds the web client's parameter-name typeahead.
func (r *PartRepo) ListParameterTemplates(ctx context.Context) ([]models.ParameterTemplate, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, name, units FROM parameter_templates ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.ParameterTemplate{}
	for rows.Next() {
		var t models.ParameterTemplate
		if err := rows.Scan(&t.ID, &t.Name, &t.Units); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SetParameter upserts a parameter value on a part, creating the parameter
// template (by name) on first use.
func (r *PartRepo) SetParameter(ctx context.Context, partID uuid.UUID, name string, units *string, value string) error {
	var templateID uuid.UUID
	err := r.pool.QueryRow(ctx, `
		INSERT INTO parameter_templates (name, units) VALUES ($1, $2)
		ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
		RETURNING id`, name, units).Scan(&templateID)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO part_parameters (part_id, template_id, value) VALUES ($1, $2, $3)
		ON CONFLICT (part_id, template_id) DO UPDATE SET value = EXCLUDED.value`,
		partID, templateID, value)
	return err
}

// itoa converts a small positive int to its decimal string for placeholder
// numbering ($1, $2, …). Avoids importing strconv for a one-liner.
func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
