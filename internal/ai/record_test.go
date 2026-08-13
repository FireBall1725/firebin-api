// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The recorder has to capture the WIRE request, not the neutral ChatRequest.
//
// The bug that prompted all of this was Ollama silently truncating a prompt
// because num_ctx was missing. num_ctx is a provider option that never appears
// in a ChatRequest, so a log built from the neutral shape would have shown
// nothing wrong at all.
func TestRecorderCapturesProviderOptionsNotJustTheNeutralRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"hi","thinking":"weighing it up"},"done_reason":"stop","prompt_eval_count":11,"eval_count":3}`))
	}))
	defer srv.Close()

	p := NewOllamaProvider()
	p.Configure(map[string]string{"base_url": srv.URL, "model": "m", "num_ctx": "32768"})

	rec := NewRoundRecord()
	ctx := WithRecorder(context.Background(), rec)
	resp, err := p.Chat(ctx, ChatRequest{Messages: []Message{{Role: RoleUser, Text: "hello"}}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	url, req, respRaw, status, errText := rec.Snapshot()
	if url == "" {
		t.Error("no URL recorded")
	}
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	if errText != "" {
		t.Errorf("unexpected error recorded: %s", errText)
	}

	var sent map[string]any
	if err := json.Unmarshal(req, &sent); err != nil {
		t.Fatalf("recorded request is not JSON: %v", err)
	}
	opts, _ := sent["options"].(map[string]any)
	if opts["num_ctx"] != float64(32768) {
		t.Errorf("num_ctx not in the recorded request: %v — this is the field that would have found the truncation bug", opts)
	}
	if !strings.Contains(string(respRaw), "weighing it up") {
		t.Errorf("response body not recorded: %s", respRaw)
	}

	// And the reasoning must survive into the response rather than be discarded.
	if resp.Thinking != "weighing it up" {
		t.Errorf("Thinking = %q, want the model's reasoning", resp.Thinking)
	}
	if resp.Text != "hi" {
		t.Errorf("Text = %q, want just the answer", resp.Text)
	}
}

// A failing call is the one most worth having a log of.
func TestRecorderKeepsTheBodyOfAFailedCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"model requires more system memory"}`))
	}))
	defer srv.Close()

	p := NewOllamaProvider()
	p.Configure(map[string]string{"base_url": srv.URL, "model": "m"})

	rec := NewRoundRecord()
	if _, err := p.Chat(WithRecorder(context.Background(), rec), ChatRequest{}); err == nil {
		t.Fatal("expected an error")
	}
	_, _, respRaw, status, _ := rec.Snapshot()
	if status != 500 {
		t.Errorf("status = %d, want 500", status)
	}
	if !strings.Contains(string(respRaw), "more system memory") {
		t.Errorf("the error body is the useful part and was not recorded: %s", respRaw)
	}
}

// No recorder on the context must cost nothing and change nothing.
func TestNoRecorderIsHarmless(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"fine"}}`))
	}))
	defer srv.Close()

	p := NewOllamaProvider()
	p.Configure(map[string]string{"base_url": srv.URL, "model": "m"})
	resp, err := p.Chat(context.Background(), ChatRequest{})
	if err != nil {
		t.Fatalf("Chat without a recorder: %v", err)
	}
	if resp.Text != "fine" {
		t.Errorf("Text = %q", resp.Text)
	}
	// And the nil recorder's methods are safe to call.
	var nilRec *RoundRecord
	nilRec.request("u", []byte("{}"))
	nilRec.response(200, []byte("{}"))
	nilRec.appendResponse("x")
	nilRec.fail(http.ErrBodyNotAllowed)
	if url, _, _, _, _ := nilRec.Snapshot(); url != "" {
		t.Error("a nil recorder should snapshot empty")
	}
}

func TestSplitThinking(t *testing.T) {
	cases := []struct{ name, in, text, thinking string }{
		{"none", "just an answer", "just an answer", ""},
		{"one block", "<think>let me see</think>the answer", "the answer", "let me see"},
		{"two blocks", "<think>a</think>mid<think>b</think>end", "midend", "a\nb"},
		// A model cut off mid-thought: the deliberation must not leak into the
		// answer, but it is still worth keeping in the log.
		{"unclosed", "before<think>trailing off", "before", "trailing off"},
		{"empty", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			text, thinking := splitThinking(c.in)
			if text != c.text {
				t.Errorf("text = %q, want %q", text, c.text)
			}
			if thinking != c.thinking {
				t.Errorf("thinking = %q, want %q", thinking, c.thinking)
			}
		})
	}
}
