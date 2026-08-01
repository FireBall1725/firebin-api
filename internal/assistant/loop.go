// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package assistant

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/firelabsca/firebin-api/internal/ai"
)

// maxToolRounds caps how many times one question may go round the tool loop.
//
// A model that keeps calling tools without answering is not making progress, it
// is looping, and each round costs a real request. Eight is enough for the
// questions this is for: find candidates, read two of them, price one, answer.
const maxToolRounds = 8

// ErrNoProvider is returned when the assistant is on but nothing is configured
// to answer with.
var ErrNoProvider = errors.New("no AI provider is configured")

// ErrDisabled is returned when the assistant is switched off.
var ErrDisabled = errors.New("the assistant is switched off")

// Turn is the result of answering one question.
type Turn struct {
	// Text is the answer. Empty when the model stopped without one, which the
	// caller should treat as a failure rather than showing a blank reply.
	Text string `json:"text"`
	// Steps records what was called and what came back, in order. Kept because
	// an answer drawn from the inventory should be checkable: without this, a
	// wrong number is indistinguishable from an invented one.
	Steps []Step `json:"steps"`
	// Usage is summed across every round of the turn, including rounds that
	// only called tools. A turn that took six requests cost six requests.
	Usage ai.UsageInfo `json:"usage"`
	// Rounds is how many provider requests the turn took.
	Rounds int `json:"rounds"`
	// HitRoundLimit is set when the loop stopped because it ran out of rounds
	// rather than because the model finished.
	HitRoundLimit bool `json:"hit_round_limit,omitempty"`
}

// Step is one tool call and its result.
type Step struct {
	Tool    string `json:"tool"`
	Input   string `json:"input"`
	Output  string `json:"output"`
	IsError bool   `json:"is_error,omitempty"`
}

// Runner answers questions with a provider and a toolbox.
type Runner struct {
	Provider ai.ChatProvider
	Tools    *Toolbox
	// System overrides the default system prompt. Empty uses SystemPrompt.
	System string
	// HistoryBudget caps how many tokens of past conversation are replayed.
	// Zero picks a default from where the provider runs.
	HistoryBudget int
}

