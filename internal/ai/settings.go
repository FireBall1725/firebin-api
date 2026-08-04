// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
)

// Settings keys under instance_settings:
//
//	ai:enabled            "true" | "false" — the whole feature
//	ai:provider:<name>    JSON object of that provider's config
//	ai:active_provider    which provider name serves chat
const (
	SettingEnabled        = "ai:enabled"
	SettingProviderPrefix = "ai:provider:"
	SettingActiveProvider = "ai:active_provider"
)

// MaskValue is what a saved secret reads back as. It is never a real key.
const MaskValue = "***"

// SettingsStore is the slice of the settings repository this needs. Declared
// here rather than importing the repository so the package stays testable
// without a database.
type SettingsStore interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
}

// ErrNotFound is what a store returns for a missing key. Matched by errors.Is
// against whatever the repository returns, so a missing setting reads as an
// empty config rather than an error.
var ErrNotFound = errors.New("setting not found")

// Service owns provider configuration: reads it at boot, applies it to the live
// registry, and saves changes.
type Service struct {
	registry *Registry
	store    SettingsStore
	notFound error // the store's own not-found sentinel
}

// NewService wires a registry to a settings store. notFound is the store's
// not-found error, so a missing key can be told apart from a broken database.
func NewService(registry *Registry, store SettingsStore, notFound error) *Service {
	if notFound == nil {
		notFound = ErrNotFound
	}
	return &Service{registry: registry, store: store, notFound: notFound}
}

func (s *Service) Registry() *Registry { return s.registry }

// Load reads every provider's config and the active selection, applying them to
// the registry. Called once at boot.
func (s *Service) Load(ctx context.Context) error {
	for _, p := range s.registry.All() {
		cfg, err := s.loadConfig(ctx, p.Info().Name)
		if err != nil {
			return err
		}
		p.Configure(cfg)
	}
	active, err := s.get(ctx, SettingActiveProvider)
	if err != nil {
		return err
	}
	s.registry.SetActive(active)
	return nil
}

// FeatureEnabled reports whether the assistant is switched on at all. Default
// is off: an admin has to turn it on before any inventory data can reach a
// third party.
func (s *Service) FeatureEnabled(ctx context.Context) (bool, error) {
	v, err := s.get(ctx, SettingEnabled)
	if err != nil {
		return false, err
	}
	return v == "true", nil
}

// SetFeatureEnabled turns the assistant on or off.
func (s *Service) SetFeatureEnabled(ctx context.Context, on bool) error {
	return s.store.Set(ctx, SettingEnabled, fmt.Sprintf("%t", on))
}

// ProviderStatus is what the settings page renders. Config carries saved values
// with secrets masked, so the page can show which model is set without ever
// receiving a key.
type ProviderStatus struct {
	Name         string            `json:"name"`
	DisplayName  string            `json:"display_name"`
	Description  string            `json:"description"`
	HelpText     string            `json:"help_text,omitempty"`
	HelpURL      string            `json:"help_url,omitempty"`
	Local        bool              `json:"local"`
	ConfigFields []ConfigField     `json:"config_fields"`
	Config       map[string]string `json:"config"`
	// Configured means it has what it needs; Enabled means it is also switched
	// on. A provider can be configured and deliberately off, which is not the
	// same as unconfigured and must not read that way.
	Configured   bool `json:"configured"`
	Enabled      bool `json:"enabled"`
	Active       bool `json:"active"`
	HasSecret    bool `json:"has_secret"`
	CanListModel bool `json:"can_list_models"`
}

// Status returns every provider with its masked config.
func (s *Service) Status(ctx context.Context) ([]ProviderStatus, error) {
	// Both halves of this page come from the store, so it cannot contradict
	// itself. Reading the selection from the registry and the fields from the
	// database meant that anything writing settings from outside this service,
	// a restore being the obvious one, left the dropdown showing the provider
	// chosen at boot next to the configuration of a different one.
	active, err := s.get(ctx, SettingActiveProvider)
	if err != nil {
		return nil, err
	}
	all := s.registry.All()
	out := make([]ProviderStatus, 0, len(all))
	for _, p := range all {
		info := p.Info()
		cfg, err := s.loadConfig(ctx, info.Name)
		if err != nil {
			return nil, err
		}
		_, canList := p.(ModelLister)
		out = append(out, ProviderStatus{
			Name:         info.Name,
			DisplayName:  info.DisplayName,
			Description:  info.Description,
			HelpText:     info.HelpText,
			HelpURL:      info.HelpURL,
			Local:        info.Local,
			ConfigFields: info.ConfigFields,
			Config:       maskConfig(info.ConfigFields, cfg),
			Enabled:      p.Enabled(),
			Active:       info.Name == active,
			HasSecret:    hasSecret(info.ConfigFields, cfg),
			CanListModel: canList,
		})
	}
	return out, nil
}

// Configure merges new config over what is stored and reconfigures the live
// provider.
//
// Any secret field whose incoming value is the mask is dropped rather than
// saved. The UI is supposed to send a secret only when the user typed a new
// one, but a client that echoes back what it was shown would otherwise
// overwrite a working key with three asterisks, and the failure would look like
// the provider rejecting a valid key.
func (s *Service) Configure(ctx context.Context, name string, cfg map[string]string) error {
	p := s.registry.Get(name)
	if p == nil {
		return fmt.Errorf("unknown provider %q", name)
	}

	incoming := make(map[string]string, len(cfg))
	maps.Copy(incoming, cfg)
	for _, f := range p.Info().ConfigFields {
		if f.Type != "password" {
			continue
		}
		if v, ok := incoming[f.Key]; ok && v == MaskValue {
			delete(incoming, f.Key)
		}
	}

	merged, err := s.loadConfig(ctx, name)
	if err != nil {
		return err
	}
	if merged == nil {
		merged = map[string]string{}
	}
	maps.Copy(merged, incoming)

	data, err := json.Marshal(merged)
	if err != nil {
		return err
	}
	if err := s.store.Set(ctx, SettingProviderPrefix+name, string(data)); err != nil {
		return err
	}
	s.registry.Configure(name, merged)
	return nil
}

