// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package assistant turns a chat provider into something that can answer
// questions about an inventory, by giving it tools that read the repositories
// directly.
//
// In-process rather than over the MCP server, which already exposes similar
// tools: every MCP tool is an HTTP call through one shared client holding a
// single fbin_pat_ token, so every session there is the same identity. A
// per-user product feature cannot use that. The tool *descriptions* are shared
// wording with MCP because they are careful prompt engineering already; the two
// surfaces have to be kept in step deliberately.
package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"

	"github.com/firelabsca/firebin-api/internal/ai"
	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/firelabsca/firebin-api/internal/picklist"
	"github.com/firelabsca/firebin-api/internal/repository"
	"github.com/google/uuid"
)

// Tool is one callable capability: what the model is told about it, and what
// running it does.
type Tool struct {
	Def ai.ToolDef
	Run func(ctx context.Context, args json.RawMessage) (any, error)
}

// Toolbox holds the repositories the tools read, and builds the tool set.
//
// Nothing here can change stock, edit an existing part, move anything, or
// delete anything. That is enforced by those methods being absent, not by a
// prompt asking the model not to: a prompt is a request, and this is the only
// list of what is reachable.
type Toolbox struct {
	Parts      *repository.PartRepo
	Categories *repository.CategoryRepo
	Locations  *repository.LocationRepo
	Stock      *repository.StockRepo
	Stats      *repository.StatsRepo
	Catalog    *repository.CatalogRepo
	Projects   *repository.ProjectRepo

	// Datasheets and DatasheetText are a pair: metadata from Postgres, extracted
	// page text from the sidecar on disk. Both optional, and the datasheet tools
	// are only offered when both are present, so an instance without attachment
	// storage does not advertise a capability it cannot deliver.
	Datasheets    *repository.DatasheetRepo
	DatasheetText datasheetSource

	// Enrich looks up an MPN with the distributor providers. Optional: when nil
	// the lookup tool is not offered at all, rather than offered and failing.
	Enrich func(ctx context.Context, mpn string) (*models.EnrichedPart, error)

	// CreateReferencePart is the single write. Optional for the same reason.
	CreateReferencePart func(ctx context.Context, in ReferencePartInput) (*models.Part, error)
}

// ReferencePartInput is the one thing the assistant may create: a part you do
// not own. It cannot set stock or a location, because those are what make a
// part real.
type ReferencePartInput struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MPN         string `json:"mpn,omitempty"`
	Package     string `json:"package,omitempty"`
	Keywords    string `json:"keywords,omitempty"`
}

// Tools returns every tool available, in a stable order.
func (t *Toolbox) Tools() []Tool {
	tools := []Tool{
		t.searchParts(),
		t.getPart(),
		t.listCategories(),
		t.listLocations(),
		t.locationContents(),
		t.lowStock(),
		t.inventoryStats(),
		t.listProjects(),
		t.getProject(),
		t.getBoard(),
		t.boardPickList(),
	}
	if t.Datasheets != nil && t.DatasheetText != nil {
		tools = append(tools, t.findDatasheet(), t.searchDatasheet(), t.readDatasheetPage())
	}
	if t.Enrich != nil {
		tools = append(tools, t.lookupMPN())
	}
	if t.CreateReferencePart != nil {
		tools = append(tools, t.addReferencePart())
	}
	return tools
}

// Defs returns just the definitions, for handing to a provider.
func (t *Toolbox) Defs() []ai.ToolDef {
	all := t.Tools()
	defs := make([]ai.ToolDef, 0, len(all))
	for _, tool := range all {
		defs = append(defs, tool.Def)
	}
	return defs
}

