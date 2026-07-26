// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Task is the client-facing record of one background job. It is the single
// source of truth for status, progress, and result; River's own job state is
// never surfaced. The worker owns these transitions.
type Task struct {
	ID            uuid.UUID       `json:"id"`
	Type          string          `json:"type"`
	Status        string          `json:"status"`
	ProgressDone  int             `json:"progress_done"`
	ProgressTotal int             `json:"progress_total"`
	ArgsSummary   json.RawMessage `json:"args_summary,omitempty"`
	Result        json.RawMessage `json:"result,omitempty"`
	Error         string          `json:"error,omitempty"`
	Attempt       int             `json:"attempt"`
	MaxAttempts   int             `json:"max_attempts"`
	Priority      int             `json:"priority"`
	CreatedBy     *uuid.UUID      `json:"created_by,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	StartedAt     *time.Time      `json:"started_at,omitempty"`
	FinishedAt    *time.Time      `json:"finished_at,omitempty"`
}

// JobLog is one line in a task's timeline. The id is the paging cursor.
type JobLog struct {
	ID      int64     `json:"id"`
	TaskID  uuid.UUID `json:"task_id"`
	TS      time.Time `json:"ts"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}
