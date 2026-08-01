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
	openAIDefaultBaseURL = "https://api.openai.com/v1"
	openAIDefaultModel   = "gpt-4.1"
)

// openAIChat implements the /chat/completions wire format, which OpenAI defined
// and several local runtimes copy. Shared rather than duplicated because the
// tool-calling shape is fiddly and getting it right twice is getting it wrong
// once.
type openAIChat struct {
	baseURL string
	apiKey  string // empty for local runtimes that do not authenticate
	model   string
}

type oaiTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type oaiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name string `json:"name"`
		// Arguments is a JSON *string*, not an object. That is the format, not
		// a mistake: it has to be unquoted before it can be parsed.
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaiMessage struct {
	Role       string        `json:"role"`
	Content    string        `json:"content,omitempty"`
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
	// Reasoning is where some local runtimes put chain-of-thought instead of
	// inlining it in content. Read so it can be discarded deliberately.
	Reasoning string `json:"reasoning,omitempty"`
}

type oaiRequest struct {
	Model         string            `json:"model"`
	Messages      []oaiMessage      `json:"messages"`
	Tools         []oaiTool         `json:"tools,omitempty"`
	MaxTokens     int               `json:"max_completion_tokens,omitempty"`
	Stream        bool              `json:"stream,omitempty"`
	StreamOptions *oaiStreamOptions `json:"stream_options,omitempty"`
}

