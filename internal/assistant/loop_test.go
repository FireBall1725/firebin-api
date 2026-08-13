// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/firelabsca/firebin-api/internal/ai"
	"github.com/firelabsca/firebin-api/internal/models"
)

// scriptedProvider replays canned responses and records what it was sent, so
// the loop can be tested without a model or a network.
type scriptedProvider struct {
	replies []ai.ChatResponse
	err     error
	calls   int
	seen    []ai.ChatRequest
}

func (p *scriptedProvider) Info() ai.ProviderInfo       { return ai.ProviderInfo{Name: "scripted"} }
func (p *scriptedProvider) Configure(map[string]string) {}
func (p *scriptedProvider) Enabled() bool               { return true }
func (p *scriptedProvider) ConfiguredModel() string     { return "scripted-1" }

func (p *scriptedProvider) Chat(_ context.Context, req ai.ChatRequest) (*ai.ChatResponse, error) {
	p.seen = append(p.seen, req)
	if p.err != nil {
		return nil, p.err
	}
	if p.calls >= len(p.replies) {
		// Keep answering rather than running out, so a runaway loop shows up as
		// the round cap rather than as an index panic.
		p.calls++
		return &ai.ChatResponse{ToolCalls: []ai.ToolCall{{ID: "x", Name: "list_categories", Input: json.RawMessage(`{}`)}}}, nil
	}
	r := p.replies[p.calls]
	p.calls++
	return &r, nil
}

func TestAskRunsToolsAndFeedsResultsBack(t *testing.T) {
	provider := &scriptedProvider{replies: []ai.ChatResponse{
		{ToolCalls: []ai.ToolCall{{ID: "c1", Name: "list_categories", Input: json.RawMessage(`{}`)}}},
		{Text: "You have 3 categories."},
	}}
	// No repositories, so list_categories trips a nil dereference. That is on
	// purpose: it exercises the loop plumbing and the panic guard at once, and a
	// failed tool takes the same path back to the model as a successful one.
	r := &Runner{Provider: provider, Tools: &Toolbox{}}

	turn, msgs, err := r.Ask(context.Background(), nil, "how many categories")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if turn.Text != "You have 3 categories." {
		t.Errorf("text = %q", turn.Text)
	}
	if turn.Rounds != 2 {
		t.Errorf("rounds = %d, want 2", turn.Rounds)
	}
	if len(turn.Steps) != 1 || turn.Steps[0].Tool != "list_categories" {
		t.Fatalf("steps = %+v", turn.Steps)
	}

	// The second request must carry the assistant's tool call and a user turn
	// answering it. Anthropic rejects the pair being split or reordered.
	second := provider.seen[1]
	last := second.Messages[len(second.Messages)-1]
	if last.Role != ai.RoleUser || len(last.ToolResults) != 1 {
		t.Fatalf("last message = %+v, want a user turn carrying the tool result", last)
	}
	if last.ToolResults[0].CallID != "c1" {
		t.Errorf("result is paired to %q, want c1", last.ToolResults[0].CallID)
	}
	prior := second.Messages[len(second.Messages)-2]
	if prior.Role != ai.RoleAssistant || len(prior.ToolCalls) != 1 {
		t.Fatalf("the assistant's own tool call must be replayed, got %+v", prior)
	}

	// What comes back is only this turn, and it has to include the tool call
	// and its result, or a follow-up question would send an unanswered call.
	if len(msgs) != 4 {
		t.Errorf("added = %d messages, want question, call, result, answer", len(msgs))
	}
	if msgs[0].Text != "how many categories" {
		t.Errorf("the first added message should be the question, got %+v", msgs[0])
	}
}

// A tool that fails must be reported to the model, not abort the turn: the
// model can pick a different tool or say it cannot answer.
func TestAToolFailureIsReportedToTheModelRatherThanEndingTheTurn(t *testing.T) {
	provider := &scriptedProvider{replies: []ai.ChatResponse{
		{ToolCalls: []ai.ToolCall{{ID: "c1", Name: "get_part", Input: json.RawMessage(`{"part_id":"the blue one"}`)}}},
		{Text: "I need an id from search_parts first."},
	}}
	r := &Runner{Provider: provider, Tools: &Toolbox{}}

	turn, _, err := r.Ask(context.Background(), nil, "tell me about the blue one")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if len(turn.Steps) != 1 || !turn.Steps[0].IsError {
		t.Fatalf("steps = %+v, want one failed step", turn.Steps)
	}
	// The message has to tell the model what it did wrong, not just "invalid".
	if !strings.Contains(turn.Steps[0].Output, "not a name") {
		t.Errorf("failure text = %q; it should explain that an id is required", turn.Steps[0].Output)
	}
	if turn.Text == "" {
		t.Error("the turn should still have produced an answer")
	}
}

