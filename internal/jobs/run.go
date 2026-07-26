// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package jobs is the FireBin background-job infrastructure: the client-facing
// task store, the run harness workers wrap their body in, cancellation, the
// River service, and the reconciler. It holds no business logic; workers live
// with the code they operate on.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/firelabsca/firebin-api/internal/events"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

// cancels maps a running task to the CancelFunc of its worker context, so a
// cancel request stops the in-flight work immediately instead of waiting for the
// worker to notice a flag on its next loop.
type cancels struct {
	mu sync.Mutex
	m  map[uuid.UUID]context.CancelFunc
}

func newCancels() *cancels { return &cancels{m: make(map[uuid.UUID]context.CancelFunc)} }

func (c *cancels) add(id uuid.UUID, fn context.CancelFunc) {
	c.mu.Lock()
	c.m[id] = fn
	c.mu.Unlock()
}
func (c *cancels) remove(id uuid.UUID) {
	c.mu.Lock()
	delete(c.m, id)
	c.mu.Unlock()
}
func (c *cancels) cancel(id uuid.UUID) {
	c.mu.Lock()
	if fn, ok := c.m[id]; ok {
		fn()
	}
	c.mu.Unlock()
}

// Deps is what a worker needs from the jobs infrastructure: the store, the SSE
// bus, and the cancel registry.
type Deps struct {
	Store   *Store
	Bus     *events.Broker
	cancels *cancels
}

func NewDeps(store *Store, bus *events.Broker) *Deps {
	return &Deps{Store: store, Bus: bus, cancels: newCancels()}
}

func (d *Deps) publishTask(id uuid.UUID) {
	if d.Bus != nil {
		d.Bus.Publish("task:" + id.String())
		d.Bus.Publish("tasks")
	}
}

// Run wraps a worker body. It marks the task running, registers cancellation,
// runs fn, then writes the one terminal status based on the outcome and River's
// attempt count. This is the single writer of a task's status, so nothing can
// drift. Terminal writes use a cancel-detached context so a cancelled run can
// still record its final state and log lines.
func (d *Deps) Run(ctx context.Context, taskID uuid.UUID, attempt, maxAttempts int, fn func(*Run) error) error {
	_ = d.Store.MarkRunning(ctx, taskID, attempt)
	d.publishTask(taskID)

	rctx, cancel := context.WithCancel(ctx)
	d.cancels.add(taskID, cancel)
	defer d.cancels.remove(taskID)
	defer cancel()

	r := &Run{ctx: rctx, taskID: taskID, deps: d}
	r.Log("info", "started")
	err := fn(r)

	fctx := context.WithoutCancel(ctx)
	switch {
	case err == nil:
		_ = d.Store.MarkTerminal(fctx, taskID, "completed", "", r.resultJSON())
		r.logIn(fctx, "info", "completed")
	case d.Store.IsCancelling(fctx, taskID):
		_ = d.Store.MarkTerminal(fctx, taskID, "cancelled", "cancelled by request", r.resultJSON())
		r.logIn(fctx, "warn", "cancelled")
		err = river.JobCancel(err) // don't let River retry a cancelled job
	case attempt < maxAttempts:
		_ = d.Store.MarkTerminal(fctx, taskID, "retrying", err.Error(), r.resultJSON())
		r.logIn(fctx, "warn", "attempt %d failed, retrying: %v", attempt, err)
	default:
		_ = d.Store.MarkTerminal(fctx, taskID, "failed", err.Error(), r.resultJSON())
		r.logIn(fctx, "error", "failed: %v", err)
	}
	d.publishTask(taskID)
	return err
}

// Run is the handle a worker body uses to log, report progress, and read its
// (cancellable) context.
type Run struct {
	ctx       context.Context
	taskID    uuid.UUID
	deps      *Deps
	result    any
	lastFlush time.Time
}

// Context returns the worker context, cancelled when the task is cancelled or
// the API is shutting down. Thread it through every blocking call.
func (r *Run) Context() context.Context { return r.ctx }

// SetResult records the job's structured output, stored on the task.
func (r *Run) SetResult(v any) { r.result = v }

func (r *Run) resultJSON() json.RawMessage {
	if r.result == nil {
		return nil
	}
	b, err := json.Marshal(r.result)
	if err != nil {
		return nil
	}
	return b
}

// Log appends a line to the task timeline and pushes it over SSE.
func (r *Run) Log(level, format string, a ...any) { r.logIn(r.ctx, level, format, a...) }

func (r *Run) logIn(ctx context.Context, level, format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	// Detach from cancellation so a cancelled run's final lines still persist.
	if _, err := r.deps.Store.AppendLog(context.WithoutCancel(ctx), r.taskID, level, msg); err == nil {
		r.deps.publishTask(r.taskID)
	}
}

// Progress updates the counters, throttled to at most one write per 500ms
// (always flushing the final tick).
func (r *Run) Progress(done, total int) {
	now := time.Now()
	if done >= total || now.Sub(r.lastFlush) > 500*time.Millisecond {
		r.lastFlush = now
		_ = r.deps.Store.SetProgress(context.WithoutCancel(r.ctx), r.taskID, done, total)
		r.deps.publishTask(r.taskID)
	}
}

// ForEach runs fn over items, checking for cancellation between each and
// advancing progress. It returns the context error if cancelled part-way.
func ForEach[T any](r *Run, items []T, fn func(int, T)) error {
	r.Progress(0, len(items))
	for i, item := range items {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		fn(i, item)
		r.Progress(i+1, len(items))
	}
	return nil
}
