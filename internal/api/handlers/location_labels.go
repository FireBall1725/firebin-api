// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/firelabsca/firebin-api/internal/api/respond"
	"github.com/firelabsca/firebin-api/internal/labels"
	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/firelabsca/firebin-api/internal/repository"
	"github.com/google/uuid"
)

// locationDeepLink is the scan target printed on a location's QR:
// firebin://l/<code>, where code is the location's barcode when set, else its id.
// It mirrors the part deep link (firebin://p/<ipn-or-id>).
func locationDeepLink(l *models.StorageLocation) string {
	code := ""
	if l.Barcode != nil {
		code = strings.TrimSpace(*l.Barcode)
	}
	if code == "" {
		code = l.ID.String()
	}
	return "firebin://l/" + code
}

// resolveLocationValue fills a template element from a location. The same field
// names as parts are honoured where they make sense (name / barcode / description
// / qr); part-only fields (mpn, package, param, …) blank out.
func resolveLocationValue(e labels.Element, deepLink string, l *models.StorageLocation) string {
	switch e.Field {
	case "", "text":
		return ""
	case "name":
		return l.Name
	case "barcode":
		return strOr(l.Barcode)
	case "description":
		return strOr(l.Description)
	case "qr", "link", "deeplink":
		return deepLink
	default:
		return ""
	}
}

func renderLocationTemplate(tmpl []labels.Element, l *models.StorageLocation, deepLink string) labels.Label {
	els := make([]labels.Element, len(tmpl))
	for i, e := range tmpl {
		if e.Field != "" && e.Field != "text" {
			e.Value = resolveLocationValue(e, deepLink, l)
		}
		els[i] = e
	}
	return labels.Label{Elements: els}
}

// resolveOneLocation loads the media + location and builds a concrete label
// (given elements, or the built-in location layout when none). Status 0 = success.
func (h *Handler) resolveOneLocation(ctx context.Context, mediaID, locID uuid.UUID, rawEls json.RawMessage) (models.LabelMedia, labels.Label, int, string) {
	media, err := h.LabelMedia.Get(ctx, mediaID)
	if errors.Is(err, repository.ErrNotFound) {
		return media, labels.Label{}, http.StatusNotFound, "label media not found"
	}
	if err != nil {
		return media, labels.Label{}, http.StatusInternalServerError, "could not load label media"
	}
	l, err := h.Locations.Get(ctx, locID)
	if err != nil {
		return media, labels.Label{}, http.StatusNotFound, "location not found"
	}
	var els []labels.Element
	if len(rawEls) > 0 {
		if err := json.Unmarshal(rawEls, &els); err != nil {
			return media, labels.Label{}, http.StatusBadRequest, "invalid elements"
		}
	}
	link := locationDeepLink(l)
	var lb labels.Label
	if len(els) > 0 {
		lb = renderLocationTemplate(els, l, link)
	} else {
		lb = labels.BuildLocationLabel(media, l.Name, strOr(l.Barcode), link)
	}
	return media, lb, 0, ""
}

type resolveLocationLabelRequest struct {
	MediaID    uuid.UUID       `json:"media_id"`
	LocationID uuid.UUID       `json:"location_id"`
	Elements   json.RawMessage `json:"elements"`
}

// ResolveLocationLabel returns a location's elements (field bindings filled) plus
// media geometry as JSON, for client-side canvas rendering (tape / WebUSB).
// @Summary     Resolve location label
// @Description Return a location's resolved label elements and media geometry.
// @Tags        locations
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     200      {object}  map[string]interface{}
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Router      /locations/labels/resolve  [post]
func (h *Handler) ResolveLocationLabel(w http.ResponseWriter, r *http.Request) {
	var req resolveLocationLabelRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	media, lb, status, msg := h.resolveOneLocation(r.Context(), req.MediaID, req.LocationID, req.Elements)
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

type printLocationLabelsRequest struct {
	MediaID     uuid.UUID   `json:"media_id"`
	TemplateID  *uuid.UUID  `json:"template_id,omitempty"`
	LocationIDs []uuid.UUID `json:"location_ids"`
	Copies      int         `json:"copies"`
	UsedCells   []int       `json:"used_cells"`
}

// PrintLocationLabels renders a PDF of labels for the given locations on the chosen
// media — the location twin of PrintLabels.
// @Summary     Print location labels
// @Description Render a PDF of labels for the given locations.
// @Tags        locations
// @Security    BearerAuth
// @Accept      json
// @Produce     application/pdf
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     200      {object}  map[string]interface{}
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Router      /locations/labels/print  [post]
func (h *Handler) PrintLocationLabels(w http.ResponseWriter, r *http.Request) {
	var req printLocationLabelsRequest
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
	if len(req.LocationIDs) == 0 {
		respond.Error(w, http.StatusBadRequest, "location_ids is required")
		return
	}
	copies := req.Copies
	if copies < 1 {
		copies = 1
	}

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
	for _, lid := range req.LocationIDs {
		l, err := h.Locations.Get(r.Context(), lid)
		if err != nil {
			continue
		}
		link := locationDeepLink(l)
		var lb labels.Label
		if tmpl != nil {
			lb = renderLocationTemplate(tmpl, l, link)
		} else {
			lb = labels.BuildLocationLabel(media, l.Name, strOr(l.Barcode), link)
		}
		for i := 0; i < copies; i++ {
			items = append(items, lb)
		}
	}
	if len(items) == 0 {
		respond.Error(w, http.StatusBadRequest, "no printable locations found")
		return
	}

	used := make(map[int]bool, len(req.UsedCells))
	for _, c := range req.UsedCells {
		used[c] = true
	}

	pdf, err := labels.RenderSheet(media, items, used, media.CutGuides)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not render labels: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="firebin-location-labels.pdf"`)
	_, _ = w.Write(pdf)
}
