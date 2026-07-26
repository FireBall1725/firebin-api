// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/firelabsca/firebin-api/internal/api/middleware"
	"github.com/firelabsca/firebin-api/internal/api/respond"
	"github.com/firelabsca/firebin-api/internal/labels"
	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/firelabsca/firebin-api/internal/repository"
	"github.com/google/uuid"
)

// renderTemplate turns a saved template's elements into a concrete label for one
// part, resolving each field binding to a value and keeping literal text where no
// field is bound.
func renderTemplate(tmpl []labels.Element, p *models.Part, deepLink string) labels.Label {
	els := make([]labels.Element, len(tmpl))
	for i, e := range tmpl {
		if e.Field != "" && e.Field != "text" {
			e.Value = resolveLabelValue(e, deepLink, p)
		}
		els[i] = e
	}
	return labels.Label{Elements: els}
}

type previewLabelRequest struct {
	MediaID  uuid.UUID       `json:"media_id"`
	PartID   uuid.UUID       `json:"part_id"`
	Elements json.RawMessage `json:"elements"`
}

// resolveOne loads the media + part and turns the given elements (or the built-in
// part layout when none are given) into a concrete label with every field binding
// resolved to a value. It returns an HTTP status + message on failure (status 0
// means success), so callers can render to PDF or hand the elements back as JSON
// without duplicating the field-resolution logic.
func (h *Handler) resolveOne(ctx context.Context, mediaID, partID uuid.UUID, rawEls json.RawMessage) (models.LabelMedia, labels.Label, int, string) {
	media, err := h.LabelMedia.Get(ctx, mediaID)
	if errors.Is(err, repository.ErrNotFound) {
		return media, labels.Label{}, http.StatusNotFound, "label media not found"
	}
	if err != nil {
		return media, labels.Label{}, http.StatusInternalServerError, "could not load label media"
	}
	p, err := h.Parts.Get(ctx, partID)
	if err != nil {
		return media, labels.Label{}, http.StatusNotFound, "part not found"
	}
	var els []labels.Element
	if len(rawEls) > 0 {
		if err := json.Unmarshal(rawEls, &els); err != nil {
			return media, labels.Label{}, http.StatusBadRequest, "invalid elements"
		}
	}
	ipn := ""
	if p.IPN != nil {
		ipn = *p.IPN
	}
	code := ipn
	if code == "" {
		code = p.ID.String()
	}
	link := "firebin://p/" + code

	var lb labels.Label
	if len(els) > 0 {
		lb = renderTemplate(els, p, link)
	} else {
		lb = labels.BuildPartLabel(media, p.Name, ipn, link)
	}
	return media, lb, 0, ""
}

