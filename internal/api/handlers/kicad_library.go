// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/firelabsca/firebin-api/internal/api/respond"
	"github.com/firelabsca/firebin-api/internal/kicad"
	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/firelabsca/firebin-api/internal/repository"
	"github.com/google/uuid"
)

// maxScanBatchBytes bounds one upload chunk, not the scan. A full scan is
// hundreds of MB of source across tens of thousands of items; the indexer
// splits it by byte budget and stays well under this.
const maxScanBatchBytes = 64 << 20

func splitLibID(libID string) (lib, name string, ok bool) {
	lib, name, ok = strings.Cut(libID, ":")
	return lib, name, ok && lib != "" && name != ""
}

func validKind(k string) bool { return k == "symbol" || k == "footprint" }

// SearchKicadLibrary powers typeahead in the part editor.
// @Summary     Search KiCad library items
// @Description Search indexed KiCad symbols or footprints by "Lib:Name" substring.
// @Tags        kicad
// @Security    BearerAuth
// @Produce     json
// @Param       kind  query     string  true   "symbol or footprint"
// @Param       q     query     string  false  "Search text; all terms must match"
// @Success     200  {array}   models.KicadLibraryItem
// @Failure     400  {object}  map[string]interface{}
// @Router      /kicad/libraries/search  [get]
func (h *Handler) SearchKicadLibrary(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	if !validKind(kind) {
		respond.Error(w, http.StatusBadRequest, "kind must be symbol or footprint")
		return
	}
	items, err := h.KicadLib.Search(r.Context(), kind, r.URL.Query().Get("q"))
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not search libraries")
		return
	}
	respond.JSON(w, http.StatusOK, items)
}

// ListKicadLibraries lists indexed libraries with their counts.
// @Summary     List KiCad libraries
// @Description List indexed KiCad libraries and how many items each holds.
// @Tags        kicad
// @Security    BearerAuth
// @Produce     json
// @Param       kind  query     string  false  "Filter to symbol or footprint"
// @Success     200  {array}   models.KicadLibrarySummary
// @Router      /kicad/libraries  [get]
func (h *Handler) ListKicadLibraries(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	if kind != "" && !validKind(kind) {
		respond.Error(w, http.StatusBadRequest, "kind must be symbol or footprint")
		return
	}
	libs, err := h.KicadLib.Libraries(r.Context(), kind)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not list libraries")
		return
	}
	respond.JSON(w, http.StatusOK, libs)
}

// ListKicadLibraryItems lists one library's contents.
// @Summary     List items in a KiCad library
// @Tags        kicad
// @Security    BearerAuth
// @Produce     json
// @Param       kind  query     string  true  "symbol or footprint"
// @Param       lib   query     string  true  "Library nickname"
// @Success     200  {array}   models.KicadLibraryItem
// @Failure     400  {object}  map[string]interface{}
// @Router      /kicad/libraries/items  [get]
func (h *Handler) ListKicadLibraryItems(w http.ResponseWriter, r *http.Request) {
	kind, lib := r.URL.Query().Get("kind"), r.URL.Query().Get("lib")
	if !validKind(kind) || lib == "" {
		respond.Error(w, http.StatusBadRequest, "kind and lib are required")
		return
	}
	items, err := h.KicadLib.Items(r.Context(), kind, lib)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not list items")
		return
	}
	respond.JSON(w, http.StatusOK, items)
}

// GetKicadDrawing returns render data for one library item.
//
// The parse happens on first request and the result is cached on the row, so a
// symbol is parsed once per upload rather than once per page view. A parse
// failure is a 422 rather than a 500: some libraries carry items this renderer
// cannot draw, and that is a property of the data, not a server fault.
// @Summary     Get KiCad item drawing
// @Description Render data for one symbol or footprint, parsed from the uploaded source.
// @Tags        kicad
// @Security    BearerAuth
// @Produce     json
// @Param       kind    query     string  true  "symbol or footprint"
// @Param       lib_id  query     string  true  "Library ID, e.g. Device:R"
// @Success     200  {object}  kicad.LibDrawing
// @Failure     404  {object}  map[string]interface{}
// @Failure     422  {object}  map[string]interface{}
// @Router      /kicad/libraries/drawing  [get]
func (h *Handler) GetKicadDrawing(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	lib, name, ok := splitLibID(r.URL.Query().Get("lib_id"))
	if !validKind(kind) || !ok {
		respond.Error(w, http.StatusBadRequest, `kind and a "Lib:Name" lib_id are required`)
		return
	}
	ctx := r.Context()

	raw, err := h.drawingJSON(ctx, kind, lib, name)
	if err != nil {
		if errors.Is(err, errNoSource) {
			respond.Error(w, http.StatusNotFound, "no uploaded source for that item; re-run the library indexer")
			return
		}
		respond.Error(w, http.StatusUnprocessableEntity, "could not render: "+err.Error())
		return
	}
	respond.Raw(w, http.StatusOK, raw)
}

