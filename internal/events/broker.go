// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package events is a tiny in-process publish/subscribe broker used to push
// "something changed" signals to connected clients over SSE, so every open
// FireBin view auto-refreshes when anyone mutates the inventory.
package events

import "sync"

// Event is a coarse change signal. Resource names the affected view
// ("parts", "stock", "locations", "categories"); clients refetch on match.
type Event struct {
	Resource string `json:"resource"`
}

// Broker fans out events to all current subscribers. Safe for concurrent use.
type Broker struct {
	mu   sync.RWMutex
	subs map[chan Event]struct{}
}

func NewBroker() *Broker {
	return &Broker{subs: make(map[chan Event]struct{})}
}

// Subscribe registers a new subscriber and returns its channel plus an
// unsubscribe function the caller must defer.
func (b *Broker) Subscribe() (<-chan Event, func()) {
	// Buffered so a briefly-slow client doesn't block the publisher.
	ch := make(chan Event, 16)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()

	return ch, func() {
		b.mu.Lock()
		if _, ok := b.subs[ch]; ok {
			delete(b.subs, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
}

// Publish delivers an event to every subscriber. A subscriber whose buffer is
// full is skipped rather than blocking the publisher — it will refetch on the
// next event or its periodic reconnect, so a dropped signal is harmless.
func (b *Broker) Publish(resource string) {
	ev := Event{Resource: resource}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}
