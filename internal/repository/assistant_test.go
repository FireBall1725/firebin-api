// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/firelabsca/firebin-api/internal/ai"
	"github.com/firelabsca/firebin-api/internal/assistant"

	"github.com/firelabsca/firebin-api/internal/db"
	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/firelabsca/firebin-api/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Conversations are per user and the tool calls have to survive storage.
//
// Needs a disposable Postgres via DATABASE_URL and TRUNCATEs its own tables, so
// it skips when that is unset. CI provides one; do not point it at real data.
func TestAssistantConversations(t *testing.T) {
	url := dbURL(t)
	ctx := context.Background()
	if err := db.Migrate(url); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)

	truncate := func() {
		// users cascades to conversations, which cascades to messages and runs.
		if _, err := pool.Exec(ctx, `TRUNCATE assistant_conversations, users CASCADE`); err != nil {
			t.Fatalf("truncate: %v", err)
		}
	}
	truncate()
	t.Cleanup(truncate)

	const (
		alice = "aaaaaaaa-0000-0000-0000-00000000000a"
		bob   = "bbbbbbbb-0000-0000-0000-00000000000b"
	)
	for _, u := range []struct{ id, name string }{{alice, "alice"}, {bob, "bob"}} {
		mustExec(t, pool, ctx,
			`INSERT INTO users (id, username, password_hash) VALUES ($1, $2, 'x')`, u.id, u.name)
	}
	aliceID, bobID := uuid.MustParse(alice), uuid.MustParse(bob)
	repo := repository.NewAssistantRepo(pool)

	conv, err := repo.CreateConversation(ctx, aliceID, "Do I have an 0603 220 ohm resistor?", nil, nil)
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}

	// A turn as the runner produces it: question, assistant tool call, tool
	// result, answer.
	calls, _ := json.Marshal([]map[string]any{{"id": "c1", "name": "search_parts", "input": map[string]string{"package": "0603"}}})
	results, _ := json.Marshal([]map[string]any{{"call_id": "c1", "name": "search_parts", "content": "{\"count\":0}"}})
	turn := []models.ConversationMessage{
		{Role: "user", Content: "Do I have an 0603 220 ohm resistor?"},
		{Role: "assistant", ToolCalls: calls},
		{Role: "user", ToolResults: results},
		{Role: "assistant", Content: "No, you do not stock one."},
	}
	if err := repo.AppendMessages(ctx, conv.ID, turn); err != nil {
		t.Fatalf("AppendMessages: %v", err)
	}

	// The round trip is what makes a follow-up question possible. A stored
	// conversation that loses its tool calls replays as an assistant turn that
	// asked for a tool and never got an answer, which Anthropic rejects, so
	// every later question in the thread would fail.
	t.Run("tool calls and results survive storage", func(t *testing.T) {
		got, err := repo.GetConversation(ctx, aliceID, conv.ID)
		if err != nil {
			t.Fatalf("GetConversation: %v", err)
		}
		if len(got.Messages) != 4 {
			t.Fatalf("got %d messages, want 4", len(got.Messages))
		}
		if len(got.Messages[1].ToolCalls) == 0 {
			t.Error("the assistant's tool call was lost")
		}
		if len(got.Messages[2].ToolResults) == 0 {
			t.Error("the tool result was lost")
		}
		var back []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(got.Messages[1].ToolCalls, &back); err != nil {
			t.Fatalf("stored tool calls are not readable: %v", err)
		}
		if len(back) != 1 || back[0].ID != "c1" || back[0].Name != "search_parts" {
			t.Errorf("tool calls came back as %+v", back)
		}
	})

	// Order is the meaning. Two messages in one turn can share a timestamp, so
	// the sequence is stored rather than inferred from time.
	t.Run("messages come back in order", func(t *testing.T) {
		got, _ := repo.GetConversation(ctx, aliceID, conv.ID)
		want := []string{"user", "assistant", "user", "assistant"}
		for i, m := range got.Messages {
			if m.Role != want[i] {
				t.Errorf("message %d is %s, want %s", i, m.Role, want[i])
			}
			if m.Seq != i+1 {
				t.Errorf("message %d has seq %d", i, m.Seq)
			}
		}
	})

	t.Run("a second turn continues the sequence", func(t *testing.T) {
		if err := repo.AppendMessages(ctx, conv.ID, []models.ConversationMessage{
			{Role: "user", Content: "what is closest?"},
			{Role: "assistant", Content: "A 100 Ω in 0603."},
		}); err != nil {
			t.Fatalf("AppendMessages: %v", err)
		}
		got, _ := repo.GetConversation(ctx, aliceID, conv.ID)
		if len(got.Messages) != 6 {
			t.Fatalf("got %d messages, want 6", len(got.Messages))
		}
		if got.Messages[5].Seq != 6 {
			t.Errorf("last seq = %d, want 6", got.Messages[5].Seq)
		}
	})

	// Another user's conversation is not found rather than forbidden: saying it
	// exists but is not yours is itself a disclosure.
	t.Run("conversations are private to their owner", func(t *testing.T) {
		if _, err := repo.GetConversation(ctx, bobID, conv.ID); err != repository.ErrNotFound {
			t.Errorf("bob got %v reading alice's conversation, want ErrNotFound", err)
		}
		list, err := repo.ListConversations(ctx, bobID)
		if err != nil {
			t.Fatalf("ListConversations: %v", err)
		}
		if len(list) != 0 {
			t.Errorf("bob sees %d of alice's conversations", len(list))
		}
		if err := repo.DeleteConversation(ctx, bobID, conv.ID); err != repository.ErrNotFound {
			t.Errorf("bob could delete alice's conversation: %v", err)
		}
	})

	// A failed turn costs the same tokens as a successful one, so usage that
	// omits failures understates the bill by exactly the turns worth checking.
	t.Run("usage counts failed turns", func(t *testing.T) {
		must := func(run models.AssistantRun) {
			t.Helper()
			if err := repo.RecordRun(ctx, run); err != nil {
				t.Fatalf("RecordRun: %v", err)
			}
		}
		must(models.AssistantRun{ConversationID: conv.ID, UserID: aliceID, Provider: "anthropic",
			Model: "claude-sonnet-5", Rounds: 2, InputTokens: 400, OutputTokens: 60,
			CostUSD: 0.0021, CostKnown: true})
		must(models.AssistantRun{ConversationID: conv.ID, UserID: aliceID, Provider: "anthropic",
			Model: "claude-sonnet-5", Rounds: 1, InputTokens: 100, OutputTokens: 4096,
			CostUSD: 0.0619, CostKnown: true, Error: "ran out of output tokens"})
		// A local model: zero cost, and that is known rather than missing.
		must(models.AssistantRun{ConversationID: conv.ID, UserID: aliceID, Provider: "ollama",
			Model: "qwen3:8b", Rounds: 2, InputTokens: 900, OutputTokens: 120, CostKnown: true})
		// An unpriced cloud model: cost genuinely unknown, stored as NULL.
		must(models.AssistantRun{ConversationID: conv.ID, UserID: aliceID, Provider: "openai",
			Model: "gpt-9-unreleased", Rounds: 1, InputTokens: 10, OutputTokens: 5})

		u, err := repo.Usage(ctx, aliceID)
		if err != nil {
			t.Fatalf("Usage: %v", err)
		}
		if u.Turns != 4 {
			t.Errorf("turns = %d, want 4", u.Turns)
		}
		if u.FailedTurns != 1 {
			t.Errorf("failed = %d, want 1", u.FailedTurns)
		}
		if u.InputTokens != 1410 || u.OutputTokens != 4281 {
			t.Errorf("tokens = %d in / %d out, want 1410 / 4281", u.InputTokens, u.OutputTokens)
		}
		// The failed turn's cost is included. Excluding it would hide the most
		// expensive kind of turn there is: one that produced nothing.
		if u.CostUSD < 0.0639 || u.CostUSD > 0.0641 {
			t.Errorf("cost = %v, want ~0.064 including the failed turn", u.CostUSD)
		}
		if u.UnpricedTurns != 1 {
			t.Errorf("unpriced = %d, want 1", u.UnpricedTurns)
		}
	})

	t.Run("the title is derived from the question", func(t *testing.T) {
		long := "Do I have enough parts on hand right now to build three of the alarm beeper boards without ordering anything"
		c, err := repo.CreateConversation(ctx, aliceID, long, nil, nil)
		if err != nil {
			t.Fatalf("CreateConversation: %v", err)
		}
		if len([]rune(c.Title)) > 62 {
			t.Errorf("title is %d chars: %q", len([]rune(c.Title)), c.Title)
		}
		// Cut on a word boundary, so a truncated title does not end mid-word.
		if trimmed := c.Title[:len(c.Title)-len("…")]; trimmed[len(trimmed)-1] == ' ' {
			t.Errorf("title ends on a space: %q", c.Title)
		}
	})

	t.Run("deleting removes the messages too", func(t *testing.T) {
		c, _ := repo.CreateConversation(ctx, aliceID, "throwaway", nil, nil)
		_ = repo.AppendMessages(ctx, c.ID, []models.ConversationMessage{{Role: "user", Content: "hi"}})
		if err := repo.DeleteConversation(ctx, aliceID, c.ID); err != nil {
			t.Fatalf("DeleteConversation: %v", err)
		}
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT COUNT(*)::int FROM assistant_messages WHERE conversation_id = $1`, c.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%d messages survived the delete", n)
		}
	})
}

// A project id and a board id are both bare uuids, and a model mixes them up.
// Answering only "not found" makes that a dead end when the id names something
// real and the tool could say what.
func TestAssistantToolsExplainAMixedUpID(t *testing.T) {
	url := dbURL(t)
	ctx := context.Background()
	if err := db.Migrate(url); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	truncate := func() {
		if _, err := pool.Exec(ctx, `TRUNCATE projects CASCADE`); err != nil {
			t.Fatalf("truncate: %v", err)
		}
	}
	truncate()
	t.Cleanup(truncate)

	projects := repository.NewProjectRepo(pool)
	proj, err := projects.Create(ctx, "Alarm Beeper", "", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	board := &models.Board{ProjectID: proj.ID, Name: "AlarmBeeper", Revision: "A", Copies: 1}
	if err := projects.CreateBoard(ctx, board); err != nil {
		t.Fatalf("CreateBoard: %v", err)
	}

	box := &assistant.Toolbox{Projects: projects, Stock: repository.NewStockRepo(pool)}

	t.Run("a board id passed to get_project", func(t *testing.T) {
		res := box.Run(ctx, ai.ToolCall{ID: "c", Name: "get_project",
			Input: json.RawMessage(`{"project_id":"` + board.ID.String() + `"}`)})
		if !res.IsError {
			t.Fatal("expected an error")
		}
		if !strings.Contains(res.Content, "board id") || !strings.Contains(res.Content, "board_pick_list") {
			t.Errorf("message = %q; it should name what the id is and the tool to use", res.Content)
		}
	})

	t.Run("a project id passed to board_pick_list", func(t *testing.T) {
		res := box.Run(ctx, ai.ToolCall{ID: "c", Name: "board_pick_list",
			Input: json.RawMessage(`{"board_id":"` + proj.ID.String() + `"}`)})
		if !res.IsError {
			t.Fatal("expected an error")
		}
		if !strings.Contains(res.Content, "project id") || !strings.Contains(res.Content, "get_project") {
			t.Errorf("message = %q; it should name what the id is and the tool to use", res.Content)
		}
	})

	// The MPN is on the BOM line. Nothing exposed it, so the assistant asked the
	// user for a part number that was sitting in the table in front of them.
	t.Run("get_board carries the manufacturer part number", func(t *testing.T) {
		mpn := "RMCF0603JT220R"
		line := &models.BOMLine{
			BoardID: board.ID, Refs: "R7", Quantity: 1, Value: "220\u03a9 (1%)",
			Footprint: "R_0603_1608Metric", MPN: mpn, Position: 1,
		}
		if err := projects.CreateBOMLine(ctx, line); err != nil {
			t.Fatalf("CreateBOMLine: %v", err)
		}
		res := box.Run(ctx, ai.ToolCall{ID: "c", Name: "get_board",
			Input: json.RawMessage(`{"board_id":"` + board.ID.String() + `"}`)})
		if res.IsError {
			t.Fatalf("get_board failed: %s", res.Content)
		}
		if !strings.Contains(res.Content, mpn) {
			t.Errorf("the MPN is not in the result: %s", res.Content)
		}
		if !strings.Contains(res.Content, "R7") {
			t.Errorf("the reference is not in the result: %s", res.Content)
		}
		// An unmatched line has to say so outright; a missing part_id is easy
		// to skim past and it is the whole point of the line.
		if !strings.Contains(res.Content, `"matched":false`) {
			t.Errorf("an unlinked line should be flagged: %s", res.Content)
		}
	})

	// The same detail has to survive into the pick list, which is what answers
	// "can I build this" and therefore what names the parts to go and buy.
	t.Run("the pick list keeps the part number on unmatched lines", func(t *testing.T) {
		res := box.Run(ctx, ai.ToolCall{ID: "c", Name: "board_pick_list",
			Input: json.RawMessage(`{"board_id":"` + board.ID.String() + `"}`)})
		if res.IsError {
			t.Fatalf("board_pick_list failed: %s", res.Content)
		}
		if !strings.Contains(res.Content, "RMCF0603JT220R") {
			t.Errorf("the unmatched line lost its MPN: %s", res.Content)
		}
	})

	t.Run("an id that is neither still says what to call", func(t *testing.T) {
		res := box.Run(ctx, ai.ToolCall{ID: "c", Name: "get_project",
			Input: json.RawMessage(`{"project_id":"11111111-1111-1111-1111-111111111111"}`)})
		if !res.IsError || !strings.Contains(res.Content, "list_projects") {
			t.Errorf("message = %q; a genuine miss should still point somewhere", res.Content)
		}
	})
}
