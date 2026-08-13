// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/firelabsca/firebin-api/internal/ai"
	"github.com/firelabsca/firebin-api/internal/api/middleware"
	"github.com/firelabsca/firebin-api/internal/api/respond"
	"github.com/firelabsca/firebin-api/internal/assistant"
	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/firelabsca/firebin-api/internal/repository"
	"github.com/google/uuid"
)

// ListConversations returns the caller's own conversations.
// @Summary     List assistant conversations
// @Description List the calling user's assistant conversations, most recently used first.
// @Tags        assistant
// @Security    BearerAuth
// @Produce     json
// @Success     200  {array}   models.Conversation
// @Failure     401  {object}  map[string]interface{}
// @Router      /assistant/conversations  [get]
func (h *Handler) ListConversations(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	list, err := h.Assistant.ListConversations(r.Context(), userID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not list conversations")
		return
	}
	respond.JSON(w, http.StatusOK, list)
}

// GetConversation returns one conversation with its messages.
// @Summary     Get an assistant conversation
// @Description Get one conversation with its messages in order.
// @Tags        assistant
// @Security    BearerAuth
// @Produce     json
// @Param       id  path      string  true  "Conversation id"
// @Success     200  {object}  models.Conversation
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /assistant/conversations/{id}  [get]
func (h *Handler) GetConversation(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	var c *models.Conversation
	c, err := h.Assistant.GetConversation(r.Context(), userID, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			respond.Error(w, http.StatusNotFound, "conversation not found")
			return
		}
		respond.Error(w, http.StatusInternalServerError, "could not load the conversation")
		return
	}
	respond.JSON(w, http.StatusOK, c)
}