// SetActive picks the provider that serves chat. An empty name clears the
// selection, which leaves the feature on but with nowhere to send a message.
func (s *Service) SetActive(ctx context.Context, name string) error {
	if name != "" && s.registry.Get(name) == nil {
		return fmt.Errorf("unknown provider %q", name)
	}
	if err := s.store.Set(ctx, SettingActiveProvider, name); err != nil {
		return err
	}
	s.registry.SetActive(name)
	return nil
}

// TestResult is the outcome of a probe call. Failure is reported in the body
// with HTTP 200, so the settings page can show the provider's own words instead
// of a generic error.
type TestResult struct {
	OK      bool   `json:"ok"`
	Model   string `json:"model"`
	Reply   string `json:"reply,omitempty"`
	Error   string `json:"error,omitempty"`
	Tokens  int    `json:"tokens,omitempty"`
	CostUSD string `json:"cost_usd,omitempty"`
}

// Test sends the smallest useful request: one short question with one trivial
// tool. It checks the credentials, the model name, and whether the model calls
// tools at all, which is the failure that actually matters here. A model that
// answers fluently but ignores tools cannot do anything useful with an
// inventory, and finding that out at setup beats finding it out mid-question.
func (s *Service) Test(ctx context.Context, name string) (TestResult, error) {
	p := s.registry.Get(name)
	if p == nil {
		return TestResult{}, fmt.Errorf("unknown provider %q", name)
	}
	res := TestResult{Model: p.ConfiguredModel()}

	schema := json.RawMessage(`{"type":"object","properties":{"answer":{"type":"integer","description":"the sum"}},"required":["answer"]}`)
	resp, err := p.Chat(ctx, ChatRequest{
		System: "You are being checked for connectivity. Use the tool provided.",
		Messages: []Message{{
			Role: RoleUser,
			Text: "Call the add tool with 2 and 3. Reply with nothing else.",
		}},
		Tools: []ToolDef{{
			Name:        "add",
			Description: "Add two integers and return the sum.",
			Schema:      schema,
		}},
		// Generous for a one-word answer, because a reasoning model spends
		// output tokens thinking before it calls anything. At 512 a working
		// local thinking model failed this test by truncation and then
		// answered a real question fine, which is the worst kind of wrong: it
		// tells you to replace a model that works.
		MaxTokens: 4096,
	})
	if err != nil {
		res.Error = err.Error()
		return res, nil
	}

	res.OK = true
	res.Tokens = resp.Usage.InputTokens + resp.Usage.OutputTokens
	if resp.Usage.CostKnown {
		res.CostUSD = fmt.Sprintf("%.6f", resp.Usage.EstimatedCostUSD)
	}
	switch {
	case len(resp.ToolCalls) > 0:
		res.Reply = fmt.Sprintf("Connected, and the model called %q as asked.", resp.ToolCalls[0].Name)
	case resp.Truncated:
		res.OK = false
		res.Error = "The model ran out of output tokens before finishing. Raise the limit or pick a different model."
	default:
		// Connected but not usable: worth saying plainly rather than passing.
		res.OK = false
		res.Error = "Connected, but the model answered in prose instead of calling the tool. It will not be able to search your inventory. Pick a model that supports tool calling."
	}
	return res, nil
}

// ListModels asks a provider what it has installed. Providers that cannot
// enumerate return a nil slice and no error, which the UI reads as "type it in".
func (s *Service) ListModels(ctx context.Context, name string) ([]string, error) {
	p := s.registry.Get(name)
	if p == nil {
		return nil, fmt.Errorf("unknown provider %q", name)
	}
	lister, ok := p.(ModelLister)
	if !ok {
		return nil, nil
	}
	return lister.ListModels(ctx)
}

// loadConfig reads one provider's stored config. A missing key is an empty
// config, not an error.
func (s *Service) loadConfig(ctx context.Context, name string) (map[string]string, error) {
	raw, err := s.get(ctx, SettingProviderPrefix+name)
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return map[string]string{}, nil
	}
	cfg := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, fmt.Errorf("decode config for %q: %w", name, err)
	}
	return cfg, nil
}

// get returns "" for a missing key and an error for anything else.
func (s *Service) get(ctx context.Context, key string) (string, error) {
	v, err := s.store.Get(ctx, key)
	if err != nil {
		if errors.Is(err, s.notFound) || errors.Is(err, ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	return v, nil
}

// maskConfig replaces every stored secret with the mask. An unset secret stays
// empty rather than becoming "***", so the UI can tell "nothing saved" from
// "something saved that you cannot see".
func maskConfig(fields []ConfigField, cfg map[string]string) map[string]string {
	secret := map[string]bool{}
	for _, f := range fields {
		if f.Type == "password" {
			secret[f.Key] = true
		}
	}
	out := make(map[string]string, len(cfg))
	for k, v := range cfg {
		if secret[k] && v != "" {
			out[k] = MaskValue
			continue
		}
		out[k] = v
	}
	return out
}

func hasSecret(fields []ConfigField, cfg map[string]string) bool {
	for _, f := range fields {
		if f.Type == "password" && cfg[f.Key] != "" {
			return true
		}
	}
	return false
}
