// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/firelabsca/firebin-api/internal/api/respond"
	"github.com/firelabsca/firebin-api/internal/kicad"
	"github.com/firelabsca/firebin-api/internal/models"
)

type projectRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (h *Handler) ListProjects(w http.ResponseWriter, r *http.Request) {
	ps, err := h.Projects.List(r.Context())
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not list projects")
		return
	}
	respond.JSON(w, http.StatusOK, ps)
}

func (h *Handler) CreateProject(w http.ResponseWriter, r *http.Request) {
	var req projectRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		respond.Error(w, http.StatusBadRequest, "name is required")
		return
	}
	p, err := h.Projects.Create(r.Context(), strings.TrimSpace(req.Name), strings.TrimSpace(req.Description))
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not create project")
		return
	}
	h.Bus.Publish("projects")
	respond.JSON(w, http.StatusCreated, p)
}

func (h *Handler) GetProject(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	p, err := h.Projects.Get(r.Context(), id)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not load project")
		return
	}
	if p == nil {
		respond.Error(w, http.StatusNotFound, "project not found")
		return
	}
	respond.JSON(w, http.StatusOK, p)
}

func (h *Handler) UpdateProject(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	var req projectRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	p, err := h.Projects.Update(r.Context(), id, strings.TrimSpace(req.Name), strings.TrimSpace(req.Description))
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not update project")
		return
	}
	if p == nil {
		respond.Error(w, http.StatusNotFound, "project not found")
		return
	}
	h.Bus.Publish("projects")
	respond.JSON(w, http.StatusOK, p)
}

func (h *Handler) DeleteProject(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	if err := h.Projects.Delete(r.Context(), id); err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not delete project")
		return
	}
	h.Bus.Publish("projects")
	respond.JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ── Boards ───────────────────────────────────────────────────────────────────

// CreateBoard adds a board to a project from an uploaded KiCad file. The file is
// parsed to a BOM (schematic s-expr or BOM CSV), each line matched against
// inventory, and the result stored. The file itself is not retained — only the
// parsed BOM.
func (h *Handler) CreateBoard(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r)
	if !ok {
		return
	}
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		respond.Error(w, http.StatusBadRequest, "expected a multipart file upload")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "missing 'file' field")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 32<<20))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "could not read upload")
		return
	}

	filename := header.Filename
	lines, format, err := parseBOM(filename, data)
	if err != nil {
		respond.Error(w, http.StatusUnprocessableEntity, "could not parse file: "+err.Error())
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = boardNameFromFilename(filename)
	}

	board := &models.Board{
		ProjectID:      projectID,
		Name:           name,
		SourceFilename: filename,
		SourceFormat:   format,
	}
	if err := h.Projects.CreateBoard(r.Context(), board); err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not create board")
		return
	}

	matched := h.matchLines(r, lines)
	if err := h.Projects.ReplaceBOMLines(r.Context(), board.ID, matched); err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not store BOM")
		return
	}

	full, err := h.Projects.GetBoard(r.Context(), board.ID)
	if err != nil || full == nil {
		respond.Error(w, http.StatusInternalServerError, "board saved but could not reload")
		return
	}
	h.Bus.Publish("projects")
	respond.JSON(w, http.StatusCreated, full)
}

func (h *Handler) GetBoard(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	b, err := h.Projects.GetBoard(r.Context(), id)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not load board")
		return
	}
	if b == nil {
		respond.Error(w, http.StatusNotFound, "board not found")
		return
	}
	respond.JSON(w, http.StatusOK, b)
}

func (h *Handler) DeleteBoard(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	if err := h.Projects.DeleteBoard(r.Context(), id); err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not delete board")
		return
	}
	h.Bus.Publish("projects")
	respond.JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// matchLines resolves each parsed BOM line to an inventory part: by MPN first,
// then by value + footprint. Unmatched lines are flagged for manual mapping.
func (h *Handler) matchLines(r *http.Request, lines []kicad.BOMLine) []models.BOMLine {
	ctx := r.Context()
	out := make([]models.BOMLine, 0, len(lines))
	for _, l := range lines {
		m := models.BOMLine{
			Refs:         strings.Join(l.Refs, ", "),
			Quantity:     l.Quantity,
			Value:        l.Value,
			Footprint:    l.Footprint,
			MPN:          l.MPN,
			Manufacturer: l.Manufacturer,
			Description:  l.Description,
			MatchKind:    "none",
		}
		if l.MPN != "" {
			if id, _, found, _ := h.Catalog.FindPartByMPN(ctx, l.MPN); found {
				m.PartID = &id
				m.MatchKind = "mpn"
			}
		}
		if m.PartID == nil && l.Value != "" {
			if id, _, found, _ := h.Projects.FindPartByValueFootprint(ctx, l.Value, l.Footprint); found {
				m.PartID = &id
				m.MatchKind = "value_footprint"
			}
		}
		out = append(out, m)
	}
	return out
}

// parseBOM dispatches on file extension, then falls back to sniffing content.
func parseBOM(filename string, data []byte) ([]kicad.BOMLine, string, error) {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".kicad_sch":
		l, err := kicad.ParseSchematic(data)
		return l, "kicad_sch", err
	case ".csv", ".tsv":
		l, err := kicad.ParseBOMCSV(data)
		return l, "bom_csv", err
	}
	// Unknown extension: sniff. A schematic starts with "(kicad_sch".
	head := data
	if len(head) > 64 {
		head = head[:64]
	}
	if strings.Contains(string(head), "kicad_sch") {
		l, err := kicad.ParseSchematic(data)
		return l, "kicad_sch", err
	}
	l, err := kicad.ParseBOMCSV(data)
	return l, "bom_csv", err
}

func boardNameFromFilename(filename string) string {
	base := filepath.Base(filename)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	if base == "" {
		return "Board"
	}
	return base
}