// Run executes a tool call and returns the result in the form the provider
// feeds back to the model.
//
// A failure comes back as a tool result marked as an error, not as a Go error:
// the model asked for something and deserves to be told it did not work, so it
// can try a different tool or say it cannot answer. Aborting the turn would
// turn a recoverable mistake into a failed question.
func (t *Toolbox) Run(ctx context.Context, call ai.ToolCall) ai.ToolResult {
	call = t.Normalise(call)
	res := ai.ToolResult{CallID: call.ID, Name: call.Name}
	for _, tool := range t.Tools() {
		if tool.Def.Name != call.Name {
			continue
		}
		out, err := runGuarded(ctx, tool, call.Input)
		if err != nil {
			res.IsError = true
			res.Content = err.Error()
			return res
		}
		encoded, err := json.Marshal(out)
		if err != nil {
			res.IsError = true
			res.Content = "could not encode the result: " + err.Error()
			return res
		}
		res.Content = string(encoded)
		return res
	}
	res.IsError = true
	// Naming what does exist turns a hallucinated tool name into a recoverable
	// mistake instead of a dead end.
	res.Content = fmt.Sprintf("no tool named %q. Available: %s", call.Name, strings.Join(t.names(), ", "))
	return res
}

// Normalise repairs a tool name the runtime mangled.
//
// gpt-oss addresses tools in the Harmony format as
// "functions.read_datasheet_page", and Ollama's parser sometimes loses the half
// after the dot: what arrives is a call named "functions?" or "2?" carrying
// perfectly good arguments. Applied by Run, and separately by the loops before
// they record what was called, so the step the user reads names the tool that
// actually ran rather than the corruption.
//
// A no-op for a name that is already right, so calling it twice is free.
func (t *Toolbox) Normalise(call ai.ToolCall) ai.ToolCall {
	name, ok := resolveToolName(call, t.Defs())
	if !ok || name == call.Name {
		return call
	}
	slog.Warn("recovered a mangled tool name",
		"received", call.Name, "resolved", name, "args", string(call.Input))
	call.Name = name
	return call
}

// runGuarded runs a tool and turns a panic into an ordinary tool failure.
//
// A tool call is model-generated input reaching repository code. A malformed
// argument that trips a nil dereference should cost the model one failed tool
// call, not take down the request that a person is waiting on. The panic is
// logged with its stack so it still gets fixed.
func runGuarded(ctx context.Context, tool Tool, args json.RawMessage) (out any, err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("assistant tool panicked",
				"tool", tool.Def.Name, "panic", r, "stack", string(debug.Stack()))
			err = fmt.Errorf("the %s tool failed unexpectedly", tool.Def.Name)
		}
	}()
	return tool.Run(ctx, args)
}

func (t *Toolbox) names() []string {
	all := t.Tools()
	out := make([]string, 0, len(all))
	for _, tool := range all {
		out = append(out, tool.Def.Name)
	}
	return out
}

// decode unmarshals a tool's arguments, reporting the problem in terms the
// model can act on.
func decode[T any](args json.RawMessage) (T, error) {
	var v T
	if len(args) == 0 {
		return v, nil
	}
	if err := json.Unmarshal(args, &v); err != nil {
		return v, fmt.Errorf("could not read the arguments: %v", err)
	}
	return v, nil
}

func parseID(s, field string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(s))
	if err != nil {
		return uuid.Nil, fmt.Errorf("%s must be an id returned by another tool, not a name; %q is not one", field, s)
	}
	return id, nil
}

func schema(s string) json.RawMessage { return json.RawMessage(s) }

// ─── Read tools ──────────────────────────────────────────────────────────────

