// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// LabelMedia is the physical geometry of a label sheet (an Avery product or a
// user-defined custom size). All lengths are in PDF points (1pt = 1/72 inch).
type LabelMedia struct {
	ID           uuid.UUID `json:"id"`
	Brand        string    `json:"brand"`
	Code         string    `json:"code"`
	Name         string    `json:"name"`
	PageW        float64   `json:"page_w"`
	PageH        float64   `json:"page_h"`
	LabelW       float64   `json:"label_w"`
	LabelH       float64   `json:"label_h"`
	CornerRadius float64   `json:"corner_radius"`
	Cols         int       `json:"cols"`
	Rows         int       `json:"rows"`
	X0           float64   `json:"x0"`
	Y0           float64   `json:"y0"`
	PitchX       float64   `json:"pitch_x"`
	PitchY       float64   `json:"pitch_y"`
	CutGuides    bool      `json:"cut_guides"` // draw cut outlines (generic full-page stock)
	Kind         string    `json:"kind"`       // "sheet" (roll/label-printer media later)
	Builtin      bool      `json:"builtin"`
}

// PerSheet is the number of labels on one sheet of this media.
func (m LabelMedia) PerSheet() int { return m.Cols * m.Rows }

// LabelTemplate is a user-designed layout: a named list of positioned, field-bound
// elements for a given label size. Elements is the raw JSON array of
// labels.Element ([{type,field,x,y,w,h,value,font,bold,align}, …]); the label
// renderer decodes it and resolves each field against the part at print time.
type LabelTemplate struct {
	ID        uuid.UUID       `json:"id"`
	Name      string          `json:"name"`
	MediaID   *uuid.UUID      `json:"label_media_id,omitempty"`
	Elements  json.RawMessage `json:"elements"`
	CreatedBy *uuid.UUID      `json:"created_by,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}
