// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package ai

import (
	"context"
	"fmt"
	"strings"
)

const (
	osaurusDefaultBaseURL = "http://127.0.0.1:1337/v1"
	osaurusDefaultModel   = "qwen3-4b"
)

// osaurusStatsMarker is the telemetry trailer Osaurus appends to its reply,
// introduced by U+FFFE. It is not part of the answer and would otherwise be
// rendered to the user as garbage at the end of every message.
const osaurusStatsMarker = "￾"

// OsaurusProvider talks to Osaurus, a local Apple-silicon runtime that serves
// the OpenAI wire format.
type OsaurusProvider struct {
	base
	openAIChat
}

func NewOsaurusProvider() *OsaurusProvider {
	p := &OsaurusProvider{}
	p.Configure(nil)
	return p
}

func (p *OsaurusProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        "osaurus",
		DisplayName: "Osaurus",
		Description: "Local models on Apple silicon. Nothing leaves the machine and there is no per-token cost.",
		HelpText:    "Runs on 127.0.0.1:1337 by default. Tool calling depends on the model: smaller ones often answer in prose instead of calling a tool.",
		HelpURL:     "https://github.com/dinoki-ai/osaurus",
		Local:       true,
		ConfigFields: []ConfigField{
			{Key: "base_url", Label: "Base URL", Type: "url", Required: true, Placeholder: osaurusDefaultBaseURL},
			{
				Key: "model", Label: "Model", Type: "model", Required: true,
				Placeholder: osaurusDefaultModel,
				HelpText:    "Models installed on the host are offered once the base URL is reachable.",
			},
		},
	}
}

func (p *OsaurusProvider) Configure(cfg map[string]string) {
	p.baseURL = firstNonEmpty(cfg["base_url"], osaurusDefaultBaseURL)
	p.model = firstNonEmpty(cfg["model"], osaurusDefaultModel)
	p.apiKey = "" // local, unauthenticated
	p.enabled = p.baseURL != ""
}

func (p *OsaurusProvider) ConfiguredModel() string { return p.model }

func (p *OsaurusProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	resp, err := p.chat(ctx, req, nil)
	if err != nil {
		return nil, fmt.Errorf("osaurus: %w", err)
	}
	if i := strings.Index(resp.Text, osaurusStatsMarker); i >= 0 {
		resp.Text = strings.TrimSpace(resp.Text[:i])
	}
	return resp, nil
}

func (p *OsaurusProvider) ListModels(ctx context.Context) ([]string, error) {
	return p.listModels(ctx)
}

func (p *OsaurusProvider) ChatStream(ctx context.Context, req ChatRequest, onText func(string)) (*ChatResponse, error) {
	// The stats trailer arrives inside the stream like any other text, so it
	// has to be cut off as it passes rather than removed afterwards: stripping
	// it at the end would still have shown it on screen first.
	stopped := false
	filtered := func(delta string) {
		if stopped {
			return
		}
		if i := strings.Index(delta, osaurusStatsMarker); i >= 0 {
			stopped = true
			if i > 0 {
				onText(delta[:i])
			}
			return
		}
		onText(delta)
	}
	resp, err := p.chatStream(ctx, req, nil, filtered)
	if err != nil {
		return nil, fmt.Errorf("osaurus: %w", err)
	}
	if i := strings.Index(resp.Text, osaurusStatsMarker); i >= 0 {
		resp.Text = strings.TrimSpace(resp.Text[:i])
	}
	return resp, nil
}
