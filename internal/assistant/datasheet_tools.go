// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/firelabsca/firebin-api/internal/ai"
	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/firelabsca/firebin-api/internal/repository"
)

// Datasheet reading is deliberately three narrow tools rather than one that
// returns a document.
//
// The binding constraint is the history budget: localHistoryBudget is 4000
// tokens, and a single datasheet page can exceed that on its own. A tool that
// returned a whole document would blow the window on its first call and every
// later turn would be answered from a truncated conversation. So the model
// finds the document, searches it for the pages that matter, and reads only
// those. Everything below is sized to keep one call well inside the budget.
const (
	// maxSnippets caps how many matching pages one search reports.
	maxSnippets = 6
	// snippetRunes is the window of text returned around a match. Enough to see
	// the sentence and the number in it, not enough to bury the answer.
	snippetRunes = 400
	// maxPageRunes caps a single read_datasheet_page result. A dense parameter
	// table can run long; the model is told plainly when it was cut.
	maxPageRunes = 6000
	// maxFindResults caps find_datasheet.
	maxFindResults = 10
)

// datasheetSource is what the tools need from storage: the metadata repository
// plus a way to read one document's extracted text.
//
// An interface rather than the concrete store so the tools can be tested
// without a filesystem, and so a future backend (object storage) does not reach
// into this package.
type datasheetSource interface {
	ReadSidecar(sha string) ([]string, error)
}

// findDatasheet locates documents by part or by free text.
func (t *Toolbox) findDatasheet() Tool {
	return Tool{
		Def: ai.ToolDef{
			Name: "find_datasheet",
			Description: "Find stored datasheets. Give part_id to get the datasheets for a part, " +
				"or query to search titles, filenames and MPNs. Returns ids to pass to " +
				"search_datasheet. text_status says whether the document can be read: 'ok' means " +
				"yes, 'no_text_layer' means it is a scan with no text (say so rather than guessing), " +
				"'pending' means it has not been read yet.",
			Schema: schema(`{"type":"object","properties":{"part_id":{"type":"string"},"query":{"type":"string"}}}`),
		},
		Run: func(ctx context.Context, args json.RawMessage) (any, error) {
			in, err := decode[struct {
				PartID string `json:"part_id"`
				Query  string `json:"query"`
			}](args)
			if err != nil {
				return nil, err
			}
			opts := repository.DatasheetListOptions{Search: in.Query, Limit: maxFindResults}
			if strings.TrimSpace(in.PartID) != "" {
				id, err := parseID(in.PartID, "part_id")
				if err != nil {
					return nil, err
				}
				opts.PartID = &id
			}
			list, err := t.Datasheets.List(ctx, opts)
			if err != nil {
				return nil, err
			}
			out := make([]map[string]any, 0, len(list))
			for _, d := range list {
				row := map[string]any{
					"datasheet_id": d.ID,
					"title":        d.Title,
					"filename":     d.Filename,
					"text_status":  d.TextStatus,
				}
				if row["title"] == nil {
					row["title"] = d.Filename
				}
				if d.PageCount != nil {
					row["pages"] = *d.PageCount
				}
				if d.Language != nil && *d.Language != "" {
					row["language"] = *d.Language
				}
				parts := make([]string, 0, len(d.Parts))
				for _, p := range d.Parts {
					if p.MPN != nil && *p.MPN != "" {
						parts = append(parts, *p.MPN)
					} else {
						parts = append(parts, p.PartName)
					}
				}
				row["parts"] = parts
				out = append(out, row)
			}
			return map[string]any{"count": len(out), "datasheets": out}, nil
		},
	}
}

// searchDatasheet finds the pages of one document that mention something.
func (t *Toolbox) searchDatasheet() Tool {
	return Tool{
		Def: ai.ToolDef{
			Name: "search_datasheet",
			Description: "Search inside one datasheet and get back the pages that match, with a " +
				"short snippet from each. Use this before read_datasheet_page: a datasheet can be " +
				"hundreds of pages, and this is how you find which ones matter. Search for the " +
				"term as it would be printed in the document (\"deep sleep\", \"absolute maximum\", " +
				"\"I2C address\"). An empty result means the words are not in the text.",
			Schema: schema(`{"type":"object","properties":{"datasheet_id":{"type":"string"},"query":{"type":"string"}},"required":["datasheet_id","query"]}`),
		},
		Run: func(ctx context.Context, args json.RawMessage) (any, error) {
			in, err := decode[struct {
				DatasheetID string `json:"datasheet_id"`
				Query       string `json:"query"`
			}](args)
			if err != nil {
				return nil, err
			}
			pages, d, err := t.datasheetPages(ctx, in.DatasheetID)
			if err != nil {
				return nil, err
			}
			q := strings.TrimSpace(in.Query)
			if q == "" {
				return nil, fmt.Errorf("query is required; say what to look for")
			}

			hits := []map[string]any{}
			for i, page := range pages {
				if len(hits) >= maxSnippets {
					break
				}
				at := indexFold(page, q)
				if at < 0 {
					continue
				}
				hits = append(hits, map[string]any{
					"page":    i + 1,
					"snippet": snippetAround(page, at, len(q)),
				})
			}
			return map[string]any{
				"datasheet": titleOf(d),
				"query":     q,
				"count":     len(hits),
				"matches":   hits,
				"note":      searchNote(len(hits), len(pages)),
			}, nil
		},
	}
}

