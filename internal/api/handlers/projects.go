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
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
}

// @Summary     List projects
// @Description Return all projects.
// @Tags        projects
// @Security    BearerAuth
// @Produce     json
// @Success     200  {array}   map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Router      /projects  [get]
func (h *Handler) ListProjects(w http.ResponseWriter, r *http.Request) {
	ps, err := h.Projects.List(r.Context())
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not list projects")
		return
	}
	respond.JSON(w, http.StatusOK, ps)
}

// @Summary     Create project
// @Description Create a new project.
// @Tags        projects
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       request  body      map[string]interface{}  true  "request body"
// @Success     201      {object}  map[string]interface{}
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Router      /projects  [post]
func (h *Handler) CreateProject(w http.ResponseWriter, r *http.Request) {
	var req projectRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		respond.Error(w, http.StatusBadRequest, "name is required")
		return
	}
	p, err := h.Projects.Create(r.Context(), strings.TrimSpace(req.Name), strings.TrimSpace(req.Description), req.Tags)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not create project")
		return
	}
	h.Bus.Publish("projects")
	respond.JSON(w, http.StatusCreated, p)
}

// @Summary     Get project
// @Description Return a single project by id.
// @Tags        projects
// @Security    BearerAuth
// @Produce     json
// @Param       id   path      string  true  "identifier"
// @Success     200  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /projects/{id}  [get]
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

// @Summary     Update project
// @Description Update a project's name, description, or tags.
// @Tags        projects
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       id       path      string                  true  "identifier"
// @Param       request  body      map[string]interface{}  true  "request body"
// @Success     200      {object}  map[string]interface{}
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Failure     404      {object}  map[string]interface{}
// @Router      /projects/{id}  [patch]
func (h *Handler) UpdateProject(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	var req projectRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	p, err := h.Projects.Update(r.Context(), id, strings.TrimSpace(req.Name), strings.TrimSpace(req.Description), req.Tags)
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

// @Summary     Delete project
// @Description Delete a project.
// @Tags        projects
// @Security    BearerAuth
// @Produce     json
// @Param       id   path      string  true  "identifier"
// @Success     200  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /projects/{id}  [delete]
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
// @Summary     Create board from upload
// @Description Add a board to a project from an uploaded KiCad file.
// @Tags        projects
// @Security    BearerAuth
// @Accept      multipart/form-data
// @Produce     json
// @Param       id    path      string  true  "identifier"
// @Param       file  formData  file    true  "KiCad file upload"
// @Success     201   {object}  map[string]interface{}
// @Failure     400   {object}  map[string]interface{}
// @Failure     401   {object}  map[string]interface{}
// @Failure     404   {object}  map[string]interface{}
// @Router      /projects/{id}/boards  [post]
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

	// A project zip holding several distinct boards imports one board per
	// sub-project. Ordinary single-board zips (len<=1) fall through to the normal
	// flow below (which keeps panel detection, iBOM, and renders).
	if format == "kicad_zip" {
		if zbs, err := kicad.ParseZipBoards(data); err == nil && len(zbs) > 1 {
			h.createMultiBoard(w, r, projectID, filename, revision, data, zbs, keepPanels, keepRenders, attachIbom)
			return
		}
	}

	// A standalone .kicad_pcb can itself be a panel (uploaded directly rather
	// than detected inside a project zip). Mark it kind=panel with an N-up copies
	// multiplier and store the single-board BOM (deduplicated by reference), so
	// the copies multiplier gives the panel total without double-counting.
	kind, copies := "", 0
	if format == "kicad_pcb" {
		if c, isPanel := kicad.DetectPanelPCB(data); isPanel {
			kind, copies = "panel", c
			if perBoard, err := kicad.ParsePanelBoardBOM(data); err == nil {
				lines = perBoard
			}
		}
	}

	board := &models.Board{
		ProjectID:      projectID,
		Name:           name,
		Revision:       revision,
		SourceFilename: filename,
		SourceFormat:   format,
		Kind:           kind,
		Copies:         copies,
	}
	if err := h.Projects.CreateBoard(r.Context(), board); err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not create board")
		return
	}

	matched := h.matchLines(r, projectID, lines)
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
						// Render the panel's own PCB so its layout tab and tile show
						// the N-up board, not a placeholder.
						h.saveRender(r.Context(), projectID, pb.ID, pb.Name, p.PCB)
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
	h.saveRender(r.Context(), projectID, board.ID, board.Name, pcbBytes)

	full, err := h.Projects.GetBoard(r.Context(), board.ID)
	if err != nil || full == nil {
		respond.Error(w, http.StatusInternalServerError, "board saved but could not reload")
		return
	}
	h.Bus.Publish("projects")
	respond.JSON(w, http.StatusCreated, full)
}

