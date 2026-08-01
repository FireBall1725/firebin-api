// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/firelabsca/firebin-api/internal/api/respond"
	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/firelabsca/firebin-api/internal/picklist"
)

// BoardPickList computes what to pull from stock to build N of a board. Required
// quantity per matched part = N x per-board qty x panel copies, aggregated across
// BOM lines. Stock is allocated bin by bin (ordered for walking); parts short of
// stock and BOM lines with no inventory match are flagged. Query: ?quantity=N.
// @Summary     Board pick list
// @Description Compute what to pull from stock to build N of a board.
// @Tags        boards
// @Security    BearerAuth
// @Produce     json
// @Param       id        path      string   true   "identifier"
// @Param       quantity  query     integer  false  "number of boards to build"
// @Success     200       {object}  models.PickList
// @Failure     401       {object}  map[string]interface{}
// @Failure     404       {object}  map[string]interface{}
// @Router      /boards/{id}/pick-list  [get]
func (h *Handler) BoardPickList(w http.ResponseWriter, r *http.Request) {
	boardID, ok := pathUUID(w, r)
	if !ok {
		return
	}
	qty := 1
	if q, err := strconv.Atoi(r.URL.Query().Get("quantity")); err == nil && q > 0 {
		qty = q
	}

	// Named rather than inferred so swaggo can resolve the response type: it
	// reads this file's imports, and the annotation names models.
	var list *models.PickList
	list, err := picklist.Compute(r.Context(), h.Projects, h.Stock, boardID, qty)
	if err != nil {
		var notFound picklist.ErrBoardNotFound
		if errors.As(err, &notFound) {
			respond.Error(w, http.StatusNotFound, "board not found")
			return
		}
		respond.Error(w, http.StatusInternalServerError, "could not build the pick list")
		return
	}
	respond.JSON(w, http.StatusOK, list)
}