// A hallucinated tool name is recoverable if the model is told what does exist.
func TestAnUnknownToolNamesTheRealOnes(t *testing.T) {
	box := &Toolbox{}
	res := box.Run(context.Background(), ai.ToolCall{ID: "c", Name: "delete_everything"})
	if !res.IsError {
		t.Fatal("an unknown tool must be an error")
	}
	if !strings.Contains(res.Content, "search_parts") {
		t.Errorf("content = %q, want it to list the real tools", res.Content)
	}
}

// The write tools simply are not there unless wired, so "add stock" cannot be
// answered by a prompt injection, only by a tool that does not exist.
func TestWritesAreAbsentNotForbidden(t *testing.T) {
	box := &Toolbox{}
	names := strings.Join(box.names(), " ")
	for _, forbidden := range []string{"adjust_stock", "update_part", "delete_part", "move_stock", "create_location"} {
		if strings.Contains(names, forbidden) {
			t.Errorf("%s is reachable; the assistant must not be able to call it", forbidden)
		}
	}
	// And the one write is opt-in: absent until a creator is supplied.
	if strings.Contains(names, "add_reference_part") {
		t.Error("add_reference_part should not exist without a creator wired in")
	}
	// Wiring it in is what makes it exist.
	box.CreateReferencePart = func(context.Context, ReferencePartInput) (*models.Part, error) { return nil, nil }
	if !strings.Contains(strings.Join(box.names(), " "), "add_reference_part") {
		t.Error("add_reference_part should appear once a creator is wired in")
	}
}

// A model that never answers must stop, not spend requests forever.
func TestTheLoopStopsInsteadOfCallingToolsForever(t *testing.T) {
	provider := &scriptedProvider{} // always calls a tool, never answers
	r := &Runner{Provider: provider, Tools: &Toolbox{}}

	turn, _, err := r.Ask(context.Background(), nil, "go forever")
	if err == nil {
		t.Fatal("expected an error when the model never answers")
	}
	if turn == nil || !turn.HitRoundLimit {
		t.Fatalf("turn = %+v, want the round limit flagged", turn)
	}
	if provider.calls != maxToolRounds {
		t.Errorf("made %d requests, want the cap of %d", provider.calls, maxToolRounds)
	}
}

// A truncated reply is a failure. The sentence it stops in the middle of is
// usually the answer.
func TestATruncatedReplyIsAFailure(t *testing.T) {
	provider := &scriptedProvider{replies: []ai.ChatResponse{
		{Text: "You have 21 parts in 0603, of which", Truncated: true},
	}}
	r := &Runner{Provider: provider, Tools: &Toolbox{}}
	if _, _, err := r.Ask(context.Background(), nil, "q"); err == nil {
		t.Error("a truncated answer must not be returned as though it were complete")
	}
}

func TestUsageIsSummedAcrossEveryRound(t *testing.T) {
	provider := &scriptedProvider{replies: []ai.ChatResponse{
		{ToolCalls: []ai.ToolCall{{ID: "c1", Name: "list_categories", Input: json.RawMessage(`{}`)}},
			Usage: ai.UsageInfo{InputTokens: 100, OutputTokens: 20, EstimatedCostUSD: 0.001, CostKnown: true}},
		{Text: "done", Usage: ai.UsageInfo{InputTokens: 300, OutputTokens: 40, EstimatedCostUSD: 0.003, CostKnown: true}},
	}}
	r := &Runner{Provider: provider, Tools: &Toolbox{}}
	turn, _, err := r.Ask(context.Background(), nil, "q")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	// A turn that took two requests cost two requests, not just the last one.
	if turn.Usage.InputTokens != 400 || turn.Usage.OutputTokens != 60 {
		t.Errorf("usage = %+v, want the rounds summed", turn.Usage)
	}
	if turn.Usage.EstimatedCostUSD < 0.0039 || turn.Usage.EstimatedCostUSD > 0.0041 {
		t.Errorf("cost = %v, want ~0.004", turn.Usage.EstimatedCostUSD)
	}
}

