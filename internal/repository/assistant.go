// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AssistantRepo struct{ pool *pgxpool.Pool }

func NewAssistantRepo(pool *pgxpool.Pool) *AssistantRepo { return &AssistantRepo{pool: pool} }

// ListConversations returns a user's conversations, most recently used first.
//
// Every query in this repository is scoped by user_id rather than filtered
// afterwards, so a wrong id returns nothing instead of someone else's history.
func (r *AssistantRepo) ListConversations(ctx context.Context, userID uuid.UUID) ([]models.Conversation, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT c.id, c.title, c.subject_kind, c.subject_id, c.created_at, c.updated_at,
		       (SELECT COUNT(*) FROM assistant_messages m WHERE m.conversation_id = c.id)::int
		FROM assistant_conversations c
		WHERE c.user_id = $1
		ORDER BY c.updated_at DESC
		LIMIT 200`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.Conversation{}
	for rows.Next() {
		var c models.Conversation
		if err := rows.Scan(&c.ID, &c.Title, &c.SubjectKind, &c.SubjectID,
			&c.CreatedAt, &c.UpdatedAt, &c.MessageCount); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetConversation returns one conversation with its messages in order, or
// ErrNotFound when it does not exist or belongs to someone else. Those two are
// deliberately the same answer: telling a caller that a conversation exists but
// is not theirs is itself a disclosure.
func (r *AssistantRepo) GetConversation(ctx context.Context, userID, id uuid.UUID) (*models.Conversation, error) {
	var c models.Conversation
	err := r.pool.QueryRow(ctx, `
		SELECT id, title, subject_kind, subject_id, created_at, updated_at
		FROM assistant_conversations WHERE id = $1 AND user_id = $2`, id, userID).
		Scan(&c.ID, &c.Title, &c.SubjectKind, &c.SubjectID, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	rows, err := r.pool.Query(ctx, `
		SELECT id, seq, role, content, tool_calls, tool_results, created_at
		FROM assistant_messages WHERE conversation_id = $1 ORDER BY seq`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	c.Messages = []models.ConversationMessage{}
	for rows.Next() {
		var m models.ConversationMessage
		var calls, results []byte
		if err := rows.Scan(&m.ID, &m.Seq, &m.Role, &m.Content, &calls, &results, &m.CreatedAt); err != nil {
			return nil, err
		}
		if len(calls) > 0 {
			if err := json.Unmarshal(calls, &m.ToolCalls); err != nil {
				return nil, err
			}
		}
		if len(results) > 0 {
			if err := json.Unmarshal(results, &m.ToolResults); err != nil {
				return nil, err
			}
		}
		c.Messages = append(c.Messages, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	c.MessageCount = len(c.Messages)
	return &c, nil
}

// CreateConversation opens one. The title is derived from the first question so
// the list reads without opening anything; generating one with the model would
// spend a request titling something that may never be reopened.
func (r *AssistantRepo) CreateConversation(ctx context.Context, userID uuid.UUID, title string, subjectKind *string, subjectID *uuid.UUID) (*models.Conversation, error) {
	var c models.Conversation
	err := r.pool.QueryRow(ctx, `
		INSERT INTO assistant_conversations (user_id, title, subject_kind, subject_id)
		VALUES ($1, $2, $3, $4)
		RETURNING id, title, subject_kind, subject_id, created_at, updated_at`,
		userID, TitleFrom(title), subjectKind, subjectID).
		Scan(&c.ID, &c.Title, &c.SubjectKind, &c.SubjectID, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	c.Messages = []models.ConversationMessage{}
	return &c, nil
}

// TitleFrom shortens a question into a list label, cutting on a word boundary
// so a truncated title does not end mid-word.
func TitleFrom(question string) string {
	t := strings.Join(strings.Fields(question), " ")
	const max = 60
	if len(t) <= max {
		return t
	}
	cut := t[:max]
	if i := strings.LastIndex(cut, " "); i > 20 {
		cut = cut[:i]
	}
	return cut + "…"
}

// AppendMessages writes a turn's messages, continuing the sequence.
//
// One transaction for the whole turn. A question stored without its answer, or
// an assistant turn whose tool calls have no results, would be replayed to the
// provider on the next question and rejected, leaving the conversation
// permanently unusable. Half a turn is worse than none.
func (r *AssistantRepo) AppendMessages(ctx context.Context, conversationID uuid.UUID, msgs []models.ConversationMessage) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var next int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(seq), 0) + 1 FROM assistant_messages WHERE conversation_id = $1`,
		conversationID).Scan(&next); err != nil {
		return err
	}

	for _, m := range msgs {
		var calls, results any
		if len(m.ToolCalls) > 0 {
			b, err := json.Marshal(m.ToolCalls)
			if err != nil {
				return err
			}
			calls = b
		}
		if len(m.ToolResults) > 0 {
			b, err := json.Marshal(m.ToolResults)
			if err != nil {
				return err
			}
			results = b
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO assistant_messages (conversation_id, seq, role, content, tool_calls, tool_results)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			conversationID, next, m.Role, m.Content, calls, results); err != nil {
			return err
		}
		next++
	}

	// Touch the parent so the list orders by real activity.
	if _, err := tx.Exec(ctx,
		`UPDATE assistant_conversations SET updated_at = NOW() WHERE id = $1`, conversationID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RecordRun stores what a turn cost, successful or not.
func (r *AssistantRepo) RecordRun(ctx context.Context, run models.AssistantRun) error {
	var cost any
	if run.CostKnown {
		cost = run.CostUSD
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO assistant_runs
			(conversation_id, user_id, provider, model, rounds, input_tokens, output_tokens, cost_usd, error)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		run.ConversationID, run.UserID, run.Provider, run.Model,
		run.Rounds, run.InputTokens, run.OutputTokens, cost, run.Error)
	return err
}

// Usage totals a user's spend. Failed runs are included, because they cost the
// same tokens as successful ones and leaving them out would understate the bill
// by exactly the turns worth looking at.
func (r *AssistantRepo) Usage(ctx context.Context, userID uuid.UUID) (models.AssistantUsage, error) {
	var u models.AssistantUsage
	err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*)::int,
		       COUNT(*) FILTER (WHERE error <> '')::int,
		       COALESCE(SUM(input_tokens), 0)::int,
		       COALESCE(SUM(output_tokens), 0)::int,
		       COALESCE(SUM(cost_usd), 0)::float8,
		       COUNT(*) FILTER (WHERE cost_usd IS NULL)::int
		FROM assistant_runs WHERE user_id = $1`, userID).
		Scan(&u.Turns, &u.FailedTurns, &u.InputTokens, &u.OutputTokens, &u.CostUSD, &u.UnpricedTurns)
	return u, err
}

// DeleteConversation removes one and everything under it.
func (r *AssistantRepo) DeleteConversation(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM assistant_conversations WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
