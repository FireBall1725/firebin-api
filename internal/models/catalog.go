// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package models

import (
	"time"

	"github.com/google/uuid"
)

type Manufacturer struct {
	ID      uuid.UUID `json:"id"`
	Name    string    `json:"name"`
	Website *string   `json:"website,omitempty"`
}

type Supplier struct {
	ID            uuid.UUID `json:"id"`
	Key           string    `json:"key"`
	Name          string    `json:"name"`
	Website       *string   `json:"website,omitempty"`
	IsDistributor bool      `json:"is_distributor"`
}

// PriceBreak is one quantity/price tier for a supplier part.
type PriceBreak struct {
	ID       uuid.UUID `json:"id"`
	Quantity float64   `json:"quantity"`
	Price    float64   `json:"price"`
	Currency string    `json:"currency"`
}

// SupplierPart is a vendor SKU for a manufacturer part, with its price breaks.
type SupplierPart struct {
	ID                 uuid.UUID    `json:"id"`
	ManufacturerPartID uuid.UUID    `json:"manufacturer_part_id"`
	SupplierID         uuid.UUID    `json:"supplier_id"`
	SupplierName       string       `json:"supplier_name"`
	SKU                string       `json:"sku"`
	Packaging          *string      `json:"packaging,omitempty"`
	MOQ                *float64     `json:"moq,omitempty"`
	URL                *string      `json:"url,omitempty"`
	Pricing            []PriceBreak `json:"pricing"`
}

// ManufacturerPart is a brand + MPN for a part, with its supplier SKUs.
type ManufacturerPart struct {
	ID               uuid.UUID      `json:"id"`
	PartID           uuid.UUID      `json:"part_id"`
	ManufacturerID   *uuid.UUID     `json:"manufacturer_id,omitempty"`
	ManufacturerName *string        `json:"manufacturer_name,omitempty"`
	MPN              string         `json:"mpn"`
	Description      *string        `json:"description,omitempty"`
	DatasheetURL     *string        `json:"datasheet_url,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	SupplierParts    []SupplierPart `json:"supplier_parts"`
}
