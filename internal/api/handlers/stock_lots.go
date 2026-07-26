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

// stockDeepLink is the scan target for a stock lot (a mini spool): firebin://s/<code>,
// where code is the lot's barcode when set, else its id.
func stockDeepLink(s *models.StockItem) string {
	code := ""
	if s.Barcode != nil {
		code = strings.TrimSpace(*s.Barcode)
	}
	if code == "" {
		code = s.ID.String()
	}
	return "firebin://s/" + code
}

func qtyStr(q float64) string { return strconv.FormatFloat(q, 'f', -1, 64) }

// GetStockLot returns one lot by id.
// @Summary     Get stock lot
// @Description Return one stock lot by id.
// @Tags        stock-lots
// @Security    BearerAuth
// @Produce     json
// @Param       id   path      string                  true   "identifier"
// @Success     200  {object}  models.StockItem
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /stock-items/{id}  [get]
func (h *Handler) GetStockLot(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	s, err := h.Stock.GetStockItem(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "stock lot not found")
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not load lot")
		return
	}
	respond.JSON(w, http.StatusOK, s)
}

// ScanStockLot resolves a lot from its barcode (?barcode=).
// @Summary     Scan stock lot
// @Description Resolve a stock lot from its barcode.
// @Tags        stock-lots
// @Security    BearerAuth
// @Produce     json
// @Param       barcode  query     string                  true   "barcode"
// @Success     200      {object}  models.StockItem
// @Failure     401      {object}  map[string]interface{}
// @Router      /stock/scan  [get]
func (h *Handler) ScanStockLot(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("barcode"))
	if code == "" {
		respond.Error(w, http.StatusBadRequest, "barcode query param is required")
		return
	}
	s, err := h.Stock.GetByBarcode(r.Context(), code)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "no lot with that barcode")
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not resolve barcode")
		return
	}
	respond.JSON(w, http.StatusOK, s)
}

type splitStockRequest struct {
	SourceID     uuid.UUID  `json:"source_id"`
	Quantity     float64    `json:"quantity"`
	ToLocationID *uuid.UUID `json:"to_location_id"`
	Name         *string    `json:"name"`
	Barcode      *string    `json:"barcode"`
}

// SplitStock cuts a quantity off a lot into a new barcoded lot (a mini spool).
// @Summary     Split stock lot
// @Description Cut a quantity off a lot into a new barcoded lot.
// @Tags        stock-lots
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     201      {object}  models.StockItem
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Router      /stock/split  [post]
func (h *Handler) SplitStock(w http.ResponseWriter, r *http.Request) {
	var req splitStockRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	uid := middleware.UserID(r.Context())
	lot, err := h.Stock.SplitLot(r.Context(), repository.SplitParams{
		SourceID: req.SourceID, Quantity: req.Quantity, ToLocationID: req.ToLocationID,
		Name: req.Name, Barcode: req.Barcode, UserID: &uid,
	})
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "source lot not found")
		return
	}
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	respond.JSON(w, http.StatusCreated, lot)
}

type mergeStockRequest struct {
	SourceID uuid.UUID `json:"source_id"`
	TargetID uuid.UUID `json:"target_id"`
}

// MergeStock pours one lot into another (same part) and deletes the source.
// @Summary     Merge stock lots
// @Description Pour one lot into another and delete the source.
// @Tags        stock-lots
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     200      {object}  map[string]string
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Router      /stock/merge  [post]
func (h *Handler) MergeStock(w http.ResponseWriter, r *http.Request) {
	var req mergeStockRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	uid := middleware.UserID(r.Context())
	if err := h.Stock.MergeLot(r.Context(), req.SourceID, req.TargetID, &uid); errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "lot not found")
		return
	} else if err != nil {
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"status": "merged"})
}

type relocateStockRequest struct {
	StockItemID  uuid.UUID  `json:"stock_item_id"`
	ToLocationID *uuid.UUID `json:"to_location_id"`
}

// RelocateStock moves a whole lot to a location (keeps its identity).
// @Summary     Relocate stock lot
// @Description Move a whole lot to another location.
// @Tags        stock-lots
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     200      {object}  models.StockItem
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Router      /stock/relocate  [post]
func (h *Handler) RelocateStock(w http.ResponseWriter, r *http.Request) {
	var req relocateStockRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	uid := middleware.UserID(r.Context())
	lot, err := h.Stock.RelocateLot(r.Context(), req.StockItemID, req.ToLocationID, &uid)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "lot not found")
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not relocate lot")
		return
	}
	respond.JSON(w, http.StatusOK, lot)
}

type lotAdjustRequest struct {
	StockItemID uuid.UUID `json:"stock_item_id"`
	Kind        string    `json:"kind"` // add | remove | count
	Quantity    float64   `json:"quantity"`
}

// AdjustStockLot changes one specific lot's quantity (lot-precise).
// @Summary     Adjust stock lot
// @Description Change one lot's quantity by add, remove, or count.
// @Tags        stock-lots
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     200      {object}  models.StockItem
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Router      /stock/lot-adjust  [post]
func (h *Handler) AdjustStockLot(w http.ResponseWriter, r *http.Request) {
	var req lotAdjustRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	uid := middleware.UserID(r.Context())
	lot, err := h.Stock.AdjustLot(r.Context(), req.StockItemID, req.Kind, req.Quantity, &uid)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "lot not found")
		return
	}
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	respond.JSON(w, http.StatusOK, lot)
}

