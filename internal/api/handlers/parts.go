// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/firelabsca/firebin-api/internal/api/respond"
	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/firelabsca/firebin-api/internal/repository"
	"github.com/google/uuid"
)

type partParameterInput struct {
	Name  string  `json:"name"`
	Units *string `json:"units"`
	Value string  `json:"value"`
}

type partRequest struct {
	CategoryID        *uuid.UUID           `json:"category_id"`
	VariantOf         *uuid.UUID           `json:"variant_of"`
	Name              string               `json:"name"`
	Description       *string              `json:"description"`
	IPN               *string              `json:"ipn"`
	Package           *string              `json:"package"`
	KicadSymbol       *string              `json:"kicad_symbol"`
	KicadFootprint    *string              `json:"kicad_footprint"`
	Keywords          *string              `json:"keywords"`
	Barcode           *string              `json:"barcode"`
	ImagePath         *string              `json:"image_path"`
	IsTemplate        bool                 `json:"is_template"`
	IsAssembly        bool                 `json:"is_assembly"`
	ReferenceOnly     bool                 `json:"reference_only"`
	MinimumStock      float64              `json:"minimum_stock"`
	DefaultLocationID *uuid.UUID           `json:"default_location_id"`
	Parameters        []partParameterInput `json:"parameters"`
}

// ListParts lists parts. Query params: category, search, top_level (default
// true so the catalog view shows templates + standalone parts, not every
// variant flattened).
// @Summary     List parts
// @Description List parts, optionally filtered by category, search text, and top-level flag.
// @Tags        parts
// @Security    BearerAuth
// @Produce     json
// @Param       category   query     string                  false  "Category id filter"
// @Param       search     query     string                  false  "Search text"
// @Param       top_level  query     string                  false  "Set to false to include all variants"
// @Success     200  {array}   models.Part
// @Failure     401  {object}  map[string]interface{}
// @Router      /parts  [get]
func (h *Handler) ListParts(w http.ResponseWriter, r *http.Request) {
	opts := repository.ListOptions{TopLevel: r.URL.Query().Get("top_level") != "false"}
	opts.Search = r.URL.Query().Get("search")
	if c := r.URL.Query().Get("category"); c != "" {
		id, err := uuid.Parse(c)
		if err != nil {
			respond.Error(w, http.StatusBadRequest, "invalid category id")
			return
		}
		opts.CategoryID = &id
	}
	parts, err := h.Parts.List(r.Context(), opts)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not list parts")
		return
	}
	respond.JSON(w, http.StatusOK, parts)
}

// @Summary     Get part
// @Description Get a part with its manufacturer parts and cached alternates.
// @Tags        parts
// @Security    BearerAuth
// @Produce     json
// @Param       id   path      string                  true  "Part id"
// @Success     200  {object}  models.Part
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /parts/{id}  [get]
func (h *Handler) GetPart(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	p, err := h.Parts.Get(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "part not found")
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not load part")
		return
	}
	// Attach the commercial tree (MPNs → supplier SKUs → price breaks).
	if mps, err := h.Catalog.ListManufacturerParts(r.Context(), id); err == nil {
		p.ManufacturerParts = mps
	}
	// Attach alternates from cached enrichment of this part's MPNs, linked to
	// inventory where we already stock them. Cache-only — no provider query.
	p.Alternatives = h.resolveAlternatives(r, p.ManufacturerParts)
	respond.JSON(w, http.StatusOK, p)
}

// @Summary     Create part
// @Description Create a part with optional parameters.
// @Tags        parts
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       request  body      map[string]interface{}  true  "Part fields"
// @Success     201  {object}  models.Part
// @Failure     400  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Router      /parts  [post]
func (h *Handler) CreatePart(w http.ResponseWriter, r *http.Request) {
	var req partRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		respond.Error(w, http.StatusBadRequest, "name is required")
		return
	}
	p := partFromRequest(&req)
	if err := h.Parts.Create(r.Context(), p); err != nil {
		if errors.Is(err, repository.ErrConflict) {
			respond.Error(w, http.StatusConflict, "that internal part number is already in use")
			return
		}
		respond.Error(w, http.StatusInternalServerError, "could not create part")
		return
	}
	if err := h.applyParameters(r, p.ID, req.Parameters); err != nil {
		respond.Error(w, http.StatusInternalServerError, "part created but parameters failed")
		return
	}
	h.Bus.Publish("parts")
	full, _ := h.Parts.Get(r.Context(), p.ID)
	respond.JSON(w, http.StatusCreated, full)
}

