// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package models

import (
	"time"

	"github.com/google/uuid"
)

type Category struct {
	ID          uuid.UUID  `json:"id"`
	ParentID    *uuid.UUID `json:"parent_id,omitempty"`
	Name        string     `json:"name"`
	Description *string    `json:"description,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	PartCount   int        `json:"part_count"` // parts directly in this category (List only)
}

type PartParameter struct {
	ID           uuid.UUID `json:"id"`
	TemplateID   uuid.UUID `json:"template_id"`
	TemplateName string    `json:"template_name"`
	Units        *string   `json:"units,omitempty"`
	Value        string    `json:"value"`
}

// ParameterTemplate is a reusable parameter name (+ default units). The web
// client lists these to power name-typeahead so users reuse "Voltage Rating"
// instead of coining "Voltage rating", "Volt Rating", etc.
type ParameterTemplate struct {
	ID    uuid.UUID `json:"id"`
	Name  string    `json:"name"`
	Units *string   `json:"units,omitempty"`
}

type Part struct {
	ID                uuid.UUID  `json:"id"`
	CategoryID        *uuid.UUID `json:"category_id,omitempty"`
	VariantOf         *uuid.UUID `json:"variant_of,omitempty"`
	Name              string     `json:"name"`
	Description       *string    `json:"description,omitempty"`
	IPN               *string    `json:"ipn,omitempty"` // FireBin internal part number
	Package           *string    `json:"package,omitempty"`
	KicadSymbol       *string    `json:"kicad_symbol,omitempty"`    // KiCad LIB_ID, e.g. "Device:R"
	KicadFootprint    *string    `json:"kicad_footprint,omitempty"` // e.g. "Resistor_SMD:R_0603_1608Metric"
	Keywords          *string    `json:"keywords,omitempty"`
	Barcode           *string    `json:"barcode,omitempty"`
	ImagePath         *string    `json:"image_path,omitempty"`
	IsTemplate        bool       `json:"is_template"`
	IsComponent       bool       `json:"is_component"`
	IsAssembly        bool       `json:"is_assembly"`
	IsPurchaseable    bool       `json:"is_purchaseable"`
	IsTrackable       bool       `json:"is_trackable"`
	MinimumStock      float64    `json:"minimum_stock"`
	DefaultLocationID *uuid.UUID `json:"default_location_id,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`

	// Joined / computed, populated on some reads.
	TotalStock        float64            `json:"total_stock"`
	Parameters        []PartParameter    `json:"parameters,omitempty"`
	Variants          []Part             `json:"variants,omitempty"`
	VariantCount      int                `json:"variant_count,omitempty"`
	ManufacturerParts []ManufacturerPart `json:"manufacturer_parts,omitempty"`
	Alternatives      []PartAlternative  `json:"alternatives,omitempty"`

	// Primary MPN/manufacturer and bin, for the parts list (from List's laterals).
	PrimaryMPN          string     `json:"primary_mpn,omitempty"`
	PrimaryManufacturer string     `json:"primary_manufacturer,omitempty"`
	PrimaryLocation     *string    `json:"primary_location,omitempty"`
	PrimaryLocationID   *uuid.UUID `json:"primary_location_id,omitempty"`
}

// PartAlternative is a similar part suggested by enrichment, linked to an
// inventory part when we already stock it.
type PartAlternative struct {
	MPN          string     `json:"mpn"`
	Manufacturer string     `json:"manufacturer,omitempty"`
	Description  string     `json:"description,omitempty"`
	PartID       *uuid.UUID `json:"part_id,omitempty"` // set if we stock it
	PartName     *string    `json:"part_name,omitempty"`
}

