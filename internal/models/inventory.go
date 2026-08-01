// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package models

import (
	"encoding/json"
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

// PartMatch is a part returned by a parametric search, carrying the parameters
// that satisfied the query. Matched is a subset of Parameters, so a caller can
// show why a part came back without a second request per candidate.
type PartMatch struct {
	Part
	Matched []PartParameter `json:"matched_parameters"`
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
	ID             uuid.UUID  `json:"id"`
	CategoryID     *uuid.UUID `json:"category_id,omitempty"`
	VariantOf      *uuid.UUID `json:"variant_of,omitempty"`
	Name           string     `json:"name"`
	Description    *string    `json:"description,omitempty"`
	IPN            *string    `json:"ipn,omitempty"` // FireBin internal part number
	Package        *string    `json:"package,omitempty"`
	KicadSymbol    *string    `json:"kicad_symbol,omitempty"`    // KiCad LIB_ID, e.g. "Device:R"
	KicadFootprint *string    `json:"kicad_footprint,omitempty"` // e.g. "Resistor_SMD:R_0603_1608Metric"
	Keywords       *string    `json:"keywords,omitempty"`
	Barcode        *string    `json:"barcode,omitempty"`
	ImagePath      *string    `json:"image_path,omitempty"`
	IsTemplate     bool       `json:"is_template"`
	IsComponent    bool       `json:"is_component"`
	IsAssembly     bool       `json:"is_assembly"`
	IsPurchaseable bool       `json:"is_purchaseable"`
	IsTrackable    bool       `json:"is_trackable"`
	// ReferenceOnly marks a part recorded but not owned: researched for a future
	// design, remembered as an alternative, or waiting to be ordered. It is about
	// intent, not the current count, which is why it cannot be derived from
	// total_stock — a part drained to zero looks identical to one that never
	// arrived.
	ReferenceOnly     bool       `json:"reference_only"`
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
	// Where this library came from and when. A full KiCad install is 438
	// libraries, so the folder you added yesterday is invisible in an
	// alphabetical list; the import it arrived in is what tells it apart.
	// Null when the import predates imports being recorded. Reads sort those
	// first rather than burying them under 438 stock libraries.
	ImportedAt *time.Time `json:"imported_at"`
	Source     string     `json:"source"`
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

// KicadUsage is a part that references a library item.
type KicadUsage struct {
	PartID   uuid.UUID `json:"part_id"`
	PartName string    `json:"part_name"`
	Category string    `json:"category,omitempty"`
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

	// NotStockedCount is parts recorded but never owned. Shown beside the
	// low-stock count rather than folded into it: they are both things to buy,
	// and they are not the same kind of thing.
	NotStockedCount int `json:"not_stocked_count"`
	// UnmatchedBOMLines is lines that resolve to no inventory part. The pick
	// list skips them without saying so, so a board can read as buildable while
	// these were never checked. That silence is why this is on the dashboard.
	UnmatchedBOMLines int `json:"unmatched_bom_lines"`
	// PartsWithoutSymbol is catalogue parts with no KiCad symbol mapped.
	PartsWithoutSymbol int `json:"parts_without_symbol"`
	// Moves30d counts stock movements in the last 30 days. A flat week usually
	// means a delivery is sitting unlogged rather than that nothing happened.
	Moves30d int         `json:"moves_30d"`
	Movement []DayCount  `json:"movement"`
	Boards   []BoardFill `json:"boards"`
}

// DayCount is one day of the movement sparkline. Days with no movement are
// present with a zero rather than absent, so the series is evenly spaced and
// the gap is visible.
type DayCount struct {
	Day   string `json:"day"`
	Count int    `json:"count"`
}

// BoardFill is how close one board is to buildable, for one of each.
//
// Quantity is deliberately fixed at one. "Buildable once" and "buildable five
// times" are different answers and a dashboard tile has to pick one; the board
// page is where a run size gets asked for.
type BoardFill struct {
	BoardID   uuid.UUID `json:"board_id"`
	ProjectID uuid.UUID `json:"project_id"`
	Name      string    `json:"name"`
	Lines     int       `json:"lines"`
	// Short is distinct matched parts with less stock than the board needs.
	Short int `json:"short"`
	// Unmatched is lines with no part at all, which Short cannot see.
	Unmatched int `json:"unmatched"`
}

// ─── Assistant conversations ────────────────────────────────────────────────

// Conversation is one thread of questions and answers, belonging to a user.
type Conversation struct {
	ID           uuid.UUID             `json:"id"`
	Title        string                `json:"title"`
	SubjectKind  *string               `json:"subject_kind,omitempty"`
	SubjectID    *uuid.UUID            `json:"subject_id,omitempty"`
	MessageCount int                   `json:"message_count"`
	Messages     []ConversationMessage `json:"messages,omitempty"`
	CreatedAt    time.Time             `json:"created_at"`
	UpdatedAt    time.Time             `json:"updated_at"`
}

// ConversationMessage is one turn. Tool calls and results are kept alongside
// the prose because a provider needs them replayed to continue the thread, and
// because they are the record of where an answer's numbers came from.
type ConversationMessage struct {
	ID          uuid.UUID       `json:"id"`
	Seq         int             `json:"seq"`
	Role        string          `json:"role"`
	Content     string          `json:"content"`
	ToolCalls   json.RawMessage `json:"tool_calls,omitempty"`
	ToolResults json.RawMessage `json:"tool_results,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

// AssistantRun is what one turn cost, whether or not it produced an answer.
type AssistantRun struct {
	ConversationID uuid.UUID `json:"conversation_id"`
	UserID         uuid.UUID `json:"user_id"`
	Provider       string    `json:"provider"`
	Model          string    `json:"model"`
	Rounds         int       `json:"rounds"`
	InputTokens    int       `json:"input_tokens"`
	OutputTokens   int       `json:"output_tokens"`
	CostUSD        float64   `json:"cost_usd"`
	// CostKnown separates a local model's real zero from a model missing from
	// the pricing table. Stored as NULL when false.
	CostKnown bool   `json:"cost_known"`
	Error     string `json:"error,omitempty"`
}

// AssistantUsage totals a user's spend.
type AssistantUsage struct {
	Turns         int     `json:"turns"`
	FailedTurns   int     `json:"failed_turns"`
	InputTokens   int     `json:"input_tokens"`
	OutputTokens  int     `json:"output_tokens"`
	CostUSD       float64 `json:"cost_usd"`
	UnpricedTurns int     `json:"unpriced_turns"`
}