func TestAskWithoutAProviderIsAClearError(t *testing.T) {
	r := &Runner{Tools: &Toolbox{}}
	_, _, err := r.Ask(context.Background(), nil, "q")
	if !errors.Is(err, ErrNoProvider) {
		t.Errorf("err = %v, want ErrNoProvider", err)
	}
}

// The system prompt has to carry the rules that stop confident wrong answers.
// Asserted because they are load-bearing, not decoration.
func TestSystemPromptCarriesTheRulesThatPreventWrongAnswers(t *testing.T) {
	for _, must := range []string{
		"unmatched", // BOM lines with no part are not parts you have
		"currency",  // never add across currencies
		"lead time", // FireBin does not store distributor availability
		"220 pF",    // units are not interchangeable
		"snapshot",  // an earlier tool result is not a standing fact
		"try again", // asked to re-check, call the tool rather than re-reading
	} {
		if !strings.Contains(SystemPrompt, must) {
			t.Errorf("the system prompt no longer mentions %q", must)
		}
	}
}

// A search result must not carry a part's whole parameter list.
//
// Enriched parts have thirty-odd parameters each. Returning all of them for
// every hit measured 10,600 tokens for one 25-part search against real data,
// against about 1,200 trimmed. That is paid on every question, so it is worth
// pinning: the fix is easy to undo by adding one convenient-looking field.
func TestSearchResultsCarryOnlyWhatMatched(t *testing.T) {
	units := "Ω"
	m := models.PartMatch{
		Part: models.Part{Name: "100 Ω Resistor", TotalStock: 50},
		Matched: []models.PartParameter{
			{TemplateName: "Resistance", Value: "100", Units: &units},
		},
	}
	// The full list is what a real enriched part looks like.
	m.Parameters = append(m.Matched,
		models.PartParameter{TemplateName: "China RoHS", Value: "Non-Compliant"},
		models.PartParameter{TemplateName: "Case Code (Imperial)", Value: "0603"},
	)

	row := searchRow(m)
	if _, ok := row["parameters"]; ok {
		t.Error("a search row must not include the full parameter list; get_part is for that")
	}
	matched, _ := row["matched"].([]map[string]string)
	if len(matched) != 1 || matched[0]["name"] != "Resistance" || matched[0]["unit"] != "Ω" {
		t.Errorf("matched = %+v, want just the Resistance parameter with its unit", matched)
	}
}

// A part recorded but not owned must say so. Reported as "in stock: 0"
// otherwise, which is true and misleading: it reads as sold out rather than as
// never stocked.
func TestAReferenceOnlyPartIsLabelled(t *testing.T) {
	row := searchRow(models.PartMatch{Part: models.Part{Name: "TPS54331", ReferenceOnly: true}})
	if row["reference_only"] != true {
		t.Error("a reference-only part must be flagged in search results")
	}
	if _, ok := row["note"]; !ok {
		t.Error("the flag needs a plain-language note; a bare boolean is easy for a model to ignore")
	}
}

// A reply with neither text nor a tool call is not an answer.
//
// Returning it as one renders an empty assistant message with no explanation,
// which is the least debuggable failure there is: nothing to read, nothing in
// the log, and the question apparently accepted. Seen for real from a local
// runtime that took the request and produced nothing.
func TestABlankReplyIsAFailureNotAnAnswer(t *testing.T) {
	provider := &scriptedProvider{replies: []ai.ChatResponse{{Text: "   "}}}
	r := &Runner{Provider: provider, Tools: &Toolbox{}}

	turn, _, err := r.Ask(context.Background(), nil, "how many parts do I have?")
	if err == nil {
		t.Fatal("a blank reply must be reported as a failure")
	}
	if !strings.Contains(err.Error(), "nothing") {
		t.Errorf("error = %q; it should say the model returned nothing", err)
	}
	// The turn still comes back so its cost is recorded: a blank answer is not
	// a free one.
	if turn == nil || turn.Rounds != 1 {
		t.Errorf("turn = %+v, want the round counted", turn)
	}
}