// DeleteConversation removes one of the caller's conversations.
// @Summary     Delete an assistant conversation
// @Description Delete one of the calling user's conversations and its messages.
// @Tags        assistant
// @Security    BearerAuth
// @Produce     json
// @Param       id  path      string  true  "Conversation id"
// @Success     200  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /assistant/conversations/{id}  [delete]
func (h *Handler) DeleteConversation(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	if err := h.Assistant.DeleteConversation(r.Context(), userID, id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			respond.Error(w, http.StatusNotFound, "conversation not found")
			return
		}
		respond.Error(w, http.StatusInternalServerError, "could not delete the conversation")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// AssistantUsage reports what the caller has spent.
// @Summary     Assistant usage
// @Description Token and cost totals for the calling user, including turns that failed.
// @Tags        assistant
// @Security    BearerAuth
// @Produce     json
// @Success     200  {object}  models.AssistantUsage
// @Failure     401  {object}  map[string]interface{}
// @Router      /assistant/usage  [get]
func (h *Handler) AssistantUsage(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	var usage models.AssistantUsage
	usage, err := h.Assistant.Usage(r.Context(), userID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not read usage")
		return
	}
	respond.JSON(w, http.StatusOK, usage)
}

type sendMessageRequest struct {
	Question string `json:"question"`
	// ConversationID continues an existing thread. Empty starts a new one.
	ConversationID string `json:"conversation_id"`
	// Subject is what the popup was opened from, recorded on a new conversation
	// so the list shows what it was about.
	SubjectKind string `json:"subject_kind"`
	SubjectID   string `json:"subject_id"`
	// Context is a short note about the page the question was asked from, such
	// as the part being viewed. Prepended to the question rather than stored as
	// a separate turn, so the transcript reads the way it was asked.
	Context string `json:"context"`
}

// SendMessage answers a question inside a conversation and stores the exchange.
// @Summary     Ask the assistant in a conversation
// @Description Answer a question, continuing an existing conversation or starting a new one, and store the exchange with what it cost.
// @Tags        assistant
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       request  body      map[string]interface{}  true  "The question"
// @Success     200  {object}  map[string]interface{}
// @Failure     400  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Failure     503  {object}  map[string]interface{}
// @Router      /assistant/messages  [post]
func (h *Handler) SendMessage(w http.ResponseWriter, r *http.Request) {
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
	provider, ok := h.activeAssistantProvider(w, ctx)
	if !ok {
		return
	}

	// Load or open the conversation before spending anything, so a bad id fails
	// for free rather than after a paid request.
	conv, history, ok := h.resolveConversation(w, ctx, userID, req)
	if !ok {
		return
	}

	question := req.Question
	if c := strings.TrimSpace(req.Context); c != "" {
		question = "[Viewing: " + c + "]\n\n" + req.Question
	}

	// Clear the server's write deadline for this request.
	//
	// http.Server applies WriteTimeout once, when the request starts, and never
	// extends it. A chat turn is several provider calls with tool lookups
	// between them and routinely outlasts 15 seconds; a local 32B model took
	// 101 seconds for one question here. The connection is then cut before the
	// handler writes anything, so the client sees the socket close with no
	// response and no status: not a timeout it can report, just nothing. The
	// request context still carries the client's own cancellation, so a caller
	// that goes away still stops the work.
	clearWriteDeadline(w)

	runner := &assistant.Runner{Provider: provider, Tools: h.assistantToolbox()}
	runner.OnRound = h.roundLogger(ctx, &conv.ID, provider.Info().Name, provider.ConfiguredModel())
	turn, added, askErr := runner.Ask(ctx, history, question)

	// Record the cost first, and record it whether or not the turn worked. A
	// failed turn still burned tokens, and a usage total that quietly omits the
	// failures understates the bill by exactly the turns worth investigating.
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
			// Worth knowing about, but not worth failing an answer the user
			// already has.
			slog.Error("could not record assistant run", "error", err, "conversation", conv.ID)
		}
	}

	if askErr != nil {
		// The question is not stored on failure. Storing it alone would leave a
		// conversation whose last turn has no answer, which replays to the
		// provider as an unanswered question and is rejected next time.
		respond.JSON(w, http.StatusOK, map[string]any{
			"conversation_id": conv.ID,
			"error":           askErr.Error(),
			"turn":            turn,
		})
		return
	}

	// Persist what this turn added, translated back from provider shape.
	stored, err := toStoredMessages(added)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not store the exchange")
		return
	}
	if err := h.Assistant.AppendMessages(ctx, conv.ID, stored); err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not store the exchange")
		return
	}

	respond.JSON(w, http.StatusOK, map[string]any{
		"conversation_id": conv.ID,
		"title":           conv.Title,
		"turn":            turn,
	})
}

// activeAssistantProvider resolves the provider to answer with, writing the
// refusal itself when there is not one.
func (h *Handler) activeAssistantProvider(w http.ResponseWriter, ctx context.Context) (ai.ChatProvider, bool) {
	enabled, err := h.AI.FeatureEnabled(ctx)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not read AI settings")
		return nil, false
	}
	if !enabled {
		respond.Error(w, http.StatusServiceUnavailable, "the assistant is switched off")
		return nil, false
	}
	provider := h.AI.Registry().Active()
	if provider == nil {
		respond.Error(w, http.StatusServiceUnavailable, h.whyNoProvider())
		return nil, false
	}
	return provider, true
}

// whyNoProvider names which of the reasons applies.
//
// One message for all of them sends the user to fix the wrong thing, so the
// provider is named along with what it is missing.
func (h *Handler) whyNoProvider() string {
	name := h.AI.Registry().ActiveName()
	if name == "" {
		return "no AI provider is selected; pick one in Settings under Assistant"
	}
	p := h.AI.Registry().Get(name)
	if p == nil {
		return "the selected AI provider no longer exists; pick one in Settings under Assistant"
	}
	return p.Info().DisplayName + " is selected but not configured; fill in its settings under Assistant, or pick another"
}

// knownSubjectKinds is what a conversation is allowed to be about.
//
// It has to match the CHECK constraint on assistant_conversations.subject_kind
// exactly. Both exist: the constraint is the guarantee, and this is what stops a
// violation of it reaching Postgres and turning a question into a 500. Adding a
// kind means editing this AND writing a migration; see 000034 for why.
var knownSubjectKinds = map[string]bool{
	"part":      true,
	"project":   true,
	"board":     true,
	"datasheet": true,
}

