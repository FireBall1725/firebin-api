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

// Streaming splits a tool call across frames, which is where it goes wrong.
// The name arrives once, the arguments in pieces, and a tool cannot be run on
// half a JSON object. These pin each dialect's reassembly.

func streamServer(t *testing.T, lines []string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, l := range lines {
			_, _ = io.WriteString(w, l+"\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestOpenAIStreamReassemblesAFragmentedToolCall(t *testing.T) {
	srv := streamServer(t, []string{
		`data: {"choices":[{"delta":{"content":"Looking"}}]}`,
		`data: {"choices":[{"delta":{"content":" it up."}}]}`,
		// The name comes once; the arguments arrive in three pieces.
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"search_parts","arguments":"{\"pack"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"age\":\"06"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"03\"}"}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":120,"completion_tokens":18}}`,
		`data: [DONE]`,
	})

	p := NewOpenAIProvider()
	p.Configure(map[string]string{"api_key": "k", "base_url": srv.URL, "model": "gpt-4.1"})

	var got []string
	resp, err := p.ChatStream(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleUser, Text: "hi"}},
	}, func(d string) { got = append(got, d) })
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if strings.Join(got, "") != "Looking it up." {
		t.Errorf("streamed text = %q", strings.Join(got, ""))
	}
	if len(got) != 2 {
		t.Errorf("got %d fragments, want them delivered as they arrived, not at the end", len(got))
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	c := resp.ToolCalls[0]
	if c.Name != "search_parts" || c.ID != "call_1" {
		t.Errorf("call = %+v", c)
	}
	if string(c.Input) != `{"package":"0603"}` {
		t.Errorf("arguments reassembled to %s", c.Input)
	}
	if !json.Valid(c.Input) {
		t.Error("reassembled arguments are not valid JSON")
	}
	// A streamed response omits usage unless it is asked for, and without it a
	// streamed turn would report as free.
	if resp.Usage.InputTokens != 120 || resp.Usage.OutputTokens != 18 {
		t.Errorf("usage = %+v, want the totals from the final frame", resp.Usage)
	}
}

func TestOpenAIStreamHandlesTwoToolCallsAtOnce(t *testing.T) {
	srv := streamServer(t, []string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"low_stock","arguments":"{}"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"b","function":{"name":"inventory_stats","arguments":"{}"}}]}}]}`,
		`data: [DONE]`,
	})
	p := NewOsaurusProvider()
	p.Configure(map[string]string{"base_url": srv.URL})

	resp, err := p.ChatStream(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Text: "hi"}}}, func(string) {})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if len(resp.ToolCalls) != 2 {
		t.Fatalf("tool calls = %+v, want both", resp.ToolCalls)
	}
	// Order is the index order, not arrival order, so results pair correctly.
	if resp.ToolCalls[0].Name != "low_stock" || resp.ToolCalls[1].Name != "inventory_stats" {
		t.Errorf("calls came back as %+v", resp.ToolCalls)
	}
}

// Osaurus appends its telemetry trailer inside the stream. Stripping it only at
// the end would still have shown it on screen first.
func TestOsaurusStreamCutsTheTrailerAsItPasses(t *testing.T) {
	srv := streamServer(t, []string{
		`data: {"choices":[{"delta":{"content":"You have 21."}}]}`,
		`data: {"choices":[{"delta":{"content":"` + osaurusStatsMarker + `stats: 42 tok/s"}}]}`,
		`data: [DONE]`,
	})
	p := NewOsaurusProvider()
	p.Configure(map[string]string{"base_url": srv.URL})

	var shown strings.Builder
	resp, err := p.ChatStream(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Text: "hi"}}},
		func(d string) { shown.WriteString(d) })
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if strings.Contains(shown.String(), "stats:") {
		t.Errorf("the trailer reached the screen: %q", shown.String())
	}
	if resp.Text != "You have 21." {
		t.Errorf("final text = %q", resp.Text)
	}
}

func TestOllamaStreamReadsNewlineDelimitedJSON(t *testing.T) {
	// Not server-sent events: one bare JSON object per line, no data: prefix.
	srv := streamServer(t, []string{
		`{"message":{"role":"assistant","content":"You have "}}`,
		`{"message":{"role":"assistant","content":"49 parts."}}`,
		`{"message":{"role":"assistant"},"done_reason":"stop","prompt_eval_count":80,"eval_count":9}`,
	})
	p := NewOllamaProvider()
	p.Configure(map[string]string{"base_url": srv.URL, "model": "qwen3:8b"})

	var got []string
	resp, err := p.ChatStream(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Text: "hi"}}},
		func(d string) { got = append(got, d) })
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if strings.Join(got, "") != "You have 49 parts." {
		t.Errorf("streamed = %q", strings.Join(got, ""))
	}
	if resp.Usage.InputTokens != 80 || resp.Usage.OutputTokens != 9 {
		t.Errorf("usage = %+v, want the counts from the final object", resp.Usage)
	}
}

func TestAnthropicStreamReassemblesContentBlocks(t *testing.T) {
	srv := streamServer(t, []string{
		`data: {"type":"message_start","message":{"usage":{"input_tokens":200,"output_tokens":1}}}`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Checking"}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" stock."}}`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_9","name":"search_parts"}}`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"value\":"}}`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"220 ohm\"}"}}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":30}}`,
	})
	p := NewAnthropicProvider()
	p.Configure(map[string]string{"api_key": "k", "base_url": srv.URL, "model": "claude-sonnet-5"})

	var got strings.Builder
	resp, err := p.ChatStream(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Text: "hi"}}},
		func(d string) { got.WriteString(d) })
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if got.String() != "Checking stock." {
		t.Errorf("streamed = %q", got.String())
	}
	if len(resp.ToolCalls) != 1 || string(resp.ToolCalls[0].Input) != `{"value":"220 ohm"}` {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	if resp.ToolCalls[0].ID != "toolu_9" {
		t.Errorf("id = %q, want the one from the block start", resp.ToolCalls[0].ID)
	}
	// Input tokens come from message_start, output from message_delta.
	if resp.Usage.InputTokens != 200 || resp.Usage.OutputTokens != 30 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}

// A mid-stream error frame has to fail the turn, not end it quietly with a
// partial answer that looks complete.
func TestAStreamErrorFrameFailsTheTurn(t *testing.T) {
	srv := streamServer(t, []string{
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Half an ans"}}`,
		`data: {"type":"error","error":{"type":"overloaded_error","message":"the service is overloaded"}}`,
	})
	p := NewAnthropicProvider()
	p.Configure(map[string]string{"api_key": "k", "base_url": srv.URL})

	_, err := p.ChatStream(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Text: "hi"}}}, func(string) {})
	if err == nil {
		t.Fatal("expected the error frame to fail the turn")
	}
	if !strings.Contains(err.Error(), "overloaded") {
		t.Errorf("error = %q, want the provider's words", err)
	}
}

// Every provider that streams must also still answer without streaming, so a
// caller can fall back.
func TestStreamingProvidersStillImplementChat(t *testing.T) {
	for _, p := range []ChatProvider{
		NewAnthropicProvider(), NewOpenAIProvider(), NewOllamaProvider(), NewOsaurusProvider(),
	} {
		if _, ok := p.(StreamingChatProvider); !ok {
			t.Errorf("%s does not stream", p.Info().Name)
		}
	}
}