// Ask runs one question to completion: call the provider, run whatever tools it
// asks for, feed the results back, and repeat until it answers.
//
// history is the conversation so far and is not modified. The second return is
// only what this turn added, question included, so a caller stores it without
// having to work out where the new messages start. It carries the tool calls
// and results as well: without those, a follow-up question arrives with an
// assistant turn whose tool calls have no results, which Anthropic rejects.
//
// The history sent to the provider is trimmed to a token budget first, so it
// can be shorter than what was passed in.
func (r *Runner) Ask(ctx context.Context, history []ai.Message, question string) (*Turn, []ai.Message, error) {
	if r.Provider == nil {
		return nil, nil, ErrNoProvider
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

	for round := 0; round < maxToolRounds; round++ {
		resp, err := r.Provider.Chat(ctx, ai.ChatRequest{
			System:   system,
			Messages: messages,
			Tools:    r.Tools.Defs(),
		})
		if err != nil {
			// Return the partial turn, not nil. It carries the tools already
			// run and the tokens already spent, and both matter: the caller
			// records cost from it, so discarding it loses the spend on exactly
			// the turns that failed, and the steps are the only evidence of how
			// far the turn got. A provider error on round three otherwise looks
			// identical to one on round one.
			return turn, messages[addedFrom:], err
		}
		turn.Rounds++
		turn.Usage.InputTokens += resp.Usage.InputTokens
		turn.Usage.OutputTokens += resp.Usage.OutputTokens
		turn.Usage.EstimatedCostUSD += resp.Usage.EstimatedCostUSD
		turn.Usage.CostKnown = resp.Usage.CostKnown

		// A truncated reply is a failure even when it contains text, because
		// the sentence it stops in the middle of is often the answer.
		if resp.Truncated {
			return turn, messages[addedFrom:], fmt.Errorf("the model ran out of output tokens before finishing")
		}

		messages = append(messages, ai.Message{
			Role: ai.RoleAssistant, Text: resp.Text, ToolCalls: resp.ToolCalls,
		})

		if len(resp.ToolCalls) == 0 {
			// A reply with no text and no tool calls is not an answer. Returning
			// it as one shows the user a blank message and no reason, which is
			// the least debuggable failure there is. Models do this when they
			// run out of context, or when a runtime accepts a request it cannot
			// serve, and it has to be reported rather than rendered.
			if strings.TrimSpace(resp.Text) == "" {
				return turn, messages[addedFrom:], fmt.Errorf(
					"%s returned nothing: no answer and no tool call. The model may be out of context for this conversation, or unable to use tools",
					r.Provider.Info().DisplayName)
			}
			turn.Text = resp.Text
			return turn, messages[addedFrom:], nil
		}

		// Run every call the model made this round, in order, and answer them
		// all in one user turn. Splitting them across turns would break the
		// pairing Anthropic requires.
		results := make([]ai.ToolResult, 0, len(resp.ToolCalls))
		for _, call := range resp.ToolCalls {
			// Check cancellation between tools: a long chain should stop when
			// the caller has gone away, not finish reading the database first.
			if err := ctx.Err(); err != nil {
				return turn, messages[addedFrom:], err
			}
			res := r.Tools.Run(ctx, call)
			results = append(results, res)
			turn.Steps = append(turn.Steps, Step{
				Tool: call.Name, Input: string(call.Input),
				Output: res.Content, IsError: res.IsError,
			})
		}
		messages = append(messages, ai.Message{Role: ai.RoleUser, ToolResults: results})
	}

	// Out of rounds. Report it rather than returning the last partial text as
	// though it were an answer.
	turn.HitRoundLimit = true
	return turn, messages[addedFrom:], fmt.Errorf("gave up after %d rounds of tool calls without an answer", maxToolRounds)
}

// SystemPrompt tells the model what it is looking at and what it must not do.
//
// The rules here are the ones that would otherwise produce confident wrong
// answers: inventing a part that is not stocked, treating an unmatched BOM line
// as a part you have, adding prices in different currencies, or claiming
// distributor availability that FireBin does not store.
const SystemPrompt = `You are the assistant inside FireBin, an electronics component inventory. You answer questions about what the user has, where it is, and what it would cost to buy more.

When a tool fails, say so plainly and say what you were looking for. A failed lookup is not an answer, and a reply that glides past it into a different subject leaves the user thinking the question was answered. Read what the failure says: it often names the mistake and the tool to use instead, and you should follow that rather than give up or ask the user for something you can look up yourself.

Always use the tools. Never answer about the inventory from memory or from what a part number usually means: the only thing that counts is what the tools return. If a search comes back empty, say the part is not in the inventory. That is a real answer, and a useful one.

A tool result earlier in this conversation is a snapshot of the moment it was read, not a standing fact. Stock moves, parts get added, and a bill of materials gets edited. When the user asks you to try again, check again, or look once more, call the tool again rather than re-reading what is already above; an earlier result that lacked something is not evidence that the thing does not exist.

Searching by specification:
- search_parts matches units properly. "220 ohm" will not match a 220 pF capacitor and "100 ohm" will not match a 100 kΩ resistor.
- Package is a separate filter from value. An 0603 220 Ω is package "0603" and value "220 ohm", not a single search string.
- If the exact part is not held, say so first, then offer the closest things that are, and say plainly how they differ.

Answering "can I build this":
- Use board_pick_list. It reads the board's real bill of materials.
- Lines under "unmatched" are BOM entries with no part linked in the inventory. They are unknowns, not parts the user has. Never count them as available.
- If a project has no board with a bill of materials, say so and stop. Do not guess what the design would need.

Looking up a part at a distributor:
- lookup_mpn takes a manufacturer part number, not a description. "220Ω (1%)" is a value and will not find anything.
- A BOM line usually carries the MPN already. Call get_board and read it from the line rather than asking the user for something you can look up yourself.

Talking about price:
- Price breaks carry their own currency and they are not always the same one. Never add or compare prices across different currencies; say which currency each figure is in.
- Watch the minimum order quantity. The cheapest unit price is not the cheapest order if it forces a reel of 5000.
- "Which supplier is best" has no single answer. Say what the trade-off is, in numbers, and let the user choose.
- FireBin does not store distributor stock levels or lead times. Never state or imply that something is in stock at a distributor or how soon it would arrive.

What you can change: nothing, except that you may record a part the user does not own with add_reference_part. You cannot adjust stock, edit an existing part, move anything, or delete anything. If asked to, say plainly that you cannot and what the user would do instead.

Be brief. Give the numbers and where they came from. Do not pad the answer with what the user already knows.`
