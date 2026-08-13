// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"context"

	"github.com/google/uuid"

	"github.com/firelabsca/firebin-api/internal/kicad/httplib/source"
	"github.com/firelabsca/firebin-api/internal/repository"
)

// kicadSource feeds the KiCad library snapshot from the repositories.
//
// This replaces the HTTP client the library server used when it ran as its own
// process. The three methods are the same three calls it used to make; they are
// now function calls, which is the entire reason there is no longer an outbound
// access token to over-grant or rotate.
type kicadSource struct {
	categories *repository.CategoryRepo
	parts      *repository.PartRepo
	catalog    *repository.CatalogRepo
	tags       *repository.TagRepo
}

func (s kicadSource) Categories(ctx context.Context) ([]source.Category, error) {
	cats, err := s.categories.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]source.Category, 0, len(cats))
	for _, c := range cats {
		out = append(out, source.CategoryFromModel(c))
	}
	return out, nil
}

// Parts returns identities only. The snapshot reads the id and category from
// these and then fetches each part in full, so anything else would be computed
// and thrown away.
func (s kicadSource) Parts(ctx context.Context) ([]source.Part, error) {
	rows, err := s.parts.ListIdentities(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]source.Part, 0, len(rows))
	for _, p := range rows {
		out = append(out, source.PartFromModel(p, nil))
	}
	return out, nil
}

// Part composes the full detail the mapping needs.
//
// PartRepo.Get populates parameters but not manufacturer parts; the parts
// handler adds those separately, and this mirrors it. Without them a part loses
// its MPN, manufacturer and datasheet, and every MPN becomes unsearchable in
// KiCad's chooser, because those identifiers reach it through the keywords field.
func (s kicadSource) Part(ctx context.Context, id string) (*source.Part, error) {
	pid, err := uuid.Parse(id)
	if err != nil {
		return nil, err
	}
	p, err := s.parts.Get(ctx, pid)
	if err != nil {
		return nil, err
	}
	mps, err := s.catalog.ListManufacturerParts(ctx, pid)
	if err != nil {
		// A part with unreadable manufacturer rows is still worth placing, so
		// carry on with what the part itself holds rather than dropping it.
		mps = nil
	}
	// Get does not populate tags, and they reach the chooser the same way MPNs
	// do — through the keywords field — so an omission here is not a missing
	// label but a part that stops answering to its own nickname inside KiCad.
	if tags, err := s.tags.TagsForPart(ctx, pid); err == nil {
		p.Tags = tags
	}
	out := source.PartFromModel(*p, mps)
	return &out, nil
}
