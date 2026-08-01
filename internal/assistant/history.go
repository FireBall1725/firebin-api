// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package assistant

import (
	"fmt"

	"github.com/firelabsca/firebin-api/internal/ai"
)

// Every question replays the whole conversation, so a thread grows until the
// model refuses it. Tool results are what make it grow: a parametric search or
// a pick list is a thousand tokens of JSON, against a couple of dozen for the
// sentence either side of it. A six-message conversation measured 2,800 tokens
// against real data, almost all of it results.
//
// So the history is trimmed to a budget before it is sent, oldest first, in two
// stages that lose the least useful thing first.

// Budgets are in tokens, and deliberately different by where the model runs. A
// local runtime is usually configured with a few thousand tokens of context and
// is the case that actually breaks; a hosted model has a hundred times that and
// the limit worth respecting there is the bill.
const (
	localHistoryBudget  = 4000
	hostedHistoryBudget = 60000
)

// stubbedResult replaces the body of an old tool result. The call and the
// result still exist, so the pairing a provider requires is intact; only the
// bulk is gone.
const stubbedResult = "[earlier result omitted to keep this conversation within the model's context; call the tool again if you need it]"

// budgetFor picks a history budget for a provider.
func budgetFor(p ai.ChatProvider) int {
	if p != nil && p.Info().Local {
		return localHistoryBudget
	}
	return hostedHistoryBudget
}

// estimateTokens is a cheap size estimate, not a tokeniser.
//
// Four characters per token is the usual rough figure for English and it is
// close enough for JSON too. Being exact would mean shipping a tokeniser per
// provider, and the number is only used to decide what to drop: an estimate
// that is ten percent out drops one more message than it strictly had to.
func estimateTokens(m ai.Message) int {
	n := len(m.Text)
	for _, c := range m.ToolCalls {
		n += len(c.Name) + len(c.Input)
	}
	for _, r := range m.ToolResults {
		n += len(r.Name) + len(r.Content)
	}
	return n/4 + 4 // a few tokens of per-message framing
}

// trimHistory reduces a conversation to fit a token budget.
//
// Two stages, because they lose different things. First the bodies of older
// tool results are replaced with a stub: that removes most of the weight while
// keeping every message, so the model still knows what it asked and roughly
// when. If that is not enough, whole messages are dropped from the oldest end.
//
// Both stages work backwards from the newest message, and dropping stops at a
// message that would orphan a tool call, because a provider rejects an
// assistant turn whose tool_use has no matching tool_result. Splitting that
// pair is the one edit that makes a conversation unusable rather than merely
// shorter.
func trimHistory(history []ai.Message, budget int) []ai.Message {
	if budget <= 0 || len(history) == 0 {
		return history
	}
	if total(history) <= budget {
		return history
	}

	// Stage one: stub old tool results, newest kept whole.
	trimmed := make([]ai.Message, len(history))
	copy(trimmed, history)
	used := 0
	for i := len(trimmed) - 1; i >= 0; i-- {
		used += estimateTokens(trimmed[i])
		if used <= budget || len(trimmed[i].ToolResults) == 0 {
			continue
		}
		// Past the budget and this message is mostly payload: stub it and
		// recount, since the stub is nearly free.
		used -= estimateTokens(trimmed[i])
		results := make([]ai.ToolResult, len(trimmed[i].ToolResults))
		for j, r := range trimmed[i].ToolResults {
			r.Content = stubbedResult
			results[j] = r
		}
		trimmed[i].ToolResults = results
		used += estimateTokens(trimmed[i])
	}
	if total(trimmed) <= budget {
		return trimmed
	}

	// Stage two: drop from the oldest end, keeping whole turns.
	start := 0
	used = 0
	for i := len(trimmed) - 1; i >= 0; i-- {
		used += estimateTokens(trimmed[i])
		if used > budget {
			start = i + 1
			break
		}
	}
	// Never cut between an assistant's tool call and the result answering it.
	// Starting on a message carrying tool results would leave results with
	// nothing that asked for them, which providers reject.
	for start < len(trimmed) && len(trimmed[start].ToolResults) > 0 {
		start++
	}
	// Always keep something. A conversation trimmed to nothing would send the
	// question with no context at all, and silently: better to send the last
	// exchange and be slightly over.
	if start >= len(trimmed) {
		start = len(trimmed) - 1
		for start > 0 && len(trimmed[start].ToolResults) > 0 {
			start--
		}
	}
	return trimmed[start:]
}

func total(msgs []ai.Message) int {
	n := 0
	for _, m := range msgs {
		n += estimateTokens(m)
	}
	return n
}

// historyNote describes a trim for the caller's log. Empty when nothing was cut.
func historyNote(before, after []ai.Message) string {
	if len(before) == len(after) && total(before) == total(after) {
		return ""
	}
	return fmt.Sprintf("trimmed history from %d messages (~%d tokens) to %d (~%d)",
		len(before), total(before), len(after), total(after))
}