// resolveConversation loads the thread being continued, or opens a new one.
func (h *Handler) resolveConversation(w http.ResponseWriter, ctx context.Context, userID uuid.UUID, req sendMessageRequest) (*models.Conversation, []ai.Message, bool) {
	if id := strings.TrimSpace(req.ConversationID); id != "" {
		convID, err := uuid.Parse(id)
		if err != nil {
			respond.Error(w, http.StatusBadRequest, "invalid conversation id")
			return nil, nil, false
		}
		conv, err := h.Assistant.GetConversation(ctx, userID, convID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				respond.Error(w, http.StatusNotFound, "conversation not found")
				return nil, nil, false
			}
			respond.Error(w, http.StatusInternalServerError, "could not load the conversation")
			return nil, nil, false
		}
		history, err := toProviderMessages(conv.Messages)
		if err != nil {
			respond.Error(w, http.StatusInternalServerError, "could not read the conversation")
			return nil, nil, false
		}
		return conv, history, true
	}

	var kind *string
	var subject *uuid.UUID
	if k := strings.TrimSpace(req.SubjectKind); k != "" {
		// Checked here, not just by the database. subject_kind is a free string
		// all the way from the browser, and the column has a CHECK constraint, so
		// a kind the schema does not know turns an INSERT into an error and the
		// whole question into a 500 — which is exactly what adding the datasheet
		// page did. Dropping an unknown kind costs the conversation its subject
		// and nothing else, where the alternative costs the user their answer.
		if knownSubjectKinds[k] {
			kind = &k
			if s := strings.TrimSpace(req.SubjectID); s != "" {
				id, err := uuid.Parse(s)
				if err != nil {
					respond.Error(w, http.StatusBadRequest, "invalid subject id")
					return nil, nil, false
				}
				subject = &id
			}
		} else {
			// Logged rather than silent: this only happens when a client knows
			// about a subject the schema does not, which is a missing migration.
			slog.Warn("ignoring an unknown assistant subject kind", "kind", k)
		}
	}
	conv, err := h.Assistant.CreateConversation(ctx, userID, req.Question, kind, subject)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not start a conversation")
		return nil, nil, false
	}
	return conv, nil, true
}

// toStoredMessages converts provider messages into rows.
func toStoredMessages(msgs []ai.Message) ([]models.ConversationMessage, error) {
	out := make([]models.ConversationMessage, 0, len(msgs))
	for _, m := range msgs {
		row := models.ConversationMessage{Role: m.Role, Content: m.Text}
		if len(m.ToolCalls) > 0 {
			b, err := json.Marshal(m.ToolCalls)
			if err != nil {
				return nil, err
			}
			row.ToolCalls = b
		}
		if len(m.ToolResults) > 0 {
			b, err := json.Marshal(m.ToolResults)
			if err != nil {
				return nil, err
			}
			row.ToolResults = b
		}
		out = append(out, row)
	}
	return out, nil
}

// toProviderMessages converts stored rows back into provider messages.
//
// The tool calls and results have to survive the round trip. A conversation
// replayed without them presents an assistant turn that asked for a tool and
// never got an answer, which Anthropic rejects outright, so every follow-up
// question in the thread would fail.
func toProviderMessages(rows []models.ConversationMessage) ([]ai.Message, error) {
	out := make([]ai.Message, 0, len(rows))
	for _, row := range rows {
		m := ai.Message{Role: row.Role, Text: row.Content}
		if len(row.ToolCalls) > 0 {
			if err := json.Unmarshal(row.ToolCalls, &m.ToolCalls); err != nil {
				return nil, err
			}
		}
		if len(row.ToolResults) > 0 {
			if err := json.Unmarshal(row.ToolResults, &m.ToolResults); err != nil {
				return nil, err
			}
		}
		out = append(out, m)
	}
	return out, nil
}
