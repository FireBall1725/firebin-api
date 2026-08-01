// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	ollamaDefaultBaseURL = "http://127.0.0.1:11434"
	ollamaDefaultModel   = "qwen3:8b"
)

// OllamaProvider talks to Ollama's native /api/chat.
//
// Ollama also serves an OpenAI-compatible endpoint, but the native one is used
// here because it returns tool arguments as a JSON object rather than a string
// and reports reasoning in its own field, both of which are less to get wrong.
type OllamaProvider struct {
	base
	baseURL string
	model   string
}

func NewOllamaProvider() *OllamaProvider {
	p := &OllamaProvider{}
	p.Configure(nil)
	return p
}

func (p *OllamaProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        "ollama",
		DisplayName: "Ollama",
		Description: "Local models via Ollama. Nothing leaves your network and there is no per-token cost.",
		HelpText:    "Point this at the host running Ollama. The model must support tool calling: llama3.1, qwen3 and mistral do, and many smaller ones do not.",
		HelpURL:     "https://ollama.com/search?c=tools",
		Local:       true,
		ConfigFields: []ConfigField{
			{Key: "base_url", Label: "Base URL", Type: "url", Required: true, Placeholder: ollamaDefaultBaseURL},
			{
				Key: "model", Label: "Model", Type: "model", Required: true,
				Placeholder: ollamaDefaultModel,
				HelpText:    "Models pulled on the host are offered once the base URL is reachable.",
			},
		},
	}
}

func (p *OllamaProvider) Configure(cfg map[string]string) {
	p.baseURL = firstNonEmpty(cfg["base_url"], ollamaDefaultBaseURL)
	p.model = firstNonEmpty(cfg["model"], ollamaDefaultModel)
	p.enabled = p.baseURL != ""
}

func (p *OllamaProvider) ConfiguredModel() string { return p.model }