// A provider failure part way through must not discard the turn.
//
// The caller records cost from the turn, so returning nil loses the spend on
// exactly the turns that failed, which are the ones worth knowing about. The
// steps are also the only evidence of how far it got: a failure on round three
// otherwise looks identical to one on round one.
func TestAProviderFailureKeepsTheWorkAlreadyDone(t *testing.T) {
	provider := &failAfterProvider{
		first: ai.ChatResponse{
			ToolCalls: []ai.ToolCall{{ID: "c1", Name: "list_categories", Input: json.RawMessage(`{}`)}},
			Usage:     ai.UsageInfo{InputTokens: 500, OutputTokens: 30},
		},
		err: errors.New("osaurus: http 500: Exceeded model context window size"),
	}
	r := &Runner{Provider: provider, Tools: &Toolbox{}}

	turn, _, err := r.Ask(context.Background(), nil, "q")
	if err == nil {
		t.Fatal("expected the provider error")
	}
	if turn == nil {
		t.Fatal("the turn was discarded, so the tokens it spent go unrecorded")
	}
	if turn.Usage.InputTokens != 500 {
		t.Errorf("usage = %+v, want the first round's tokens kept", turn.Usage)
	}
	if len(turn.Steps) != 1 || turn.Steps[0].Tool != "list_categories" {
		t.Errorf("steps = %+v, want the tool that did run", turn.Steps)
	}
}

// failAfterProvider answers once, then fails.
type failAfterProvider struct {
	first ai.ChatResponse
	err   error
	calls int
}

func (p *failAfterProvider) Info() ai.ProviderInfo {
	return ai.ProviderInfo{Name: "f", DisplayName: "F"}
}
func (p *failAfterProvider) Configure(map[string]string) {}
func (p *failAfterProvider) Enabled() bool               { return true }
func (p *failAfterProvider) ConfiguredModel() string     { return "f-1" }
func (p *failAfterProvider) Chat(context.Context, ai.ChatRequest) (*ai.ChatResponse, error) {
	p.calls++
	if p.calls == 1 {
		r := p.first
		return &r, nil
	}
	return nil, p.err
}

// streamingProvider is a scriptedProvider that also streams, splitting each
// reply into fragments so the caller sees more than one.
type streamingProvider struct{ scriptedProvider }

func (p *streamingProvider) ChatStream(ctx context.Context, req ai.ChatRequest, onText func(string)) (*ai.ChatResponse, error) {
	resp, err := p.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	for _, word := range strings.SplitAfter(resp.Text, " ") {
		if word != "" {
			onText(word)
		}
	}
	return resp, nil
}

func TestAskStreamReportsTextAndToolsInOrder(t *testing.T) {
	provider := &streamingProvider{scriptedProvider{replies: []ai.ChatResponse{
		{ToolCalls: []ai.ToolCall{{ID: "c1", Name: "list_categories", Input: json.RawMessage(`{}`)}}},
		{Text: "You have 3 categories."},
	}}}
	r := &Runner{Provider: provider, Tools: &Toolbox{}}

	var events []Event
	turn, msgs, err := r.AskStream(context.Background(), nil, "how many categories", func(e Event) {
		events = append(events, e)
	})
	if err != nil {
		t.Fatalf("AskStream: %v", err)
	}

	// Usage events are filtered out of the sequence check: they are reported
	// whenever a count becomes known, and pinning where they land would make
	// this test fail every time that timing changes without anything breaking.
	var kinds []string
	var text strings.Builder
	usageEvents := 0
	for _, e := range events {
		if e.Kind == EventUsage {
			usageEvents++
			continue
		}
		kinds = append(kinds, e.Kind)
		if e.Kind == EventText {
			text.WriteString(e.Text)
		}
	}
	// The tool has to be announced before the round that follows it, or the UI
	// cannot show what it is waiting on.
	want := []string{EventRound, EventTool, EventRound, EventText, EventText, EventText, EventText}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Errorf("events = %v, want %v", kinds, want)
	}
	if usageEvents == 0 {
		t.Error("no usage was reported during the turn")
	}
	if text.String() != "You have 3 categories." {
		t.Errorf("streamed text = %q", text.String())
	}
	if turn.Text != "You have 3 categories." {
		t.Errorf("turn text = %q; the whole answer must still be there for storage", turn.Text)
	}
	// What is stored has to match what the unstreamed path produces, or a
	// conversation would read differently depending on how it was asked.
	if len(msgs) != 4 {
		t.Errorf("added = %d messages, want question, call, result, answer", len(msgs))
	}
}

