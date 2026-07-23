// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package models

import (
	"time"

	"github.com/google/uuid"
)

// Project is a design made of one or more boards.
type Project struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Boards      []Board   `json:"boards,omitempty"`
	BoardCount  int       `json:"board_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Board is one PCB within a project, its BOM parsed from an uploaded source.
type Board struct {
	ID             uuid.UUID `json:"id"`
	ProjectID      uuid.UUID `json:"project_id"`
	Name           string    `json:"name"`
	Description    string    `json:"description,omitempty"`
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
	MPN          string     `json:"mpn,omitempty"`
	Manufacturer string     `json:"manufacturer,omitempty"`
	Description  string     `json:"description,omitempty"`
	PartID       *uuid.UUID `json:"part_id,omitempty"`
	PartName     string     `json:"part_name,omitempty"`
	MatchKind    string     `json:"match_kind"`
	Position     int        `json:"position"`
}
