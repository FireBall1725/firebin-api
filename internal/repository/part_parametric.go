// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/firelabsca/firebin-api/internal/units"
	"github.com/google/uuid"
)

// ParametricOptions searches parts by what they are rather than what they are
// called.
//
// The existing List only substring-matches name, keywords, IPN and MPN, so a
// part named "100 kΩ Resistor" with package "0603 (1608 Metric)" cannot be found
// by asking for an 0603 220 Ω: the package lives in its own column that no
// search reads, and the value lives in part_parameters, which no listing joins.
type ParametricOptions struct {
	CategoryID *uuid.UUID
	Search     string // free text over name/keywords/IPN/MPN, as in List
	Package    string // substring, so "0603" finds "0603 (1608 Metric)"
	Parameter  string // restrict Value to a named parameter, e.g. "Resistance"
	Value      string // "220", "220 ohm", "4.7uF", or plain text like "X7R"
	Limit      int    // 0 means the default cap
}

const parametricDefaultLimit = 200

// SearchParametric returns parts matching the options, each carrying its full
// parameter list and the subset that satisfied the value filter.
//
// Category, package and free text are filtered in SQL. The value filter is not,
// because units are stored as text next to the magnitude: 100 kΩ is value "100"
// with units "kΩ" and sits in the same column as 33 Ω. Comparing those in SQL
// means comparing "100" against "33", which is why this reads the candidates and
// compares them through the units package instead.
func (r *PartRepo) SearchParametric(ctx context.Context, opts ParametricOptions) ([]models.PartMatch, error) {
	q := strings.Builder{}
	q.WriteString(`SELECT ` + partCols + `,
		COALESCE((SELECT SUM(quantity) FROM stock_items s
			WHERE s.part_id = parts.id
			   OR s.part_id IN (SELECT id FROM parts v WHERE v.variant_of = parts.id)), 0)::float8 AS total_stock,
		(SELECT COUNT(*) FROM parts v WHERE v.variant_of = parts.id)::int AS variant_count,
		COALESCE((
			SELECT json_agg(json_build_object(
				'id', pp.id, 'template_id', pp.template_id,
				'template_name', pt.name, 'units', pp.units, 'value', pp.value)
				ORDER BY pt.name)
			FROM part_parameters pp
			JOIN parameter_templates pt ON pt.id = pp.template_id
			WHERE pp.part_id = parts.id
		), '[]')::text AS params,
		EXISTS (SELECT 1 FROM datasheet_parts dp WHERE dp.part_id = parts.id) AS has_datasheet
		FROM parts
		WHERE 1=1`)

	args := []any{}
	if opts.CategoryID != nil {
		args = append(args, *opts.CategoryID)
		q.WriteString(` AND parts.category_id = $` + itoa(len(args)))
	}
	if p := strings.TrimSpace(opts.Package); p != "" {
		args = append(args, "%"+p+"%")
		q.WriteString(` AND parts.package ILIKE $` + itoa(len(args)))
	}
	if s := strings.TrimSpace(opts.Search); s != "" {
		args = append(args, "%"+s+"%")
		n := itoa(len(args))
		q.WriteString(` AND (parts.name ILIKE $` + n + ` OR parts.keywords ILIKE $` + n +
			` OR parts.ipn ILIKE $` + n +
			` OR EXISTS (SELECT 1 FROM manufacturer_parts mp WHERE mp.part_id = parts.id AND mp.mpn ILIKE $` + n + `))`)
	}
	// A named parameter is a SQL filter on its own: a part without that
	// parameter can never match, so there is no reason to read it.
	if name := strings.TrimSpace(opts.Parameter); name != "" {
		args = append(args, "%"+name+"%")
		q.WriteString(` AND EXISTS (SELECT 1 FROM part_parameters pp
			JOIN parameter_templates pt ON pt.id = pp.template_id
			WHERE pp.part_id = parts.id AND pt.name ILIKE $` + itoa(len(args)) + `)`)
	}
	q.WriteString(` ORDER BY parts.name`)

	rows, err := r.pool.Query(ctx, q.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	limit := opts.Limit
	if limit <= 0 {
		limit = parametricDefaultLimit
	}
	wantValue := strings.TrimSpace(opts.Value)
	wantParam := strings.TrimSpace(opts.Parameter)

	out := []models.PartMatch{}
	for rows.Next() {
		var m models.PartMatch
		var params string
		if err := rows.Scan(
			&m.ID, &m.CategoryID, &m.VariantOf, &m.Name, &m.Description, &m.IPN, &m.Package,
			&m.KicadSymbol, &m.KicadFootprint, &m.Keywords,
			&m.Barcode, &m.ImagePath, &m.IsTemplate, &m.IsComponent, &m.IsAssembly, &m.IsPurchaseable,
			&m.IsTrackable, &m.ReferenceOnly, &m.MinimumStock, &m.DefaultLocationID, &m.CreatedAt, &m.UpdatedAt,
			&m.TotalStock, &m.VariantCount, &params, &m.HasDatasheet,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(params), &m.Parameters); err != nil {
			return nil, err
		}
		if m.Parameters == nil {
			m.Parameters = []models.PartParameter{}
		}

		m.Matched = matchParameters(m.Parameters, wantParam, wantValue)
		// With no value asked for, every part that got this far qualifies and
		// nothing is highlighted.
		if wantValue != "" && len(m.Matched) == 0 {
			continue
		}
		out = append(out, m)
		if len(out) >= limit {
			break
		}
	}
	return out, rows.Err()
}

// matchParameters returns the parameters satisfying a value query, restricted to
// a named parameter when one is given. An empty value query matches nothing,
// which the caller reads as "no value filter" rather than "no results".
func matchParameters(params []models.PartParameter, wantParam, wantValue string) []models.PartParameter {
	if wantValue == "" {
		return nil
	}
	query, numeric := units.ParseQuery(wantValue)
	matched := []models.PartParameter{}
	for _, p := range params {
		if wantParam != "" && !strings.Contains(strings.ToLower(p.TemplateName), strings.ToLower(wantParam)) {
			continue
		}
		if numeric {
			u := ""
			if p.Units != nil {
				u = *p.Units
			}
			if stored, ok := units.Parse(p.Value, u); ok && units.Matches(stored, query) {
				matched = append(matched, p)
				continue
			}
		}
		// Not every parameter is a quantity. "X7R", "SMD" and "-55°C ~ 125°C"
		// only ever compare as text, and a numeric query still falls back here
		// so that asking for "10" finds a part whose tolerance reads "10%".
		if strings.Contains(strings.ToLower(p.Value), strings.ToLower(wantValue)) {
			matched = append(matched, p)
		}
	}
	return matched
}