type ollamaToolCall struct {
	Function struct {
		Name string `json:"name"`
		// An object here, unlike the OpenAI format's JSON string.
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

type ollamaMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content,omitempty"`
	Thinking  string           `json:"thinking,omitempty"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
	ToolName  string           `json:"tool_name,omitempty"`
}

type ollamaRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Tools    []oaiTool       `json:"tools,omitempty"`
	Stream   bool            `json:"stream"`
	Options  map[string]any  `json:"options,omitempty"`
}

type ollamaResponse struct {
	Message    ollamaMessage `json:"message"`
	DoneReason string        `json:"done_reason"`
	// Ollama names these differently from everyone else.
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
	Error           string `json:"error"`
}

// buildRequest turns the neutral request into Ollama's wire shape, shared so a
// streamed turn and an unstreamed one cannot drift apart.
func (p *OllamaProvider) buildRequest(req ChatRequest) ollamaRequest {
	body := ollamaRequest{Model: p.model}
	if n := maxTokensOr(req.MaxTokens, 4096); n > 0 {
		body.Options = map[string]any{"num_predict": n}
	}
	if req.System != "" {
		body.Messages = append(body.Messages, ollamaMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		for _, tr := range m.ToolResults {
			content := tr.Content
			if tr.IsError {
				content = "error: " + content
			}
			// Ollama pairs a result to a call by tool name, not by id: there is
			// no id in its wire format at all.
			body.Messages = append(body.Messages, ollamaMessage{
				Role: "tool", ToolName: tr.Name, Content: content,
			})
		}
		msg := ollamaMessage{Role: m.Role, Content: m.Text}
		for _, tc := range m.ToolCalls {
			var call ollamaToolCall
			call.Function.Name = tc.Name
			call.Function.Arguments = tc.Input
			msg.ToolCalls = append(msg.ToolCalls, call)
		}
		if msg.Content == "" && len(msg.ToolCalls) == 0 {
			continue
		}
		body.Messages = append(body.Messages, msg)
	}
	for _, t := range req.Tools {
		var tool oaiTool
		tool.Type = "function"
		tool.Function.Name = t.Name
		tool.Function.Description = t.Description
		tool.Function.Parameters = t.Schema
		body.Tools = append(body.Tools, tool)
	}
	return body
}

func (p *OllamaProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	body := p.buildRequest(req)

	var out ollamaResponse
	if err := postJSON(ctx, strings.TrimSuffix(p.baseURL, "/")+"/api/chat", body, &out, nil); err != nil {
		return nil, fmt.Errorf("ollama: %w", err)
	}
	if out.Error != "" {
		return nil, fmt.Errorf("ollama: %s", out.Error)
	}

	resp := &ChatResponse{
		// Reasoning arrives in its own field on newer versions and inline in
		// <think> tags on older ones. Drop both.
		Text:      stripThinking(out.Message.Content),
		Truncated: out.DoneReason == "length",
	}
	for i, tc := range out.Message.ToolCalls {
		args := tc.Function.Arguments
		if len(strings.TrimSpace(string(args))) == 0 {
			args = json.RawMessage("{}")
		}
		resp.ToolCalls = append(resp.ToolCalls, ToolCall{
			ID:    fmt.Sprintf("call_%d", i), // synthesised: Ollama sends none
			Name:  tc.Function.Name,
			Input: args,
		})
	}
	resp.Usage = localUsage(p.model, out.PromptEvalCount, out.EvalCount)
	return resp, nil
}

// ListModels reads /api/tags, Ollama's own listing endpoint.
func (p *OllamaProvider) ListModels(ctx context.Context) ([]string, error) {
	var out struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := getJSON(ctx, strings.TrimSuffix(p.baseURL, "/")+"/api/tags", &out, nil); err != nil {
		return nil, fmt.Errorf("ollama: %w", err)
	}
	names := make([]string, 0, len(out.Models))
	for _, m := range out.Models {
		names = append(names, m.Name)
	}
	return names, nil
}

// ChatStream runs one streamed turn over Ollama's newline-delimited JSON.
//
// Not server-sent events: Ollama emits one bare JSON object per line, so there
// is no "data:" prefix to strip and no [DONE] sentinel, just a final object
// carrying done and the token counts.
func (p *OllamaProvider) ChatStream(ctx context.Context, req ChatRequest, onText func(string)) (*ChatResponse, error) {
	body := p.buildRequest(req)
	body.Stream = true

	resp := &ChatResponse{}
	var text strings.Builder
	var toolCalls []ollamaToolCall
	var doneReason string

	err := streamLines(ctx, strings.TrimSuffix(p.baseURL, "/")+"/api/chat", body, nil, func(line string) error {
		line = strings.TrimSpace(line)
		if line == "" {
			return nil
		}
		var chunk ollamaResponse
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			return nil
		}
		if chunk.Error != "" {
			return fmt.Errorf("%s", chunk.Error)
		}
		if c := chunk.Message.Content; c != "" {
			text.WriteString(c)
			onText(c)
		}
		toolCalls = append(toolCalls, chunk.Message.ToolCalls...)
		if chunk.DoneReason != "" {
			doneReason = chunk.DoneReason
		}
		// The counts arrive on the final object only.
		if chunk.PromptEvalCount > 0 {
			resp.Usage.InputTokens = chunk.PromptEvalCount
		}
		if chunk.EvalCount > 0 {
			resp.Usage.OutputTokens = chunk.EvalCount
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("ollama: %w", err)
	}

	resp.Text = stripThinking(text.String())
	resp.Truncated = doneReason == "length"
	for i, tc := range toolCalls {
		args := tc.Function.Arguments
		if len(strings.TrimSpace(string(args))) == 0 {
			args = json.RawMessage("{}")
		}
		resp.ToolCalls = append(resp.ToolCalls, ToolCall{
			ID: fmt.Sprintf("call_%d", i), Name: tc.Function.Name, Input: args,
		})
	}
	resp.Usage = localUsage(p.model, resp.Usage.InputTokens, resp.Usage.OutputTokens)
	return resp, nil
}
