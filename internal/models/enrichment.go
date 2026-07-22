// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package models

// EnrichedPart is a normalized parts-data lookup result from a provider
// (Nexar/Octopart, Digi-Key, …). Used to prefill the scan create-flow.
type EnrichedPart struct {
	MPN          string              `json:"mpn"`
	Name         string              `json:"name"`         // suggested part name
	Description  string              `json:"description"`
	Manufacturer string              `json:"manufacturer"`
	Category     string              `json:"category"`
	Package      string              `json:"package"`
	DatasheetURL string              `json:"datasheet_url"`
	ImageURL     string              `json:"image_url"`
	Parameters   []EnrichedParameter `json:"parameters"`
	Suppliers    []EnrichedSupplier  `json:"suppliers"`
	Source       string              `json:"source"` // provider name
}

type EnrichedParameter struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Units string `json:"units,omitempty"`
}

type EnrichedSupplier struct {
	Name   string       `json:"name"`
	SKU    string       `json:"sku"`
	Prices []PriceBreak `json:"prices"`
}
