// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package httplib

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/firelabsca/firebin-api/internal/kicad/httplib/source"
)

// uncategorizedID is the synthetic category holding parts whose category_id is
// null. Without it those parts would be invisible: KiCad can only browse by
// category, and a per-category listing never returns them.
const uncategorizedID = "uncategorized"

// apiListLimit mirrors the hardcoded LIMIT 500 in the API's part repository.
// The API exposes no pagination, so hitting this is silent truncation on their
// side; we detect and log it rather than serve a quietly partial catalogue.
const apiListLimit = 500

// Snapshot is an immutable view of the whole catalogue, already projected into
// KiCad shapes. Rebuilt wholesale on refresh and swapped in under a lock, so a
// reader never sees a half-updated catalogue.
type Snapshot struct {
	Categories []Category
	ByCategory map[string][]Part
	ByID       map[string]Part
	BuiltAt    time.Time
}

// Cache holds the current Snapshot and refreshes it off the request path.
//
// This is the mechanism that makes the integration usable. Opening KiCad's
// Symbol Chooser enumerates every category and, in KiCad 10, back-fills detail
// for any part whose listing entry lacked it — one HTTP request per part. We
// avoid that by serving complete detail objects straight from this cache, so a
// chooser open costs one request per category against memory, and the upstream
// fan-out happens on a timer instead.
// Source is where a snapshot is built from. Implemented in the API against the
// repositories; stubbed in tests.
//
// Parts returns list rows, from which only the id and category are read; Part
// returns the full detail the mapping needs, because a list row carries neither
// parameters nor manufacturer parts.
type Source interface {
	Categories(ctx context.Context) ([]source.Category, error)
	Parts(ctx context.Context) ([]source.Part, error)
	Part(ctx context.Context, id string) (*source.Part, error)
}

type Cache struct {
	src    Source
	marker string
	ttl    time.Duration
	log    *slog.Logger

	mu   sync.RWMutex
	snap *Snapshot

	// refreshMu serializes rebuilds so overlapping triggers do one pass, not
	// several.
	refreshMu sync.Mutex
}

// NewCache builds a cache. Run WarmUp in a goroutine and Start alongside it.
func NewCache(src Source, marker string, ttl time.Duration, log *slog.Logger) *Cache {
	return &Cache{src: src, marker: marker, ttl: ttl, log: log}
}

// Get returns the current snapshot. It never rebuilds and never blocks.
//
// This deliberately does not refresh on a stale or missing snapshot, which is a
// change from when this ran as its own service with no write timeout. The API's
// server sets WriteTimeout to 15s, and a cold rebuild is one detail composition
// per part; exceeding the timeout truncates the response mid-JSON, which KiCad
// reports as a parse error. Refreshing only ever happens on the timer.
//
// A missing snapshot yields an empty but structurally valid one rather than an
// error, because KiCad discards the whole library on any non-200. An empty
// chooser that fills in a moment beats a library that vanished.
func (c *Cache) Get(_ context.Context) (*Snapshot, error) {
	c.mu.RLock()
	snap := c.snap
	c.mu.RUnlock()

	if snap == nil {
		return &Snapshot{
			ByCategory: map[string][]Part{},
			ByID:       map[string]Part{},
		}, nil
	}
	return snap, nil
}

// Marker is the prefix put on the name of a part KiCad cannot resolve, so the
// reason is legible in the chooser rather than looking like a broken library.
func (c *Cache) Marker() string { return c.marker }

// WarmUp builds the first snapshot. Run it in a goroutine at boot: it must not
// gate the API coming up, since this feature is off by default and most
// instances never turn it on.
func (c *Cache) WarmUp(ctx context.Context) {
	if err := c.Refresh(ctx); err != nil {
		c.log.Warn("kicad catalogue warm-up failed; will retry on the timer", "error", err)
	}
}

// Start refreshes on a ticker until ctx is cancelled.
func (c *Cache) Start(ctx context.Context) {
	ticker := time.NewTicker(c.ttl)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.Refresh(ctx); err != nil {
				c.log.Warn("background refresh failed", "error", err)
			}
		}
	}
}

// Refresh rebuilds the snapshot from upstream and swaps it in.
func (c *Cache) Refresh(ctx context.Context) error {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()

	started := time.Now()

	cats, err := c.src.Categories(ctx)
	if err != nil {
		return err
	}
	// Category name drives which spec parameter becomes a symbol's Value.
	catName := map[string]string{}
	for _, cat := range cats {
		catName[cat.ID] = cat.Name
	}

	// One list call for the whole catalogue rather than one per category: it
	// is fewer round trips, and it is the only way to see parts whose
	// category_id is null.
	rows, err := c.src.Parts(ctx)
	if err != nil {
		return err
	}
	if len(rows) >= apiListLimit {
		c.log.Warn("part list hit the API's hardcoded limit; catalogue may be truncated",
			"returned", len(rows), "limit", apiListLimit)
	}

	snap := &Snapshot{
		ByCategory: map[string][]Part{},
		ByID:       map[string]Part{},
		BuiltAt:    time.Now(),
	}

	// Detail fetches are sequential. They run on the refresh timer, never in a
	// request, so wall-clock here costs nobody anything; keeping it serial
	// keeps the failure handling honest.
	var skipped int
	for _, row := range rows {
		full, err := c.src.Part(ctx, row.ID)
		if err != nil {
			// One unreadable part should not empty the library.
			c.log.Warn("skipping part; detail fetch failed", "part_id", row.ID, "error", err)
			skipped++
			continue
		}
		catID := uncategorizedID
		if full.CategoryID != nil && *full.CategoryID != "" {
			catID = *full.CategoryID
		}

		mapped := MapPart(*full, catName[catID], c.marker)
		snap.ByID[mapped.ID] = mapped
		snap.ByCategory[catID] = append(snap.ByCategory[catID], mapped)
	}

	for _, cat := range cats {
		if len(snap.ByCategory[cat.ID]) == 0 {
			// KiCad shows empty sub-libraries as dead ends; skip them.
			continue
		}
		snap.Categories = append(snap.Categories, MapCategory(cat))
	}
	if len(snap.ByCategory[uncategorizedID]) > 0 {
		snap.Categories = append(snap.Categories, Category{
			ID:          uncategorizedID,
			Name:        "Uncategorized",
			Description: "Parts with no FireBin category",
		})
	}

	c.mu.Lock()
	c.snap = snap
	c.mu.Unlock()

	c.log.Info("catalogue refreshed",
		"categories", len(snap.Categories),
		"parts", len(snap.ByID),
		"skipped", skipped,
		"took", time.Since(started).String())
	return nil
}
