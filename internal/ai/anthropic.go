// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const (
	anthropicDefaultBaseURL = "https://api.anthropic.com"
	anthropicDefaultModel   = "claude-sonnet-5"
	anthropicVersion        = "2023-06-01"
)

// AnthropicProvider talks to the Messages API.
type AnthropicProvider struct {
	base
	apiKey  string
	model   string
	baseURL string
}

func NewAnthropicProvider() *AnthropicProvider {
	p := &AnthropicProvider{}
	p.Configure(nil)
	return p
}

func (p *AnthropicProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        "anthropic",
		DisplayName: "Anthropic",
		Description: "Claude. Strong at multi-step questions that need several lookups before answering.",
		HelpText:    "Create a key at console.anthropic.com under Settings, API keys. Usage is metered per token; the cost of each answer is recorded.",
		HelpURL:     "https://console.anthropic.com/settings/keys",
		ConfigFields: []ConfigField{
			{Key: "api_key", Label: "API key", Type: "password", Required: true, Placeholder: "sk-ant-..."},
			{
				Key: "model", Label: "Model", Type: "model", Required: true,
				Placeholder: anthropicDefaultModel,
				HelpText:    "Pick one from the list or type any model ID. Opus is the most capable, Haiku the cheapest.",
				Options:     []string{"claude-opus-5", "claude-sonnet-5", "claude-haiku-4-5"},
			},
		},
	}
}

func (p *AnthropicProvider) Configure(cfg map[string]string) {
	p.apiKey = cfg["api_key"]
	p.model = firstNonEmpty(cfg["model"], anthropicDefaultModel)
	p.baseURL = firstNonEmpty(cfg["base_url"], anthropicDefaultBaseURL)
	p.enabled = p.apiKey != ""
}

func (p *AnthropicProvider) ConfiguredModel() string { return p.model }

// Anthropic wire types. Only the fields used are declared; the API adds more.
type anthropicBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

type anthropicMessage struct {
	Role    string           `json:"role"`
	Content []anthropicBlock `json:"content"`
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
	Stream    bool               `json:"stream,omitempty"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicResponse struct {
	Content    []anthropicBlock `json:"content"`
	StopReason string           `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// buildRequest turns the neutral request into the wire shape, shared so a
// streamed turn and an unstreamed one order blocks identically. Anthropic
// rejects a turn whose tool_result is not the first block, so this ordering is
// not cosmetic.
func (p *AnthropicProvider) buildRequest(req ChatRequest) anthropicRequest {
	body := anthropicRequest{
		Model:     p.model,
		MaxTokens: maxTokensOr(req.MaxTokens, 4096),
		System:    req.System,
		Messages:  make([]anthropicMessage, 0, len(req.Messages)),
	}
	for _, t := range req.Tools {
		body.Tools = append(body.Tools, anthropicTool{
			Name: t.Name, Description: t.Description, InputSchema: t.Schema,
		})
	}
	for _, m := range req.Messages {
		msg := anthropicMessage{Role: m.Role}
		// Tool results come first in the block list. Anthropic requires every
		// tool_result to be at the start of the user message answering the
		// tool_use, and rejects the turn otherwise.
		for _, tr := range m.ToolResults {
			msg.Content = append(msg.Content, anthropicBlock{
				Type: "tool_result", ToolUseID: tr.CallID, Content: tr.Content, IsError: tr.IsError,
			})
		}
		if m.Text != "" {
			msg.Content = append(msg.Content, anthropicBlock{Type: "text", Text: m.Text})
		}
		for _, tc := range m.ToolCalls {
			msg.Content = append(msg.Content, anthropicBlock{
				Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: tc.Input,
			})
		}
		if len(msg.Content) == 0 {
			continue // an empty turn is rejected, and carries nothing anyway
		}
		body.Messages = append(body.Messages, msg)
	}
	return body
}

func (p *AnthropicProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("anthropic: no API key configured")
	}
	body := p.buildRequest(req)

	var out anthropicResponse
	err := postJSON(ctx, p.baseURL+"/v1/messages", body, &out, func(h http.Header) {
		h.Set("x-api-key", p.apiKey)
		h.Set("anthropic-version", anthropicVersion)
	})
	if err != nil {
		return nil, fmt.Errorf("anthropic: %w", err)
	}
	if out.Error != nil {
		return nil, fmt.Errorf("anthropic: %s: %s", out.Error.Type, out.Error.Message)
	}

	resp := &ChatResponse{Truncated: out.StopReason == "max_tokens"}
	var text strings.Builder
	for _, b := range out.Content {
		switch b.Type {
		case "text":
			text.WriteString(b.Text)
		case "tool_use":
			resp.ToolCalls = append(resp.ToolCalls, ToolCall{ID: b.ID, Name: b.Name, Input: b.Input})
		}
	}
	resp.Text = text.String()
	resp.Usage = usage(anthropicPricing, p.model, out.Usage.InputTokens, out.Usage.OutputTokens)
	return resp, nil
}