// oaiStreamOptions asks for the usage totals, which a streamed response omits
// by default.
type oaiStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type oaiResponse struct {
	Choices []struct {
		Message      oaiMessage `json:"message"`
		FinishReason string     `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// chat runs one turn. priced selects the pricing table; nil means local.
// buildRequest turns the neutral request into the wire shape. Shared so a
// streamed turn and an unstreamed one cannot drift apart in how they order
// messages or pair tool results.
func (c openAIChat) buildRequest(req ChatRequest) oaiRequest {
	body := oaiRequest{Model: c.model, MaxTokens: maxTokensOr(req.MaxTokens, 4096)}
	if req.System != "" {
		body.Messages = append(body.Messages, oaiMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		// A tool result is its own message with role "tool", one per result,
		// and it must come before the user text that follows it.
		for _, tr := range m.ToolResults {
			content := tr.Content
			if tr.IsError {
				content = "error: " + content
			}
			body.Messages = append(body.Messages, oaiMessage{
				Role: "tool", ToolCallID: tr.CallID, Content: content,
			})
		}
		msg := oaiMessage{Role: m.Role, Content: m.Text}
		for _, tc := range m.ToolCalls {
			call := oaiToolCall{ID: tc.ID, Type: "function"}
			call.Function.Name = tc.Name
			call.Function.Arguments = string(tc.Input)
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

func (c openAIChat) chat(ctx context.Context, req ChatRequest, priced map[string]pricePer1M) (*ChatResponse, error) {
	body := c.buildRequest(req)

	var out oaiResponse
	err := postJSON(ctx, strings.TrimSuffix(c.baseURL, "/")+"/chat/completions", body, &out, func(h http.Header) {
		if c.apiKey != "" {
			h.Set("Authorization", "Bearer "+c.apiKey)
		}
	})
	if err != nil {
		return nil, err
	}
	if out.Error != nil {
		return nil, fmt.Errorf("%s: %s", out.Error.Type, out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("no choices returned")
	}

	choice := out.Choices[0]
	resp := &ChatResponse{
		Text:      stripThinking(choice.Message.Content),
		Truncated: choice.FinishReason == "length",
	}
	for i, tc := range choice.Message.ToolCalls {
		args := tc.Function.Arguments
		if strings.TrimSpace(args) == "" {
			args = "{}" // a no-argument call sometimes arrives as an empty string
		}
		id := tc.ID
		if id == "" {
			// Some runtimes omit the id. Anything stable within the turn works,
			// since it only has to pair a call with its result.
			id = fmt.Sprintf("call_%d", i)
		}
		resp.ToolCalls = append(resp.ToolCalls, ToolCall{
			ID: id, Name: tc.Function.Name, Input: json.RawMessage(args),
		})
	}
	if priced != nil {
		resp.Usage = usage(priced, c.model, out.Usage.PromptTokens, out.Usage.CompletionTokens)
	} else {
		resp.Usage = localUsage(c.model, out.Usage.PromptTokens, out.Usage.CompletionTokens)
	}
	return resp, nil
}

// listModels reads /models, which every OpenAI-compatible runtime exposes.
func (c openAIChat) listModels(ctx context.Context) ([]string, error) {
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	err := getJSON(ctx, strings.TrimSuffix(c.baseURL, "/")+"/models", &out, func(h http.Header) {
		if c.apiKey != "" {
			h.Set("Authorization", "Bearer "+c.apiKey)
		}
	})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		names = append(names, m.ID)
	}
	return names, nil
}

// OpenAIProvider calls the OpenAI API.
type OpenAIProvider struct {
	base
	openAIChat
}

func NewOpenAIProvider() *OpenAIProvider {
	p := &OpenAIProvider{}
	p.Configure(nil)
	return p
}

func (p *OpenAIProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        "openai",
		DisplayName: "OpenAI",
		Description: "GPT models. Widely available and reliable at tool calling.",
		HelpText:    "Create a key at platform.openai.com under API keys. Usage is metered per token.",
		HelpURL:     "https://platform.openai.com/api-keys",
		ConfigFields: []ConfigField{
			{Key: "api_key", Label: "API key", Type: "password", Required: true, Placeholder: "sk-..."},
			{
				Key: "model", Label: "Model", Type: "model", Required: true,
				Placeholder: openAIDefaultModel,
				Options:     []string{"gpt-4.1", "gpt-4.1-mini", "gpt-4o", "gpt-4o-mini"},
			},
			{
				Key: "base_url", Label: "Base URL", Type: "url",
				Placeholder: openAIDefaultBaseURL,
				HelpText:    "Only change this for an OpenAI-compatible gateway or proxy.",
			},
		},
	}
}

func (p *OpenAIProvider) Configure(cfg map[string]string) {
	p.apiKey = cfg["api_key"]
	p.model = firstNonEmpty(cfg["model"], openAIDefaultModel)
	p.baseURL = firstNonEmpty(cfg["base_url"], openAIDefaultBaseURL)
	p.enabled = p.apiKey != ""
}

func (p *OpenAIProvider) ConfiguredModel() string { return p.model }

func (p *OpenAIProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("openai: no API key configured")
	}
	resp, err := p.chat(ctx, req, openAIPricing)
	if err != nil {
		return nil, fmt.Errorf("openai: %w", err)
	}
	return resp, nil
}

func (p *OpenAIProvider) ListModels(ctx context.Context) ([]string, error) {
	return p.listModels(ctx)
}

// oaiStreamChunk is one delta in a streamed /chat/completions response.
type oaiStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			Reasoning string `json:"reasoning"`
			ToolCalls []struct {
				// Index is what ties fragments together. A streamed tool call
				// arrives in pieces, and only the first carries the name.
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// chatStream runs one streamed turn over the /chat/completions wire format.
func (c openAIChat) chatStream(ctx context.Context, req ChatRequest, priced map[string]pricePer1M, onText func(string)) (*ChatResponse, error) {
	body := c.buildRequest(req)
	body.Stream = true
	// Usage is omitted from a streamed response unless it is asked for, and
	// without it a streamed turn would report zero tokens and zero cost.
	body.StreamOptions = &oaiStreamOptions{IncludeUsage: true}

	resp := &ChatResponse{}
	// Tool calls accumulate by index: the name arrives once, the arguments in
	// fragments, and a tool cannot be run on half a JSON object.
	type pending struct {
		id, name string
		args     strings.Builder
	}
	calls := map[int]*pending{}
	var order []int
	var text strings.Builder
	var finish string

	err := streamLines(ctx, strings.TrimSuffix(c.baseURL, "/")+"/chat/completions", body, func(h http.Header) {
		if c.apiKey != "" {
			h.Set("Authorization", "Bearer "+c.apiKey)
		}
	}, func(line string) error {
		data, ok := sseData(line)
		if !ok || data == "" {
			return nil
		}
		if data == "[DONE]" {
			return nil
		}
		var chunk oaiStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			// A frame that does not parse is skipped rather than failing the
			// turn: some runtimes emit keep-alive comments and vendor extras.
			return nil
		}
		if chunk.Usage != nil {
			resp.Usage.InputTokens = chunk.Usage.PromptTokens
			resp.Usage.OutputTokens = chunk.Usage.CompletionTokens
		}
		if len(chunk.Choices) == 0 {
			return nil
		}
		ch := chunk.Choices[0]
		if ch.FinishReason != "" {
			finish = ch.FinishReason
		}
		if ch.Delta.Content != "" {
			text.WriteString(ch.Delta.Content)
			onText(ch.Delta.Content)
		}
		for _, tc := range ch.Delta.ToolCalls {
			p, seen := calls[tc.Index]
			if !seen {
				p = &pending{}
				calls[tc.Index] = p
				order = append(order, tc.Index)
			}
			if tc.ID != "" {
				p.id = tc.ID
			}
			if tc.Function.Name != "" {
				p.name = tc.Function.Name
			}
			p.args.WriteString(tc.Function.Arguments)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	resp.Text = stripThinking(text.String())
	resp.Truncated = finish == "length"
	for _, i := range order {
		p := calls[i]
		args := strings.TrimSpace(p.args.String())
		if args == "" {
			args = "{}"
		}
		id := p.id
		if id == "" {
			id = fmt.Sprintf("call_%d", i)
		}
		resp.ToolCalls = append(resp.ToolCalls, ToolCall{ID: id, Name: p.name, Input: json.RawMessage(args)})
	}
	if priced != nil {
		resp.Usage = usage(priced, c.model, resp.Usage.InputTokens, resp.Usage.OutputTokens)
	} else {
		resp.Usage = localUsage(c.model, resp.Usage.InputTokens, resp.Usage.OutputTokens)
	}
	return resp, nil
}

func (p *OpenAIProvider) ChatStream(ctx context.Context, req ChatRequest, onText func(string)) (*ChatResponse, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("openai: no API key configured")
	}
	resp, err := p.chatStream(ctx, req, openAIPricing, onText)
	if err != nil {
		return nil, fmt.Errorf("openai: %w", err)
	}
	return resp, nil
}