// @Summary     Update part
// @Description Update a part and its parameters.
// @Tags        parts
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       id       path      string                  true  "Part id"
// @Param       request  body      map[string]interface{}  true  "Part fields"
// @Success     200  {object}  models.Part
// @Failure     400  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /parts/{id}  [patch]
func (h *Handler) UpdatePart(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	// Read the body once, then read it twice: strictly into the typed request,
	// and loosely into a key set. The key set is the only way to tell a field the
	// client omitted from one it sent as null, and PATCH has to treat those
	// differently — keep versus clear.
	raw, err := io.ReadAll(io.LimitReader(r.Body, respond.DefaultMaxBody))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "could not read request body")
		return
	}

	var req partRequest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	var present map[string]json.RawMessage
	if err := json.Unmarshal(raw, &present); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Start from what is stored, so anything the caller did not mention survives.
	p, err := h.Parts.Get(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "part not found")
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not load part")
		return
	}
	applyPartPatch(p, &req, present)
	p.ID = id
	err = h.Parts.Update(r.Context(), p)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "part not found")
		return
	}
	if errors.Is(err, repository.ErrConflict) {
		respond.Error(w, http.StatusConflict, "that internal part number is already in use")
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not update part")
		return
	}
	if err := h.applyParameters(r, id, req.Parameters); err != nil {
		respond.Error(w, http.StatusInternalServerError, "part updated but parameters failed")
		return
	}
	h.Bus.Publish("parts")
	full, _ := h.Parts.Get(r.Context(), id)
	respond.JSON(w, http.StatusOK, full)
}

// @Summary     Delete part
// @Description Delete a part by id.
// @Tags        parts
// @Security    BearerAuth
// @Produce     json
// @Param       id   path      string                  true  "Part id"
// @Success     200  {object}  map[string]string
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /parts/{id}  [delete]
func (h *Handler) DeletePart(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	err := h.Parts.Delete(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "part not found")
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not delete part")
		return
	}
	h.Bus.Publish("parts")
	respond.JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// UploadPartImage stores a custom image for a part (replacing any existing) and
// points parts.image_path at the serving endpoint. Bundled symbols don't come
// through here — they're just a "/symbols/<name>.svg" path set via UpdatePart.
// @Summary     Upload part image
// @Description Store a custom image for a part.
// @Tags        parts
// @Security    BearerAuth
// @Accept      multipart/form-data
// @Produce     json
// @Param       id    path      string                  true  "Part id"
// @Param       file  formData  file                    true  "Image file (.png/.jpg/.svg/.webp/.gif)"
// @Success     200  {object}  models.Part
// @Failure     400  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /parts/{id}/image  [post]
func (h *Handler) UploadPartImage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
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
	mime := imageMime(header.Filename)
	if mime == "" {
		respond.Error(w, http.StatusUnprocessableEntity, "the image must be a .png/.jpg/.svg/.webp/.gif")
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, 16<<20))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "could not read upload")
		return
	}
	if err := h.Parts.SetPartImage(r.Context(), id, mime, data); err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not save the image")
		return
	}
	h.Bus.Publish("parts")
	full, _ := h.Parts.Get(r.Context(), id)
	respond.JSON(w, http.StatusOK, full)
}

