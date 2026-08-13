// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/firelabsca/firebin-api/internal/api/middleware"
	"github.com/firelabsca/firebin-api/internal/api/respond"
	"github.com/firelabsca/firebin-api/internal/assistant"
	"github.com/firelabsca/firebin-api/internal/models"
)

// StreamMessage answers a question, reporting the answer as it is written.
//
// A POST that responds with an event stream, rather than an EventSource GET.
// The question would otherwise have to travel in the query string, where it
// would land in access logs and browser history, and questions about your own
// inventory are not something to leave lying in a URL.
//
// Deliberately separate from the global /events broker, which carries a bare
// resource name and no payload. Overloading it would mean every open tab
// receiving one user's answer.
// @Summary     Ask the assistant, streamed
// @Description Answer a question and stream the reply as server-sent events. Emits text fragments, the tools being run, and a final event carrying the stored turn.
// @Tags        assistant
// @Security    BearerAuth
// @Accept      json
// @Produce     text/event-stream
// @Param       request  body      map[string]interface{}  true  "The question"
// @Success     200  {object}  map[string]interface{}
// @Failure     400  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Failure     503  {object}  map[string]interface{}
// @Router      /assistant/messages/stream  [post]
func (h *Handler) StreamMessage(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	if h.AI == nil {
		respond.Error(w, http.StatusServiceUnavailable, "the assistant is not available on this instance")
		return
	}
	var req sendMessageRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	req.Question = strings.TrimSpace(req.Question)
	if req.Question == "" {
		respond.Error(w, http.StatusBadRequest, "question is required")
		return
	}

	ctx := r.Context()
	// Everything that can be refused is refused before a single byte of the
	// stream is written. Once the status line is out it is 200 forever, and an
	// error after that can only be a line in the body.
	provider, ok := h.activeAssistantProvider(w, ctx)
	if !ok {
		return
	}
	conv, history, ok := h.resolveConversation(w, ctx, userID, req)
	if !ok {
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Stop nginx and Traefik buffering the stream into one lump at the end,
	// which would defeat the whole point.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	rc := http.NewResponseController(w)
	clearWriteDeadline(w)

	send := func(event string, payload any) {
		data, err := json.Marshal(payload)
		if err != nil {
			return
		}
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		_ = rc.Flush()
	}

	// Tell the client which conversation this is before anything else, so a new
	// thread has an id even if the answer then fails.
	send("start", map[string]any{"conversation_id": conv.ID, "title": conv.Title})

	question := req.Question
	if c := strings.TrimSpace(req.Context); c != "" {
		question = "[Viewing: " + c + "]\n\n" + req.Question
	}

	runner := &assistant.Runner{Provider: provider, Tools: h.assistantToolbox()}
	runner.OnRound = h.roundLogger(ctx, &conv.ID, provider.Info().Name, provider.ConfiguredModel())
	turn, added, askErr := runner.AskStream(ctx, history, question, func(ev assistant.Event) {
		send(ev.Kind, ev)
	})

	// Cost is recorded whether or not the turn worked, exactly as in the
	// unstreamed path. A failed turn burned the same tokens.
	if turn != nil {
		run := models.AssistantRun{
			ConversationID: conv.ID, UserID: userID,
			Provider: provider.Info().Name, Model: turn.Usage.ModelID,
			Rounds:       turn.Rounds,
			InputTokens:  turn.Usage.InputTokens,
			OutputTokens: turn.Usage.OutputTokens,
			CostUSD:      turn.Usage.EstimatedCostUSD,
			CostKnown:    turn.Usage.CostKnown,
		}
		if askErr != nil {
			run.Error = askErr.Error()
		}
		if err := h.Assistant.RecordRun(ctx, run); err != nil {
			slog.Error("could not record assistant run", "error", err, "conversation", conv.ID)
		}
	}

	if askErr != nil {
		send("error", map[string]any{"error": askErr.Error(), "turn": turn})
		return
	}

	// Persist the same messages the unstreamed path would, so a conversation
	// reads the same however it was asked.
	stored, err := toStoredMessages(added)
	if err == nil {
		err = h.Assistant.AppendMessages(ctx, conv.ID, stored)
	}
	if err != nil {
		// The answer is already on screen, so this is not a failed question. It
		// is a conversation that will not remember it, which the user has to be
		// told rather than left to discover on reload.
		slog.Error("could not store the streamed exchange", "error", err, "conversation", conv.ID)
		send("error", map[string]any{
			"error": "the answer could not be saved to this conversation",
			"turn":  turn,
		})
		return
	}

	send("done", map[string]any{"conversation_id": conv.ID, "title": conv.Title, "turn": turn})
}
