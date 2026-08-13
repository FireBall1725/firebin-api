// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import "strings"

// partSearchClause builds the free-text WHERE fragment shared by the two part
// searches, appends its bind parameter to args, and returns both.
//
// It exists because there are two search endpoints and they have to agree.
// GET /parts (PartRepo.List) and GET /parts/search (PartRepo.SearchParametric)
// carried byte-identical copies of this predicate, which is the arrangement
// where a new searchable field gets added to one and forgotten in the other, and
// the same query then finds a part from the parts page but not from the spec
// filter. One caller cannot drift from the other if there is only one copy.
//
// The empty string yields an empty fragment and leaves args untouched, so
// callers can append unconditionally.
//
// Matching is a single unanchored ILIKE per field, as it always has been. The
// trigram GIN indexes on parts.name, parts.keywords, manufacturer_parts.mpn and
// tags.name are what keep that affordable.
func partSearchClause(search string, args []any) (string, []any) {
	s := strings.TrimSpace(search)
	if s == "" {
		return "", args
	}
	args = append(args, "%"+s+"%")
	n := itoa(len(args))
	// Match name/keywords/IPN, any linked manufacturer part number, or any tag
	// the part carries. Tags are here so the word you actually remember finds
	// the part: a JST SH header answers to "qwiic" without that word touching
	// its name or its MPN.
	return ` AND (parts.name ILIKE $` + n + ` OR parts.keywords ILIKE $` + n +
		` OR parts.ipn ILIKE $` + n +
		` OR EXISTS (SELECT 1 FROM manufacturer_parts mp WHERE mp.part_id = parts.id AND mp.mpn ILIKE $` + n + `)` +
		` OR EXISTS (SELECT 1 FROM part_tags pt JOIN tags t ON t.id = pt.tag_id
			WHERE pt.part_id = parts.id AND t.name ILIKE $` + n + `))`, args
}
