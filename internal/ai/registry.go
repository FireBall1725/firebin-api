// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package ai

import "sync"

// Registry holds the registered providers and remembers which one is active.
// Safe for concurrent use: chat requests read it while an admin saving settings
// writes to it.
type Registry struct {
	mu        sync.RWMutex
	providers []ChatProvider
	active    string
}

func NewRegistry() *Registry { return &Registry{} }

func (r *Registry) Register(p ChatProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers = append(r.providers, p)
}

// All returns every registered provider, active or not.
func (r *Registry) All() []ChatProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ChatProvider, len(r.providers))
	copy(out, r.providers)
	return out
}

// Get returns the provider with this name, or nil.
func (r *Registry) Get(name string) ChatProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.providers {
		if p.Info().Name == name {
			return p
		}
	}
	return nil
}

// Configure applies a config map to one provider by name.
func (r *Registry) Configure(name string, cfg map[string]string) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.providers {
		if p.Info().Name == name {
			p.Configure(cfg)
			return
		}
	}
}

// SetActive records which provider serves chat.
func (r *Registry) SetActive(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active = name
}

// ActiveName is the selected provider's name, whether or not it is usable.
func (r *Registry) ActiveName() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.active
}

// Active returns the provider that should serve chat, or nil when none is
// selected or the selected one is not configured. A selected-but-broken
// provider returning nil is deliberate: falling back to a different provider
// would silently send the conversation somewhere the admin did not choose.
func (r *Registry) Active() ChatProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.providers {
		if p.Info().Name != r.active {
			continue
		}
		if !p.Enabled() {
			return nil
		}
		return p
	}
	return nil
}