// A provider that cannot stream must still work, without the caller needing a
// second code path.
func TestAskStreamFallsBackForANonStreamingProvider(t *testing.T) {
	provider := &scriptedProvider{replies: []ai.ChatResponse{{Text: "49 parts."}}}
	r := &Runner{Provider: provider, Tools: &Toolbox{}}

	var events []Event
	turn, _, err := r.AskStream(context.Background(), nil, "how many", func(e Event) { events = append(events, e) })
	if err != nil {
		t.Fatalf("AskStream: %v", err)
	}
	if turn.Text != "49 parts." {
		t.Errorf("turn text = %q", turn.Text)
	}
	if len(events) != 1 || events[0].Kind != EventText || events[0].Text != "49 parts." {
		t.Errorf("events = %+v, want the whole answer as one text event", events)
	}
}

// The streamed path must fail on the same things the unstreamed one does.
func TestAskStreamRejectsABlankReply(t *testing.T) {
	provider := &streamingProvider{scriptedProvider{replies: []ai.ChatResponse{{Text: ""}}}}
	r := &Runner{Provider: provider, Tools: &Toolbox{}}
	if _, _, err := r.AskStream(context.Background(), nil, "q", func(Event) {}); err == nil {
		t.Error("a blank streamed reply must be a failure, as it is unstreamed")
	}
}

// Token totals are reported as they become known, and they are cumulative so a
// caller displays them rather than adding up.
func TestAskStreamReportsRunningTokenTotals(t *testing.T) {
	provider := &streamingProvider{scriptedProvider{replies: []ai.ChatResponse{
		{ToolCalls: []ai.ToolCall{{ID: "c1", Name: "list_categories", Input: json.RawMessage(`{}`)}},
			Usage: ai.UsageInfo{InputTokens: 500, OutputTokens: 20}},
		{Text: "done", Usage: ai.UsageInfo{InputTokens: 900, OutputTokens: 35}},
	}}}
	r := &Runner{Provider: provider, Tools: &Toolbox{}}

	var totals []ai.UsageInfo
	turn, _, err := r.AskStream(context.Background(), nil, "q", func(e Event) {
		if e.Kind == EventUsage && e.Usage != nil {
			totals = append(totals, *e.Usage)
		}
	})
	if err != nil {
		t.Fatalf("AskStream: %v", err)
	}
	if len(totals) != 2 {
		t.Fatalf("got %d usage events, want one per round", len(totals))
	}
	if totals[0].InputTokens != 500 || totals[0].OutputTokens != 20 {
		t.Errorf("first = %+v, want the first round's counts", totals[0])
	}
	// Cumulative, not per round.
	if totals[1].InputTokens != 1400 || totals[1].OutputTokens != 55 {
		t.Errorf("second = %+v, want the running total", totals[1])
	}
	if turn.Usage.InputTokens != totals[1].InputTokens {
		t.Error("the last usage event should match the finished turn")
	}
}

// The events are snapshots. A caller holding one must not see it change when a
// later round adds to the total.
func TestUsageEventsAreSnapshots(t *testing.T) {
	provider := &streamingProvider{scriptedProvider{replies: []ai.ChatResponse{
		{ToolCalls: []ai.ToolCall{{ID: "c1", Name: "low_stock", Input: json.RawMessage(`{}`)}},
			Usage: ai.UsageInfo{InputTokens: 100}},
		{Text: "done", Usage: ai.UsageInfo{InputTokens: 200}},
	}}}
	r := &Runner{Provider: provider, Tools: &Toolbox{}}

	var first *ai.UsageInfo
	_, _, err := r.AskStream(context.Background(), nil, "q", func(e Event) {
		if e.Kind == EventUsage && first == nil {
			first = e.Usage
		}
	})
	if err != nil {
		t.Fatalf("AskStream: %v", err)
	}
	if first == nil || first.InputTokens != 100 {
		t.Errorf("the first event now reads %+v; it was mutated by a later round", first)
	}
}

