// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import (
	"context"
	"regexp"
	"strings"

	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/google/uuid"
)

// SuggestKicad proposes a symbol and footprint for a part, ranked by how much
// the evidence can be trusted.
//
// Nothing here guesses at a pinout. The sources, strongest first:
//
//  1. A board this part is already matched to. The designer chose that symbol
//     and footprint and shipped it, so it is fact rather than inference.
//  2. The part's MPN matching a symbol name in the library index. KiCad names a
//     lot of symbols after the manufacturer part number.
//  3. Package size code plus category, for passives only. This is the weakest:
//     package strings describe the part someone ordered, not the land pattern
//     they laid out, and the two diverge (a part listed TO-253-4 laid out as
//     SOT-143).
//
// Category alone never suggests a semiconductor symbol. A generic Q_NMOS has a
// fixed gate/drain/source order that may not match a given SOT-23 part, and a
// wrong pinout produces a board that looks correct and is wired wrong.
func (r *PartRepo) SuggestKicad(ctx context.Context, partID uuid.UUID) (*models.KicadSuggestions, error) {
	out := &models.KicadSuggestions{
		Symbols:    []models.KicadSuggestion{},
		Footprints: []models.KicadSuggestion{},
	}

	var name, pkg, category, mpn string
	err := r.pool.QueryRow(ctx, `
		SELECT p.name, COALESCE(p.package,''), COALESCE(c.name,''),
		       COALESCE((SELECT mp.mpn FROM manufacturer_parts mp
		                 WHERE mp.part_id = p.id ORDER BY mp.created_at LIMIT 1), '')
		FROM parts p LEFT JOIN categories c ON c.id = p.category_id
		WHERE p.id = $1`, partID).Scan(&name, &pkg, &category, &mpn)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	add := func(list *[]models.KicadSuggestion, kind, libID, source, detail string, conf int) {
		libID = strings.TrimSpace(libID)
		if libID == "" || !strings.Contains(libID, ":") || seen[kind+libID] {
			return
		}
		seen[kind+libID] = true
		*list = append(*list, models.KicadSuggestion{
			LibID: libID, Source: source, Detail: detail, Confidence: conf,
		})
	}

	// ── 1. Boards this part is already on ────────────────────────────────────
	rows, err := r.pool.Query(ctx, `
		SELECT COALESCE(l.lib_id,''), COALESCE(l.footprint,''), b.name, pr.name, l.refs
		FROM board_bom_lines l
		JOIN project_boards b ON b.id = l.board_id
		JOIN projects pr ON pr.id = b.project_id
		WHERE l.part_id = $1 AND (l.lib_id <> '' OR l.footprint <> '')
		ORDER BY b.created_at DESC`, partID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var libID, fp, board, project, refs string
		if err := rows.Scan(&libID, &fp, &board, &project, &refs); err != nil {
			return nil, err
		}
		where := project + " / " + board
		if refs != "" {
			where += " (" + refs + ")"
		}
		add(&out.Symbols, "symbol", libID, "bom", where, 100)
		add(&out.Footprints, "footprint", fp, "bom", where, 100)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// ── 2. MPN matching a symbol name ────────────────────────────────────────
	if mpn != "" {
		rows, err := r.pool.Query(ctx, `
			SELECT lib, name FROM kicad_library_items
			WHERE kind = 'symbol' AND (name ILIKE $1 OR name ILIKE $2)
			ORDER BY length(name), lib LIMIT 5`, mpn, mpn+"%")
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var lib, nm string
			if err := rows.Scan(&lib, &nm); err != nil {
				return nil, err
			}
			conf := 70
			if strings.EqualFold(nm, mpn) {
				conf = 90
			}
			add(&out.Symbols, "symbol", lib+":"+nm, "mpn", "symbol named after "+mpn, conf)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	// ── 3. Passive category + package size code ──────────────────────────────
	if prefix, sym, ok := passiveFor(category); ok {
		if sym != "" {
			add(&out.Symbols, "symbol", sym, "category", category+" are two-pin and orientation-free", 80)
		}
		if code := sizeCode(pkg); code != "" {
			rows, err := r.pool.Query(ctx, `
				SELECT lib, name FROM kicad_library_items
				WHERE kind = 'footprint' AND name ILIKE $1
				ORDER BY length(lib) + length(name) LIMIT 5`, prefix+"\\_"+code+"%")
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			for rows.Next() {
				var lib, nm string
				if err := rows.Scan(&lib, &nm); err != nil {
					return nil, err
				}
				add(&out.Footprints, "footprint", lib+":"+nm, "package",
					pkg+" + "+category, 75)
			}
			if err := rows.Err(); err != nil {
				return nil, err
			}
		}
	}

	return out, nil
}

// passiveFor maps a category to a footprint name prefix and a safe generic
// symbol. Restricted to two-pin passives on purpose: these symbols have no
// meaningful pin order to get wrong.
func passiveFor(category string) (prefix, symbol string, ok bool) {
	switch c := strings.ToLower(category); {
	case strings.Contains(c, "resistor"):
		return "R", "Device:R", true
	case strings.Contains(c, "capacitor"):
		return "C", "Device:C", true
	case strings.Contains(c, "inductor"):
		return "L", "Device:L", true
	case strings.Contains(c, "ferrite"):
		return "L", "Device:FerriteBead", true
	case strings.Contains(c, "led"):
		return "LED", "Device:LED", true
	case strings.Contains(c, "schottky"):
		// Footprints are shared with plain diodes; the symbol is the difference.
		return "D", "Device:D_Schottky", true
	case strings.Contains(c, "zener"):
		return "D", "Device:D_Zener", true
	case strings.Contains(c, "diode"):
		return "D", "Device:D", true
	case strings.Contains(c, "fuse"):
		return "Fuse", "Device:Fuse", true
	case strings.Contains(c, "crystal"):
		return "Crystal", "Device:Crystal", true
	}
	return "", "", false
}

// sizeCodeRe pulls an imperial chip size out of a package string such as
// "0603 (1608 Metric)". KiCad names these "<P>_0603_1608Metric", so the
// imperial code alone is enough to prefix-match.
var sizeCodeRe = regexp.MustCompile(`\b(0(?:1005|201|402|603|805)|1(?:206|210|218|812)|2(?:010|512))\b`)

func sizeCode(pkg string) string {
	return sizeCodeRe.FindString(pkg)
}
