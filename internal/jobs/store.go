// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a task does not exist.
var ErrNotFound = errors.New("task not found")

// db is satisfied by both *pgxpool.Pool and pgx.Tx, so store methods work inside
// or outside a transaction.
type db interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Store is the data layer for tasks and their log lines.
type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// CreateTask inserts a queued task. Runs inside the enqueue transaction (pass
// the tx as q) so the task row and the River job commit together.
func (s *Store) CreateTask(ctx context.Context, q db, t models.Task) error {
	summary := t.ArgsSummary
	if summary == nil {
		summary = json.RawMessage(`{}`)
	}
	_, err := q.Exec(ctx, `
		INSERT INTO tasks (id, type, status, args_summary, max_attempts, priority, created_by, progress_total)
		VALUES ($1, $2, 'queued', $3, $4, $5, $6, $7)`,
		t.ID, t.Type, summary, t.MaxAttempts, t.Priority, t.CreatedBy, t.ProgressTotal)
	return err
}

// SetRiverJobID links the task to its River job. Runs inside the enqueue tx.
func (s *Store) SetRiverJobID(ctx context.Context, q db, taskID uuid.UUID, riverJobID int64) error {
	_, err := q.Exec(ctx, `UPDATE tasks SET river_job_id = $2 WHERE id = $1`, taskID, riverJobID)
	return err
}

// MarkRunning flags a task running and stamps started_at on first run.
func (s *Store) MarkRunning(ctx context.Context, taskID uuid.UUID, attempt int) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE tasks SET status = 'running', attempt = $2,
			started_at = COALESCE(started_at, now())
		WHERE id = $1`, taskID, attempt)
	return err
}

// MarkTerminal sets a final status (completed, failed, retrying, cancelled) with
// its error and result. finished_at is stamped only for truly terminal states.
func (s *Store) MarkTerminal(ctx context.Context, taskID uuid.UUID, status, errMsg string, result json.RawMessage) error {
	final := status == "completed" || status == "failed" || status == "cancelled"
	_, err := s.pool.Exec(ctx, `
		UPDATE tasks SET status = $2, error = $3, result = COALESCE($4, result),
			finished_at = CASE WHEN $5 THEN now() ELSE finished_at END
		WHERE id = $1`, taskID, status, errMsg, result, final)
	return err
}

// SetProgress updates the counters.
func (s *Store) SetProgress(ctx context.Context, taskID uuid.UUID, done, total int) error {
	_, err := s.pool.Exec(ctx, `UPDATE tasks SET progress_done = $2, progress_total = $3 WHERE id = $1`, taskID, done, total)
	return err
}

// RequestCancel flags a live task cancelling and returns its River job id (if
// any) so the caller can also cancel it in River. Returns ErrNotFound if the
// task is missing, and ok=false if it was already terminal.
func (s *Store) RequestCancel(ctx context.Context, taskID uuid.UUID) (riverJobID *int64, ok bool, err error) {
	row := s.pool.QueryRow(ctx, `
		UPDATE tasks SET status = 'cancelling'
		WHERE id = $1 AND status IN ('queued','running','retrying')
		RETURNING river_job_id`, taskID)
	err = row.Scan(&riverJobID)
	if errors.Is(err, pgx.ErrNoRows) {
		// Either missing or already terminal; distinguish for a good message.
		var exists bool
		if e := s.pool.QueryRow(ctx, `SELECT true FROM tasks WHERE id = $1`, taskID).Scan(&exists); errors.Is(e, pgx.ErrNoRows) {
			return nil, false, ErrNotFound
		}
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return riverJobID, true, nil
}

// IsCancelling reports whether a cancel has been requested for this task.
func (s *Store) IsCancelling(ctx context.Context, taskID uuid.UUID) bool {
	var status string
	if err := s.pool.QueryRow(ctx, `SELECT status FROM tasks WHERE id = $1`, taskID).Scan(&status); err != nil {
		return false
	}
	return status == "cancelling" || status == "cancelled"
}

// AppendLog writes one log line and returns its cursor id.
func (s *Store) AppendLog(ctx context.Context, taskID uuid.UUID, level, message string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO job_logs (task_id, level, message) VALUES ($1, $2, $3) RETURNING id`,
		taskID, level, message).Scan(&id)
	return id, err
}

const taskCols = `id, type, status, progress_done, progress_total, args_summary, result,
	error, attempt, max_attempts, priority, created_by, created_at, started_at, finished_at`

func scanTask(row pgx.Row) (models.Task, error) {
	var t models.Task
	err := row.Scan(&t.ID, &t.Type, &t.Status, &t.ProgressDone, &t.ProgressTotal,
		&t.ArgsSummary, &t.Result, &t.Error, &t.Attempt, &t.MaxAttempts, &t.Priority,
		&t.CreatedBy, &t.CreatedAt, &t.StartedAt, &t.FinishedAt)
	return t, err
}

// Get returns one task.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (models.Task, error) {
	t, err := scanTask(s.pool.QueryRow(ctx, `SELECT `+taskCols+` FROM tasks WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

// List returns recent tasks, newest first, optionally filtered by status/type.
func (s *Store) List(ctx context.Context, status, typ string, limit int) ([]models.Task, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+taskCols+` FROM tasks
		WHERE ($1 = '' OR status = $1) AND ($2 = '' OR type = $2)
		ORDER BY created_at DESC LIMIT $3`, status, typ, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]models.Task, 0)
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ClearFinished deletes terminal tasks (completed/failed/cancelled) created
// before the cutoff; their job_logs cascade away. Running/queued/retrying tasks
// are never touched. Returns how many were removed. Pass time.Now() to clear all
// finished tasks (manual "clear"), or now-minus-retention for the periodic prune.
func (s *Store) ClearFinished(ctx context.Context, before time.Time) (int64, error) {
	ct, err := s.pool.Exec(ctx, `
		DELETE FROM tasks
		WHERE status IN ('completed', 'failed', 'cancelled') AND created_at < $1`, before)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

// Logs returns a task's log lines after the given cursor id.
func (s *Store) Logs(ctx context.Context, taskID uuid.UUID, afterID int64, limit int) ([]models.JobLog, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, task_id, ts, level, message FROM job_logs
		WHERE task_id = $1 AND id > $2 ORDER BY id ASC LIMIT $3`, taskID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]models.JobLog, 0)
	for rows.Next() {
		var l models.JobLog
		if err := rows.Scan(&l.ID, &l.TaskID, &l.TS, &l.Level, &l.Message); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// stuckTask is one row the reconciler considers.
type stuckTask struct {
	ID         uuid.UUID
	RiverJobID *int64
}

// FindStuck returns non-terminal tasks last touched before the cutoff. The
// reconciler checks each against River and fails the orphans.
func (s *Store) FindStuck(ctx context.Context, olderThan time.Duration) ([]stuckTask, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, river_job_id FROM tasks
		WHERE status IN ('queued','running','retrying','cancelling')
		  AND updated_at < now() - $1::interval`, olderThan.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []stuckTask
	for rows.Next() {
		var st stuckTask
		if err := rows.Scan(&st.ID, &st.RiverJobID); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// riverJobLive reports whether a River job is still active (not finished,
// discarded, or missing). This is the one place we read River's own table, and
// only the reconciler calls it.
func (s *Store) riverJobLive(ctx context.Context, riverJobID int64) (bool, error) {
	var state string
	err := s.pool.QueryRow(ctx, `SELECT state FROM river_job WHERE id = $1`, riverJobID).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	switch state {
	case "available", "running", "retryable", "scheduled", "pending":
		return true, nil
	default: // completed, discarded, cancelled
		return false, nil
	}
}