// A local runtime that loses the tool-call encoding writes the call into the
// answer instead. The model picked the tool and the arguments; only the wire
// format failed, so the turn should carry on rather than showing the user a
// JSON object where the answer belongs.
func TestAToolCallWrittenAsTheAnswerIsRecovered(t *testing.T) {
	provider := &scriptedProvider{replies: []ai.ChatResponse{
		{Text: `{"name":"list_categories","arguments":{}}`},
		{Text: "You have 3 categories."},
	}}
	r := &Runner{Provider: provider, Tools: &Toolbox{}}

	turn, _, err := r.Ask(context.Background(), nil, "how many categories")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if turn.Text != "You have 3 categories." {
		t.Errorf("text = %q, want the answer from the round after the recovery", turn.Text)
	}
	if len(turn.Steps) != 1 || turn.Steps[0].Tool != "list_categories" {
		t.Fatalf("the recovered call did not run: steps = %+v", turn.Steps)
	}
}

// A JSON object that is not any tool's call is not an answer either. Rendering
// it as one is how this failure reached the user in the first place.
func TestAnUnrecognisableJSONReplyIsAFailureNotAnAnswer(t *testing.T) {
	provider := &scriptedProvider{replies: []ai.ChatResponse{
		{Text: `{"datasheet_id":"x","page":64,"unexpected":true}`},
	}}
	r := &Runner{Provider: provider, Tools: &Toolbox{}}

	turn, _, err := r.Ask(context.Background(), nil, "max voltage")
	if err == nil {
		t.Fatal("a raw tool call was accepted as the answer")
	}
	if turn.Text != "" {
		t.Errorf("text = %q, want it withheld", turn.Text)
	}
	if !strings.Contains(err.Error(), "tool call") {
		t.Errorf("error does not say what went wrong: %v", err)
	}
}

// unparsedThenFine fails the first n rounds the way Ollama does when the model
// writes tool-call arguments it cannot parse, then behaves.
type unparsedThenFine struct {
	fails  int
	calls  int
	answer string
}

func (p *unparsedThenFine) Info() ai.ProviderInfo {
	return ai.ProviderInfo{Name: "flaky", DisplayName: "Flaky"}
}
func (p *unparsedThenFine) Configure(map[string]string) {}
func (p *unparsedThenFine) Enabled() bool               { return true }
func (p *unparsedThenFine) ConfiguredModel() string     { return "flaky-1" }
func (p *unparsedThenFine) Chat(context.Context, ai.ChatRequest) (*ai.ChatResponse, error) {
	p.calls++
	if p.calls <= p.fails {
		return nil, fmt.Errorf("ollama: %w", ai.ErrToolCallUnparsed)
	}
	return &ai.ChatResponse{Text: p.answer}, nil
}

// The runtime throwing away the model's tool call is a sampling accident, not a
// property of the question. Asking again is safe: a round that errored added
// nothing to the conversation.
func TestARoundTheRuntimeCouldNotParseIsRetried(t *testing.T) {
	provider := &unparsedThenFine{fails: 2, answer: "3.6 V absolute maximum."}
	r := &Runner{Provider: provider, Tools: &Toolbox{}}

	turn, _, err := r.Ask(context.Background(), nil, "max voltage")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if turn.Text != "3.6 V absolute maximum." {
		t.Errorf("text = %q", turn.Text)
	}
	if provider.calls != 3 {
		t.Errorf("provider called %d times, want 2 retries and an answer", provider.calls)
	}
}

// A model whose tool calling is simply broken has to be reported, not retried
// until the round budget is gone.
func TestRetriesForAnUnparseableToolCallAreCapped(t *testing.T) {
	provider := &unparsedThenFine{fails: 99}
	r := &Runner{Provider: provider, Tools: &Toolbox{}}

	if _, _, err := r.Ask(context.Background(), nil, "max voltage"); err == nil {
		t.Fatal("a permanently broken runtime was never reported")
	}
	if provider.calls != maxUnparsedRetries+1 {
		t.Errorf("provider called %d times, want %d", provider.calls, maxUnparsedRetries+1)
	}
}
