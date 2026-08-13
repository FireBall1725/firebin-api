// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// chatHTTPTimeout bounds a single provider call. A chat turn that reads the
// inventory and reasons about it runs far longer than an ordinary API request,
// and a local model on a busy machine is slower still, so this is generous.
// It exists to stop a hung provider pinning a request forever, not to enforce
// a latency target.
const chatHTTPTimeout = 5 * time.Minute

// maxProviderBody caps how much of a provider's reply is read. A provider that
// streams an unbounded body would otherwise be able to exhaust memory.
const maxProviderBody = 16 << 20 // 16 MiB

var httpClient = &http.Client{Timeout: chatHTTPTimeout}

// postJSON sends body as JSON and decodes the reply into out. setHeaders adds
// per-provider auth. A non-2xx status is returned as an error carrying the
// response body, because every one of these APIs explains the real problem
// there ("model not found", "credit balance too low") and a bare status code
// sends the user hunting.
func postJSON(ctx context.Context, url string, body, out any, setHeaders func(http.Header)) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	// The one place every unstreamed provider call passes through, so recording
	// here covers all four providers and keeps the wire truth — including the
	// provider-specific options a neutral ChatRequest cannot show.
	rec := recorderFrom(ctx)
	rec.request(url, buf)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if setHeaders != nil {
		setHeaders(req.Header)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		rec.fail(err)
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxProviderBody))
	if err != nil {
		rec.fail(err)
		return fmt.Errorf("read response: %w", err)
	}
	rec.response(resp.StatusCode, raw)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return classifyProviderError(providerError(resp.StatusCode, raw))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response: %w: %s", err, snippet(raw))
	}
	return nil
}

// getJSON is postJSON's counterpart for the model-listing endpoints.
func getJSON(ctx context.Context, url string, out any, setHeaders func(http.Header)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if setHeaders != nil {
		setHeaders(req.Header)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxProviderBody))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return classifyProviderError(providerError(resp.StatusCode, raw))
	}
	return json.Unmarshal(raw, out)
}

// providerError turns an error response into one readable sentence.
//
// Every one of these APIs explains the real problem in its body, and every one
// wraps it in a different envelope. Showing the raw JSON puts braces and escaped
// quotes in front of the user for a message that is usually a plain sentence
// underneath, so the message is dug out when it can be found and the body shown
// only when it cannot.
func providerError(status int, raw []byte) string {
	var body struct {
		// Anthropic, OpenAI and OpenAI-compatible runtimes.
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
		// Ollama puts a bare string here instead.
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &body); err == nil {
		if m := strings.TrimSpace(body.Error.Message); m != "" {
			return m
		}
		if m := strings.TrimSpace(body.Message); m != "" {
			return m
		}
	}
	// Ollama's plain-string error field, which collides with the struct above.
	var plain struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &plain); err == nil && strings.TrimSpace(plain.Error) != "" {
		return strings.TrimSpace(plain.Error)
	}
	return fmt.Sprintf("http %d: %s", status, snippet(raw))
}

// snippet trims a provider error body to something a settings page can show on
// one line without dumping a stack trace at the user.
func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func maxTokensOr(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}

// stripThinking removes the reasoning some local models emit inline in their
// reply. Qwen and DeepSeek distills wrap it in <think> tags and will happily
// hand you three paragraphs of deliberation followed by one sentence of answer.
// Providers that return reasoning in a separate field are handled at the call
// site instead; this is only for the ones that inline it.
func stripThinking(s string) string {
	text, _ := splitThinking(s)
	return text
}

// splitThinking is stripThinking that keeps what it removed.
//
// The reasoning is worth having even though it is never shown in an answer: it
// is frequently the only thing that explains a wrong one. A model that reasoned
// its way to the wrong tool and one that guessed produce the same reply, and
// only the deliberation tells them apart.
func splitThinking(s string) (text, thinking string) {
	var thoughts []string
	for {
		start := strings.Index(s, "<think>")
		if start < 0 {
			break
		}
		end := strings.Index(s[start:], "</think>")
		if end < 0 {
			// An unclosed tag means the model was cut off mid-thought. Keeping
			// the prefix is better than returning the whole deliberation.
			thoughts = append(thoughts, s[start+len("<think>"):])
			s = s[:start]
			break
		}
		thoughts = append(thoughts, s[start+len("<think>"):start+end])
		s = s[:start] + s[start+end+len("</think>"):]
	}
	return strings.TrimSpace(s), strings.TrimSpace(strings.Join(thoughts, "\n"))
}

// joinThinking merges reasoning from a provider's own field with any that came
// inline, keeping whichever is present without inventing a separator when only
// one is.
func joinThinking(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			kept = append(kept, s)
		}
	}
	return strings.Join(kept, "\n")
}
