// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"context"
	"strings"

	"github.com/firelabsca/firebin-api/internal/assistant"
	"github.com/firelabsca/firebin-api/internal/models"
)

// assistantToolbox builds the tool set the assistant may use.
//
// This function is the whole permission boundary. What is not wired here is not
// reachable, whatever a question or a system prompt says, so read it as the
// answer to "what can the assistant do to my data".
func (h *Handler) assistantToolbox() *assistant.Toolbox {
	return &assistant.Toolbox{
		Parts:      h.Parts,
		Categories: h.Categories,
		Locations:  h.Locations,
		Stock:      h.Stock,
		Stats:      h.Stats,
		Catalog:    h.Catalog,
		Projects:   h.Projects,

		// The one write besides add-a-reference-part. Deliberate: a tag is
		// additive and reversible, it cannot change what a part IS, and "call
		// this one qwiic" is exactly the kind of thing you want to say out loud
		// rather than click through. Nothing here can delete a tag from the
		// vocabulary — only take one off a part.
		Tags: h.Tags,

		// Read-only, like everything else here: the tools can find, search and
		// read a stored datasheet, and cannot upload, delete, or relink one.
		Datasheets:    h.Datasheets,
		DatasheetText: h.DatasheetFiles,

		// Reuses the ordinary lookup chain, including its 30-day cache, so a
		// repeated question does not spend a second distributor call.
		Enrich: func(ctx context.Context, mpn string) (*models.EnrichedPart, error) {
			return h.enrichAll(ctx, mpn, nil)
		},

		CreateReferencePart: h.createReferencePart,
	}
}

// createReferencePart records a part the user does not own.
//
// reference_only is set here, not taken from the caller, so there is no
// argument the model can pass to create an ordinary part. Stock and location
// are not settable at all: a row created this way is a note, and it takes a
// person to turn it into inventory.
func (h *Handler) createReferencePart(ctx context.Context, in assistant.ReferencePartInput) (*models.Part, error) {
	p := &models.Part{
		Name:          strings.TrimSpace(in.Name),
		ReferenceOnly: true,
	}
	if d := strings.TrimSpace(in.Description); d != "" {
		p.Description = &d
	}
	if pkg := strings.TrimSpace(in.Package); pkg != "" {
		p.Package = &pkg
	}
	if k := strings.TrimSpace(in.Keywords); k != "" {
		p.Keywords = &k
	}
	if err := h.Parts.Create(ctx, p); err != nil {
		return nil, err
	}
	// The MPN is a separate row, and failing to attach it should not undo a
	// part that was created successfully. Recorded as best effort.
	if mpn := strings.TrimSpace(in.MPN); mpn != "" {
		_, _ = h.Catalog.CreateManufacturerPart(ctx, p.ID, "", mpn, nil)
	}
	return p, nil
}
