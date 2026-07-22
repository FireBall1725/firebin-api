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
}

type PartParameter struct {
	ID           uuid.UUID `json:"id"`
	TemplateID   uuid.UUID `json:"template_id"`
	TemplateName string    `json:"template_name"`
	Units        *string   `json:"units,omitempty"`
	Value        string    `json:"value"`
}

type Part struct {
	ID                uuid.UUID  `json:"id"`
	CategoryID        *uuid.UUID `json:"category_id,omitempty"`
	VariantOf         *uuid.UUID `json:"variant_of,omitempty"`
	Name              string     `json:"name"`
	Description       *string    `json:"description,omitempty"`
	Package           *string    `json:"package,omitempty"`
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
}

// PartAlternative is a similar part suggested by enrichment, linked to an
// inventory part when we already stock it.
type PartAlternative struct {
	MPN          string     `json:"mpn"`
	Manufacturer string     `json:"manufacturer,omitempty"`
	Description  string     `json:"description,omitempty"`
	PartID       *uuid.UUID `json:"part_id,omitempty"`   // set if we stock it
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
	LocationID     *uuid.UUID `json:"location_id,omitempty"`
	LocationName   *string    `json:"location_name,omitempty"`
	SupplierPartID *uuid.UUID `json:"supplier_part_id,omitempty"`
	Quantity       float64    `json:"quantity"`
	Batch          *string    `json:"batch,omitempty"`
	Serial         *string    `json:"serial,omitempty"`
	PurchasePrice  *float64   `json:"purchase_price,omitempty"`
	Status         string     `json:"status"`
	Note           *string    `json:"note,omitempty"`
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
	Note              *string    `json:"note,omitempty"`
	UserID            *uuid.UUID `json:"user_id,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
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
