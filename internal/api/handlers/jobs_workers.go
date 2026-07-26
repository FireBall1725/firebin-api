// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"context"
	"time"

	"github.com/firelabsca/firebin-api/internal/jobs"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

// BulkEnrichArgs is the payload for the bulk metadata refresh. ArgsVersion lets
// the shape evolve without breaking jobs already queued.
type BulkEnrichArgs struct {
	TaskID      uuid.UUID   `json:"task_id"`
	ArgsVersion int         `json:"args_version"`
	PartIDs     []uuid.UUID `json:"part_ids"`
}

func (BulkEnrichArgs) Kind() string { return "bulk_enrich" }

// bulkEnrichWorker refreshes each part's metadata from its primary MPN,
// force-fresh, applying package, description, parameters, and datasheet. It runs
// inside the jobs.Run harness so status, progress, logs, and cancellation are
// handled for it. Idempotent: SetParameter upserts, package and datasheet
// overwrite, so a retry changes nothing.
type bulkEnrichWorker struct {
	river.WorkerDefaults[BulkEnrichArgs]
	h    *Handler
	deps *jobs.Deps
}

// Timeout is a real ceiling, neither River's 60s default nor disabled: a large
// bulk gets minutes to finish, and a wedged run still dies.
func (w *bulkEnrichWorker) Timeout(*river.Job[BulkEnrichArgs]) time.Duration { return 30 * time.Minute }

func (w *bulkEnrichWorker) Work(ctx context.Context, job *river.Job[BulkEnrichArgs]) error {
	a := job.Args
	return w.deps.Run(ctx, a.TaskID, job.Attempt, job.MaxAttempts, func(r *jobs.Run) error {
		rctx := r.Context()
		updated, skipped := 0, 0

		err := jobs.ForEach(r, a.PartIDs, func(_ int, pid uuid.UUID) {
			part, err := w.h.Parts.Get(rctx, pid)
			if err != nil {
				r.Log("warn", "part %s not found, skipped", pid)
				skipped++
				return
			}
			mps, _ := w.h.Catalog.ListManufacturerParts(rctx, pid)
			if len(mps) == 0 || mps[0].MPN == "" {
				r.Log("warn", "%s: no MPN, skipped", part.Name)
				skipped++
				return
			}
			primary := mps[0]

			en, err := w.h.enrichAll(rctx, primary.MPN, nil)
			if err != nil || en == nil {
				r.Log("warn", "%s (%s): no enrichment", part.Name, primary.MPN)
				skipped++
				return
			}
			_ = w.h.EnrichCache.Set(rctx, primary.MPN, en)
			// Same apply path as the single-part Update: package, description,
			// parameters, datasheet, AND supplier SKUs + pricing.
			w.h.applyEnrichment(rctx, part, primary, en)
			r.Log("info", "%s (%s) refreshed from %s: %s", part.Name, primary.MPN, en.Source, pkgOrDash(en.Package))
			updated++
		})

		r.SetResult(map[string]int{"updated": updated, "skipped": skipped})
		w.h.Bus.Publish("parts") // refresh open parts views
		if err != nil {
			return err // cancelled part-way
		}
		r.Log("info", "done: %d updated, %d skipped", updated, skipped)
		return nil
	})
}

func pkgOrDash(s string) string {
	if s == "" {
		return "(no package)"
	}
	return s
}
