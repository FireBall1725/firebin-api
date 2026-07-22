// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"errors"
	"net/http"

	"github.com/firelabsca/firebin-api/internal/api/middleware"
	"github.com/firelabsca/firebin-api/internal/api/respond"
	"github.com/firelabsca/firebin-api/internal/repository"
	"github.com/google/uuid"
)

// ListPartStock returns every stock lot for a part.
func (h *Handler) ListPartStock(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	items, err := h.Stock.ListForPart(r.Context(), id)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not list stock")
		return
	}
	respond.JSON(w, http.StatusOK, items)
}

// ListPartStockHistory returns the recent movement log for a part.
func (h *Handler) ListPartStockHistory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	txns, err := h.Stock.ListTransactions(r.Context(), id, 100)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not list history")
		return
	}
	respond.JSON(w, http.StatusOK, txns)
}

type adjustRequest struct {
	LocationID     *uuid.UUID `json:"location_id"`
	SupplierPartID *uuid.UUID `json:"supplier_part_id"`
	Kind           string     `json:"kind"` // add | remove | count | adjust
	Quantity       float64    `json:"quantity"`
	Note           *string    `json:"note"`
}

// AdjustPartStock applies an add/remove/count/adjust to a part's stock at a
// location, recording the movement.
func (h *Handler) AdjustPartStock(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	var req adjustRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	switch req.Kind {
	case "add", "remove", "count", "adjust":
	default:
		respond.Error(w, http.StatusBadRequest, "kind must be one of add, remove, count, adjust")
		return
	}
	userID := middleware.UserID(r.Context())
	item, err := h.Stock.Adjust(r.Context(), repository.AdjustParams{
		PartID:         id,
		LocationID:     req.LocationID,
		SupplierPartID: req.SupplierPartID,
		Kind:           req.Kind,
		Quantity:       req.Quantity,
		Note:           req.Note,
		UserID:         &userID,
	})
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	h.Bus.Publish("stock")
	h.Bus.Publish("parts")
	respond.JSON(w, http.StatusOK, item)
}

type moveRequest struct {
	StockItemID  uuid.UUID  `json:"stock_item_id"`
	ToLocationID *uuid.UUID `json:"to_location_id"`
	Quantity     float64    `json:"quantity"`
	Note         *string    `json:"note"`
}

// MoveStock transfers quantity between locations.
func (h *Handler) MoveStock(w http.ResponseWriter, r *http.Request) {
	var req moveRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	if req.Quantity <= 0 {
		respond.Error(w, http.StatusBadRequest, "quantity must be positive")
		return
	}
	userID := middleware.UserID(r.Context())
	err := h.Stock.Move(r.Context(), repository.MoveParams{
		StockItemID:  req.StockItemID,
		ToLocationID: req.ToLocationID,
		Quantity:     req.Quantity,
		Note:         req.Note,
		UserID:       &userID,
	})
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "stock item not found")
		return
	}
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	h.Bus.Publish("stock")
	h.Bus.Publish("parts")
	respond.JSON(w, http.StatusOK, map[string]string{"status": "moved"})
}
