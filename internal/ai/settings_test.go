// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package ai

import (
	"context"
	"testing"
)

// memStore is an in-memory SettingsStore, so these tests need no database.
type memStore struct{ m map[string]string }

func newMemStore() *memStore { return &memStore{m: map[string]string{}} }

func (s *memStore) Get(_ context.Context, key string) (string, error) {
	v, ok := s.m[key]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (s *memStore) Set(_ context.Context, key, value string) error {
	s.m[key] = value
	return nil
}

func testService(t *testing.T) (*Service, *memStore) {
	t.Helper()
	store := newMemStore()
	reg := NewRegistry()
	reg.Register(NewAnthropicProvider())
	reg.Register(NewOllamaProvider())
	return NewService(reg, store, ErrNotFound), store
}

// Saving the mask back must never overwrite a stored key.
//
// The settings page shows "***" for a saved secret. A client that submits the
// whole form, or a user who reloads and saves without retyping, sends that
// string straight back. Storing it replaces a working key with three asterisks
// and the next request fails as an authentication error, which reads like the
// key was revoked rather than like the app destroyed it.
func TestSavingTheMaskDoesNotClobberTheKey(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()

	if err := svc.Configure(ctx, "anthropic", map[string]string{
		"api_key": "sk-ant-real-key", "model": "claude-sonnet-5",
	}); err != nil {
		t.Fatalf("configure: %v", err)
	}

	// What the page shows back.
	status, err := svc.Status(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	var shown map[string]string
	for _, p := range status {
		if p.Name == "anthropic" {
			shown = p.Config
			if !p.HasSecret {
				t.Error("has_secret should be true once a key is saved")
			}
		}
	}
	if shown["api_key"] != MaskValue {
		t.Fatalf("api_key came back as %q; the real key must never be echoed", shown["api_key"])
	}
	if shown["model"] != "claude-sonnet-5" {
		t.Errorf("model should not be masked, got %q", shown["model"])
	}

	// The page submits what it was shown, plus a real edit.
	if err := svc.Configure(ctx, "anthropic", map[string]string{
		"api_key": MaskValue, "model": "claude-opus-5",
	}); err != nil {
		t.Fatalf("configure: %v", err)
	}

	cfg, err := svc.loadConfig(ctx, "anthropic")
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg["api_key"] != "sk-ant-real-key" {
		t.Errorf("api_key is now %q; the mask overwrote the stored key", cfg["api_key"])
	}
	if cfg["model"] != "claude-opus-5" {
		t.Errorf("the model edit was lost, got %q", cfg["model"])
	}
}

// A real new secret must still replace the old one.
func TestANewKeyStillReplacesTheOldOne(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	mustConfigure(t, svc, "anthropic", map[string]string{"api_key": "old"})
	mustConfigure(t, svc, "anthropic", map[string]string{"api_key": "new"})

	cfg, _ := svc.loadConfig(ctx, "anthropic")
	if cfg["api_key"] != "new" {
		t.Errorf("api_key = %q, want the new key", cfg["api_key"])
	}
}

// Configure merges rather than replaces, so saving one section does not blank
// the fields another section owns.
func TestConfigureMergesOverStoredConfig(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	mustConfigure(t, svc, "ollama", map[string]string{"base_url": "http://box:11434", "model": "qwen3:8b"})
	mustConfigure(t, svc, "ollama", map[string]string{"model": "llama3.1:8b"})

	cfg, _ := svc.loadConfig(ctx, "ollama")
	if cfg["base_url"] != "http://box:11434" {
		t.Errorf("base_url was lost: %q", cfg["base_url"])
	}
	if cfg["model"] != "llama3.1:8b" {
		t.Errorf("model = %q, want the edit", cfg["model"])
	}
}

// The feature is off until an admin turns it on, because turning it on is what
// permits inventory data to leave the instance.
func TestFeatureIsOffByDefault(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	on, err := svc.FeatureEnabled(ctx)
	if err != nil {
		t.Fatalf("FeatureEnabled: %v", err)
	}
	if on {
		t.Error("the assistant should be off until explicitly enabled")
	}
	if err := svc.SetFeatureEnabled(ctx, true); err != nil {
		t.Fatalf("SetFeatureEnabled: %v", err)
	}
	if on, _ = svc.FeatureEnabled(ctx); !on {
		t.Error("enabling did not stick")
	}
}

// A selected provider that is not configured must not silently fall through to
// a different one: that would send the conversation somewhere unchosen.
func TestActiveReturnsNilWhenTheChosenProviderIsUnconfigured(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	if err := svc.SetActive(ctx, "anthropic"); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	mustConfigure(t, svc, "ollama", map[string]string{"base_url": ollamaDefaultBaseURL})

	if svc.Registry().Active() != nil {
		t.Error("Active() returned a provider even though the chosen one has no key")
	}
	if svc.Registry().ActiveName() != "anthropic" {
		t.Error("the selection itself should be remembered so the UI can show it")
	}
}

func TestSetActiveRejectsUnknownProvider(t *testing.T) {
	svc, _ := testService(t)
	if err := svc.SetActive(context.Background(), "nope"); err == nil {
		t.Error("expected an error for an unregistered provider")
	}
}

// Load applies stored config to the live registry at boot.
func TestLoadAppliesStoredConfig(t *testing.T) {
	svc, store := testService(t)
	ctx := context.Background()
	mustConfigure(t, svc, "anthropic", map[string]string{"api_key": "k", "model": "claude-opus-5"})
	if err := svc.SetActive(ctx, "anthropic"); err != nil {
		t.Fatalf("SetActive: %v", err)
	}

	// A fresh registry, as at boot, reading the same store.
	reg := NewRegistry()
	reg.Register(NewAnthropicProvider())
	fresh := NewService(reg, store, ErrNotFound)
	if err := fresh.Load(ctx); err != nil {
		t.Fatalf("Load: %v", err)
	}
	p := reg.Active()
	if p == nil {
		t.Fatal("no active provider after Load")
	}
	if p.ConfiguredModel() != "claude-opus-5" {
		t.Errorf("model = %q, want the stored one", p.ConfiguredModel())
	}
}

func mustConfigure(t *testing.T, svc *Service, name string, cfg map[string]string) {
	t.Helper()
	if err := svc.Configure(context.Background(), name, cfg); err != nil {
		t.Fatalf("configure %s: %v", name, err)
	}
}

// A hosted provider with no key cannot run, and says so.
func TestAProviderWithNoKeyIsNotEnabled(t *testing.T) {
	svc, _ := testService(t)
	status, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	for _, p := range status {
		if p.Name != "anthropic" {
			continue
		}
		if p.Enabled {
			t.Error("anthropic has no key, so it cannot be enabled")
		}
	}
}