// createMultiBoard imports a project zip that holds several distinct boards,
// creating one board per sub-project (each with its own BOM + render). The root
// board also gets the zip's panels, iBOM, and image renders. Responds with the
// root board.
func (h *Handler) createMultiBoard(
	w http.ResponseWriter, r *http.Request, projectID uuid.UUID, filename, revision string,
	data []byte, zbs []kicad.ZipBoard, keepPanels, keepRenders, attachIbom bool,
) {
	ctx := r.Context()
	var root *models.Board
	var rootMatched []models.BOMLine
	for _, zb := range zbs {
		matched := h.matchLines(r, projectID, zb.Lines)
		b := &models.Board{
			ProjectID: projectID, Name: zb.Name, Revision: revision, SourceFilename: filename,
			SourceFormat: "kicad_zip", Kind: "board", Copies: 1,
		}
		if err := h.Projects.CreateBoard(ctx, b); err != nil {
			continue
		}
		_ = h.Projects.ReplaceBOMLines(ctx, b.ID, matched)
		h.saveRender(ctx, projectID, b.ID, b.Name, zb.PCB)
		if zb.Root {
			root, rootMatched = b, matched
		}
	}
	if root == nil {
		respond.Error(w, http.StatusInternalServerError, "could not import the project")
		return
	}

	// Panels and the zip's iBOM/image renders attach to the root board.
	if keepPanels {
		if panels, err := kicad.DetectPanels(data); err == nil {
			for _, p := range panels {
				pb := &models.Board{
					ProjectID: projectID, Name: p.Name, Revision: revision, SourceFilename: filename,
					SourceFormat: "kicad_panel", Kind: "panel", Copies: p.Copies,
				}
				if err := h.Projects.CreateBoard(ctx, pb); err == nil {
					_ = h.Projects.ReplaceBOMLines(ctx, pb.ID, rootMatched)
					h.saveRender(ctx, projectID, pb.ID, pb.Name, p.PCB)
				}
			}
		}
	}
	if assets, err := kicad.ExtractAssets(data); err == nil {
		for _, a := range assets {
			if (a.Kind == "ibom" && !attachIbom) || (a.Kind == "image" && !keepRenders) {
				continue
			}
			rec := &models.ProjectAsset{ProjectID: projectID, BoardID: &root.ID, Name: a.Name, Kind: a.Kind, Mime: a.Mime}
			_ = h.Projects.CreateAsset(ctx, rec, a.Content)
		}
	}

	full, err := h.Projects.GetBoard(ctx, root.ID)
	if err != nil || full == nil {
		respond.Error(w, http.StatusInternalServerError, "boards saved but could not reload")
		return
	}
	h.Bus.Publish("projects")
	respond.JSON(w, http.StatusCreated, full)
}

type previewPanel struct {
	Name   string `json:"name"`
	Copies int    `json:"copies"`
}

// previewUnmatched is a BOM identity the upload wizard can match before committing.
type previewUnmatched struct {
	Key       string `json:"key"`
	Refs      string `json:"refs"`
	Value     string `json:"value"`
	Footprint string `json:"footprint"`
	MPN       string `json:"mpn"`
}

