// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/firelabsca/firebin-api/internal/api/middleware"
	"github.com/firelabsca/firebin-api/internal/api/respond"
	"github.com/firelabsca/firebin-api/internal/jobs"
	"github.com/google/uuid"
)

type bulkMoveRequest struct {
	PartIDs    []uuid.UUID `json:"part_ids"`
	LocationID *uuid.UUID  `json:"location_id"` // null = unassigned
}

// BulkMoveParts consolidates all stock of each given part into one location.
// @Summary     Bulk move parts
// @Description Consolidate all stock of each given part into one location.
// @Tags        parts
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       request  body      map[string]interface{}  true  "Part ids and location"
// @Success     200  {object}  map[string]int
// @Failure     400  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Router      /parts/bulk/move  [post]
func (h *Handler) BulkMoveParts(w http.ResponseWriter, r *http.Request) {
	var req bulkMoveRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	if len(req.PartIDs) == 0 {
		respond.Error(w, http.StatusBadRequest, "part_ids is required")
		return
	}
	uid := middleware.UserID(r.Context())
	moved, failed := 0, 0
	for _, pid := range req.PartIDs {
		if _, err := h.Stock.MovePartToLocation(r.Context(), pid, req.LocationID, &uid); err != nil {
			failed++
			continue
		}
		moved++
	}
	h.Bus.Publish("stock")
	respond.JSON(w, http.StatusOK, map[string]int{"moved": moved, "failed": failed})
}

type bulkEnrichRequest struct {
	PartIDs []uuid.UUID `json:"part_ids"`
}

// BulkEnrichParts enqueues a background job that refreshes metadata for the
// given parts from their primary MPN. It returns 202 with a task id the client
// watches; the work itself runs in the bulk_enrich worker off the request path.
// @Summary     Bulk enrich parts
// @Description Enqueue a background job to refresh metadata for the given parts.
// @Tags        parts
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       request  body      map[string]interface{}  true  "Part ids"
// @Success     202  {object}  map[string]interface{}
// @Failure     400  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Router      /parts/bulk/enrich  [post]
func (h *Handler) BulkEnrichParts(w http.ResponseWriter, r *http.Request) {
	var req bulkEnrichRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	if len(req.PartIDs) == 0 {
		respond.Error(w, http.StatusBadRequest, "part_ids is required")
		return
	}
	uid := middleware.UserID(r.Context())
	taskID := uuid.New()
	args := BulkEnrichArgs{TaskID: taskID, PartIDs: req.PartIDs}
	summary, _ := json.Marshal(args)
	if err := h.Jobs.Enqueue(r.Context(), taskID, args, jobs.EnqueueMeta{
		Type: "bulk_enrich", Queue: jobs.QueueEnrich, MaxAttempts: 3,
		CreatedBy: &uid, ArgsSummary: summary, ProgressTotal: len(req.PartIDs),
	}); err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not enqueue refresh")
		return
	}
	respond.JSON(w, http.StatusAccepted, map[string]any{"task_id": taskID})
}