func (t *Toolbox) searchParts() Tool {
	return Tool{
		Def: ai.ToolDef{
			Name: "search_parts",
			Description: "Search individual components in the inventory by specification. This searches " +
				"PARTS ONLY. It does not know about projects or boards, and a project name will never " +
				"match: to answer anything about building a design, use list_projects. " +
				"Use this first for any question about what parts are held. `value` understands units: \"220 ohm\" will not match 220 pF and " +
				"\"100 ohm\" will not match 100 kΩ, while a bare number like \"220\" matches the value " +
				"printed on the part whatever its unit. `package` matches part of the name, so \"0603\" " +
				"finds \"0603 (1608 Metric)\". `search` is free text over name, keywords, internal part " +
				"number and manufacturer part number. Every filter is optional; with none it lists parts. " +
				"An empty result means the part is not in the inventory, which is a real answer. " +
				"Results carry only the parameters that matched; call get_part for a part's full specification.",
			Schema: schema(`{
				"type":"object",
				"properties":{
					"search":{"type":"string","description":"free text over name, keywords, IPN and MPN"},
					"package":{"type":"string","description":"package substring, e.g. 0603"},
					"parameter":{"type":"string","description":"restrict value to a named parameter, e.g. Resistance"},
					"value":{"type":"string","description":"parameter value, e.g. 220 ohm, 4.7uF, X7R"},
					"limit":{"type":"integer","description":"maximum parts to return, default 25"}
				}
			}`),
		},
		Run: func(ctx context.Context, args json.RawMessage) (any, error) {
			in, err := decode[struct {
				Search    string `json:"search"`
				Package   string `json:"package"`
				Parameter string `json:"parameter"`
				Value     string `json:"value"`
				Limit     int    `json:"limit"`
			}](args)
			if err != nil {
				return nil, err
			}
			if in.Limit <= 0 {
				in.Limit = 25
			}
			matches, err := t.Parts.SearchParametric(ctx, repository.ParametricOptions{
				Search: in.Search, Package: in.Package, Parameter: in.Parameter,
				Value: in.Value, Limit: in.Limit,
			})
			if err != nil {
				return nil, err
			}
			out := make([]map[string]any, 0, len(matches))
			for _, m := range matches {
				out = append(out, searchRow(m))
			}
			return map[string]any{"count": len(out), "parts": out}, nil
		},
	}
}

// searchRow is one part in a search result.
//
// Only the parameters that matched, not all of them. Enriched parts carry
// thirty or more ("China RoHS", "Case Code (Imperial)", "Composition"), and
// returning every one for every hit made a 25-part search 10,600 tokens of
// mostly irrelevant text against real data; trimmed it is about 1,200. get_part
// is where a part's full specification lives. This is for choosing which part
// to look at.
func searchRow(m models.PartMatch) map[string]any {
	row := map[string]any{
		"id":          m.ID,
		"name":        m.Name,
		"ipn":         m.IPN,
		"in_stock":    m.TotalStock,
		"matched":     paramPairs(m.Matched),
		"description": m.Description,
	}
	if m.Package != nil {
		row["package"] = *m.Package
	}
	// A part flagged as not owned reads as "in stock: 0" otherwise, which is
	// true but hides that it was never meant to be stocked.
	if m.ReferenceOnly {
		row["reference_only"] = true
		row["note"] = "recorded for reference; this part is not stocked"
	}
	return row
}

// paramPairs flattens parameters to name/value/unit, which is all the model
// needs and a third of the bytes of the full rows.
func paramPairs(ps []models.PartParameter) []map[string]string {
	out := make([]map[string]string, 0, len(ps))
	for _, p := range ps {
		row := map[string]string{"name": p.TemplateName, "value": p.Value}
		if p.Units != nil && *p.Units != "" {
			row["unit"] = *p.Units
		}
		out = append(out, row)
	}
	return out
}

func (t *Toolbox) getPart() Tool {
	return Tool{
		Def: ai.ToolDef{
			Name: "get_part",
			Description: "Full detail for one part: parameters, where its stock sits, and its " +
				"manufacturer and supplier parts with price breaks. Call this only after search_parts " +
				"has given you an id.",
			Schema: schema(`{"type":"object","properties":{"part_id":{"type":"string","description":"id from search_parts"}},"required":["part_id"]}`),
		},
		Run: func(ctx context.Context, args json.RawMessage) (any, error) {
			in, err := decode[struct {
				PartID string `json:"part_id"`
			}](args)
			if err != nil {
				return nil, err
			}
			id, err := parseID(in.PartID, "part_id")
			if err != nil {
				return nil, err
			}
			p, err := t.Parts.Get(ctx, id)
			if err != nil {
				return nil, err
			}
			stock, err := t.Stock.ListForPart(ctx, id)
			if err != nil {
				return nil, err
			}
			bins := make([]map[string]any, 0, len(stock))
			for _, s := range stock {
				row := map[string]any{"quantity": s.Quantity}
				if s.LocationName != nil {
					row["location"] = *s.LocationName
				}
				bins = append(bins, row)
			}
			mfgParts, err := t.Catalog.ListManufacturerParts(ctx, id)
			if err != nil {
				return nil, err
			}
			out := map[string]any{
				"id": p.ID, "name": p.Name, "ipn": p.IPN, "description": p.Description,
				"in_stock": p.TotalStock, "minimum_stock": p.MinimumStock,
				"parameters":        paramPairs(p.Parameters),
				"stock_by_location": bins,
				"suppliers":         supplierRows(mfgParts),
			}
			if p.Package != nil {
				out["package"] = *p.Package
			}
			if p.ReferenceOnly {
				out["reference_only"] = true
			}
			return out, nil
		},
	}
}

