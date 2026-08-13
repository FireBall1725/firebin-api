// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package assistant

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/firelabsca/firebin-api/internal/ai"
)

// EventKind is what a streamed turn is reporting.
const (
	// EventText is a fragment of the answer.
	EventText = "text"
	// EventTool is a tool about to run. Emitted because a turn spends most of
	// its time between text, and a cursor that stops moving for twenty seconds
	// with no explanation reads as a hang. Naming the lookup makes the wait
	// legible rather than merely shorter.
	EventTool = "tool"
	// EventRound marks the start of another provider call, so a caller can tell
	// a long chain from a slow one.
	EventRound = "round"
	// EventRetract tells the caller to throw away the answer text streamed so
	// far this turn.
	//
	// Needed because a stray tool call is only recognisable once the round is
	// complete, and by then its JSON has already been streamed to the screen as
	// if it were the answer. Without this the user watches a tool call being
	// typed out and then replaced, which looks like a glitch rather than a
	// recovery.
	EventRetract = "retract"
	// EventUsage reports the turn's token totals so far.
	//
	// Emitted when a round finishes, because that is when a provider actually
	// reports its counts: the OpenAI-compatible runtimes send usage only in the
	// final frame of a stream, and Anthropic sends the output count near the
	// end. Nothing here is estimated; a caller that wants a number while a
	// round is still running has to say that it is guessing.
	EventUsage = "usage"
)

// Event is one thing that happened during a streamed turn.
type Event struct {
	Kind string `json:"kind"`
	// Text carries the fragment for EventText.
	Text string `json:"text,omitempty"`
	// Tool and Input describe an EventTool.
	Tool  string `json:"tool,omitempty"`
	Input string `json:"input,omitempty"`
	// Round is the provider call number, from 1.
	Round int `json:"round,omitempty"`
	// Usage is the turn's running totals, on EventUsage. Cumulative across
	// rounds, so a caller displays it rather than adding it up.
	Usage *ai.UsageInfo `json:"usage,omitempty"`
}