// anthropicStreamEvent is one frame of a streamed Messages response.
type anthropicStreamEvent struct {
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentBlock struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Message struct {
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
	Usage struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// ChatStream runs one streamed turn over the Messages API.
//
// Anthropic streams content blocks rather than a flat delta: each block is
// announced, then filled, then closed. A tool_use block's arguments arrive as
// partial JSON fragments that only parse once the block is complete.
func (p *AnthropicProvider) ChatStream(ctx context.Context, req ChatRequest, onText func(string)) (*ChatResponse, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("anthropic: no API key configured")
	}
	body := p.buildRequest(req)
	body.Stream = true

	resp := &ChatResponse{}
	var text strings.Builder
	type pending struct {
		id, name string
		args     strings.Builder
	}
	blocks := map[int]*pending{}
	var order []int
	var inTokens, outTokens int

	err := streamLines(ctx, p.baseURL+"/v1/messages", body, func(h http.Header) {
		h.Set("x-api-key", p.apiKey)
		h.Set("anthropic-version", anthropicVersion)
	}, func(line string) error {
		data, ok := sseData(line)
		if !ok || data == "" {
			return nil
		}
		var ev anthropicStreamEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return nil
		}
		switch ev.Type {
		case "error":
			if ev.Error != nil {
				return fmt.Errorf("%s: %s", ev.Error.Type, ev.Error.Message)
			}
		case "message_start":
			inTokens = ev.Message.Usage.InputTokens
			outTokens = ev.Message.Usage.OutputTokens
		case "content_block_start":
			if ev.ContentBlock.Type == "tool_use" {
				blocks[ev.Index] = &pending{id: ev.ContentBlock.ID, name: ev.ContentBlock.Name}
				order = append(order, ev.Index)
			}
		case "content_block_delta":
			switch ev.Delta.Type {
			case "text_delta":
				if ev.Delta.Text != "" {
					text.WriteString(ev.Delta.Text)
					onText(ev.Delta.Text)
				}
			case "input_json_delta":
				if b := blocks[ev.Index]; b != nil {
					b.args.WriteString(ev.Delta.PartialJSON)
				}
			}
		case "message_delta":
			if ev.Delta.StopReason != "" {
				resp.Truncated = ev.Delta.StopReason == "max_tokens"
			}
			if ev.Usage.OutputTokens > 0 {
				outTokens = ev.Usage.OutputTokens
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("anthropic: %w", err)
	}

	resp.Text = text.String()
	for _, i := range order {
		b := blocks[i]
		args := strings.TrimSpace(b.args.String())
		if args == "" {
			args = "{}"
		}
		resp.ToolCalls = append(resp.ToolCalls, ToolCall{ID: b.id, Name: b.name, Input: json.RawMessage(args)})
	}
	resp.Usage = usage(anthropicPricing, p.model, inTokens, outTokens)
	return resp, nil
}