// PreviewLabel renders the given (possibly unsaved) template elements for one
// part as a single-label PDF sized to the label — the designer's live preview.
func (h *Handler) PreviewLabel(w http.ResponseWriter, r *http.Request) {
	var req previewLabelRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	media, lb, status, msg := h.resolveOne(r.Context(), req.MediaID, req.PartID, req.Elements)
	if status != 0 {
		respond.Error(w, status, msg)
		return
	}
	pdf, err := labels.RenderLabel(media, lb)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not render preview: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="firebin-label-preview.pdf"`)
	_, _ = w.Write(pdf)
}

type resolvedLabelResponse struct {
	LabelW   float64          `json:"label_w"`
	LabelH   float64          `json:"label_h"`
	Kind     string           `json:"kind"`
	Code     string           `json:"code"`
	Elements []labels.Element `json:"elements"`
}

// ResolveLabel resolves a template's field bindings for one part and returns the
// concrete elements (values filled) plus the media geometry as JSON. The web
// client renders these to a canvas for WebUSB tape printing, so field resolution
// stays authoritative on the server and is never duplicated in the browser.
func (h *Handler) ResolveLabel(w http.ResponseWriter, r *http.Request) {
	var req previewLabelRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	media, lb, status, msg := h.resolveOne(r.Context(), req.MediaID, req.PartID, req.Elements)
	if status != 0 {
		respond.Error(w, status, msg)
		return
	}
	respond.JSON(w, http.StatusOK, resolvedLabelResponse{
		LabelW:   media.LabelW,
		LabelH:   media.LabelH,
		Kind:     media.Kind,
		Code:     media.Code,
		Elements: lb.Elements,
	})
}

// ── Label templates (drag-and-drop builder) ─────────────────────────────────

type labelTemplateRequest struct {
	Name     string          `json:"name"`
	MediaID  *uuid.UUID      `json:"label_media_id"`
	Elements json.RawMessage `json:"elements"`
}

// ListLabelTemplates returns the saved builder templates.
func (h *Handler) ListLabelTemplates(w http.ResponseWriter, r *http.Request) {
	ts, err := h.LabelTemplates.List(r.Context())
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not list templates")
		return
	}
	respond.JSON(w, http.StatusOK, ts)
}

// CreateLabelTemplate saves a new template.
func (h *Handler) CreateLabelTemplate(w http.ResponseWriter, r *http.Request) {
	var req labelTemplateRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		respond.Error(w, http.StatusBadRequest, "name is required")
		return
	}
	uid := middleware.UserID(r.Context())
	t := &models.LabelTemplate{Name: strings.TrimSpace(req.Name), MediaID: req.MediaID, Elements: req.Elements, CreatedBy: &uid}
	if err := h.LabelTemplates.Create(r.Context(), t); err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not save template")
		return
	}
	respond.JSON(w, http.StatusCreated, t)
}

// UpdateLabelTemplate replaces a template's name, size, and elements.
func (h *Handler) UpdateLabelTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req labelTemplateRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	t, err := h.LabelTemplates.Update(r.Context(), id, strings.TrimSpace(req.Name), req.MediaID, req.Elements)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "template not found")
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not update template")
		return
	}
	respond.JSON(w, http.StatusOK, t)
}

// DeleteLabelTemplate removes a template.
func (h *Handler) DeleteLabelTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.LabelTemplates.Delete(r.Context(), id); errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "template not found")
		return
	} else if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not delete template")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ListLabelMedia returns the user's curated list of label sheets (the ones they
// print on), built-ins first.
func (h *Handler) ListLabelMedia(w http.ResponseWriter, r *http.Request) {
	m, err := h.LabelMedia.List(r.Context())
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not list label media")
		return
	}
	respond.JSON(w, http.StatusOK, m)
}

// SearchLabelCatalog searches the bundled catalogue of known label products
// (hundreds of Avery sizes) so the user can add the ones they use.
func (h *Handler) SearchLabelCatalog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	limit := 60
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 500 {
		limit = n
	}
	respond.JSON(w, http.StatusOK, labels.SearchCatalog(q, limit))
}

// CreateLabelMedia adds a sheet to the user's list, either imported from the
// catalogue or a custom size. The body carries the full geometry (in points).
func (h *Handler) CreateLabelMedia(w http.ResponseWriter, r *http.Request) {
	var m models.LabelMedia
	if !respond.Decode(w, r, &m) {
		return
	}
	m.Brand = strings.TrimSpace(m.Brand)
	m.Code = strings.TrimSpace(m.Code)
	m.Name = strings.TrimSpace(m.Name)
	if m.Brand == "" {
		m.Brand = "Custom"
	}
	if m.Code == "" || m.Cols < 1 || m.Rows < 1 || m.PageW <= 0 || m.PageH <= 0 || m.LabelW <= 0 || m.LabelH <= 0 {
		respond.Error(w, http.StatusBadRequest, "code, page size, label size, and a 1+ column/row grid are required")
		return
	}
	created, err := h.LabelMedia.Create(r.Context(), m)
	if errors.Is(err, repository.ErrConflict) {
		respond.Error(w, http.StatusConflict, "that label ("+m.Brand+" "+m.Code+") is already in your list")
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not add label media")
		return
	}
	respond.JSON(w, http.StatusCreated, created)
}

// DeleteLabelMedia removes a sheet from the user's list.
func (h *Handler) DeleteLabelMedia(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	err := h.LabelMedia.Delete(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not delete")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

type printLabelsRequest struct {
	MediaID    uuid.UUID   `json:"media_id"`
	Template   string      `json:"template"`              // "part" — the built-in
	TemplateID *uuid.UUID  `json:"template_id,omitempty"` // a saved builder template; overrides Template
	PartIDs    []uuid.UUID `json:"part_ids"`
	Copies     int         `json:"copies"`     // per part; default 1
	UsedCells  []int       `json:"used_cells"` // cells already peeled off the first sheet
}

func strOr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// resolveLabelValue fills a template element's value from its field binding for a
// given part. An empty (or "text") field keeps the element's literal value.
func resolveLabelValue(e labels.Element, deepLink string, p *models.Part) string {
	switch e.Field {
	case "", "text":
		return "" // caller keeps the literal Value
	case "param":
		// Reference a part parameter by name; missing → blank (never an error).
		name := strings.TrimSpace(e.ParamName)
		for _, pp := range p.Parameters {
			if strings.EqualFold(pp.TemplateName, name) {
				return pp.Value
			}
		}
		return ""
	case "name":
		return p.Name
	case "ipn":
		return strOr(p.IPN)
	case "package":
		return strOr(p.Package)
	case "mpn":
		return p.PrimaryMPN
	case "manufacturer":
		return p.PrimaryManufacturer
	case "location":
		return strOr(p.PrimaryLocation)
	case "description":
		return strOr(p.Description)
	case "barcode":
		return strOr(p.Barcode)
	case "quantity":
		return strconv.FormatFloat(p.TotalStock, 'f', -1, 64)
	case "qr", "link", "deeplink":
		return deepLink
	default:
		return ""
	}
}

// PrintLabels renders a PDF of labels for the given parts onto the chosen media,
// skipping any cells already used on the first sheet. Responds with the PDF
// bytes (Content-Type application/pdf).
func (h *Handler) PrintLabels(w http.ResponseWriter, r *http.Request) {
	var req printLabelsRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	media, err := h.LabelMedia.Get(r.Context(), req.MediaID)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "label media not found")
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not load label media")
		return
	}
	if len(req.PartIDs) == 0 {
		respond.Error(w, http.StatusBadRequest, "part_ids is required")
		return
	}
	copies := req.Copies
	if copies < 1 {
		copies = 1
	}

	// A saved builder template (if given) overrides the built-in part layout.
	var tmpl []labels.Element
	if req.TemplateID != nil {
		t, err := h.LabelTemplates.Get(r.Context(), *req.TemplateID)
		if errors.Is(err, repository.ErrNotFound) {
			respond.Error(w, http.StatusNotFound, "label template not found")
			return
		}
		if err != nil {
			respond.Error(w, http.StatusInternalServerError, "could not load label template")
			return
		}
		if err := json.Unmarshal(t.Elements, &tmpl); err != nil {
			respond.Error(w, http.StatusInternalServerError, "label template is corrupt")
			return
		}
	}

	var items []labels.Label
	for _, pid := range req.PartIDs {
		p, err := h.Parts.Get(r.Context(), pid)
		if err != nil {
			continue // skip a missing/deleted part rather than fail the whole sheet
		}
		ipn := ""
		if p.IPN != nil {
			ipn = *p.IPN
		}
		// Deep link, not a web URL: scanning opens the FireBin app straight to the
		// part. Use the product code (IPN) when present so it's human-meaningful
		// and app-resolvable, falling back to the id for parts without an IPN.
		code := ipn
		if code == "" {
			code = p.ID.String()
		}
		link := "firebin://p/" + code

		var lb labels.Label
		if tmpl != nil {
			lb = renderTemplate(tmpl, p, link)
		} else {
			lb = labels.BuildPartLabel(media, p.Name, ipn, link)
		}
		for i := 0; i < copies; i++ {
			items = append(items, lb)
		}
	}
	if len(items) == 0 {
		respond.Error(w, http.StatusBadRequest, "no printable parts found")
		return
	}

	used := make(map[int]bool, len(req.UsedCells))
	for _, c := range req.UsedCells {
		used[c] = true
	}

	// Cut guides are a property of the paper (generic full-page stock needs them;
	// pre-cut Avery sheets do not), not a per-print choice.
	pdf, err := labels.RenderSheet(media, items, used, media.CutGuides)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not render labels: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="firebin-labels.pdf"`)
	_, _ = w.Write(pdf)
}
