// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/firelabsca/firebin-api/internal/ai"
)

// bigResult is the shape that makes a conversation grow: one search result is a
// thousand tokens of JSON against a couple of dozen for the sentence either side.
func bigResult(id string) ai.Message {
	return ai.Message{Role: ai.RoleUser, ToolResults: []ai.ToolResult{
		{CallID: id, Name: "search_parts", Content: strings.Repeat("{\"part\":\"220 ohm 0603\"},", 200)},
	}}
}

func call(id string) ai.Message {
	return ai.Message{Role: ai.RoleAssistant, ToolCalls: []ai.ToolCall{
		{ID: id, Name: "search_parts", Input: json.RawMessage(`{"value":"220 ohm"}`)},
	}}
}

// A conversation of several tool round trips, as the runner stores them.
func longHistory(turns int) []ai.Message {
	var out []ai.Message
	for i := range turns {
		id := string(rune('a' + i))
		out = append(out,
			ai.Message{Role: ai.RoleUser, Text: "question " + id},
			call(id),
			bigResult(id),
			ai.Message{Role: ai.RoleAssistant, Text: "answer " + id},
		)
	}
	return out
}

func TestHistoryUnderBudgetIsUntouched(t *testing.T) {
	h := longHistory(1)
	got := trimHistory(h, 100000)
	if len(got) != len(h) {
		t.Errorf("got %d messages, want all %d", len(got), len(h))
	}
	if total(got) != total(h) {
		t.Error("a conversation that fits should not be edited at all")
	}
}

// The first thing to go is the body of an old tool result, not the message.
// Keeping the message means the model still knows what it asked and when.
func TestOldToolResultsAreStubbedBeforeAnythingIsDropped(t *testing.T) {
	h := longHistory(6)
	got := trimHistory(h, 2000)

	if len(got) != len(h) {
		t.Fatalf("got %d messages, want all %d kept with results stubbed", len(got), len(h))
	}
	if total(got) > 2000 {
		t.Errorf("still %d tokens, over the budget of 2000", total(got))
	}
	// The newest exchange keeps its real result: that is the one being asked about.
	last := got[len(got)-2]
	if len(last.ToolResults) != 1 || strings.Contains(last.ToolResults[0].Content, "omitted") {
		t.Error("the most recent tool result should survive intact")
	}
	// An old one is stubbed rather than deleted, so the call still has an answer.
	if !strings.Contains(got[2].ToolResults[0].Content, "omitted") {
		t.Error("an old tool result should have been stubbed")
	}
	if got[2].ToolResults[0].CallID != got[1].ToolCalls[0].ID {
		t.Error("stubbing must not break the pairing between a call and its result")
	}
}

// Past that, whole messages go, and never in a way that leaves a tool result
// with nothing that asked for it. That pairing is what a provider rejects.
func TestDroppingNeverOrphansAToolResult(t *testing.T) {
	for _, budget := range []int{1500, 800, 400, 200, 100, 50} {
		got := trimHistory(longHistory(8), budget)
		if len(got) == 0 {
			t.Fatalf("budget %d trimmed the conversation to nothing", budget)
		}
		if len(got[0].ToolResults) > 0 {
			t.Errorf("budget %d starts on a tool result, whose call is gone", budget)
		}
		// Every result present must have a call before it.
		seen := map[string]bool{}
		for _, m := range got {
			for _, c := range m.ToolCalls {
				seen[c.ID] = true
			}
			for _, r := range m.ToolResults {
				if !seen[r.CallID] {
					t.Errorf("budget %d: result %q has no matching call", budget, r.CallID)
				}
			}
		}
	}
}

// The newest exchange is what the next question is about, so something always
// survives even at an absurd budget.
func TestSomethingAlwaysSurvives(t *testing.T) {
	got := trimHistory(longHistory(8), 1)
	if len(got) == 0 {
		t.Fatal("trimmed to nothing")
	}
	if len(got[0].ToolResults) > 0 {
		t.Error("what survived starts on an orphaned tool result")
	}
}

func TestEmptyHistoryIsFine(t *testing.T) {
	if got := trimHistory(nil, 1000); got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}

// A local runtime is the case that actually breaks: it is usually configured
// with a few thousand tokens, where a hosted model has a hundred times that.
func TestLocalProvidersGetASmallerBudget(t *testing.T) {
	local := budgetFor(ai.NewOllamaProvider())
	hosted := budgetFor(ai.NewAnthropicProvider())
	if local >= hosted {
		t.Errorf("local budget %d is not smaller than hosted %d", local, hosted)
	}
	if local > 8000 {
		t.Errorf("local budget %d is too large for a runtime serving a 4k window", local)
	}
}

// The note is what tells someone reading the log why an answer lost context.
func TestTheTrimIsReported(t *testing.T) {
	h := longHistory(6)
	if note := historyNote(h, trimHistory(h, 2000)); note == "" {
		t.Error("a trim should be reported")
	}
	if note := historyNote(h, trimHistory(h, 100000)); note != "" {
		t.Errorf("no trim happened but it reported %q", note)
	}
}

// Ask returns only what the turn added, whatever happened to the history.
//
// The caller stores that slice. It used to return the whole transcript and the
// caller sliced it by the length of the history it had passed in, which held
// only while nothing was trimmed: once a long conversation was cut, the slice
// was shorter than that index and the handler panicked, closing the connection
// with no response at all. Seen for real on a forty-message thread.
func TestAskReturnsOnlyThisTurnEvenWhenHistoryIsTrimmed(t *testing.T) {
	provider := &scriptedProvider{replies: []ai.ChatResponse{{Text: "51 parts."}}}
	r := &Runner{Provider: provider, Tools: &Toolbox{}, HistoryBudget: 500}

	long := longHistory(10) // far over the budget, so it will be cut
	turn, added, err := r.Ask(context.Background(), long, "how many parts?")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if turn.Text != "51 parts." {
		t.Errorf("text = %q", turn.Text)
	}
	if len(added) != 2 {
		t.Fatalf("added = %d messages, want the question and the answer", len(added))
	}
	if added[0].Text != "how many parts?" || added[1].Role != ai.RoleAssistant {
		t.Errorf("added = %+v", added)
	}
	// The history that was passed in is not modified.
	if len(long) != 40 {
		t.Errorf("the caller's history was mutated: now %d messages", len(long))
	}
	// And the provider was sent less than it was given.
	sent := provider.seen[0].Messages
	if len(sent) >= len(long) {
		t.Errorf("provider got %d messages from a history of %d; nothing was trimmed", len(sent), len(long))
	}
}