// supplierRows gives the model structured price breaks with the currency on
// every one.
//
// Not the flattened "1: 0.4200 USD, 10: 0.3100 USD" string the MCP wire format
// uses, because the model has to do arithmetic on these. And the currency is
// per break on purpose: Digi-Key and Mouser honour the instance currency
// setting but Nexar stores whatever each seller reported, so two rows on the
// same part can be in different currencies and comparing them as bare numbers
// compares different things.
func supplierRows(mfgParts []models.ManufacturerPart) []map[string]any {
	out := []map[string]any{}
	for _, mp := range mfgParts {
		for _, sp := range mp.SupplierParts {
			breaks := make([]map[string]any, 0, len(sp.Pricing))
			for _, b := range sp.Pricing {
				breaks = append(breaks, map[string]any{
					"quantity": b.Quantity, "price": b.Price, "currency": b.Currency,
				})
			}
			row := map[string]any{
				"manufacturer": mp.ManufacturerName,
				"mpn":          mp.MPN,
				"supplier":     sp.SupplierName,
				"sku":          sp.SKU,
				"price_breaks": breaks,
			}
			if sp.MOQ != nil {
				row["minimum_order_quantity"] = *sp.MOQ
			}
			out = append(out, row)
		}
	}
	return out
}

func (t *Toolbox) listCategories() Tool {
	return Tool{
		Def: ai.ToolDef{
			Name:        "list_categories",
			Description: "Every category, with how many parts sit directly in each.",
			Schema:      schema(`{"type":"object","properties":{}}`),
		},
		Run: func(ctx context.Context, _ json.RawMessage) (any, error) {
			cats, err := t.Categories.List(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]map[string]any, 0, len(cats))
			for _, c := range cats {
				out = append(out, map[string]any{"id": c.ID, "name": c.Name, "parts": c.PartCount})
			}
			return map[string]any{"categories": out}, nil
		},
	}
}

func (t *Toolbox) listLocations() Tool {
	return Tool{
		Def: ai.ToolDef{
			Name:        "list_locations",
			Description: "Every storage location (bin, drawer, shelf) by name.",
			Schema:      schema(`{"type":"object","properties":{}}`),
		},
		Run: func(ctx context.Context, _ json.RawMessage) (any, error) {
			locs, err := t.Locations.List(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]map[string]any, 0, len(locs))
			for _, l := range locs {
				out = append(out, map[string]any{"id": l.ID, "name": l.Name, "description": l.Description})
			}
			return map[string]any{"locations": out}, nil
		},
	}
}

func (t *Toolbox) locationContents() Tool {
	return Tool{
		Def: ai.ToolDef{
			Name:        "location_contents",
			Description: "What is stored in one location, with quantities. Needs an id from list_locations.",
			Schema:      schema(`{"type":"object","properties":{"location_id":{"type":"string"}},"required":["location_id"]}`),
		},
		Run: func(ctx context.Context, args json.RawMessage) (any, error) {
			in, err := decode[struct {
				LocationID string `json:"location_id"`
			}](args)
			if err != nil {
				return nil, err
			}
			id, err := parseID(in.LocationID, "location_id")
			if err != nil {
				return nil, err
			}
			items, err := t.Stock.ListForLocation(ctx, id)
			if err != nil {
				return nil, err
			}
			out := make([]map[string]any, 0, len(items))
			for _, s := range items {
				out = append(out, map[string]any{
					"part_id": s.PartID, "part": s.PartName, "quantity": s.Quantity,
				})
			}
			return map[string]any{"count": len(out), "items": out}, nil
		},
	}
}