// SetProjectMatch writes a project match rule (a BOM identity → a part) and
// re-matches every board in the project. Used by the upload wizard to match
// leftover lines before committing.
// @Summary     Set project match
// @Description Write a project match rule and re-match every board in the project.
// @Tags        projects
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       id       path      string                  true  "identifier"
// @Param       request  body      map[string]interface{}  true  "request body"
// @Success     200      {object}  map[string]interface{}
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Failure     404      {object}  map[string]interface{}
// @Router      /projects/{id}/matches  [post]
func (h *Handler) SetProjectMatch(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r)
	if !ok {
		return
	}
	var req struct {
		MatchKey string `json:"match_key"`
		PartID   string `json:"part_id"`
	}
	if !respond.Decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.MatchKey) == "" {
		respond.Error(w, http.StatusBadRequest, "match_key is required")
		return
	}
	pid, err := uuid.Parse(strings.TrimSpace(req.PartID))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "part_id must be a valid id")
		return
	}
	if err := h.Projects.UpsertProjectMatch(r.Context(), projectID, req.MatchKey, pid); err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not save the match")
		return
	}
	h.rematchProject(r.Context(), projectID)
	h.Bus.Publish("projects")
	respond.JSON(w, http.StatusOK, map[string]string{"status": "matched"})
}

// PreviewBoard parses an upload without committing and returns what was detected
// (board name, title-block revision, panels, iBOM, renders) so the upload wizard
// can show a mapping/confirm step before creating anything.
// @Summary     Preview board upload
// @Description Parse an upload without committing and return what was detected.
// @Tags        projects
// @Security    BearerAuth
// @Accept      multipart/form-data
// @Produce     json
// @Param       id    path      string  true  "identifier"
// @Param       file  formData  file    true  "KiCad file upload"
// @Success     200   {object}  map[string]interface{}
// @Failure     400   {object}  map[string]interface{}
// @Failure     401   {object}  map[string]interface{}
// @Failure     404   {object}  map[string]interface{}
// @Router      /projects/{id}/boards/preview  [post]
func (h *Handler) PreviewBoard(w http.ResponseWriter, r *http.Request) {
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
	panelCopies := 0 // >0 when the uploaded board is itself an N-up panel

	switch format {
	case "kicad_pcb":
		if c, isPanel := kicad.DetectPanelPCB(data); isPanel {
			panelCopies = c
		}
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
				switch a.Kind {
				case "ibom":
					ibom = a.Name
				case "image":
					renders = append(renders, a.Name)
				}
			}
		}
	case "kicad_sch":
		revision = kicad.SchematicRevision(data)
	}

	// Match preview: resolve each line against inventory + project rules so the
	// wizard can offer to match the leftovers before committing. Unmatched lines
	// are deduped by match key (only lines that can carry a project rule).
	matched := 0
	unmatched := []previewUnmatched{}
	seen := map[string]bool{}
	for _, l := range lines {
		m := models.BOMLine{Value: l.Value, Footprint: l.Footprint, MPN: l.MPN, SupplierSKU: l.SupplierSKU, IPN: l.IPN}
		if pid, _ := h.resolveMatch(r.Context(), projectID, m); pid != nil {
			matched++
			continue
		}
		key := matchKey(m)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		unmatched = append(unmatched, previewUnmatched{
			Key: key, Refs: strings.Join(l.Refs, ", "), Value: l.Value, Footprint: l.Footprint, MPN: l.MPN,
		})
	}

	respond.JSON(w, http.StatusOK, map[string]any{
		"format":       format,
		"name":         name,
		"revision":     revision,
		"line_count":   len(lines),
		"matched":      matched,
		"unmatched":    unmatched,
		"panels":       panels,
		"panel_copies": panelCopies,
		"ibom":         ibom,
		"renders":      renders,
	})
}

// @Summary     Get board
// @Description Return a single board by id.
// @Tags        boards
// @Security    BearerAuth
// @Produce     json
// @Param       id   path      string  true  "identifier"
// @Success     200  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /boards/{id}  [get]
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

// @Summary     List project assets
// @Description Return all assets attached to a project.
// @Tags        projects
// @Security    BearerAuth
// @Produce     json
// @Param       id   path      string  true  "identifier"
// @Success     200  {array}   map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /projects/{id}/assets  [get]
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
// @Summary     Get asset
// @Description Stream a stored asset's raw bytes with its content type.
// @Tags        projects
// @Security    BearerAuth
// @Produce     octet-stream
// @Param       id   path      string  true  "identifier"
// @Success     200  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /assets/{id}  [get]
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

