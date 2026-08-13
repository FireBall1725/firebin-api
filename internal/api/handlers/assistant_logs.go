// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"net/http"
	"strconv"

	"github.com/firelabsca/firebin-api/internal/api/middleware"
	"github.com/firelabsca/firebin-api/internal/api/respond"
	"github.com/firelabsca/firebin-api/internal/models"
)

// ConversationLogs returns the provider calls made for one conversation.
//
// Scoped to the owner by loading the conversation first: these rows carry the
// full prompt, which is the user's own inventory data and their questions about
// it. Another user's conversation reads as not-found, matching how the
// conversation itself behaves.
// @Summary     Get the AI logs for a conversation
// @Description Every provider call made in a conversation: what was sent, what came back, the model's reasoning, tokens and timing.
// @Tags        assistant
// @Security    BearerAuth
// @Produce     json
// @Param       id   path      string  true  "Conversation id"
// @Success     200  {array}   models.AssistantRoundLog
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /assistant/conversations/{id}/logs  [get]
func (h *Handler) ConversationLogs(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	userID := middleware.UserID(r.Context())
	if _, err := h.Assistant.GetConversation(r.Context(), userID, id); err != nil {
		respond.Error(w, http.StatusNotFound, "conversation not found")
		return
	}
	logs, err := h.Assistant.RoundLogsForConversation(r.Context(), id, 200)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not read the logs")
		return
	}
	respond.JSON(w, http.StatusOK, logs)
}

// AssistantLogs lists the most recent provider calls on the instance.
//
// Admin only, unlike the per-conversation view: this crosses users, and the
// rows contain whatever anyone asked.
// @Summary     Recent AI logs
// @Description The most recent provider calls on this instance, newest first.
// @Tags        settings
// @Security    BearerAuth
// @Produce     json
// @Param       limit  query     int  false  "How many to return (default 50, max 200)"
// @Success     200  {array}   models.AssistantRoundLog
// @Failure     401  {object}  map[string]interface{}
// @Router      /settings/assistant/logs  [get]
func (h *Handler) AssistantLogs(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	logs, err := h.Assistant.RecentRoundLogs(r.Context(), limit)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not read the logs")
		return
	}
	if logs == nil {
		logs = []models.AssistantRoundLog{}
	}
	respond.JSON(w, http.StatusOK, logs)
}

// ClearAssistantLogs deletes every stored provider call.
//
// The rows are a debug aid holding full prompts, so there has to be a way to
// drop them without waiting a week for the retention sweep.
// @Summary     Clear the AI logs
// @Description Delete every stored provider request and response.
// @Tags        settings
// @Security    BearerAuth
// @Produce     json
// @Success     200  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Router      /settings/assistant/logs  [delete]
func (h *Handler) ClearAssistantLogs(w http.ResponseWriter, r *http.Request) {
	n, err := h.Assistant.ClearRoundLogs(r.Context())
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not clear the logs")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"deleted": n})
}