// AskStream is Ask with the answer reported as it is written.
//
// Falls back to the unstreamed path when the provider cannot stream, emitting
// the finished answer as one text event. A caller therefore does not need two
// code paths, and a provider that gains streaming later needs no change here.
func (r *Runner) AskStream(ctx context.Context, history []ai.Message, question string, emit func(Event)) (*Turn, []ai.Message, error) {
	if r.Provider == nil {
		return nil, nil, ErrNoProvider
	}
	streamer, canStream := r.Provider.(ai.StreamingChatProvider)
	if !canStream {
		turn, msgs, err := r.Ask(ctx, history, question)
		if turn != nil && turn.Text != "" {
			emit(Event{Kind: EventText, Text: turn.Text})
		}
		return turn, msgs, err
	}

	system := r.System
	if system == "" {
		system = SystemPrompt
	}
	// Trimmed before the question is added, so the question itself is never
	// what gets cut.
	budget := r.HistoryBudget
	if budget == 0 {
		budget = budgetFor(r.Provider)
	}
	kept := trimHistory(history, budget)
	if note := historyNote(history, kept); note != "" {
		slog.Info("assistant history trimmed", "detail", note, "model", r.Provider.ConfiguredModel())
	}

	messages := make([]ai.Message, 0, len(kept)+4)
	messages = append(messages, kept...)
	// Everything from here is this turn's, and it is what the caller stores.
	// Returning the whole transcript and letting the caller slice it by the
	// length of the history it passed in was a trap: trimming made the returned
	// slice shorter than that, and slicing past the end panicked, which closes
	// the connection with no response at all.
	addedFrom := len(messages)
	messages = append(messages, ai.Message{Role: ai.RoleUser, Text: question})

	turn := &Turn{}
	turn.Usage.ModelID = r.Provider.ConfiguredModel()

	unparsed := 0
	for round := 0; round < maxToolRounds; round++ {
		emit(Event{Kind: EventRound, Round: round + 1})

		rec := ai.NewRoundRecord()
		started := time.Now()
		resp, err := streamer.ChatStream(ai.WithRecorder(ctx, rec), ai.ChatRequest{
			System:   system,
			Messages: messages,
			Tools:    r.Tools.Defs(),
		}, func(delta string) {
			emit(Event{Kind: EventText, Text: delta})
		})
		r.recordRound(round+1, rec, resp, started, err)
		// Same retry as the unstreamed loop, plus a retraction: the round may
		// have streamed some text before the runtime gave up on it, and that
		// text belongs to an attempt that is being thrown away.
		if errors.Is(err, ai.ErrToolCallUnparsed) && unparsed < maxUnparsedRetries {
			unparsed++
			slog.Warn("retrying a round the runtime could not parse",
				"model", r.Provider.ConfiguredModel(), "attempt", unparsed, "error", err)
			// Not counted against the round budget. That budget exists to stop a
			// model calling tools forever; a round the runtime threw away did no
			// work and looked at nothing, so charging the turn for it just means
			// a question that hit two bad samples has two fewer lookups to answer
			// with. maxUnparsedRetries is the bound here.
			round--
			emit(Event{Kind: EventRetract})
			continue
		}
		if err != nil {
			// The partial turn goes back for the same reason as in Ask: the
			// caller records cost from it, and the steps show how far it got.
			return turn, messages[addedFrom:], err
		}
		turn.Rounds++
		turn.Usage.InputTokens += resp.Usage.InputTokens
		turn.Usage.OutputTokens += resp.Usage.OutputTokens
		turn.Usage.EstimatedCostUSD += resp.Usage.EstimatedCostUSD
		turn.Usage.CostKnown = resp.Usage.CostKnown
		// A copy, so a caller holding the event cannot see later rounds mutate
		// the numbers it was handed.
		running := turn.Usage
		emit(Event{Kind: EventUsage, Usage: &running})

		if resp.Truncated {
			return turn, messages[addedFrom:], fmt.Errorf("the model ran out of output tokens before finishing")
		}

		// Same recovery as the unstreamed loop, plus a retraction: the call's
		// JSON has already been streamed to the screen as though it were the
		// answer, and leaving it there would make the recovery look like a fault.
		if len(resp.ToolCalls) == 0 && strings.TrimSpace(resp.Text) != "" {
			if call, ok := strayToolCall(resp.Text, r.Tools.Defs()); ok {
				slog.Warn("recovered a tool call from the model's answer",
					"model", r.Provider.ConfiguredModel(), "tool", call.Name, "round", round+1)
				resp.ToolCalls = []ai.ToolCall{call}
				resp.Text = ""
				emit(Event{Kind: EventRetract})
			}
		}

		messages = append(messages, ai.Message{
			Role: ai.RoleAssistant, Text: resp.Text, ToolCalls: resp.ToolCalls,
		})

		if len(resp.ToolCalls) == 0 {
			if strings.TrimSpace(resp.Text) == "" {
				return turn, messages[addedFrom:], fmt.Errorf(
					"%s returned nothing: no answer and no tool call. The model may be out of context for this conversation, or unable to use tools",
					r.Provider.Info().DisplayName)
			}
			if isJSONObject(unfence(strings.TrimSpace(resp.Text))) {
				emit(Event{Kind: EventRetract})
				return turn, messages[addedFrom:], fmt.Errorf(
					"%s wrote a tool call instead of making one, and it matched no tool here. This usually means the model's tool calling is unreliable on this runtime; a different model is the fix",
					r.Provider.Info().DisplayName)
			}
			turn.Text = resp.Text
			return turn, messages[addedFrom:], nil
		}

		results := make([]ai.ToolResult, 0, len(resp.ToolCalls))
		for _, call := range resp.ToolCalls {
			// Repaired here as well as inside Run, so the step recorded for the
			// user names the tool that ran and not the mangled name it arrived as.
			call = r.Tools.Normalise(call)
			if err := ctx.Err(); err != nil {
				return turn, messages[addedFrom:], err
			}
			emit(Event{Kind: EventTool, Tool: call.Name, Input: string(call.Input)})
			res := r.Tools.Run(ctx, call)
			results = append(results, res)
			turn.Steps = append(turn.Steps, Step{
				Tool: call.Name, Input: string(call.Input),
				Output: res.Content, IsError: res.IsError,
			})
		}
		messages = append(messages, ai.Message{Role: ai.RoleUser, ToolResults: results})
	}

	turn.HitRoundLimit = true
	return turn, messages[addedFrom:], fmt.Errorf("gave up after %d rounds of tool calls without an answer", maxToolRounds)
}
