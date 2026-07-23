// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/firelabsca/firebin-api/internal/api/respond"
	"github.com/firelabsca/firebin-api/internal/kicad"
	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/google/uuid"
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
	// KiCad project zips bundle STEP/render assets we don't need but must still
	// read past to reach the zip's central directory, so allow a generous cap.
	data, err := io.ReadAll(io.LimitReader(file, 256<<20))
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
	// Mapping fields (set by the upload wizard); default to keeping everything.
	revision := strings.TrimSpace(r.FormValue("revision"))
	keepPanels := r.FormValue("keep_panels") != "false"
	keepRenders := r.FormValue("keep_renders") != "false"
	attachIbom := r.FormValue("attach_ibom") != "false"

	board := &models.Board{
		ProjectID:      projectID,
		Name:           name,
		Revision:       revision,
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

	if format == "kicad_zip" {
		// A panel is the same board arrayed N-up (PCB only, no schematic). Add
		// it as its own board sharing the per-board BOM with a copies multiplier.
		if keepPanels {
			if panels, err := kicad.DetectPanels(data); err == nil {
				for _, p := range panels {
					pb := &models.Board{
						ProjectID: projectID, Name: p.Name, Revision: revision, SourceFilename: filename,
						SourceFormat: "kicad_panel", Kind: "panel", Copies: p.Copies,
					}
					if err := h.Projects.CreateBoard(r.Context(), pb); err == nil {
						_ = h.Projects.ReplaceBOMLines(r.Context(), pb.ID, matched)
					}
				}
			}
		}
		// Pull renderable files (iBOM, image renders) out of the zip.
		if assets, err := kicad.ExtractAssets(data); err == nil {
			for _, a := range assets {
				if a.Kind == "ibom" && !attachIbom {
					continue
				}
				if a.Kind == "image" && !keepRenders {
					continue
				}
				rec := &models.ProjectAsset{ProjectID: projectID, BoardID: &board.ID, Name: a.Name, Kind: a.Kind, Mime: a.Mime}
				_ = h.Projects.CreateAsset(r.Context(), rec, a.Content)
			}
		}
	}

	// Generate a board render from the .kicad_pcb (fallback for the board-layout
	// tab when there's no interactive BOM). Uses the zip's root PCB, or the
	// uploaded .kicad_pcb directly.
	var pcbBytes []byte
	switch format {
	case "kicad_zip":
		pcbBytes = kicad.RootPCB(data)
	case "kicad_pcb":
		pcbBytes = data
	}
	if pcbBytes != nil {
		if pcb, err := kicad.GeneratePcbData(pcbBytes); err == nil {
			if js, err := json.Marshal(pcb); err == nil {
				rec := &models.ProjectAsset{ProjectID: projectID, BoardID: &board.ID, Name: board.Name + ".pcbrender.json", Kind: "pcbrender", Mime: "application/json"}
				_ = h.Projects.CreateAsset(r.Context(), rec, js)
			}
		}
	}

	full, err := h.Projects.GetBoard(r.Context(), board.ID)
	if err != nil || full == nil {
		respond.Error(w, http.StatusInternalServerError, "board saved but could not reload")
		return
	}
	h.Bus.Publish("projects")
	respond.JSON(w, http.StatusCreated, full)
}

type previewPanel struct {
	Name   string `json:"name"`
	Copies int    `json:"copies"`
}

