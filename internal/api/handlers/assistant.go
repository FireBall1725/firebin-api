// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/firelabsca/firebin-api/internal/api/respond"
	"github.com/firelabsca/firebin-api/internal/assistant"
	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/google/uuid"
)

type assistantAskRequest struct {
	Question string `json:"question"`
}

// AskAssistant answers one question about the inventory.
//
// Stateless for now: no history is kept, so each question starts fresh.
// Conversations are their own change, and a question that works without them is
// worth having first.
// @Summary     Ask the assistant
// @Description Answer one question about the inventory, using the configured AI provider and the read-only inventory tools. Returns the answer with the tool calls it made, so the answer can be checked.
// @Tags        assistant
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       request  body      map[string]interface{}  true  "The question"
// @Success     200  {object}  map[string]interface{}
// @Failure     400  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Failure     503  {object}  map[string]interface{}
// @Router      /assistant/ask  [post]
func (h *Handler) AskAssistant(w http.ResponseWriter, r *http.Request) {
	if h.AI == nil {
		respond.Error(w, http.StatusServiceUnavailable, "the assistant is not available on this instance")
		return
	}
	var req assistantAskRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	req.Question = strings.TrimSpace(req.Question)
	if req.Question == "" {
		respond.Error(w, http.StatusBadRequest, "question is required")
		return
	}

	ctx := r.Context()
	enabled, err := h.AI.FeatureEnabled(ctx)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not read AI settings")
		return
	}
	// Off means off. Answering anyway because a provider happens to be
	// configured would send inventory data somewhere the admin switched off.
	if !enabled {
		respond.Error(w, http.StatusServiceUnavailable, "the assistant is switched off")
		return
	}
	provider := h.AI.Registry().Active()
	if provider == nil {
		respond.Error(w, http.StatusServiceUnavailable, h.whyNoProvider())
		return
	}

	clearWriteDeadline(w) // see the note in SendMessage
	runner := &assistant.Runner{Provider: provider, Tools: h.assistantToolbox()}
	runner.OnRound = h.roundLogger(ctx, nil, provider.Info().Name, provider.ConfiguredModel())
	turn, _, err := runner.Ask(ctx, nil, req.Question)
	if err != nil {
		// The turn is still returned when there is one, because the tool calls
		// it managed are worth seeing even when the answer never arrived.
		respond.JSON(w, http.StatusOK, map[string]any{
			"error": err.Error(),
			"turn":  turn,
		})
		return
	}
	respond.JSON(w, http.StatusOK, turn)
}

// AssistantStatus reports whether the assistant is usable, for any signed-in
// user.
//
// Separate from the admin settings endpoint because every user needs this and
// almost none of them may read provider configuration. Without it the web app
// had no way to know, so it showed the assistant in the sidebar and a button on
// every page whether or not anything could answer.
// @Summary     Assistant availability
// @Description Whether the assistant is switched on and has a usable provider. Readable by any signed-in user.
// @Tags        assistant
// @Security    BearerAuth
// @Produce     json
// @Success     200  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Router      /assistant/status  [get]
func (h *Handler) AssistantStatus(w http.ResponseWriter, r *http.Request) {
	if h.AI == nil {
		respond.JSON(w, http.StatusOK, map[string]any{"enabled": false, "ready": false})
		return
	}
	enabled, err := h.AI.FeatureEnabled(r.Context())
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not read AI settings")
		return
	}
	// Ready means a question would actually be answered: switched on, and a
	// provider selected that has what it needs. Reported separately from
	// enabled so an admin can be told the difference.
	respond.JSON(w, http.StatusOK, map[string]any{
		"enabled": enabled,
		"ready":   enabled && h.AI.Registry().Active() != nil,
	})
}

// roundLogger returns an OnRound handler that stores each provider call.
//
// Best-effort by design: a debug log that fails to write must never fail the
// answer someone is waiting for, so an error here is logged and dropped.
// context.WithoutCancel because the most valuable rounds to have a record of
// are the ones on a turn that was cancelled or timed out.
func (h *Handler) roundLogger(ctx context.Context, conversationID *uuid.UUID, provider, model string) func(assistant.RoundLog) {
	return func(l assistant.RoundLog) {
		row := models.AssistantRoundLog{
			ConversationID: conversationID,
			Round:          l.Round,
			Provider:       provider,
			Model:          model,
			URL:            l.URL,
			Request:        string(l.Request),
			Response:       string(l.Response),
			Thinking:       l.Thinking,
			Status:         l.Status,
			InputTokens:    l.InputTokens,
			OutputTokens:   l.OutputTokens,
			DurationMS:     l.DurationMS,
			Error:          l.Err,
		}
		if err := h.Assistant.RecordRoundLog(context.WithoutCancel(ctx), row); err != nil {
			slog.Warn("could not store an assistant round log", "error", err, "round", l.Round)
		}
	}
}