var errNoSource = errors.New("no uploaded source")

// drawingJSON returns an item's render data, parsing and caching it on first
// use so a symbol is parsed once per upload rather than once per page view.
func (h *Handler) drawingJSON(ctx context.Context, kind, lib, name string) ([]byte, error) {
	if cached, err := h.KicadLib.Drawing(ctx, kind, lib, name); err == nil && len(cached) > 0 {
		return cached, nil
	}
	src, err := h.KicadLib.Source(ctx, kind, lib, name)
	if err != nil {
		return nil, errNoSource
	}

	var drawing *kicad.LibDrawing
	if kind == "symbol" {
		src = h.resolveSymbolExtends(ctx, lib, src)
		drawing, err = kicad.RenderSymbol(src)
	} else {
		drawing, err = kicad.RenderFootprint(src)
	}
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(drawing)
	if err != nil {
		return nil, err
	}
	// Best-effort cache; a failure here costs a re-parse, not a wrong answer.
	_ = h.KicadLib.SaveDrawing(ctx, kind, lib, name, raw)
	return raw, nil
}

// drawingOf decodes what drawingJSON returns.
func (h *Handler) drawingOf(ctx context.Context, kind, libID string) (*kicad.LibDrawing, bool) {
	lib, name, ok := splitLibID(libID)
	if !ok {
		return nil, false
	}
	raw, err := h.drawingJSON(ctx, kind, lib, name)
	if err != nil {
		return nil, false
	}
	var d kicad.LibDrawing
	if json.Unmarshal(raw, &d) != nil {
		return nil, false
	}
	return &d, true
}

// resolveSymbolExtends walks a derived symbol up to the ancestor that actually
// holds the graphics, staying within the same library as KiCad does.
//
// A missing ancestor is not an error: the caller renders what it has and the
// symbol comes out empty, which is a better failure than a 500 on a library
// that was only partially scanned.
func (h *Handler) resolveSymbolExtends(ctx context.Context, lib string, src []byte) []byte {
	const maxDepth = 8 // guards a library with a circular extends chain
	for range maxDepth {
		base, err := kicad.SymbolExtends(src)
		if err != nil || base == "" {
			return src
		}
		next, err := h.KicadLib.Source(ctx, "symbol", lib, base)
		if err != nil {
			return src
		}
		src = next
	}
	return src
}

// GetKicadIndexMeta reports what the current index came from.
// @Summary     KiCad index status
// @Description Provenance and counts for the current KiCad library index.
// @Tags        kicad
// @Security    BearerAuth
// @Produce     json
// @Success     200  {object}  models.KicadIndexMeta
// @Router      /kicad/libraries/status  [get]
func (h *Handler) GetKicadIndexMeta(w http.ResponseWriter, r *http.Request) {
	meta, err := h.KicadLib.Meta(r.Context())
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not read index status")
		return
	}
	if meta == nil {
		respond.JSON(w, http.StatusOK, map[string]any{"scanned": false})
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"scanned": true, "meta": meta})
}

type scanBatchRequest struct {
	ScanID uuid.UUID                   `json:"scan_id"`
	Items  []models.KicadLibraryUpload `json:"items"`
	// Overwrite replaces a symbol or footprint already stored under the same
	// library and name. Off by default: importing a folder is not a reason to
	// replace something already curated here.
	Overwrite bool `json:"overwrite"`
}