// ── Lot labels (firebin://s/…) ───────────────────────────────────────────────

func resolveStockValue(e labels.Element, deepLink string, s *models.StockItem) string {
	switch e.Field {
	case "", "text":
		return ""
	case "name":
		return s.PartName
	case "quantity":
		return qtyStr(s.Quantity)
	case "location":
		return strOr(s.LocationName)
	case "barcode":
		return strOr(s.Barcode)
	case "qr", "link", "deeplink":
		return deepLink
	default:
		return ""
	}
}

func renderStockTemplate(tmpl []labels.Element, s *models.StockItem, deepLink string) labels.Label {
	els := make([]labels.Element, len(tmpl))
	for i, e := range tmpl {
		if e.Field != "" && e.Field != "text" {
			e.Value = resolveStockValue(e, deepLink, s)
		}
		els[i] = e
	}
	return labels.Label{Elements: els}
}

func (h *Handler) resolveOneStock(ctx context.Context, mediaID, lotID uuid.UUID, rawEls json.RawMessage) (models.LabelMedia, labels.Label, int, string) {
	media, err := h.LabelMedia.Get(ctx, mediaID)
	if errors.Is(err, repository.ErrNotFound) {
		return media, labels.Label{}, http.StatusNotFound, "label media not found"
	}
	if err != nil {
		return media, labels.Label{}, http.StatusInternalServerError, "could not load label media"
	}
	s, err := h.Stock.GetStockItem(ctx, lotID)
	if err != nil {
		return media, labels.Label{}, http.StatusNotFound, "stock lot not found"
	}
	var els []labels.Element
	if len(rawEls) > 0 {
		if err := json.Unmarshal(rawEls, &els); err != nil {
			return media, labels.Label{}, http.StatusBadRequest, "invalid elements"
		}
	}
	link := stockDeepLink(s)
	var lb labels.Label
	if len(els) > 0 {
		lb = renderStockTemplate(els, s, link)
	} else {
		sub := "Qty " + qtyStr(s.Quantity)
		if s.Name != nil && *s.Name != "" {
			sub = *s.Name + " · " + sub
		}
		lb = labels.BuildStockLabel(media, s.PartName, sub, link)
	}
	return media, lb, 0, ""
}

type resolveStockLabelRequest struct {
	MediaID     uuid.UUID       `json:"media_id"`
	StockItemID uuid.UUID       `json:"stock_item_id"`
	Elements    json.RawMessage `json:"elements"`
}

// ResolveStockLabel returns a lot's resolved elements + media geometry (tape/WebUSB).
// @Summary     Resolve stock label
// @Description Return a stock lot's resolved label elements and media geometry.
// @Tags        stock-lots
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     200      {object}  map[string]interface{}
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Router      /stock/labels/resolve  [post]
func (h *Handler) ResolveStockLabel(w http.ResponseWriter, r *http.Request) {
	var req resolveStockLabelRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	media, lb, status, msg := h.resolveOneStock(r.Context(), req.MediaID, req.StockItemID, req.Elements)
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

type printStockLabelsRequest struct {
	MediaID      uuid.UUID   `json:"media_id"`
	TemplateID   *uuid.UUID  `json:"template_id,omitempty"`
	StockItemIDs []uuid.UUID `json:"stock_item_ids"`
	Copies       int         `json:"copies"`
	UsedCells    []int       `json:"used_cells"`
}

// PrintStockLabels renders a PDF of lot labels — the lot twin of PrintLabels.
// @Summary     Print stock labels
// @Description Render a PDF of stock lot labels.
// @Tags        stock-lots
// @Security    BearerAuth
// @Accept      json
// @Produce     application/pdf
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     200      {file}    binary
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Router      /stock/labels/print  [post]
func (h *Handler) PrintStockLabels(w http.ResponseWriter, r *http.Request) {
	var req printStockLabelsRequest
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
	if len(req.StockItemIDs) == 0 {
		respond.Error(w, http.StatusBadRequest, "stock_item_ids is required")
		return
	}
	copies := req.Copies
	if copies < 1 {
		copies = 1
	}

	var tmpl []labels.Element
	if req.TemplateID != nil {
		t, err := h.LabelTemplates.Get(r.Context(), *req.TemplateID)
		if err != nil {
			respond.Error(w, http.StatusNotFound, "label template not found")
			return
		}
		if err := json.Unmarshal(t.Elements, &tmpl); err != nil {
			respond.Error(w, http.StatusInternalServerError, "label template is corrupt")
			return
		}
	}

	var items []labels.Label
	for _, sid := range req.StockItemIDs {
		s, err := h.Stock.GetStockItem(r.Context(), sid)
		if err != nil {
			continue
		}
		link := stockDeepLink(s)
		var lb labels.Label
		if tmpl != nil {
			lb = renderStockTemplate(tmpl, s, link)
		} else {
			sub := "Qty " + qtyStr(s.Quantity)
			if s.Name != nil && *s.Name != "" {
				sub = *s.Name + " · " + sub
			}
			lb = labels.BuildStockLabel(media, s.PartName, sub, link)
		}
		for i := 0; i < copies; i++ {
			items = append(items, lb)
		}
	}
	if len(items) == 0 {
		respond.Error(w, http.StatusBadRequest, "no printable lots found")
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
	w.Header().Set("Content-Disposition", `inline; filename="firebin-lot-labels.pdf"`)
	_, _ = w.Write(pdf)
}