// @Summary     Delete asset
// @Description Delete a stored asset.
// @Tags        projects
// @Security    BearerAuth
// @Produce     json
// @Param       id   path      string  true  "identifier"
// @Success     200  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /assets/{id}  [delete]
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
	Name     string `json:"name"`
	Revision string `json:"revision"`
	Copies   int    `json:"copies"`
}

// UpdateBoard renames a board or corrects a panel's copy count.
// @Summary     Update board
// @Description Rename a board or correct a panel's copy count.
// @Tags        boards
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       id       path      string                  true  "identifier"
// @Param       request  body      map[string]interface{}  true  "request body"
// @Success     200      {object}  map[string]interface{}
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Failure     404      {object}  map[string]interface{}
// @Router      /boards/{id}  [patch]
func (h *Handler) UpdateBoard(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	var req boardUpdate
	if !respond.Decode(w, r, &req) {
		return
	}
	b, err := h.Projects.UpdateBoard(r.Context(), id, strings.TrimSpace(req.Name), strings.TrimSpace(req.Revision), req.Copies)
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

// @Summary     Delete board
// @Description Delete a board.
// @Tags        boards
// @Security    BearerAuth
// @Produce     json
// @Param       id   path      string  true  "identifier"
// @Success     200  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /boards/{id}  [delete]
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

// matchKey is a BOM line's cross-board identity for project match memory: its
// MPN when present, else value + footprint. Empty when the line has neither.
func matchKey(l models.BOMLine) string {
	if m := strings.TrimSpace(l.MPN); m != "" {
		return "mpn:" + strings.ToLower(m)
	}
	v := strings.TrimSpace(l.Value)
	if v == "" {
		return ""
	}
	return "vf:" + strings.ToLower(v) + "|" + strings.ToLower(strings.TrimSpace(l.Footprint))
}

// resolveMatch resolves a BOM line to an inventory part in priority order:
// FireBin PN → project match rule → MPN → supplier SKU → value+footprint.
// The project rule captures manual choices so they apply across every board in
// the project. Returns (nil, "none") when nothing matches.
func (h *Handler) resolveMatch(ctx context.Context, projectID uuid.UUID, l models.BOMLine) (*uuid.UUID, string) {
	if l.IPN != "" {
		if id, _, found, _ := h.Catalog.FindPartByIPN(ctx, l.IPN); found {
			return &id, "fbpn"
		}
	}
	if key := matchKey(l); key != "" {
		if id, found, _ := h.Projects.ProjectMatch(ctx, projectID, key); found {
			return &id, "project"
		}
	}
	if l.MPN != "" {
		if id, _, found, _ := h.Catalog.FindPartByMPN(ctx, l.MPN); found {
			return &id, "mpn"
		}
	}
	if l.SupplierSKU != "" {
		if id, _, found, _ := h.Catalog.FindPartBySupplierSKU(ctx, l.SupplierSKU); found {
			return &id, "supplier"
		}
	}
	if l.Value != "" {
		if id, _, found, _ := h.Projects.FindPartByValueFootprint(ctx, l.Value, l.Footprint); found {
			return &id, "value_footprint"
		}
	}
	return nil, "none"
}

// matchLines resolves each parsed BOM line to an inventory part.
func (h *Handler) matchLines(r *http.Request, projectID uuid.UUID, lines []kicad.BOMLine) []models.BOMLine {
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
			SupplierSKU:  l.SupplierSKU,
			IPN:          l.IPN,
			Description:  l.Description,
		}
		m.PartID, m.MatchKind = h.resolveMatch(ctx, projectID, m)
		out = append(out, m)
	}
	return out
}

// rematchProject re-resolves every BOM line across a project's boards, so a
// changed project match rule propagates everywhere.
func (h *Handler) rematchProject(ctx context.Context, projectID uuid.UUID) {
	boardIDs, err := h.Projects.ProjectBoardIDs(ctx, projectID)
	if err != nil {
		return
	}
	for _, bid := range boardIDs {
		lines, err := h.Projects.LinesForBoard(ctx, bid)
		if err != nil {
			continue
		}
		for _, l := range lines {
			pid, kind := h.resolveMatch(ctx, projectID, l)
			same := kind == l.MatchKind &&
				((pid == nil && l.PartID == nil) || (pid != nil && l.PartID != nil && *pid == *l.PartID))
			if !same {
				_ = h.Projects.SetLineMatch(ctx, l.ID, pid, kind)
			}
		}
	}
}