// UploadKicadLibraryBatch stores one chunk of a scan.
// @Summary     Upload a KiCad library batch
// @Description Store one chunk of a library scan. Repeat, then call finish.
// @Tags        kicad
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       body  body      object  true  "scan_id and items"
// @Success     200  {object}  map[string]interface{}
// @Failure     400  {object}  map[string]interface{}
// @Router      /kicad/libraries/batch  [post]
func (h *Handler) UploadKicadLibraryBatch(w http.ResponseWriter, r *http.Request) {
	var req scanBatchRequest
	if !respond.DecodeMax(w, r, &req, maxScanBatchBytes) {
		return
	}
	if req.ScanID == uuid.Nil {
		respond.Error(w, http.StatusBadRequest, "scan_id is required")
		return
	}
	for _, it := range req.Items {
		if !validKind(it.Kind) || it.Lib == "" || it.Name == "" {
			respond.Error(w, http.StatusBadRequest, "each item needs a valid kind, lib and name")
			return
		}
	}
	stored, skipped, err := h.KicadLib.UpsertBatch(r.Context(), req.ScanID, req.Items, req.Overwrite)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not store batch: "+err.Error())
		return
	}
	// skipped is reported rather than folded into stored: "imported 4" when
	// three of them already existed and were left alone is a lie by omission.
	respond.JSON(w, http.StatusOK, map[string]any{"stored": stored, "skipped": skipped})
}

type scanFinishRequest struct {
	ScanID       uuid.UUID `json:"scan_id"`
	Source       string    `json:"source"`
	KicadVersion string    `json:"kicad_version"`
}

// FinishKicadLibraryScan commits an uploaded scan.
// @Summary     Finish a KiCad library scan
// @Description Record provenance for a finished scan. Importing never deletes: an item already in the index is kept unless the batch asked to overwrite it.
// @Tags        kicad
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       body  body      object  true  "scan_id, source, kicad_version"
// @Success     200  {object}  models.KicadIndexMeta
// @Failure     400  {object}  map[string]interface{}
// @Router      /kicad/libraries/finish  [post]
func (h *Handler) FinishKicadLibraryScan(w http.ResponseWriter, r *http.Request) {
	var req scanFinishRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	if req.ScanID == uuid.Nil {
		respond.Error(w, http.StatusBadRequest, "scan_id is required")
		return
	}
	if strings.TrimSpace(req.Source) == "" {
		respond.Error(w, http.StatusBadRequest, "source is required (the machine this scan came from)")
		return
	}
	meta, err := h.KicadLib.FinishScan(r.Context(), req.ScanID, req.Source, req.KicadVersion)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not finish scan: "+err.Error())
		return
	}
	respond.JSON(w, http.StatusOK, meta)
}

// SuggestKicadForPart proposes symbol and footprint mappings for one part.
//
// Suggestions only: nothing is written. The endpoint reports where each
// candidate came from so the choice stays with the person who knows the part.
// @Summary     Suggest KiCad mappings for a part
// @Description Propose symbol and footprint candidates from past BOMs, MPN name matches, and package/category rules.
// @Tags        kicad
// @Security    BearerAuth
// @Produce     json
// @Param       id   path      string  true  "Part id"
// @Success     200  {object}  models.KicadSuggestions
// @Failure     404  {object}  map[string]interface{}
// @Router      /parts/{id}/kicad/suggestions  [get]
func (h *Handler) SuggestKicadForPart(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid part id")
		return
	}
	out, err := h.Parts.SuggestKicad(r.Context(), id)
	if err != nil {
		respond.Error(w, http.StatusNotFound, "no such part")
		return
	}
	h.dropTerminalCountMismatches(r.Context(), out)
	respond.JSON(w, http.StatusOK, out)
}

// dropTerminalCountMismatches removes category-derived symbol suggestions that
// the evidence contradicts.
//
// A category rule can only ever assert "this is a two-terminal device", and
// FireBin's categories are not that precise: SP0503BAHTG is filed under "Zener
// Diodes" but is a TVS array in SOT-143, four pads. Pairing a 2-pin symbol with
// it would produce a board that looks right on screen and is wired wrong, which
// is the failure worth spending code to prevent.
//
// Only a footprint the user actually shipped is trusted as the contradicting
// evidence. Judging one guess by another guess would just compound them.
func (h *Handler) dropTerminalCountMismatches(ctx context.Context, s *models.KicadSuggestions) {
	var pads int
	var from string
	for _, f := range s.Footprints {
		if f.Source != "bom" {
			continue
		}
		if d, ok := h.drawingOf(ctx, "footprint", f.LibID); ok && d.ElectricalPads > 0 {
			pads, from = d.ElectricalPads, f.LibID
			break
		}
	}
	if pads <= 2 {
		return
	}

	kept := s.Symbols[:0]
	for _, sym := range s.Symbols {
		if sym.Source != "category" {
			kept = append(kept, sym)
			continue
		}
		// Category rules only ever propose two-terminal passives, so any
		// footprint with more terminals rules them out.
		if d, ok := h.drawingOf(ctx, "symbol", sym.LibID); ok && d.Pins > 0 && d.Pins >= pads {
			kept = append(kept, sym)
			continue
		}
		// Say what was dropped and why. A suggestion that silently disappears
		// reads as "we found nothing", which is a different and wrong message.
		s.Notes = append(s.Notes, fmt.Sprintf(
			"Skipped %s: %s has %d terminals, so a two-terminal symbol would be wrong here.",
			sym.LibID, from, pads))
	}
	s.Symbols = kept
}