// readDatasheetPage returns one page of a document.
func (t *Toolbox) readDatasheetPage() Tool {
	return Tool{
		Def: ai.ToolDef{
			Name: "read_datasheet_page",
			Description: "Read one page of a datasheet in full, by page number (1-based). Use it " +
				"after search_datasheet has told you which page holds the answer. Read one page at " +
				"a time; asking for many pages of a long document wastes the context you need to " +
				"answer with.",
			Schema: schema(`{"type":"object","properties":{"datasheet_id":{"type":"string"},"page":{"type":"integer"}},"required":["datasheet_id","page"]}`),
		},
		Run: func(ctx context.Context, args json.RawMessage) (any, error) {
			in, err := decode[struct {
				DatasheetID string `json:"datasheet_id"`
				Page        int    `json:"page"`
			}](args)
			if err != nil {
				return nil, err
			}
			pages, d, err := t.datasheetPages(ctx, in.DatasheetID)
			if err != nil {
				return nil, err
			}
			if in.Page < 1 || in.Page > len(pages) {
				return nil, errors.New("page " + itoa(in.Page) + " is out of range; this datasheet has " +
					itoa(len(pages)) + " pages of readable text")
			}
			text := pages[in.Page-1]
			truncated := false
			if r := []rune(text); len(r) > maxPageRunes {
				text = string(r[:maxPageRunes])
				truncated = true
			}
			out := map[string]any{
				"datasheet": titleOf(d),
				"page":      in.Page,
				"pages":     len(pages),
				"text":      text,
			}
			if truncated {
				// Said plainly so the model reports a partial page rather than
				// treating a cut-off table as the whole story.
				out["truncated"] = true
				out["note"] = "This page was longer than the limit and has been cut off. " +
					"Search for a more specific term if the answer is not here."
			}
			if text == "" {
				out["note"] = "This page has no text. It is probably an image or a blank page."
			}
			return out, nil
		},
	}
}

// datasheetPages loads one document's extracted text, turning every "cannot
// read this" case into a message written for the model rather than an error it
// has to guess at.
func (t *Toolbox) datasheetPages(ctx context.Context, rawID string) ([]string, *models.Datasheet, error) {
	id, err := parseID(rawID, "datasheet_id")
	if err != nil {
		return nil, nil, err
	}
	d, err := t.Datasheets.Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if d == nil {
		return nil, nil, fmt.Errorf("no datasheet with that id; call find_datasheet first")
	}
	switch d.TextStatus {
	case models.TextNoTextLayer:
		return nil, nil, errors.New(titleOf(d) + " is a scan with no text layer, so it cannot be read. " +
			"Tell the user the document is image-only rather than guessing at its contents.")
	case models.TextPending:
		return nil, nil, errors.New(titleOf(d) + " has not been read yet. Tell the user it is still " +
			"being processed and to try again shortly.")
	case models.TextFailed:
		return nil, nil, errors.New(titleOf(d) + " could not be read; the file may be malformed.")
	}
	pages, err := t.DatasheetText.ReadSidecar(d.SHA256)
	if err != nil {
		return nil, nil, errors.New(titleOf(d) + " has no extracted text available.")
	}
	return pages, d, nil
}

func titleOf(d *models.Datasheet) string {
	if d == nil {
		return "the datasheet"
	}
	if d.Title != nil && *d.Title != "" {
		return *d.Title
	}
	return d.Filename
}

// indexFold is a case-insensitive substring search.
//
// Deliberately not a tokeniser or a fuzzy match. A datasheet query is almost
// always a printed phrase ("absolute maximum ratings") or a symbol ("VDD"), and
// an exact fold match either finds it or honestly does not.
func indexFold(haystack, needle string) int {
	return strings.Index(strings.ToLower(haystack), strings.ToLower(needle))
}

// snippetAround returns a window of text centred on a match, on rune boundaries
// so a multi-byte character is never cut in half.
func snippetAround(page string, byteAt, needleLen int) string {
	runes := []rune(page)
	// Convert the byte offset to a rune offset.
	at := len([]rune(page[:byteAt]))
	half := (snippetRunes - len([]rune(page[byteAt:byteAt+needleLen]))) / 2
	start := at - half
	if start < 0 {
		start = 0
	}
	end := start + snippetRunes
	if end > len(runes) {
		end = len(runes)
		if start > end-snippetRunes {
			start = end - snippetRunes
		}
		if start < 0 {
			start = 0
		}
	}
	out := strings.TrimSpace(string(runes[start:end]))
	if start > 0 {
		out = "…" + out
	}
	if end < len(runes) {
		out += "…"
	}
	return out
}

func searchNote(hits, pages int) string {
	if pages == 0 {
		return "This datasheet has no extracted text."
	}
	if hits == 0 {
		return "Those words do not appear in this datasheet. Try the wording the document would " +
			"use, or a shorter phrase."
	}
	if hits >= maxSnippets {
		return "Showing the first " + itoa(maxSnippets) + " matching pages; there may be more."
	}
	return ""
}

// itoa avoids importing strconv for two call sites, matching the repository
// package's own helper.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	if neg {
		return "-" + string(d)
	}
	return string(d)
}
