// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package source is the input model for the KiCad HTTP library mapping: a
// FireBin part and category reduced to exactly the fields the mapping reads.
package source

import (
	"strings"

	"github.com/google/uuid"

	"github.com/firelabsca/firebin-api/internal/models"
)

// The types here are the mapping's *input* model, deliberately kept separate
// from models.Part, and in their own package so the output type can also be
// called Part without a collision.
//
// This package used to live in its own service and read these shapes off the
// wire. Folding it into the API removed the HTTP hop but not the reason for the
// boundary: mapping.go and its thirteen tests are the contract with KiCad, and
// rewriting every fixture onto models.Part would put a silent regression in the
// six tests that guard that contract exactly where nobody would look for it.
//
// So the mapping keeps reading a small struct with string ids, and one adapter
// below converts. The shapes do line up field-for-field with models, which is
// what makes the adapter boring.

// Part is a FireBin part as the mapping needs to see it.
type Part struct {
	ID string
	// CategoryID decides which spec parameter becomes the symbol's Value, and
	// which chooser sub-library the part lands in. Nil means uncategorized.
	CategoryID  *string
	Name        string
	Description *string
	IPN         *string
	Package     *string

	KicadSymbol    *string
	KicadFootprint *string
	Keywords       *string
	// Tags are the other names the part answers to. They fold into the chooser's
	// keyword blob, which is the only reason they are here: KiCad's Symbol
	// Chooser does not search custom fields, so a tag that is not in keywords
	// might as well not exist inside KiCad.
	Tags []string

	TotalStock float64

	Parameters        []Parameter
	ManufacturerParts []ManufacturerPart

	PrimaryMPN          string
	PrimaryManufacturer string
}

// Parameter is one spec row. TemplateName, not Name: the value comes from a
// joined parameter template, and getting this wrong silently blanks every
// resistor's value.
type Parameter struct {
	TemplateName string
	Units        *string
	Value        string
}

type ManufacturerPart struct {
	ManufacturerName *string
	MPN              string
	DatasheetURL     *string
	SupplierParts    []SupplierPart
}

type SupplierPart struct {
	SupplierName string
	SKU          string
}

// Category is a FireBin category as the mapping needs to see it.
//
// Path is retained but is currently always empty: models.Category has no such
// field and GET /categories has never returned one, so the nesting branch in
// MapCategory has never fired and the chooser tree is flat. parent_id is
// available, so building a real path is possible — but it changes the tree shape
// users see, so it belongs in its own change rather than arriving as a side
// effect of this one.
type Category struct {
	ID   string
	Name string
	Path string
}

// Deref is a nil-safe *string read. Most optional columns arrive as pointers and
// the mapping only ever wants the empty string for absent.
func Deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// PartFromModel adapts an API part. Manufacturer parts are passed separately
// because the repository does not populate them: the parts handler composes
// PartRepo.Get with CatalogRepo.ListManufacturerParts, and this mirrors that.
func PartFromModel(p models.Part, mps []models.ManufacturerPart) Part {
	out := Part{
		ID:                  p.ID.String(),
		Name:                p.Name,
		CategoryID:          uuidPtrString(p.CategoryID),
		Description:         p.Description,
		IPN:                 p.IPN,
		Package:             p.Package,
		KicadSymbol:         p.KicadSymbol,
		KicadFootprint:      p.KicadFootprint,
		Keywords:            p.Keywords,
		TotalStock:          p.TotalStock,
		PrimaryMPN:          p.PrimaryMPN,
		PrimaryManufacturer: p.PrimaryManufacturer,
	}

	for _, t := range p.Tags {
		out.Tags = append(out.Tags, t.Name)
	}

	for _, param := range p.Parameters {
		out.Parameters = append(out.Parameters, Parameter{
			TemplateName: param.TemplateName,
			Units:        param.Units,
			Value:        param.Value,
		})
	}

	// Fall back to whatever the part already carries when the caller has none to
	// add, so a part built from a list row still maps.
	if mps == nil {
		mps = p.ManufacturerParts
	}
	for _, mp := range mps {
		m := ManufacturerPart{
			ManufacturerName: mp.ManufacturerName,
			MPN:              mp.MPN,
			DatasheetURL:     mp.DatasheetURL,
		}
		for _, sp := range mp.SupplierParts {
			m.SupplierParts = append(m.SupplierParts, SupplierPart{
				SupplierName: sp.SupplierName,
				SKU:          sp.SKU,
			})
		}
		out.ManufacturerParts = append(out.ManufacturerParts, m)
	}

	return out
}

// CategoryFromModel adapts an API category.
func CategoryFromModel(c models.Category) Category {
	return Category{ID: c.ID.String(), Name: strings.TrimSpace(c.Name)}
}

func uuidPtrString(id *uuid.UUID) *string {
	if id == nil {
		return nil
	}
	s := id.String()
	return &s
}
