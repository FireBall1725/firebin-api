// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package ai defines the chat provider plugin system. Each provider implements
// one capability interface, is configured from instance_settings, and is
// registered at startup. Exactly one provider is active at a time.
//
// The capability here is a multi-turn chat with tool calling, not a one-shot
// prompt. Answering "do I have an 0603 220 Ω" means searching the inventory and
// reading the result before replying, which a single prompt-in/text-out call
// cannot express. Every provider therefore has to carry a messages array and
// tool definitions, and report the tool calls the model asked for.
//
// Providers are written against the HTTP APIs directly rather than through
// vendor SDKs. Four SDKs would be four dependencies with four release cadences
// for what is, in each case, one POST and one JSON shape, and the tool-calling
// wire format has to be understood here regardless.
package ai

import (
	"context"
	"encoding/json"
)

// ConfigField describes one config input the settings UI should render. The
// shape varies per provider (Anthropic needs api_key + model, Ollama needs
// base_url + model), so each provider declares its own and the UI renders
// whatever it is handed. Adding a provider needs no UI change.
type ConfigField struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Type        string `json:"type"` // "password" | "text" | "url" | "model"
	Required    bool   `json:"required"`
	Placeholder string `json:"placeholder,omitempty"`
	HelpText    string `json:"help_text,omitempty"`
	// Options suggests values without restricting to them: the UI offers a
	// list and still accepts free text, because model IDs change faster than
	// this table does.
	Options []string `json:"options,omitempty"`
}

// ProviderInfo is a provider's static metadata for the settings UI.
type ProviderInfo struct {
	Name         string        `json:"name"`
	DisplayName  string        `json:"display_name"`
	Description  string        `json:"description"`
	HelpText     string        `json:"help_text,omitempty"`
	HelpURL      string        `json:"help_url,omitempty"`
	Local        bool          `json:"local"` // runs on your own hardware; no key, no per-token cost
	ConfigFields []ConfigField `json:"config_fields"`
}

// Roles a message can carry. There is no "tool" role: a tool result belongs to
// the turn that requested it, and every provider is fed from the same history,
// so results ride on the user message that follows the assistant's request.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Message is one turn of conversation in provider-neutral form.
//
// A single assistant turn can carry text and tool calls together, and a single
// user turn can carry text and tool results together. Splitting those would
// lose the pairing that Anthropic requires between a tool_use block and the
// tool_result that answers it.
type Message struct {
	Role        string       `json:"role"`
	Text        string       `json:"text,omitempty"`
	ToolCalls   []ToolCall   `json:"tool_calls,omitempty"`   // assistant turns only
	ToolResults []ToolResult `json:"tool_results,omitempty"` // user turns only
}

// ToolDef describes a tool the model may call. Schema is a JSON Schema object
// describing the arguments.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

// ToolCall is the model asking to run a tool.
//
// ID is what pairs a call with its result. Anthropic and OpenAI both supply
// one; Ollama does not, so the provider synthesises a stable ID from the call's
// position in the turn. Never assume the ID means anything to the model.
type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ToolResult is what a tool returned, fed back on the next turn.
type ToolResult struct {
	CallID  string `json:"call_id"`
	Name    string `json:"name"`
	Content string `json:"content"`
	IsError bool   `json:"is_error,omitempty"`
}

// ChatRequest is the provider-neutral call shape.
type ChatRequest struct {
	System    string
	Messages  []Message
	Tools     []ToolDef
	MaxTokens int // 0 means the provider's default
}

// ChatResponse is what a provider returns on success.
type ChatResponse struct {
	Text      string
	ToolCalls []ToolCall
	Usage     UsageInfo
	// Truncated is set when the model stopped because it hit the output cap
	// rather than finishing. Treat it as a failure: a partial answer that ends
	// mid-sentence, or mid-tool-call, is worse than no answer, and thinking
	// models hit this often enough that accepting it silently would be a
	// steady trickle of wrong replies.
	Truncated bool
	// Thinking is the model's reasoning, when it reports any. Kept rather than
	// discarded: it is not shown in an answer, but it is often the only thing
	// that explains a wrong one, and a model that reasons its way to calling the
	// wrong tool looks identical to one that guessed until you can read it.
	Thinking string
}

// UsageInfo is the per-call token count and an estimated cost. Local providers
// report zero cost, which is true rather than unknown.
type UsageInfo struct {
	ModelID          string  `json:"model_id"`
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
	// CostKnown separates "this cost nothing" from "this model is not in the
	// pricing table". Showing $0.00 for an unpriced cloud model would be a lie.
	CostKnown bool `json:"cost_known"`
}

// ChatProvider is the one capability interface providers implement.
type ChatProvider interface {
	Info() ProviderInfo
	// Configure applies saved settings. Every constructor calls it with a nil
	// map so a provider is valid the moment it exists: a local one comes up
	// pointing at its default host, a hosted one comes up unconfigured. Without
	// that, a provider was inert until the boot-time load happened to run.
	Configure(cfg map[string]string)
	// Enabled reports whether the provider has what it needs to run: a key for
	// a hosted one, a host for a local one.
	//
	// There is deliberately no separate per-provider on/off switch. Exactly one
	// provider is active at a time and the settings page picks it, so a second
	// control would only add a state where a provider is chosen and refuses to
	// answer, which needs its own explanation on screen to be understood at all.
	Enabled() bool
	// ConfiguredModel is the model this provider will call right now, known
	// before any request so a conversation row can record it even if the call
	// fails. Empty when unconfigured.
	ConfiguredModel() string
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
}

// ModelLister is implemented by providers that can enumerate the models
// installed on their host. Local providers can; cloud ones are asked to publish
// a list they do not offer over the API, so they do not implement this and the
// UI falls back to free text.
type ModelLister interface {
	ListModels(ctx context.Context) ([]string, error)
}

// base carries the shared enabled flag.
type base struct{ enabled bool }

func (b *base) Enabled() bool { return b.enabled }
