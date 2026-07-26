// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/firelabsca/firebin-api/internal/api/middleware"
	"github.com/firelabsca/firebin-api/internal/api/respond"
	"github.com/firelabsca/firebin-api/internal/jobs"
	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/google/uuid"
)

// ClearTasks deletes every finished (completed/failed/cancelled) task and its
// logs — the manual "clear" from the Activity screen. Admin only. Running or
// queued tasks are never removed.
// @Summary     Clear finished tasks
// @Description Delete every finished task and its logs; running or queued tasks are kept.
// @Tags        tasks
// @Security    BearerAuth
// @Produce     json
// @Success     200  {object}  map[string]int64
// @Failure     401  {object}  map[string]interface{}
// @Router      /tasks  [delete]
func (h *Handler) ClearTasks(w http.ResponseWriter, r *http.Request) {
	n, err := h.Jobs.Store().ClearFinished(r.Context(), time.Now())
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not clear tasks")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]int64{"cleared": n})
}

// ListTasks returns recent background tasks, newest first.
// @Summary     List tasks
// @Description Return recent background tasks, newest first, optionally filtered.
// @Tags        tasks
// @Security    BearerAuth
// @Produce     json
// @Param       status  query     string  false  "filter by status"
// @Param       type    query     string  false  "filter by task type"
// @Param       limit   query     int     false  "maximum tasks to return"
// @Success     200     {array}   models.Task
// @Failure     401     {object}  map[string]interface{}
// @Router      /tasks  [get]
func (h *Handler) ListTasks(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	typ := r.URL.Query().Get("type")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	var tasks []models.Task
	tasks, err := h.Jobs.Store().List(r.Context(), status, typ, limit)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not list tasks")
		return
	}
	respond.JSON(w, http.StatusOK, tasks)
}

// GetTask returns one task with its progress and result.
// @Summary     Get task
// @Description Return one task with its progress and result.
// @Tags        tasks
// @Security    BearerAuth
// @Produce     json
// @Param       id   path      string                  true   "task ID"
// @Success     200  {object}  models.Task
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /tasks/{id}  [get]
func (h *Handler) GetTask(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	t, err := h.Jobs.Store().Get(r.Context(), id)
	if errors.Is(err, jobs.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not load task")
		return
	}
	respond.JSON(w, http.StatusOK, t)
}

// GetTaskLogs returns the task's log lines after ?after_id=N.
// @Summary     Get task logs
// @Description Return the task's log lines after the given after_id cursor.
// @Tags        tasks
// @Security    BearerAuth
// @Produce     json
// @Param       id        path      string                  true   "task ID"
// @Param       after_id  query     int                     false  "return logs after this id"
// @Success     200       {array}   models.JobLog
// @Failure     401       {object}  map[string]interface{}
// @Failure     404       {object}  map[string]interface{}
// @Router      /tasks/{id}/logs  [get]
func (h *Handler) GetTaskLogs(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after_id"), 10, 64)
	logs, err := h.Jobs.Store().Logs(r.Context(), id, after, 0)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not load logs")
		return
	}
	respond.JSON(w, http.StatusOK, logs)
}

// CancelTask requests cancellation.
// @Summary     Cancel task
// @Description Request cancellation of a running or queued task.
// @Tags        tasks
// @Security    BearerAuth
// @Produce     json
// @Param       id   path      string                  true   "task ID"
// @Success     200  {object}  map[string]string
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /tasks/{id}/cancel  [post]
func (h *Handler) CancelTask(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	err := h.Jobs.Cancel(r.Context(), id)
	if errors.Is(err, jobs.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not cancel")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"status": "cancelling"})
}

// RetryTask enqueues a fresh job from a finished task's arguments.
// @Summary     Retry task
// @Description Enqueue a fresh job from a finished task's arguments.
// @Tags        tasks
// @Security    BearerAuth
// @Produce     json
// @Param       id   path      string                  true   "task ID"
// @Success     200  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /tasks/{id}/retry  [post]
func (h *Handler) RetryTask(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	t, err := h.Jobs.Store().Get(r.Context(), id)
	if errors.Is(err, jobs.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not load task")
		return
	}

	uid := middleware.UserID(r.Context())
	switch t.Type {
	case "bulk_enrich":
		var a BulkEnrichArgs
		if err := json.Unmarshal(t.ArgsSummary, &a); err != nil || len(a.PartIDs) == 0 {
			respond.Error(w, http.StatusBadRequest, "cannot retry: missing arguments")
			return
		}
		newID := uuid.New()
		a.TaskID = newID
		summary, _ := json.Marshal(a)
		if err := h.Jobs.Enqueue(r.Context(), newID, a, jobs.EnqueueMeta{
			Type: "bulk_enrich", Queue: jobs.QueueEnrich, MaxAttempts: 3,
			CreatedBy: &uid, ArgsSummary: summary, ProgressTotal: len(a.PartIDs),
		}); err != nil {
			respond.Error(w, http.StatusInternalServerError, "could not re-enqueue")
			return
		}
		respond.JSON(w, http.StatusAccepted, map[string]any{"task_id": newID})
	default:
		respond.Error(w, http.StatusBadRequest, "retry not supported for this task type")
	}
}