func (t *Toolbox) lowStock() Tool {
	return Tool{
		Def: ai.ToolDef{
			Name: "low_stock",
			Description: "Parts at or below their minimum stock level, most depleted first. Only parts " +
				"with a minimum set appear; a part with no minimum is never low.",
			Schema: schema(`{"type":"object","properties":{}}`),
		},
		Run: func(ctx context.Context, _ json.RawMessage) (any, error) {
			parts, err := t.Parts.ListLowStock(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]map[string]any, 0, len(parts))
			for _, p := range parts {
				out = append(out, map[string]any{
					"id": p.ID, "name": p.Name, "in_stock": p.TotalStock, "minimum": p.MinimumStock,
				})
			}
			return map[string]any{"count": len(out), "parts": out}, nil
		},
	}
}

func (t *Toolbox) inventoryStats() Tool {
	return Tool{
		Def: ai.ToolDef{
			Name:        "inventory_stats",
			Description: "Totals for the whole instance: part count, stock on hand, locations, low-stock count.",
			Schema:      schema(`{"type":"object","properties":{}}`),
		},
		Run: func(ctx context.Context, _ json.RawMessage) (any, error) {
			return t.Stats.Get(ctx)
		},
	}
}

func (t *Toolbox) listProjects() Tool {
	return Tool{
		Def: ai.ToolDef{
			Name:        "list_projects",
			Description: "Every project. A project holds boards, and a board holds the bill of materials.",
			Schema:      schema(`{"type":"object","properties":{}}`),
		},
		Run: func(ctx context.Context, _ json.RawMessage) (any, error) {
			projects, err := t.Projects.List(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]map[string]any, 0, len(projects))
			for _, p := range projects {
				out = append(out, map[string]any{"id": p.ID, "name": p.Name, "description": p.Description})
			}
			return map[string]any{"projects": out}, nil
		},
	}
}

func (t *Toolbox) getProject() Tool {
	return Tool{
		Def: ai.ToolDef{
			Name:        "get_project",
			Description: "One project with its boards. Use a board id with board_pick_list to find out whether it can be built.",
			Schema:      schema(`{"type":"object","properties":{"project_id":{"type":"string"}},"required":["project_id"]}`),
		},
		Run: func(ctx context.Context, args json.RawMessage) (any, error) {
			in, err := decode[struct {
				ProjectID string `json:"project_id"`
			}](args)
			if err != nil {
				return nil, err
			}
			id, err := parseID(in.ProjectID, "project_id")
			if err != nil {
				return nil, err
			}
			p, err := t.Projects.Get(ctx, id)
			if err != nil {
				return nil, err
			}
			if p == nil {
				// Projects and boards are both bare uuids, and the model mixes
				// them up. Saying only "not found" makes that a dead end, when
				// the id usually names something real and the tool can say
				// what. A recoverable mistake beats a correct refusal.
				if b, berr := t.Projects.GetBoard(ctx, id); berr == nil && b != nil {
					return nil, fmt.Errorf(
						"that is a board id, not a project id: it is the board %q. Use board_pick_list with it, or list_projects to find the project it belongs to",
						b.Name)
				}
				return nil, fmt.Errorf("no project with that id. Call list_projects to get one")
			}
			boards, err := t.Projects.ListBoards(ctx, id)
			if err != nil {
				return nil, err
			}
			rows := make([]map[string]any, 0, len(boards))
			for _, b := range boards {
				rows = append(rows, map[string]any{
					"id": b.ID, "name": b.Name, "revision": b.Revision, "copies": b.Copies,
				})
			}
			return map[string]any{
				"id": p.ID, "name": p.Name, "description": p.Description, "boards": rows,
			}, nil
		},
	}
}

