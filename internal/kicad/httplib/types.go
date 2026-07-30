// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package kicadlib serves a FireBin inventory as a KiCad HTTP library
// (.kicad_httplib), and holds the mapping from FireBin's data model onto the
// shapes KiCad expects.
//
// The contract is fixed by KiCad and is not negotiable: api_version must be
// exactly "v1", auth is `Authorization: Token <token>`, and every value in
// every response must be a JSON string. Numbers and JSON booleans are rejected
// by KiCad's parser; the only array is footprint_filters. That is why every
// field below is typed string even where the underlying FireBin value is
// numeric.
package httplib

// Category is one node in the Symbol Chooser tree. KiCad renders each as a
// sub-library under the library nickname, and a "/" in Name creates nesting.
type Category struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Field is one symbol field. Visible is a KiCad boolean-as-string; omitting it
// means visible, so anything that should not clutter the schematic has to say
// "False" explicitly.
type Field struct {
	Value   string `json:"value"`
	Visible string `json:"visible,omitempty"`
}

// Part is a KiCad part detail object.
//
// SymbolIDStr is a KiCad LIB_ID ("Library:Symbol") that must resolve against a
// symbol library already installed where KiCad is running. When it is empty or
// unresolvable, KiCad builds an empty placeholder carrying this metadata and
// flags the error in the chooser, which is how unmapped parts surface.
type Part struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	SymbolIDStr string `json:"symbolIdStr,omitempty"`

	// Description and Keywords are emitted at the top level rather than inside
	// Fields: KiCad applies the top-level values *after* the fields loop, so
	// these win on conflict. Keywords is also the only searchable free-text we
	// control (see mapping.go).
	Description string `json:"description,omitempty"`
	Keywords    string `json:"keywords,omitempty"`

	ExcludeFromBOM   string `json:"exclude_from_bom,omitempty"`
	ExcludeFromBoard string `json:"exclude_from_board,omitempty"`

	FootprintFilters []string `json:"footprint_filters,omitempty"`

	Fields map[string]Field `json:"fields,omitempty"`
}

// validation is the response to `GET /v1/`. KiCad checks only that both keys
// are present; the values are ignored.
type validation struct {
	Categories string `json:"categories"`
	Parts      string `json:"parts"`
}