// CreateBlankBoard makes an empty board (no upload) to build a BOM by hand.
// @Summary     Create blank board
// @Description Make an empty board to build a BOM by hand.
// @Tags        projects
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       id       path      string                  true  "identifier"
// @Param       request  body      map[string]interface{}  true  "request body"
// @Success     201      {object}  map[string]interface{}
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Failure     404      {object}  map[string]interface{}
// @Router      /projects/{id}/boards/blank  [post]
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

// UploadBoardAsset attaches a file to a board, detecting its kind: an Interactive
// HTML BOM (replaces the board's existing iBOM, so the layout tab uses it over
// the generated render) or an image (added as a render; several are allowed).
// @Summary     Upload board asset
// @Description Attach a file to a board, detecting an interactive BOM or image.
// @Tags        boards
// @Security    BearerAuth
// @Accept      multipart/form-data
// @Produce     json
// @Param       id    path      string  true  "identifier"
// @Param       file  formData  file    true  "file upload"
// @Success     201   {object}  map[string]interface{}
// @Failure     400   {object}  map[string]interface{}
// @Failure     401   {object}  map[string]interface{}
// @Failure     404   {object}  map[string]interface{}
// @Router      /boards/{id}/assets  [post]
func (h *Handler) UploadBoardAsset(w http.ResponseWriter, r *http.Request) {
	boardID, ok := pathUUID(w, r)
	if !ok {
		return
	}
	board, err := h.Projects.GetBoard(r.Context(), boardID)
	if err != nil || board == nil {
		respond.Error(w, http.StatusNotFound, "board not found")
		return
	}
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		respond.Error(w, http.StatusBadRequest, "expected a multipart file upload")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "missing 'file' field")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 64<<20))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "could not read upload")
		return
	}

	name := header.Filename
	if name == "" {
		name = "file"
	}
	rec := &models.ProjectAsset{ProjectID: board.ProjectID, BoardID: &boardID, Name: name}
	switch {
	case kicad.LooksLikeIBOM(data):
		// An iBOM replaces the board's existing one (only one drives the layout).
		if _, err := h.Projects.DeleteBoardAssetsOfKind(r.Context(), boardID, "ibom"); err != nil {
			respond.Error(w, http.StatusInternalServerError, "could not replace the existing iBOM")
			return
		}
		rec.Kind, rec.Mime = "ibom", "text/html"
	case imageMime(name) != "":
		rec.Kind, rec.Mime = "image", imageMime(name)
	default:
		respond.Error(w, http.StatusUnprocessableEntity, "unsupported file — attach an interactive BOM (.html) or an image (.png/.jpg/.svg…)")
		return
	}

	if err := h.Projects.CreateAsset(r.Context(), rec, data); err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not save the file")
		return
	}
	h.Bus.Publish("projects")
	respond.JSON(w, http.StatusCreated, rec)
}

// UploadProjectCover sets a project's cover image from an uploaded image,
// replacing any previous cover.
// @Summary     Upload project cover
// @Description Set a project's cover image from an uploaded image.
// @Tags        projects
// @Security    BearerAuth
// @Accept      multipart/form-data
// @Produce     json
// @Param       id    path      string  true  "identifier"
// @Param       file  formData  file    true  "cover image upload"
// @Success     201   {object}  map[string]interface{}
// @Failure     400   {object}  map[string]interface{}
// @Failure     401   {object}  map[string]interface{}
// @Failure     404   {object}  map[string]interface{}
// @Router      /projects/{id}/cover  [post]
func (h *Handler) UploadProjectCover(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r)
	if !ok {
		return
	}
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		respond.Error(w, http.StatusBadRequest, "expected a multipart file upload")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "missing 'file' field")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 64<<20))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "could not read upload")
		return
	}
	name := header.Filename
	mime := imageMime(name)
	if mime == "" {
		respond.Error(w, http.StatusUnprocessableEntity, "the cover must be an image (.png/.jpg/.svg…)")
		return
	}

	old, _ := h.Projects.CoverImageID(r.Context(), projectID)
	rec := &models.ProjectAsset{ProjectID: projectID, Name: name, Kind: "image", Mime: mime}
	if err := h.Projects.CreateAsset(r.Context(), rec, data); err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not save the cover")
		return
	}
	if err := h.Projects.SetProjectCover(r.Context(), projectID, &rec.ID); err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not set the cover")
		return
	}
	if old != nil {
		_ = h.Projects.DeleteAsset(r.Context(), *old)
	}
	h.Bus.Publish("projects")
	respond.JSON(w, http.StatusCreated, rec)
}

