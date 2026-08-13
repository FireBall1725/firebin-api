// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/firelabsca/firebin-api/internal/ai"
)

// listTags reports the vocabulary in use.
//
// Offered alongside tagPart rather than left implicit: without it the model
// coins a second spelling of a tag that already exists. The server folds
// spellings together so nothing breaks either way, but "STEMMA QT" is not the
// same word as "Qwiic" and only the existing list says which one this user
// reaches for.
func (t *Toolbox) listTags() Tool {
	return Tool{
		Def: ai.ToolDef{
			Name: "list_tags",
			Description: "Every tag in use, with how many parts carry each. Tags are the informal " +
				"names a part answers to besides its own: a JST SH 1.0 mm header tagged \"Qwiic\" and " +
				"\"STEMMA QT\" is findable by either word even though neither appears in its part " +
				"number. Call this before tag_part so you reuse a tag that already exists.",
			Schema: schema(`{"type":"object","properties":{}}`),
		},
		Run: func(ctx context.Context, _ json.RawMessage) (any, error) {
			tags, err := t.Tags.List(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]map[string]any, 0, len(tags))
			for _, tag := range tags {
				out = append(out, map[string]any{
					"name": tag.Name, "slug": tag.Slug, "part_count": tag.PartCount,
				})
			}
			return map[string]any{"tags": out, "total": len(out)}, nil
		},
	}
}

// tagPart adds and removes tags on a part.
//
// Add and remove, never replace. "Call this one qwiic" tells the model one name
// and nothing about the rest of the set, and a replace from that position
// silently drops every tag it was not told about.
func (t *Toolbox) tagPart() Tool {
	return Tool{
		Def: ai.ToolDef{
			Name: "tag_part",
			Description: "Add or remove tags on a part. A tag is another name the part answers to, " +
				"like \"Qwiic\" or \"STEMMA QT\" on a JST SH connector, and searching for it finds the " +
				"part. A tag never replaces the part's name or its manufacturer part number. Adding a " +
				"tag that does not exist creates it, so call list_tags first and reuse what is there. " +
				"Tags you do not name are left alone.",
			Schema: schema(`{
				"type":"object",
				"properties":{
					"part_id":{"type":"string","description":"id from search_parts"},
					"add":{"type":"array","items":{"type":"string"},"description":"tag names to put on the part"},
					"remove":{"type":"array","items":{"type":"string"},"description":"tag names to take off this part; the tag itself survives for other parts"}
				},
				"required":["part_id"]
			}`),
		},
		Run: func(ctx context.Context, args json.RawMessage) (any, error) {
			in, err := decode[struct {
				PartID string   `json:"part_id"`
				Add    []string `json:"add"`
				Remove []string `json:"remove"`
			}](args)
			if err != nil {
				return nil, err
			}
			id, err := parseID(in.PartID, "part_id")
			if err != nil {
				return nil, err
			}
			if len(in.Add) == 0 && len(in.Remove) == 0 {
				return nil, fmt.Errorf("pass add, remove, or both")
			}
			if _, err := t.Parts.Get(ctx, id); err != nil {
				return nil, err
			}

			if len(in.Remove) > 0 {
				if _, err := t.Tags.RemovePartTags(ctx, id, in.Remove); err != nil {
					return nil, err
				}
			}
			tags, err := t.Tags.AddPartTags(ctx, id, in.Add)
			if err != nil {
				return nil, err
			}
			names := make([]string, 0, len(tags))
			for _, tag := range tags {
				names = append(names, tag.Name)
			}
			return map[string]any{
				"part_id": id.String(),
				"tags":    names,
				"summary": fmt.Sprintf("Now tagged: %s", strings.Join(names, ", ")),
			}, nil
		},
	}
}
