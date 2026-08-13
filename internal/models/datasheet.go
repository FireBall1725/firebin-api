// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package models

import (
	"time"

	"github.com/google/uuid"
)

// Datasheet text extraction states. The zero value on a fresh row is
// TextPending; NoTextLayer is a normal outcome for a scanned document (a
// mechanical drawing is an image), not a failure.
const (
	TextPending     = "pending"
	TextOK          = "ok"
	TextNoTextLayer = "no_text_layer"
	TextFailed      = "failed"
)

// Datasheet origins.
const (
	OriginUpload = "upload"
	OriginMirror = "mirror"
)

// DatasheetPartLink is a part a datasheet describes. One document covers a whole
// family, so this is a list rather than a single part.
type DatasheetPartLink struct {
	PartID             uuid.UUID  `json:"part_id"`
	PartName           string     `json:"part_name"`
	ManufacturerPartID *uuid.UUID `json:"manufacturer_part_id,omitempty"`
	MPN                *string    `json:"mpn,omitempty"`
	CategoryID         *uuid.UUID `json:"category_id,omitempty"`
	CategoryName       *string    `json:"category_name,omitempty"`
}

// Datasheet is one stored PDF. The bytes live on disk keyed by SHA256; this is
// only the metadata. Parts is empty for a document not linked to anything, which
// is a supported state, not an error.
type Datasheet struct {
	ID          uuid.UUID  `json:"id"`
	SHA256      string     `json:"sha256"`
	Filename    string     `json:"filename"`
	Title       *string    `json:"title,omitempty"`
	Mime        string     `json:"mime"`
	SizeBytes   int64      `json:"size_bytes"`
	PageCount   *int       `json:"page_count,omitempty"`
	SourceURL   *string    `json:"source_url,omitempty"`
	Origin      string     `json:"origin"`
	Language    *string    `json:"language,omitempty"`
	TextStatus  string     `json:"text_status"`
	ExtractedAt *time.Time `json:"extracted_at,omitempty"`
	// CategoryID is the document's own category, set by hand. Null is the normal
	// state for a mirrored datasheet, which is sorted by the parts it is linked
	// to; this exists so a loose upload can be sorted without inventing a part
	// for it. The name is not carried: the web already holds the category list.
	CategoryID *uuid.UUID `json:"category_id,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`

	Parts []DatasheetPartLink `json:"parts"`
}