// RemoveProjectCover clears a project's uploaded cover (the card falls back to
// the first board's render).
// @Summary     Remove project cover
// @Description Clear a project's uploaded cover.
// @Tags        projects
// @Security    BearerAuth
// @Produce     json
// @Param       id   path      string  true  "identifier"
// @Success     200  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /projects/{id}/cover  [delete]
func (h *Handler) RemoveProjectCover(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r)
	if !ok {
		return
	}
	old, _ := h.Projects.CoverImageID(r.Context(), projectID)
	_ = h.Projects.SetProjectCover(r.Context(), projectID, nil)
	if old != nil {
		_ = h.Projects.DeleteAsset(r.Context(), *old)
	}
	h.Bus.Publish("projects")
	respond.JSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

// imageMime returns the image MIME type for a filename, or "" if it isn't one.
func imageMime(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".bmp":
		return "image/bmp"
	}
	return ""
}

type bomLineRequest struct {
	Refs         string `json:"refs"`
	Quantity     int    `json:"quantity"`
	Value        string `json:"value"`
	Footprint    string `json:"footprint"`
	MPN          string `json:"mpn"`
	Manufacturer string `json:"manufacturer"`
	SupplierSKU  string `json:"supplier_sku"`
	IPN          string `json:"ipn"`
	Description  string `json:"description"`
	// PartID, when set, pins the line to a specific inventory part (a manual
	// substitution). "" or absent leaves the match to auto-resolution; "none"
	// clears the match back to unmatched.
	PartID *string `json:"part_id"`
}

func (req bomLineRequest) toLine() models.BOMLine {
	return models.BOMLine{
		Refs: strings.TrimSpace(req.Refs), Quantity: req.Quantity, Value: strings.TrimSpace(req.Value),
		Footprint: strings.TrimSpace(req.Footprint), MPN: strings.TrimSpace(req.MPN),
		Manufacturer: strings.TrimSpace(req.Manufacturer), SupplierSKU: strings.TrimSpace(req.SupplierSKU),
		IPN: strings.TrimSpace(req.IPN), Description: strings.TrimSpace(req.Description),
	}
}

// applyMatch resolves a line's match. An explicit part_id override pins the line
// via a project match rule, so the choice applies to every board in the project;
// "none" clears that rule. Returns true when a project rule was written or
// removed, so the caller re-matches the whole project. A line with no match key
// (no MPN/value) falls back to a per-line manual pin.
func (h *Handler) applyMatch(ctx context.Context, projectID uuid.UUID, l *models.BOMLine, override *string) (ruleChanged bool) {
	if override != nil {
		v := strings.TrimSpace(*override)
		key := matchKey(*l)
		if v == "" || v == "none" {
			if key != "" {
				_ = h.Projects.DeleteProjectMatch(ctx, projectID, key)
				ruleChanged = true
			}
			l.PartID, l.MatchKind = h.resolveMatch(ctx, projectID, *l)
			return
		}
		if id, err := uuid.Parse(v); err == nil {
			if key != "" {
				_ = h.Projects.UpsertProjectMatch(ctx, projectID, key, id)
				l.PartID, l.MatchKind = &id, "project"
				ruleChanged = true
			} else {
				l.PartID, l.MatchKind = &id, "manual"
			}
			return
		}
	}
	l.PartID, l.MatchKind = h.resolveMatch(ctx, projectID, *l)
	return
}

