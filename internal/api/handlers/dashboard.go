// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"net/http"

	"github.com/firelabsca/firebin-api/internal/api/respond"
)

// GetStats returns the dashboard summary.
func (h *Handler) GetStats(w http.ResponseWriter, r *http.Request) {
	s, err := h.Stats.Get(r.Context())
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not load stats")
		return
	}
	respond.JSON(w, http.StatusOK, s)
}

// LowStock returns parts at or below their minimum.
func (h *Handler) LowStock(w http.ResponseWriter, r *http.Request) {
	parts, err := h.Parts.ListLowStock(r.Context())
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not load low stock")
		return
	}
	respond.JSON(w, http.StatusOK, parts)
}

// RecentActivity returns the newest stock movements across all parts.
func (h *Handler) RecentActivity(w http.ResponseWriter, r *http.Request) {
	txns, err := h.Stock.Recent(r.Context(), 20)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not load activity")
		return
	}
	respond.JSON(w, http.StatusOK, txns)
}

// ListLocationStock returns the contents of a bin (scan-a-bin).
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