// PreviewBoard parses an upload without committing and returns what was detected
// (board name, title-block revision, panels, iBOM, renders) so the upload wizard
// can show a mapping/confirm step before creating anything.
func (h *Handler) PreviewBoard(w http.ResponseWriter, r *http.Request) {
	if _, ok := pathUUID(w, r); !ok {
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
	data, err := io.ReadAll(io.LimitReader(file, 256<<20))
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

	name := boardNameFromFilename(filename)
	revision := ""
	panels := []previewPanel{}
	renders := []string{}
	ibom := ""

	switch format {
	case "kicad_zip":
		if n, rev := kicad.ProjectInfo(data); n != "" {
			name = n
			revision = rev
		}
		if ps, err := kicad.DetectPanels(data); err == nil {
			for _, p := range ps {
				panels = append(panels, previewPanel{Name: p.Name, Copies: p.Copies})
			}
		}
		if assets, err := kicad.ExtractAssets(data); err == nil {
			for _, a := range assets {
				if a.Kind == "ibom" {
					ibom = a.Name
				} else if a.Kind == "image" {
					renders = append(renders, a.Name)
				}
			}
		}
	case "kicad_sch":
		revision = kicad.SchematicRevision(data)
	}

	respond.JSON(w, http.StatusOK, map[string]any{
		"format":     format,
		"name":       name,
		"revision":   revision,
		"line_count": len(lines),
		"panels":     panels,
		"ibom":       ibom,
		"renders":    renders,
	})
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

// ── Assets ───────────────────────────────────────────────────────────────────

func (h *Handler) ListProjectAssets(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	assets, err := h.Projects.ListAssets(r.Context(), id)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not list assets")
		return
	}
	respond.JSON(w, http.StatusOK, assets)
}

// GetAsset streams a stored asset's raw bytes with its content type, so the web
// client can render it (iBOM in an iframe, images inline).
func (h *Handler) GetAsset(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	mime, _, content, found, err := h.Projects.GetAssetContent(r.Context(), id)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not load asset")
		return
	}
	if !found {
		respond.Error(w, http.StatusNotFound, "asset not found")
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

func (h *Handler) DeleteAsset(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	if err := h.Projects.DeleteAsset(r.Context(), id); err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not delete asset")
		return
	}
	h.Bus.Publish("projects")
	respond.JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

type boardUpdate struct {
	Name   string `json:"name"`
	Copies int    `json:"copies"`
}

// UpdateBoard renames a board or corrects a panel's copy count.
func (h *Handler) UpdateBoard(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	var req boardUpdate
	if !respond.Decode(w, r, &req) {
		return
	}
	b, err := h.Projects.UpdateBoard(r.Context(), id, strings.TrimSpace(req.Name), req.Copies)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not update board")
		return
	}
	if b == nil {
		respond.Error(w, http.StatusNotFound, "board not found")
		return
	}
	h.Bus.Publish("projects")
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

// resolveMatch resolves a BOM line to an inventory part: by MPN first, then by
// value + footprint. Returns (nil, "none") when nothing matches.
func (h *Handler) resolveMatch(ctx context.Context, mpn, value, footprint string) (*uuid.UUID, string) {
	if mpn != "" {
		if id, _, found, _ := h.Catalog.FindPartByMPN(ctx, mpn); found {
			return &id, "mpn"
		}
	}
	if value != "" {
		if id, _, found, _ := h.Projects.FindPartByValueFootprint(ctx, value, footprint); found {
			return &id, "value_footprint"
		}
	}
	return nil, "none"
}

// matchLines resolves each parsed BOM line to an inventory part.
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
		}
		m.PartID, m.MatchKind = h.resolveMatch(ctx, l.MPN, l.Value, l.Footprint)
		out = append(out, m)
	}
	return out
}

// CreateBlankBoard makes an empty board (no upload) to build a BOM by hand.
func (h *Handler) CreateBlankBoard(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r)
	if !ok {
		return
	}
	var req struct {
		Name     string `json:"name"`
		Revision string `json:"revision"`
	}
	if !respond.Decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		respond.Error(w, http.StatusBadRequest, "name is required")
		return
	}
	board := &models.Board{
		ProjectID: projectID, Name: strings.TrimSpace(req.Name), Revision: strings.TrimSpace(req.Revision),
		SourceFormat: "manual", Kind: "board", Copies: 1,
	}
	if err := h.Projects.CreateBoard(r.Context(), board); err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not create board")
		return
	}
	h.Bus.Publish("projects")
	respond.JSON(w, http.StatusCreated, board)
}