// GetPartImage serves a part's uploaded custom image. Public (no auth) so it can
// be used directly as an <img src>, like the static /symbols/*.svg files.
// @Summary     Get part image
// @Description Serve a part's uploaded custom image.
// @Tags        parts
// @Produce     png
// @Param       id   path      string                  true  "Part id"
// @Success     200  {file}    binary
// @Failure     404  {object}  map[string]interface{}
// @Router      /parts/{id}/image  [get]
func (h *Handler) GetPartImage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	mime, content, found, err := h.Parts.GetPartImage(r.Context(), id)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not load image")
		return
	}
	if !found {
		respond.Error(w, http.StatusNotFound, "no image")
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

// normNilString trims a pointer string and returns nil when it's empty, so an
// omitted/blank IPN stores as NULL (the unique index ignores NULLs).
func normNilString(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}

// partFromRequest builds a new part. Create only: every column is set, and the
// flags nothing exposes take their defaults.
func partFromRequest(req *partRequest) *models.Part {
	return &models.Part{
		CategoryID:        req.CategoryID,
		VariantOf:         req.VariantOf,
		Name:              strings.TrimSpace(req.Name),
		Description:       req.Description,
		IPN:               normNilString(req.IPN),
		Package:           req.Package,
		KicadSymbol:       normNilString(req.KicadSymbol),
		KicadFootprint:    normNilString(req.KicadFootprint),
		Keywords:          req.Keywords,
		Barcode:           req.Barcode,
		ImagePath:         normNilString(req.ImagePath),
		IsTemplate:        req.IsTemplate,
		IsComponent:       true,
		IsAssembly:        req.IsAssembly,
		IsPurchaseable:    true,
		ReferenceOnly:     req.ReferenceOnly,
		MinimumStock:      req.MinimumStock,
		DefaultLocationID: req.DefaultLocationID,
	}
}

// applyPartPatch updates only the fields the caller actually sent.
//
// PATCH used to build a whole part from the request and write every column, so
// any field a client did not know about was overwritten with its zero value.
// That was not theoretical: editing a part in the web form erased its keywords
// and barcode and quietly demoted a template, because the form does not send
// those three, and every MCP update_part erased the KiCad symbol and footprint,
// because that client's request struct predates the columns. Each client
// destroyed whatever the other cared about.
//
// `present` carries the keys that appeared in the request body, which is what
// separates "the client left this out" from "the client sent null to clear it".
// Pointer fields alone cannot tell those apart: both arrive as nil. Absent means
// keep, null means clear.
func applyPartPatch(cur *models.Part, req *partRequest, present map[string]json.RawMessage) {
	has := func(k string) bool { _, ok := present[k]; return ok }

	if has("name") {
		cur.Name = strings.TrimSpace(req.Name)
	}
	if has("category_id") {
		cur.CategoryID = req.CategoryID
	}
	if has("variant_of") {
		cur.VariantOf = req.VariantOf
	}
	if has("description") {
		cur.Description = req.Description
	}
	if has("ipn") {
		cur.IPN = normNilString(req.IPN)
	}
	if has("package") {
		cur.Package = req.Package
	}
	if has("kicad_symbol") {
		cur.KicadSymbol = normNilString(req.KicadSymbol)
	}
	if has("kicad_footprint") {
		cur.KicadFootprint = normNilString(req.KicadFootprint)
	}
	if has("keywords") {
		cur.Keywords = req.Keywords
	}
	if has("barcode") {
		cur.Barcode = req.Barcode
	}
	if has("image_path") {
		cur.ImagePath = normNilString(req.ImagePath)
	}
	if has("is_template") {
		cur.IsTemplate = req.IsTemplate
	}
	if has("is_assembly") {
		cur.IsAssembly = req.IsAssembly
	}
	if has("reference_only") {
		cur.ReferenceOnly = req.ReferenceOnly
	}
	if has("minimum_stock") {
		cur.MinimumStock = req.MinimumStock
	}
	if has("default_location_id") {
		cur.DefaultLocationID = req.DefaultLocationID
	}

	// is_component, is_purchaseable and is_trackable are deliberately absent.
	// No request exposes them, and the old code hardcoded the first two to true
	// and left the third at false on every write, so they were reset on every
	// edit. Carrying `cur` through means whatever is in the database survives.
}

// resolveAlternatives pulls similar-part suggestions from the cached enrichment
// of this part's MPNs and links each to an inventory part when we stock it.
func (h *Handler) resolveAlternatives(r *http.Request, mps []models.ManufacturerPart) []models.PartAlternative {
	ctx := r.Context()
	own := map[string]bool{}
	for _, mp := range mps {
		own[strings.ToLower(mp.MPN)] = true
	}
	seen := map[string]bool{}
	out := []models.PartAlternative{}
	for _, mp := range mps {
		enr, ok, err := h.EnrichCache.Get(ctx, mp.MPN)
		if err != nil || !ok || enr == nil {
			continue
		}
		for _, alt := range enr.Alternatives {
			key := strings.ToLower(alt.MPN)
			if alt.MPN == "" || own[key] || seen[key] {
				continue
			}
			seen[key] = true
			pa := models.PartAlternative{MPN: alt.MPN, Manufacturer: alt.Manufacturer, Description: alt.Description}
			if pid, name, found, e := h.Catalog.FindPartByMPN(ctx, alt.MPN); e == nil && found {
				pa.PartID = &pid
				pa.PartName = &name
			}
			out = append(out, pa)
		}
	}
	return out
}

// ListParameterTemplates returns known parameter names for the client's
// name-typeahead (so users reuse names instead of coining misspellings).
// @Summary     List parameter templates
// @Description Return known parameter names for the client typeahead.
// @Tags        parts
// @Security    BearerAuth
// @Produce     json
// @Success     200  {array}   models.ParameterTemplate
// @Failure     401  {object}  map[string]interface{}
// @Router      /parameter-templates  [get]
func (h *Handler) ListParameterTemplates(w http.ResponseWriter, r *http.Request) {
	t, err := h.Parts.ListParameterTemplates(r.Context())
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not list parameter templates")
		return
	}
	respond.JSON(w, http.StatusOK, t)
}

func (h *Handler) applyParameters(r *http.Request, partID uuid.UUID, params []partParameterInput) error {
	for _, p := range params {
		if strings.TrimSpace(p.Name) == "" {
			continue
		}
		if err := h.Parts.SetParameter(r.Context(), partID, strings.TrimSpace(p.Name), p.Units, p.Value); err != nil {
			return err
		}
	}
	return nil
}