type StorageLocation struct {
	ID          uuid.UUID  `json:"id"`
	ParentID    *uuid.UUID `json:"parent_id,omitempty"`
	Name        string     `json:"name"`
	Description *string    `json:"description,omitempty"`
	Barcode     *string    `json:"barcode,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type StockItem struct {
	ID             uuid.UUID  `json:"id"`
	PartID         uuid.UUID  `json:"part_id"`
	PartName       string     `json:"part_name,omitempty"`
	CategoryName   *string    `json:"category_name,omitempty"`
	ImagePath      *string    `json:"image_path,omitempty"`
	LocationID     *uuid.UUID `json:"location_id,omitempty"`
	LocationName   *string    `json:"location_name,omitempty"`
	SupplierPartID *uuid.UUID `json:"supplier_part_id,omitempty"`
	Quantity       float64    `json:"quantity"`
	Batch          *string    `json:"batch,omitempty"`
	Serial         *string    `json:"serial,omitempty"`
	PurchasePrice  *float64   `json:"purchase_price,omitempty"`
	Status         string     `json:"status"`
	Note           *string    `json:"note,omitempty"`
	Barcode        *string    `json:"barcode,omitempty"`    // scannable lot identity (a mini spool)
	Name           *string    `json:"name,omitempty"`       // human label for the lot ("Mini spool #1")
	SplitFrom      *uuid.UUID `json:"split_from,omitempty"` // the lot this was cut from
	AddedAt        time.Time  `json:"added_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type StockTransaction struct {
	ID                uuid.UUID  `json:"id"`
	StockItemID       uuid.UUID  `json:"stock_item_id"`
	PartID            *uuid.UUID `json:"part_id,omitempty"`
	PartName          *string    `json:"part_name,omitempty"`
	Kind              string     `json:"kind"`
	Delta             float64    `json:"delta"`
	ResultingQuantity float64    `json:"resulting_quantity"`
	FromLocationID    *uuid.UUID `json:"from_location_id,omitempty"`
	ToLocationID      *uuid.UUID `json:"to_location_id,omitempty"`
	FromLocationName  *string    `json:"from_location_name,omitempty"`
	ToLocationName    *string    `json:"to_location_name,omitempty"`
	Note              *string    `json:"note,omitempty"`
	UserID            *uuid.UUID `json:"user_id,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
}

// KicadLibraryItem is one entry in the uploaded copy of the user's KiCad
// libraries. Lib and Name combine into the "Lib:Name" identifier KiCad calls a
// LIB_ID, e.g. lib "Device" + name "R" for "Device:R".
type KicadLibraryItem struct {
	Kind string `json:"kind"` // symbol | footprint
	Lib  string `json:"lib"`
	Name string `json:"name"`
	// HasSource distinguishes an item we can draw from one we only know the
	// name of, which is what an index-only scan leaves behind.
	HasSource bool `json:"has_source"`
}

// LibID renders the "Lib:Name" identifier stored on a part.
func (k KicadLibraryItem) LibID() string { return k.Lib + ":" + k.Name }

// KicadLibraryUpload is one item as the indexer sends it. Source is the raw
// S-expression; the server compresses it on the way in.
type KicadLibraryUpload struct {
	Kind   string `json:"kind"`
	Lib    string `json:"lib"`
	Name   string `json:"name"`
	Source string `json:"source,omitempty"`
}

// KicadLibrarySummary is one library's row in the viewer.
type KicadLibrarySummary struct {
	Kind       string `json:"kind"`
	Lib        string `json:"lib"`
	Count      int    `json:"count"`
	WithSource int    `json:"with_source"`
}

// KicadIndexMeta records which machine produced the current index, so a missing
// library can be told apart from a scan run on the wrong laptop.
type KicadIndexMeta struct {
	Source         string    `json:"source"`
	KicadVersion   string    `json:"kicad_version,omitempty"`
	ScannedAt      time.Time `json:"scanned_at"`
	SymbolCount    int       `json:"symbol_count"`
	FootprintCount int       `json:"footprint_count"`
	BytesStored    int64     `json:"bytes_stored"`
}

// KicadSuggestion is one proposed mapping with the evidence behind it. Source
// is "bom", "mpn", "category" or "package"; Confidence is 0-100 and exists to
// order the list, not to be shown as a number.
type KicadSuggestion struct {
	LibID      string `json:"lib_id"`
	Source     string `json:"source"`
	Detail     string `json:"detail,omitempty"`
	Confidence int    `json:"confidence"`
}

// KicadSuggestions groups proposals for one part. Always non-nil slices: the UI
// checks .length and a JSON null would crash it.
type KicadSuggestions struct {
	Symbols    []KicadSuggestion `json:"symbols"`
	Footprints []KicadSuggestion `json:"footprints"`
	// Notes explains anything deliberately withheld. A suggestion that vanishes
	// without explanation reads as "nothing found", which is a different claim.
	Notes []string `json:"notes,omitempty"`
}

// Stats is the dashboard summary.
type Stats struct {
	PartsCount     int     `json:"parts_count"`
	VariantsCount  int     `json:"variants_count"`
	LocationsCount int     `json:"locations_count"`
	LowStockCount  int     `json:"low_stock_count"`
	TotalUnits     float64 `json:"total_units"`
	InventoryValue float64 `json:"inventory_value"`
}
