// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The four providers speak three different tool-calling dialects. These tests
// pin each dialect against a fake server, because the differences are exactly
// the kind that compile fine and fail at runtime against a real key.

// fakeProvider serves one canned reply and records the request it was sent.
func fakeProvider(t *testing.T, reply string) (*httptest.Server, *[]byte) {
	t.Helper()
	var got []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

// A conversation that has already been round a tool call once, which is the
// shape that breaks if a provider mis-orders or mis-pairs the blocks.
func toolRoundTrip() []Message {
	return []Message{
		{Role: RoleUser, Text: "do I have an 0603 220 ohm"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{
			{ID: "call_abc", Name: "search_parts", Input: json.RawMessage(`{"package":"0603"}`)},
		}},
		{Role: RoleUser, ToolResults: []ToolResult{
			{CallID: "call_abc", Name: "search_parts", Content: "no matches"},
		}},
	}
}

func TestAnthropicWireFormat(t *testing.T) {
	srv, got := fakeProvider(t, `{
		"content":[
			{"type":"text","text":"Looking."},
			{"type":"tool_use","id":"toolu_1","name":"search_parts","input":{"package":"0603"}}
		],
		"stop_reason":"tool_use",
		"usage":{"input_tokens":100,"output_tokens":20}
	}`)

	p := NewAnthropicProvider()
	p.Configure(map[string]string{"api_key": "k", "model": "claude-sonnet-5", "base_url": srv.URL})

	resp, err := p.Chat(context.Background(), ChatRequest{
		System:   "sys",
		Messages: toolRoundTrip(),
		Tools:    []ToolDef{{Name: "search_parts", Description: "d", Schema: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Text != "Looking." {
		t.Errorf("text = %q", resp.Text)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "search_parts" || resp.ToolCalls[0].ID != "toolu_1" {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	if string(resp.ToolCalls[0].Input) != `{"package":"0603"}` {
		t.Errorf("input = %s", resp.ToolCalls[0].Input)
	}
	if !resp.Usage.CostKnown || resp.Usage.EstimatedCostUSD == 0 {
		t.Errorf("a priced model should report a cost, got %+v", resp.Usage)
	}

	var sent anthropicRequest
	if err := json.Unmarshal(*got, &sent); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if sent.System != "sys" {
		t.Error("system prompt is a top-level field for Anthropic, not a message")
	}
	if len(sent.Tools) != 1 || sent.Tools[0].Name != "search_parts" {
		t.Fatalf("tools = %+v", sent.Tools)
	}
	// Anthropic rejects a turn whose tool_result is not the first block.
	last := sent.Messages[len(sent.Messages)-1]
	if last.Role != RoleUser || len(last.Content) == 0 || last.Content[0].Type != "tool_result" {
		t.Fatalf("tool_result must lead the answering user turn, got %+v", last)
	}
	if last.Content[0].ToolUseID != "call_abc" {
		t.Errorf("tool_use_id = %q, want the id of the call it answers", last.Content[0].ToolUseID)
	}
}

func TestAnthropicReportsTruncation(t *testing.T) {
	srv, _ := fakeProvider(t, `{"content":[{"type":"text","text":"half an ans"}],
		"stop_reason":"max_tokens","usage":{"input_tokens":1,"output_tokens":1}}`)
	p := NewAnthropicProvider()
	p.Configure(map[string]string{"api_key": "k", "base_url": srv.URL})

	resp, err := p.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Text: "hi"}}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if !resp.Truncated {
		t.Error("a max_tokens stop must be reported, not passed off as a complete answer")
	}
}

func TestAnthropicSurfacesTheAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"type":"invalid_request_error","message":"model: nope not found"}}`)
	}))
	defer srv.Close()

	p := NewAnthropicProvider()
	p.Configure(map[string]string{"api_key": "k", "base_url": srv.URL})
	_, err := p.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Text: "hi"}}})
	if err == nil {
		t.Fatal("expected an error")
	}
	// The provider's own words are what tell the user what to fix.
	if !strings.Contains(err.Error(), "nope not found") {
		t.Errorf("error should carry the API's message, got %q", err)
	}
}

func TestOpenAIWireFormat(t *testing.T) {
	srv, got := fakeProvider(t, `{
		"choices":[{"message":{"role":"assistant","content":"",
			"tool_calls":[{"id":"call_1","type":"function",
				"function":{"name":"search_parts","arguments":"{\"package\":\"0603\"}"}}]},
			"finish_reason":"tool_calls"}],
		"usage":{"prompt_tokens":50,"completion_tokens":10}
	}`)

	p := NewOpenAIProvider()
	p.Configure(map[string]string{"api_key": "k", "model": "gpt-4.1", "base_url": srv.URL})

	resp, err := p.Chat(context.Background(), ChatRequest{
		System:   "sys",
		Messages: toolRoundTrip(),
		Tools:    []ToolDef{{Name: "search_parts", Schema: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	// OpenAI sends arguments as a JSON string; it has to come back as an object.
	if len(resp.ToolCalls) != 1 || string(resp.ToolCalls[0].Input) != `{"package":"0603"}` {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}

	var sent oaiRequest
	if err := json.Unmarshal(*got, &sent); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if sent.Messages[0].Role != "system" {
		t.Error("the system prompt is the first message for OpenAI")
	}
	// A tool result is its own message with role "tool", carrying the call id.
	var toolMsg *oaiMessage
	for i := range sent.Messages {
		if sent.Messages[i].Role == "tool" {
			toolMsg = &sent.Messages[i]
		}
	}
	if toolMsg == nil {
		t.Fatal("no tool-role message was sent")
	}
	if toolMsg.ToolCallID != "call_abc" {
		t.Errorf("tool_call_id = %q", toolMsg.ToolCallID)
	}
	// And the assistant's own call must be echoed back with arguments as a string.
	for _, m := range sent.Messages {
		if m.Role == RoleAssistant && len(m.ToolCalls) > 0 {
			if m.ToolCalls[0].Function.Arguments != `{"package":"0603"}` {
				t.Errorf("arguments must be sent as a JSON string, got %q", m.ToolCalls[0].Function.Arguments)
			}
		}
	}
}

// Some OpenAI-compatible runtimes omit the tool-call id. Without one, a result
// can never be paired to its call and the next turn is rejected.
func TestOpenAICompatibleSynthesisesAMissingToolCallID(t *testing.T) {
	srv, _ := fakeProvider(t, `{"choices":[{"message":{"role":"assistant",
		"tool_calls":[{"type":"function","function":{"name":"t","arguments":""}}]},
		"finish_reason":"tool_calls"}],"usage":{}}`)

	p := NewOsaurusProvider()
	p.Configure(map[string]string{"base_url": srv.URL})
	resp, err := p.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Text: "hi"}}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ID == "" {
		t.Fatalf("tool calls = %+v, want a synthesised id", resp.ToolCalls)
	}
	// Empty arguments must still parse as an object, not as invalid JSON.
	if !json.Valid(resp.ToolCalls[0].Input) {
		t.Errorf("input %q is not valid JSON", resp.ToolCalls[0].Input)
	}
}

// Osaurus appends a telemetry trailer after U+FFFE that is not part of the reply.
func TestOsaurusStripsItsStatsTrailer(t *testing.T) {
	srv, _ := fakeProvider(t, `{"choices":[{"message":{"role":"assistant",
		"content":"You have 21 parts in 0603.￾stats: 42 tok/s"},"finish_reason":"stop"}],"usage":{}}`)
	p := NewOsaurusProvider()
	p.Configure(map[string]string{"base_url": srv.URL})

	resp, err := p.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Text: "hi"}}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Text != "You have 21 parts in 0603." {
		t.Errorf("text = %q, want the trailer removed", resp.Text)
	}
}

// A local provider costs nothing, and that is known rather than missing.
func TestLocalProviderReportsAKnownZeroCost(t *testing.T) {
	srv, _ := fakeProvider(t, `{"message":{"role":"assistant","content":"hi"},
		"done_reason":"stop","prompt_eval_count":7,"eval_count":3}`)
	p := NewOllamaProvider()
	p.Configure(map[string]string{"base_url": srv.URL})

	resp, err := p.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Text: "hi"}}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if !resp.Usage.CostKnown || resp.Usage.EstimatedCostUSD != 0 {
		t.Errorf("usage = %+v, want a known zero cost", resp.Usage)
	}
	if resp.Usage.InputTokens != 7 || resp.Usage.OutputTokens != 3 {
		t.Errorf("Ollama names its token counts differently; got %+v", resp.Usage)
	}
}

func TestOllamaWireFormat(t *testing.T) {
	srv, got := fakeProvider(t, `{"message":{"role":"assistant","content":"",
		"tool_calls":[{"function":{"name":"search_parts","arguments":{"package":"0603"}}}]},
		"done_reason":"stop","prompt_eval_count":1,"eval_count":1}`)

	p := NewOllamaProvider()
	p.Configure(map[string]string{"base_url": srv.URL, "model": "qwen3:8b"})
	resp, err := p.Chat(context.Background(), ChatRequest{
		Messages: toolRoundTrip(),
		Tools:    []ToolDef{{Name: "search_parts", Schema: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	// Ollama sends arguments as an object already.
	if len(resp.ToolCalls) != 1 || string(resp.ToolCalls[0].Input) != `{"package":"0603"}` {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	if resp.ToolCalls[0].ID == "" {
		t.Error("Ollama sends no id, so one must be synthesised to pair the result")
	}

	var sent ollamaRequest
	if err := json.Unmarshal(*got, &sent); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if sent.Stream {
		t.Error("stream must be false; a streamed body is not one JSON document")
	}
	// Ollama pairs a result to its call by tool name, having no ids.
	var toolMsg *ollamaMessage
	for i := range sent.Messages {
		if sent.Messages[i].Role == "tool" {
			toolMsg = &sent.Messages[i]
		}
	}
	if toolMsg == nil || toolMsg.ToolName != "search_parts" {
		t.Fatalf("tool message = %+v, want it named for the call it answers", toolMsg)
	}
}

// Local models emit reasoning that is not part of the answer. Showing it would
// be showing the user three paragraphs of deliberation before one sentence of
// reply.
func TestThinkingIsStripped(t *testing.T) {
	cases := []struct{ in, want string }{
		{"<think>hmm, 0603 is a package</think>You have 21.", "You have 21."},
		{"before<think>a</think>middle<think>b</think>after", "beforemiddleafter"},
		{"<think>cut off mid thou", ""},
		{"no tags here", "no tags here"},
	}
	for _, c := range cases {
		if got := stripThinking(c.in); got != c.want {
			t.Errorf("stripThinking(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A model that answers in prose instead of calling the tool cannot search an
// inventory. The connectivity test has to fail it, not pass it.
func TestTestFailsAModelThatIgnoresTools(t *testing.T) {
	srv, _ := fakeProvider(t, `{"message":{"role":"assistant","content":"The sum is 5."},
		"done_reason":"stop","prompt_eval_count":1,"eval_count":1}`)

	reg := NewRegistry()
	p := NewOllamaProvider()
	reg.Register(p)
	svc := NewService(reg, newMemStore(), ErrNotFound)
	if err := svc.Configure(context.Background(), "ollama", map[string]string{"base_url": srv.URL}); err != nil {
		t.Fatalf("configure: %v", err)
	}

	res, err := svc.Test(context.Background(), "ollama")
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if res.OK {
		t.Error("a model that never calls the tool must not pass the connectivity test")
	}
	if !strings.Contains(res.Error, "tool") {
		t.Errorf("the reason should name tool calling, got %q", res.Error)
	}
}

func TestTestPassesWhenTheModelCallsTheTool(t *testing.T) {
	srv, _ := fakeProvider(t, `{"message":{"role":"assistant",
		"tool_calls":[{"function":{"name":"add","arguments":{"answer":5}}}]},
		"done_reason":"stop","prompt_eval_count":1,"eval_count":1}`)

	reg := NewRegistry()
	reg.Register(NewOllamaProvider())
	svc := NewService(reg, newMemStore(), ErrNotFound)
	if err := svc.Configure(context.Background(), "ollama", map[string]string{"base_url": srv.URL}); err != nil {
		t.Fatalf("configure: %v", err)
	}

	res, err := svc.Test(context.Background(), "ollama")
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if !res.OK {
		t.Errorf("expected a pass, got %+v", res)
	}
}

// A failed provider call is reported in the body with HTTP 200 so the settings
// page can show the provider's own error rather than a generic failure.
func TestTestReportsFailureInTheBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
	}))
	defer srv.Close()

	reg := NewRegistry()
	reg.Register(NewAnthropicProvider())
	svc := NewService(reg, newMemStore(), ErrNotFound)
	if err := svc.Configure(context.Background(), "anthropic", map[string]string{"api_key": "bad", "base_url": srv.URL}); err != nil {
		t.Fatalf("configure: %v", err)
	}

	res, err := svc.Test(context.Background(), "anthropic")
	if err != nil {
		t.Fatalf("Test should not return a transport error for a provider rejection: %v", err)
	}
	if res.OK {
		t.Error("a rejected key must not pass")
	}
	if !strings.Contains(res.Error, "invalid x-api-key") {
		t.Errorf("the provider's own message should reach the UI, got %q", res.Error)
	}
}

// A provider's error reaches the user as a sentence, not as JSON.
//
// Each of these APIs explains the real problem in its body and each wraps it
// differently. Handing the raw envelope to the UI put braces and escaped quotes
// in front of a message that was a plain sentence underneath.
func TestProviderErrorsAreUnwrapped(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"anthropic/openai shape",
			`{"error":{"type":"invalid_request_error","message":"model: nope not found"}}`,
			"model: nope not found"},
		{"osaurus shape",
			`{"error":{"message":"The operation couldn't be completed. (FoundationModels.LanguageModelSession.GenerationError error -1.)","type":"internal_error"}}`,
			"The operation couldn't be completed. (FoundationModels.LanguageModelSession.GenerationError error -1.)"},
		{"ollama's bare string",
			`{"error":"model requires more system memory than is available"}`,
			"model requires more system memory than is available"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, c.body)
			}))
			defer srv.Close()

			p := NewOsaurusProvider()
			p.Configure(map[string]string{"base_url": srv.URL})
			_, err := p.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Text: "hi"}}})
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, want it to carry %q", err, c.want)
			}
			if strings.Contains(err.Error(), `{"error"`) {
				t.Errorf("the raw JSON envelope reached the message: %q", err)
			}
		})
	}
}

// A body that is not JSON, or carries no message, still says something.
func TestAnUnparseableErrorStillReportsTheStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "<html>502 Bad Gateway</html>")
	}))
	defer srv.Close()

	p := NewOsaurusProvider()
	p.Configure(map[string]string{"base_url": srv.URL})
	_, err := p.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Text: "hi"}}})
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Errorf("error = %v, want the status when there is no message to show", err)
	}
}