type bomLineRequest struct {
	Refs         string `json:"refs"`
	Quantity     int    `json:"quantity"`
	Value        string `json:"value"`
	Footprint    string `json:"footprint"`
	MPN          string `json:"mpn"`
	Manufacturer string `json:"manufacturer"`
	Description  string `json:"description"`
}

func (req bomLineRequest) toLine() models.BOMLine {
	return models.BOMLine{
		Refs: strings.TrimSpace(req.Refs), Quantity: req.Quantity, Value: strings.TrimSpace(req.Value),
		Footprint: strings.TrimSpace(req.Footprint), MPN: strings.TrimSpace(req.MPN),
		Manufacturer: strings.TrimSpace(req.Manufacturer), Description: strings.TrimSpace(req.Description),
	}
}

// AddBOMLine appends a manually-entered line to a board's BOM (auto-matched).
func (h *Handler) AddBOMLine(w http.ResponseWriter, r *http.Request) {
	boardID, ok := pathUUID(w, r)
	if !ok {
		return
	}
	var req bomLineRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	l := req.toLine()
	l.BoardID = boardID
	if l.Quantity < 1 {
		l.Quantity = 1
	}
	l.PartID, l.MatchKind = h.resolveMatch(r.Context(), l.MPN, l.Value, l.Footprint)
	if err := h.Projects.CreateBOMLine(r.Context(), &l); err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not add line")
		return
	}
	full, _ := h.Projects.GetBOMLine(r.Context(), l.ID)
	h.Bus.Publish("projects")
	respond.JSON(w, http.StatusCreated, full)
}

// UpdateBOMLine edits a BOM line and re-matches it.
func (h *Handler) UpdateBOMLine(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	var req bomLineRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	l := req.toLine()
	l.ID = id
	if l.Quantity < 1 {
		l.Quantity = 1
	}
	l.PartID, l.MatchKind = h.resolveMatch(r.Context(), l.MPN, l.Value, l.Footprint)
	boardID, err := h.Projects.UpdateBOMLine(r.Context(), &l)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not update line")
		return
	}
	if boardID == uuid.Nil {
		respond.Error(w, http.StatusNotFound, "line not found")
		return
	}
	full, _ := h.Projects.GetBOMLine(r.Context(), id)
	h.Bus.Publish("projects")
	respond.JSON(w, http.StatusOK, full)
}

func (h *Handler) DeleteBOMLine(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	if _, err := h.Projects.DeleteBOMLine(r.Context(), id); err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not delete line")
		return
	}
	h.Bus.Publish("projects")
	respond.JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// parseBOM dispatches on file extension, then falls back to sniffing content.
func parseBOM(filename string, data []byte) ([]kicad.BOMLine, string, error) {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".zip":
		l, err := kicad.ParseZip(data)
		return l, "kicad_zip", err
	case ".kicad_sch":
		l, err := kicad.ParseSchematic(data)
		return l, "kicad_sch", err
	case ".kicad_pcb":
		l, err := kicad.ParsePCB(data)
		return l, "kicad_pcb", err
	case ".csv", ".tsv":
		l, err := kicad.ParseBOMCSV(data)
		return l, "bom_csv", err
	case ".html", ".htm":
		return nil, "", errors.New("a standalone interactive BOM (.html) isn't a BOM source — upload the zipped KiCad project so it attaches to the board")
	}
	// Unknown extension: sniff by magic/content.
	if kicad.IsZip(data) {
		l, err := kicad.ParseZip(data)
		return l, "kicad_zip", err
	}
	head := data
	if len(head) > 64 {
		head = head[:64]
	}
	if strings.Contains(string(head), "(kicad_sch") {
		l, err := kicad.ParseSchematic(data)
		return l, "kicad_sch", err
	}
	if strings.Contains(string(head), "(kicad_pcb") {
		l, err := kicad.ParsePCB(data)
		return l, "kicad_pcb", err
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
