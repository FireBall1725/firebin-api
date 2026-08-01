// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package models

import (
	"time"

	"github.com/google/uuid"
)

// PickList is the result of assembling N of a board: what to pull from which
// bin, plus shortfalls and lines with no inventory match.
type PickList struct {
	BoardID    uuid.UUID       `json:"board_id"`
	BoardName  string          `json:"board_name"`
	Quantity   int             `json:"quantity"` // boards to build
	Copies     int             `json:"copies"`   // panel N-up (per board)
	TotalUnits float64         `json:"total_units"`
	Entries    []PickEntry     `json:"entries"`    // sorted by location, then part
	Shortfalls []PickShortfall `json:"shortfalls"` // parts short of stock
	Unmatched  []PickUnmatched `json:"unmatched"`  // BOM lines with no matched part
}

// PickEntry is one line of the pick list: pull Quantity of a part from a bin.
type PickEntry struct {
	StockItemID  uuid.UUID  `json:"stock_item_id"`
	PartID       uuid.UUID  `json:"part_id"`
	PartName     string     `json:"part_name"`
	LocationID   *uuid.UUID `json:"location_id,omitempty"`
	LocationName string     `json:"location_name"` // "" when the lot has no bin
	Quantity     float64    `json:"quantity"`
}

// PickShortfall flags a matched part with too little stock for the build.
type PickShortfall struct {
	PartID    uuid.UUID `json:"part_id"`
	PartName  string    `json:"part_name"`
	Required  float64   `json:"required"`
	Available float64   `json:"available"`
	Short     float64   `json:"short"`
}

// PickUnmatched is a BOM line that can't be picked because it isn't matched to
// an inventory part.
type PickUnmatched struct {
	Refs     string `json:"refs"`
	Value    string `json:"value"`
	Quantity int    `json:"quantity"` // total needed across the whole build
	// What the BOM says about the part, carried through rather than dropped.
	// An unmatched line is the one a caller most needs detail on: it is the
	// thing to go and buy, and the MPN is usually sitting right there on it.
	Footprint    string `json:"footprint,omitempty"`
	MPN          string `json:"mpn,omitempty"`
	Manufacturer string `json:"manufacturer,omitempty"`
}

// Project is a design made of one or more boards.
type Project struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Tags        []string  `json:"tags"`
	Boards      []Board   `json:"boards,omitempty"`
	BoardCount  int       `json:"board_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`

	// Cover thumbnail for project cards: an uploaded image, else the first
	// board's render. Computed on read.
	CoverAssetID   *uuid.UUID `json:"cover_asset_id,omitempty"`
	CoverAssetKind string     `json:"cover_asset_kind,omitempty"`
}

// Board is one PCB within a project, its BOM parsed from an uploaded source.
type Board struct {
	ID             uuid.UUID `json:"id"`
	ProjectID      uuid.UUID `json:"project_id"`
	Name           string    `json:"name"`
	Description    string    `json:"description,omitempty"`
	Revision       string    `json:"revision,omitempty"`
	SourceFilename string    `json:"source_filename,omitempty"`
	SourceFormat   string    `json:"source_format"`
	Kind           string    `json:"kind"`   // board | panel
	Copies         int       `json:"copies"` // panel = N-up, board = 1
	Position       int       `json:"position"`
	Lines          []BOMLine `json:"lines,omitempty"`
	LineCount      int       `json:"line_count"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ProjectAsset is a renderable file pulled from a project upload (iBOM HTML or
// an image render). Content is served separately, not in listings.
type ProjectAsset struct {
	ID        uuid.UUID  `json:"id"`
	ProjectID uuid.UUID  `json:"project_id"`
	BoardID   *uuid.UUID `json:"board_id,omitempty"`
	Name      string     `json:"name"`
	Kind      string     `json:"kind"`
	Mime      string     `json:"mime"`
	Size      int64      `json:"size"`
	CreatedAt time.Time  `json:"created_at"`
}

// BOMLine is one grouped BOM row: components sharing value+footprint(+MPN).
type BOMLine struct {
	ID           uuid.UUID  `json:"id"`
	BoardID      uuid.UUID  `json:"board_id"`
	Refs         string     `json:"refs"`
	Quantity     int        `json:"quantity"`
	Value        string     `json:"value"`
	Footprint    string     `json:"footprint"`
	LibID        string     `json:"lib_id,omitempty"`
	MPN          string     `json:"mpn,omitempty"`
	Manufacturer string     `json:"manufacturer,omitempty"`
	SupplierSKU  string     `json:"supplier_sku,omitempty"`
	IPN          string     `json:"ipn,omitempty"`
	Description  string     `json:"description,omitempty"`
	PartID       *uuid.UUID `json:"part_id,omitempty"`
	PartName     string     `json:"part_name,omitempty"`
	MatchKind    string     `json:"match_kind"`
	Position     int        `json:"position"`
}
