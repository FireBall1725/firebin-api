// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"net/http"

	"github.com/firelabsca/firebin-api/internal/api/respond"
	"github.com/firelabsca/firebin-api/internal/models"
)

// GetStats returns the dashboard summary.
// @Summary     Get dashboard stats
// @Description Return the dashboard summary counts and totals.
// @Tags        dashboard
// @Security    BearerAuth
// @Produce     json
// @Success     200  {object}  models.Stats
// @Failure     401  {object}  map[string]interface{}
// @Router      /stats [get]
func (h *Handler) GetStats(w http.ResponseWriter, r *http.Request) {
	s, err := h.Stats.Get(r.Context())
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not load stats")
		return
	}
	respond.JSON(w, http.StatusOK, s)
}

// LowStock returns parts at or below their minimum.
// @Summary     List low-stock parts
// @Description Return parts at or below their minimum stock level.
// @Tags        dashboard
// @Security    BearerAuth
// @Produce     json
// @Success     200  {array}   models.Part
// @Failure     401  {object}  map[string]interface{}
// @Router      /parts/low-stock [get]
func (h *Handler) LowStock(w http.ResponseWriter, r *http.Request) {
	var parts []models.Part
	parts, err := h.Parts.ListLowStock(r.Context())
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not load low stock")
		return
	}
	respond.JSON(w, http.StatusOK, parts)
}

// RecentActivity returns the newest stock movements across all parts.
// @Summary     List recent stock activity
// @Description Return the newest stock movements across all parts.
// @Tags        dashboard
// @Security    BearerAuth
// @Produce     json
// @Success     200  {array}   models.StockTransaction
// @Failure     401  {object}  map[string]interface{}
// @Router      /stock/recent [get]
func (h *Handler) RecentActivity(w http.ResponseWriter, r *http.Request) {
	txns, err := h.Stock.Recent(r.Context(), 20)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not load activity")
		return
	}
	respond.JSON(w, http.StatusOK, txns)
}

// ListLocationStock returns the contents of a bin (scan-a-bin).
// @Summary     List location stock
// @Description Return the stock contents of a bin (scan-a-bin).
// @Tags        dashboard
// @Security    BearerAuth
// @Produce     json
// @Param       id   path      string  true   "identifier"
// @Success     200  {array}   models.StockItem
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /locations/{id}/stock [get]
func (h *Handler) ListLocationStock(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	items, err := h.Stock.ListForLocation(r.Context(), id)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not load location contents")
		return
	}
	respond.JSON(w, http.StatusOK, items)
}
