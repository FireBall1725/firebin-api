// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package jobs

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

// Queue names. Quota-sensitive work runs on low-concurrency queues.
const (
	QueueDefault = river.QueueDefault
	QueueEnrich  = "enrich"
	QueueLabels  = "labels"
	QueueIngest  = "ingest"
)

// Service owns the River client and the transactional enqueue. River is a pure
// executor here; task status lives only in our tables.
type Service struct {
	pool      *pgxpool.Pool
	client    *river.Client[pgx.Tx]
	store     *Store
	deps      *Deps
	retention time.Duration // finished tasks older than this are pruned; 0 disables
}

// SetRetention configures how long finished tasks are kept before the periodic
// prune removes them. Call before Start.
func (s *Service) SetRetention(d time.Duration) { s.retention = d }

// New builds the service and the River client with the given registered workers.
func New(pool *pgxpool.Pool, store *Store, deps *Deps, workers *river.Workers) (*Service, error) {
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			QueueDefault: {MaxWorkers: 8},
			QueueEnrich:  {MaxWorkers: 2},
			QueueLabels:  {MaxWorkers: 4},
			QueueIngest:  {MaxWorkers: 2},
		},
		Workers: workers,
	})
	if err != nil {
		return nil, err
	}
	return &Service{pool: pool, client: client, store: store, deps: deps}, nil
}

func (s *Service) Store() *Store { return s.store }
func (s *Service) Deps() *Deps   { return s.deps }

// Migrate runs River's own schema migrations (its river_* tables), alongside
// FireBin's golang-migrate set.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	m, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return err
	}
	_, err = m.Migrate(ctx, rivermigrate.DirectionUp, nil)
	return err
}

// Start begins the worker pool and the reconciler. Stop drains them.
func (s *Service) Start(ctx context.Context) error {
	if err := s.client.Start(ctx); err != nil {
		return err
	}
	s.startReconciler(ctx)
	return nil
}

func (s *Service) Stop(ctx context.Context) error { return s.client.Stop(ctx) }

// EnqueueMeta carries the non-arg task metadata.
type EnqueueMeta struct {
	Type          string
	Queue         string
	MaxAttempts   int
	CreatedBy     *uuid.UUID
	ArgsSummary   json.RawMessage
	ProgressTotal int
}

// Enqueue inserts the task row and the River job in one transaction, so a job
// can never reference a task that did not commit, and vice versa. The caller
// generates taskID and sets it inside args before calling.
func (s *Service) Enqueue(ctx context.Context, taskID uuid.UUID, args river.JobArgs, meta EnqueueMeta) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	ma := meta.MaxAttempts
	if ma < 1 {
		ma = 1
	}
	task := models.Task{
		ID:            taskID,
		Type:          meta.Type,
		MaxAttempts:   ma,
		CreatedBy:     meta.CreatedBy,
		ArgsSummary:   meta.ArgsSummary,
		ProgressTotal: meta.ProgressTotal,
	}
	if err := s.store.CreateTask(ctx, tx, task); err != nil {
		return err
	}

	opts := &river.InsertOpts{MaxAttempts: ma}
	if meta.Queue != "" {
		opts.Queue = meta.Queue
	}
	res, err := s.client.InsertTx(ctx, tx, args, opts)
	if err != nil {
		return err
	}
	if err := s.store.SetRiverJobID(ctx, tx, taskID, res.Job.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Cancel flags the task cancelling, cancels the in-flight worker context, and
// tells River to stop retries or drop a queued job.
func (s *Service) Cancel(ctx context.Context, taskID uuid.UUID) error {
	riverJobID, ok, err := s.store.RequestCancel(ctx, taskID)
	if err != nil {
		return err
	}
	if !ok {
		return nil // already terminal; nothing to cancel
	}
	s.deps.cancels.cancel(taskID)
	if riverJobID != nil {
		_, _ = s.client.JobCancel(ctx, *riverJobID)
	}
	s.deps.publishTask(taskID)
	return nil
}

// startReconciler runs a once-a-minute sweep that fails tasks stuck non-terminal
// whose River job is gone (a hard crash the worker could not write itself). It
// only ever corrects, never leads.
func (s *Service) startReconciler(ctx context.Context) {
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		s.pruneOnce(ctx) // clear anything already past retention on boot
		ticks := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.reconcileOnce(ctx)
				if ticks++; ticks%60 == 0 { // roughly hourly
					s.pruneOnce(ctx)
				}
			}
		}
	}()
}

// pruneOnce deletes finished tasks older than the retention window (logs cascade),
// keeping the tasks table bounded without user action. A no-op when retention is 0.
func (s *Service) pruneOnce(ctx context.Context) {
	if s.retention <= 0 {
		return
	}
	n, err := s.store.ClearFinished(ctx, time.Now().Add(-s.retention))
	if err != nil {
		slog.Warn("prune finished tasks", "error", err)
		return
	}
	if n > 0 {
		slog.Info("pruned finished tasks", "count", n, "retention", s.retention.String())
	}
}

func (s *Service) reconcileOnce(ctx context.Context) {
	stuck, err := s.store.FindStuck(ctx, 2*time.Minute)
	if err != nil {
		slog.Warn("reconcile: find stuck tasks", "error", err)
		return
	}
	for _, st := range stuck {
		live := false
		if st.RiverJobID != nil {
			live, _ = s.store.riverJobLive(ctx, *st.RiverJobID)
		}
		if live {
			continue
		}
		_ = s.store.MarkTerminal(ctx, st.ID, "failed", "worker lost (reconciled)", nil)
		_, _ = s.store.AppendLog(ctx, st.ID, "error", "reconciler: no live River job, marking failed")
		s.deps.publishTask(st.ID)
		slog.Warn("reconciled stuck task", "task", st.ID)
	}
}