// ListKicadUsage lists the parts referencing one library item.
// @Summary     Parts using a KiCad library item
// @Tags        kicad
// @Security    BearerAuth
// @Produce     json
// @Param       kind    query     string  true  "symbol or footprint"
// @Param       lib_id  query     string  true  "Library ID, e.g. Device:R"
// @Success     200  {array}   models.KicadUsage
// @Failure     400  {object}  map[string]interface{}
// @Router      /kicad/libraries/usage  [get]
func (h *Handler) ListKicadUsage(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	libID := r.URL.Query().Get("lib_id")
	if !validKind(kind) || libID == "" {
		respond.Error(w, http.StatusBadRequest, "kind and lib_id are required")
		return
	}
	out, err := h.KicadLib.Usage(r.Context(), kind, libID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not list usage")
		return
	}
	respond.JSON(w, http.StatusOK, out)
}

type renameLibraryRequest struct {
	Kind string `json:"kind"`
	Lib  string `json:"lib"`
	Name string `json:"name"`
}

// RenameKicadLibrary changes a library's name.
//
// The name comes from the file it was imported from, which is often a datestamp
// or the word "footprints", and it is the name KiCad matches on. Admin-only
// because it changes what every workstation resolves against.
// @Summary     Rename a KiCad library
// @Description Move every symbol or footprint from one library name to another. Merging into an existing library is allowed; an item whose name is already taken there stays where it is.
// @Tags        kicad
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       request  body      map[string]interface{}  true  "kind, lib and the new name"
// @Success     200  {object}  map[string]interface{}
// @Failure     400  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Router      /kicad/libraries/rename  [post]
func (h *Handler) RenameKicadLibrary(w http.ResponseWriter, r *http.Request) {
	var req renameLibraryRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	req.Lib = strings.TrimSpace(req.Lib)
	req.Name = strings.TrimSpace(req.Name)
	if !validKind(req.Kind) || req.Lib == "" || req.Name == "" {
		respond.Error(w, http.StatusBadRequest, "kind, lib and name are required")
		return
	}
	if req.Lib == req.Name {
		respond.JSON(w, http.StatusOK, map[string]any{"moved": 0})
		return
	}
	moved, err := h.KicadLib.RenameLibrary(r.Context(), req.Kind, req.Lib, req.Name)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not rename: "+err.Error())
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"moved": moved})
}

// DeleteKicadLibraryItems removes a whole library, or one item from it.
// @Summary     Delete a KiCad library or one of its items
// @Description Remove every symbol or footprint in a library, or a single one when name is given.
// @Tags        kicad
// @Security    BearerAuth
// @Produce     json
// @Param       kind  query     string  true   "symbol or footprint"
// @Param       lib   query     string  true   "Library name"
// @Param       name  query     string  false  "One item; omit to delete the whole library"
// @Success     200  {object}  map[string]interface{}
// @Failure     400  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /kicad/libraries  [delete]
func (h *Handler) DeleteKicadLibraryItems(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kind, lib, name := q.Get("kind"), strings.TrimSpace(q.Get("lib")), strings.TrimSpace(q.Get("name"))
	if !validKind(kind) || lib == "" {
		respond.Error(w, http.StatusBadRequest, "kind and lib are required")
		return
	}
	if name != "" {
		if err := h.KicadLib.DeleteItem(r.Context(), kind, lib, name); err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				respond.Error(w, http.StatusNotFound, "no such item")
				return
			}
			respond.Error(w, http.StatusInternalServerError, "could not delete: "+err.Error())
			return
		}
		respond.JSON(w, http.StatusOK, map[string]any{"deleted": 1})
		return
	}
	n, err := h.KicadLib.DeleteLibrary(r.Context(), kind, lib)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not delete: "+err.Error())
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"deleted": n})
}
