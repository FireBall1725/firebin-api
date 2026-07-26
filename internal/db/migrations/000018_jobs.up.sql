-- Client-facing job/task records. This is the single source of truth for a
-- job's status, progress, and result; River is a pure executor whose internal
-- state is never surfaced to clients. The worker owns these transitions.
-- Status is a one-vocabulary CHECK constraint (DB-enforced) rather than free
-- text, so the dual-vocabulary drift that plagued the Librarium jobs never
-- happens here.
CREATE TABLE tasks (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    type           TEXT        NOT NULL,
    status         TEXT        NOT NULL DEFAULT 'queued'
                   CHECK (status IN ('queued','running','retrying','completed','failed','cancelling','cancelled')),
    progress_done  INT         NOT NULL DEFAULT 0,
    progress_total INT         NOT NULL DEFAULT 0,
    args_summary   JSONB       NOT NULL DEFAULT '{}',
    result         JSONB,
    error          TEXT        NOT NULL DEFAULT '',
    attempt        INT         NOT NULL DEFAULT 0,
    max_attempts   INT         NOT NULL DEFAULT 1,
    priority       INT         NOT NULL DEFAULT 0,
    river_job_id   BIGINT,
    created_by     UUID        REFERENCES users(id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at     TIMESTAMPTZ,
    finished_at    TIMESTAMPTZ,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_tasks_status     ON tasks(status);
CREATE INDEX idx_tasks_type       ON tasks(type);
CREATE INDEX idx_tasks_created_at ON tasks(created_at DESC);

CREATE TRIGGER trg_tasks_updated_at BEFORE UPDATE ON tasks
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Per-job log lines. The bigserial id IS the cursor: globally monotonic and
-- race-free, so there is no MAX(seq)+1 subquery for concurrent writers to
-- collide on. Clients page with ?after_id=N.
CREATE TABLE job_logs (
    id      BIGSERIAL   PRIMARY KEY,
    task_id UUID        NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    ts      TIMESTAMPTZ NOT NULL DEFAULT now(),
    level   TEXT        NOT NULL DEFAULT 'info' CHECK (level IN ('debug','info','warn','error')),
    message TEXT        NOT NULL
);
CREATE INDEX idx_job_logs_task ON job_logs(task_id, id);
