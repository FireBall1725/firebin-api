// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package models

// EnrichedPart is a normalized parts-data lookup result from a provider
// (Nexar/Octopart, Digi-Key, …). Used to prefill the scan create-flow.
type EnrichedPart struct {
	MPN          string              `json:"mpn"`
	Name         string              `json:"name"` // suggested part name
	Description  string              `json:"description"`
	Manufacturer string              `json:"manufacturer"`
	Category     string              `json:"category"`
	Package      string              `json:"package"`
	DatasheetURL string              `json:"datasheet_url"`
	ImageURL     string              `json:"image_url"`
	Parameters   []EnrichedParameter `json:"parameters"`
	Suppliers    []EnrichedSupplier  `json:"suppliers"`
	Alternatives []EnrichedAlt       `json:"alternatives"`
	Source       string              `json:"source"` // provider name
}

// EnrichedAlt is a similar/alternate part suggested by the provider.
type EnrichedAlt struct {
	MPN          string `json:"mpn"`
	Manufacturer string `json:"manufacturer"`
	Description  string `json:"description"`
}

type EnrichedParameter struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Units string `json:"units,omitempty"`
}

type EnrichedSupplier struct {
	Name      string `json:"name"`
	SKU       string `json:"sku"`
	URL       string `json:"url,omitempty"`       // the vendor's product page
	Packaging string `json:"packaging,omitempty"` // e.g. "Cut Tape (CT)", "Tape & Reel (TR)"
	// MOQ is the vendor's minimum order quantity. A pointer because "not
	// reported" and "no minimum" are different answers, and only some providers
	// tell us.
	MOQ    *float64     `json:"moq,omitempty"`
	Prices []PriceBreak `json:"prices"`
}
