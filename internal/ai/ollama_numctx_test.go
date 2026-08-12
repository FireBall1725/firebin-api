// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package ai

import (
	"strings"
	"testing"
)

// Ollama ignores the model's own context length and applies its own, much
// smaller default, then truncates a longer prompt in silence. A real question
// against gpt-oss:20b — a model advertising 131,072 tokens — reached 10,724
// input tokens over four tool rounds and came back with no answer and no tool
// call. Nothing in the response says the prompt was cut.
//
// So num_ctx has to go out on every request, not only when someone has found
// the setting and filled it in.
func TestOllamaAlwaysSendsNumCtx(t *testing.T) {
	p := NewOllamaProvider() // never Configure'd with a value
	body := p.buildRequest(ChatRequest{Messages: []Message{{Role: "user", Text: "hi"}}})

	got, ok := body.Options["num_ctx"]
	if !ok {
		t.Fatal("num_ctx absent; Ollama will silently truncate a long tool-calling turn")
	}
	if got != ollamaDefaultNumCtx {
		t.Errorf("num_ctx = %v, want the default %d", got, ollamaDefaultNumCtx)
	}
	// The output cap has to survive alongside it: both live in the same map, and
	// an earlier version assigned the map rather than adding to it.
	if _, ok := body.Options["num_predict"]; !ok {
		t.Error("num_predict was lost when num_ctx was added")
	}
}

func TestOllamaNumCtxIsConfigurable(t *testing.T) {
	p := NewOllamaProvider()
	p.Configure(map[string]string{"base_url": "http://h:11434", "model": "m", "num_ctx": "32768"})
	if got := p.buildRequest(ChatRequest{}).Options["num_ctx"]; got != 32768 {
		t.Errorf("num_ctx = %v, want 32768", got)
	}

	// Junk falls back to the default rather than sending nonsense to Ollama or
	// disabling the setting entirely.
	for _, bad := range []string{"", "  ", "lots", "-1", "0", "16k"} {
		p.Configure(map[string]string{"base_url": "http://h:11434", "num_ctx": bad})
		if got := p.buildRequest(ChatRequest{}).Options["num_ctx"]; got != ollamaDefaultNumCtx {
			t.Errorf("num_ctx %q gave %v, want the default %d", bad, got, ollamaDefaultNumCtx)
		}
	}

	// Whitespace around a real value is a paste artefact, not an error.
	p.Configure(map[string]string{"base_url": "http://h:11434", "num_ctx": "  8192 "})
	if got := p.buildRequest(ChatRequest{}).Options["num_ctx"]; got != 8192 {
		t.Errorf("padded num_ctx gave %v, want 8192", got)
	}
}

// The field has to be advertised or the settings page cannot render it, and the
// user is back to editing the database to make the assistant work.
func TestOllamaAdvertisesNumCtx(t *testing.T) {
	var f *ConfigField
	for _, c := range NewOllamaProvider().Info().ConfigFields {
		if c.Key == "num_ctx" {
			f = &c
			break
		}
	}
	if f == nil {
		t.Fatal("num_ctx is not a ConfigField, so it cannot be set from Settings")
	}
	if f.Required {
		t.Error("num_ctx must not be required; the default has to work unconfigured")
	}
	if !strings.Contains(strings.ToLower(f.HelpText), "truncat") {
		t.Error("the help text should say what goes wrong, since the symptom is a silent empty answer")
	}
}