// AddBOMLine appends a manually-entered line to a board's BOM (auto-matched).
// @Summary     Add BOM line
// @Description Append a manually-entered line to a board's BOM.
// @Tags        boards
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       id       path      string                  true  "identifier"
// @Param       request  body      map[string]interface{}  true  "request body"
// @Success     201      {object}  map[string]interface{}
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Failure     404      {object}  map[string]interface{}
// @Router      /boards/{id}/lines  [post]
func (h *Handler) AddBOMLine(w http.ResponseWriter, r *http.Request) {
	boardID, ok := pathUUID(w, r)
	if !ok {
		return
	}
	var req bomLineRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	projectID, err := h.Projects.ProjectIDForBoard(r.Context(), boardID)
	if err != nil {
		respond.Error(w, http.StatusNotFound, "board not found")
		return
	}
	l := req.toLine()
	l.BoardID = boardID
	if l.Quantity < 1 {
		l.Quantity = 1
	}
	ruleChanged := h.applyMatch(r.Context(), projectID, &l, req.PartID)
	if err := h.Projects.CreateBOMLine(r.Context(), &l); err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not add line")
		return
	}
	if ruleChanged {
		h.rematchProject(r.Context(), projectID)
	}
	full, _ := h.Projects.GetBOMLine(r.Context(), l.ID)
	h.Bus.Publish("projects")
	respond.JSON(w, http.StatusCreated, full)
}

// UpdateBOMLine edits a BOM line and re-matches it.
// @Summary     Update BOM line
// @Description Edit a BOM line and re-match it.
// @Tags        boards
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       id       path      string                  true  "identifier"
// @Param       request  body      map[string]interface{}  true  "request body"
// @Success     200      {object}  map[string]interface{}
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Failure     404      {object}  map[string]interface{}
// @Router      /lines/{id}  [patch]
func (h *Handler) UpdateBOMLine(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	var req bomLineRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	existing, err := h.Projects.GetBOMLine(r.Context(), id)
	if err != nil || existing == nil {
		respond.Error(w, http.StatusNotFound, "line not found")
		return
	}
	projectID, err := h.Projects.ProjectIDForBoard(r.Context(), existing.BoardID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not resolve project")
		return
	}
	l := req.toLine()
	l.ID = id
	if l.Quantity < 1 {
		l.Quantity = 1
	}
	ruleChanged := h.applyMatch(r.Context(), projectID, &l, req.PartID)
	if _, err := h.Projects.UpdateBOMLine(r.Context(), &l); err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not update line")
		return
	}
	if ruleChanged {
		h.rematchProject(r.Context(), projectID)
	}
	full, _ := h.Projects.GetBOMLine(r.Context(), id)
	h.Bus.Publish("projects")
	respond.JSON(w, http.StatusOK, full)
}

// @Summary     Delete BOM line
// @Description Delete a BOM line.
// @Tags        boards
// @Security    BearerAuth
// @Produce     json
// @Param       id   path      string  true  "identifier"
// @Success     200  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /lines/{id}  [delete]
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
	case ".xlsx":
		l, err := kicad.ParseBOMXLSX(data)
		return l, "bom_xlsx", err
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

// saveRender generates an iBOM-style board render from a .kicad_pcb and stores it
// as the board's 'pcbrender' asset (the layout fallback when there's no iBOM).
// No-op on nil bytes or a parse/marshal failure.
func (h *Handler) saveRender(ctx context.Context, projectID, boardID uuid.UUID, name string, pcbBytes []byte) {
	if pcbBytes == nil {
		return
	}
	pcb, err := kicad.GeneratePcbData(pcbBytes)
	if err != nil {
		return
	}
	js, err := json.Marshal(pcb)
	if err != nil {
		return
	}
	rec := &models.ProjectAsset{ProjectID: projectID, BoardID: &boardID, Name: name + ".pcbrender.json", Kind: "pcbrender", Mime: "application/json"}
	_ = h.Projects.CreateAsset(ctx, rec, js)
}

func boardNameFromFilename(filename string) string {
	base := filepath.Base(filename)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	if base == "" {
		return "Board"
	}
	return base
}