func (t *Toolbox) getBoard() Tool {
	return Tool{
		Def: ai.ToolDef{
			Name: "get_board",
			Description: "One board's bill of materials, line by line: the references (R7, C1), the " +
				"value, the footprint, the manufacturer part number where the BOM carries one, and " +
				"which inventory part each line is matched to. Use this for any question about a " +
				"specific component on a board, and read the mpn from the line rather than asking " +
				"the user for it. Use board_pick_list instead to ask whether the board can be built.",
			Schema: schema(`{"type":"object","properties":{"board_id":{"type":"string","description":"id from get_project"}},"required":["board_id"]}`),
		},
		Run: func(ctx context.Context, args json.RawMessage) (any, error) {
			in, err := decode[struct {
				BoardID string `json:"board_id"`
			}](args)
			if err != nil {
				return nil, err
			}
			id, err := parseID(in.BoardID, "board_id")
			if err != nil {
				return nil, err
			}
			b, err := t.Projects.GetBoard(ctx, id)
			if err != nil {
				return nil, err
			}
			if b == nil {
				// The same confusion get_project handles, from the other side.
				if pr, perr := t.Projects.Get(ctx, id); perr == nil && pr != nil {
					return nil, fmt.Errorf(
						"that is a project id, not a board id: it is the project %q. Call get_project with it to list its boards",
						pr.Name)
				}
				return nil, fmt.Errorf("no board with that id. Call get_project to list a project's boards")
			}

			lines := make([]map[string]any, 0, len(b.Lines))
			for _, l := range b.Lines {
				// Only the fields a line actually carries. A BOM is mostly
				// sparse and empty keys are bytes the model has to read past.
				row := map[string]any{"refs": l.Refs, "quantity": l.Quantity, "value": l.Value}
				for k, v := range map[string]string{
					"footprint": l.Footprint, "mpn": l.MPN, "manufacturer": l.Manufacturer,
					"supplier_sku": l.SupplierSKU, "description": l.Description,
				} {
					if v != "" {
						row[k] = v
					}
				}
				if l.PartID != nil {
					row["part_id"] = *l.PartID
					row["part_name"] = l.PartName
				} else {
					// Said outright, because "no part_id" is easy to skim past
					// and it is the whole point of the line.
					row["matched"] = false
					row["note"] = "no inventory part is linked to this line"
				}
				lines = append(lines, row)
			}
			return map[string]any{
				"board": b.Name, "revision": b.Revision, "copies": b.Copies,
				"lines": len(lines), "bom": lines,
			}, nil
		},
	}
}

func (t *Toolbox) boardPickList() Tool {
	return Tool{
		Def: ai.ToolDef{
			Name: "board_pick_list",
			Description: "Work out whether a board can be built, and what to pull from which bin. " +
				"This is the only way to answer \"do I have the parts to build X\": it uses the board's " +
				"real bill of materials. `shortfalls` lists what is short and by how much; `unmatched` " +
				"lists BOM lines with no part linked in the inventory, which are unknowns rather than " +
				"things you have; each carries the manufacturer part number where the BOM has one. " +
				"Call get_board for the full line-by-line bill of materials. If the project has no " +
				"board with a BOM, say so instead of guessing what the design would need.",
			Schema: schema(`{
				"type":"object",
				"properties":{
					"board_id":{"type":"string","description":"id from get_project"},
					"quantity":{"type":"integer","description":"how many boards to build, default 1"}
				},
				"required":["board_id"]
			}`),
		},
		Run: func(ctx context.Context, args json.RawMessage) (any, error) {
			in, err := decode[struct {
				BoardID  string `json:"board_id"`
				Quantity int    `json:"quantity"`
			}](args)
			if err != nil {
				return nil, err
			}
			id, err := parseID(in.BoardID, "board_id")
			if err != nil {
				return nil, err
			}
			list, err := picklist.Compute(ctx, t.Projects, t.Stock, id, in.Quantity)
			if err != nil {
				// The mirror of the confusion get_project handles: a project id
				// passed where a board id belongs.
				var notFound picklist.ErrBoardNotFound
				if errors.As(err, &notFound) {
					if pr, perr := t.Projects.Get(ctx, id); perr == nil && pr != nil {
						return nil, fmt.Errorf(
							"that is a project id, not a board id: it is the project %q. Call get_project with it to list its boards, then use one of those ids",
							pr.Name)
					}
					return nil, fmt.Errorf("no board with that id. Call get_project to list a project's boards")
				}
				return nil, err
			}
			// The answer to "can I build this" is the shortfalls and the
			// unmatched lines. The pick entries are the walk list, one row per
			// stock lot, and sending all of them pushed a local model past its
			// context window on a board with a few dozen lines. Capped, and the
			// cap is stated rather than left to look like the whole list.
			out := map[string]any{
				"board":       list.BoardName,
				"quantity":    list.Quantity,
				"buildable":   len(list.Shortfalls) == 0 && len(list.Unmatched) == 0,
				"shortfalls":  list.Shortfalls,
				"unmatched":   list.Unmatched,
				"total_units": list.TotalUnits,
				"lines":       len(list.Entries),
			}
			const maxEntries = 20
			if len(list.Entries) <= maxEntries {
				out["entries"] = list.Entries
			} else {
				out["entries"] = list.Entries[:maxEntries]
				out["entries_note"] = fmt.Sprintf(
					"showing the first %d of %d pick lines; the shortfalls and unmatched lists above are complete",
					maxEntries, len(list.Entries))
			}
			return out, nil
		},
	}
}

func (t *Toolbox) lookupMPN() Tool {
	return Tool{
		Def: ai.ToolDef{
			Name: "lookup_mpn",
			Description: "Look up a manufacturer part number at the distributors, for a part that may " +
				"not be in the inventory. Returns specifications and supplier pricing. This spends a " +
				"distributor lookup, so search_parts first if the part might already be held. " +
				"Distributor stock levels and lead times are not available.",
			Schema: schema(`{"type":"object","properties":{"mpn":{"type":"string"}},"required":["mpn"]}`),
		},
		Run: func(ctx context.Context, args json.RawMessage) (any, error) {
			in, err := decode[struct {
				MPN string `json:"mpn"`
			}](args)
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(in.MPN) == "" {
				return nil, fmt.Errorf("mpn is required")
			}
			return t.Enrich(ctx, strings.TrimSpace(in.MPN))
		},
	}
}

// ─── The one write ───────────────────────────────────────────────────────────

func (t *Toolbox) addReferencePart() Tool {
	return Tool{
		Def: ai.ToolDef{
			Name: "add_reference_part",
			Description: "Record a part that is NOT owned, so it can be found later. Use this when the " +
				"user asks to note or save a part you have found and do not hold. It adds a catalogue " +
				"entry only: no stock, no location, and it is marked as not stocked. It cannot change " +
				"an existing part. Say that you have done it, so the user knows a row was created.",
			Schema: schema(`{
				"type":"object",
				"properties":{
					"name":{"type":"string","description":"what the part is, e.g. \"TPS54331 buck converter\""},
					"description":{"type":"string"},
					"mpn":{"type":"string","description":"manufacturer part number, if known"},
					"package":{"type":"string","description":"e.g. SOIC-8"},
					"keywords":{"type":"string","description":"space-separated search terms"}
				},
				"required":["name"]
			}`),
		},
		Run: func(ctx context.Context, args json.RawMessage) (any, error) {
			in, err := decode[ReferencePartInput](args)
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(in.Name) == "" {
				return nil, fmt.Errorf("name is required")
			}
			p, err := t.CreateReferencePart(ctx, in)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"id": p.ID, "name": p.Name, "ipn": p.IPN, "reference_only": true,
				"note": "added to the catalogue as a part you do not own; no stock was recorded",
			}, nil
		},
	}
}
