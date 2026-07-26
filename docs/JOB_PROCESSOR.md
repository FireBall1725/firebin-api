# FireBin Job Processor: Design

Status: proposed, 2026-07-24. Owner: FireBall. Scope: `firebin-api`.

## Why this exists

Right now FireBin does heavy work in the wrong place. The part-detail "Update from provider" chains four API calls from the browser (`enrich`, then `updatePart`, `updateManufacturerPart`, `createSupplierPart`), so that logic lives only in the React client. The bulk version runs server-side but synchronously inside the HTTP handler, which means a 500-part refresh is one request that blocks, times out around 30 to 60 seconds, and reports no progress. A job processor moves both cases behind one API the desktop and mobile clients call the same way.

This document is the contract for that processor. The goal is to design it once and not refactor the core the way Librarium's did. Every decision below names the failure mode it prevents.

## Decisions locked

1. **Queue engine: River** (`github.com/riverqueue/river`). It is a Postgres-backed job queue built on pgx by the pgx authors. It already implements the parts that are hard to get right and easy to bug: atomic claiming with `SELECT ... FOR UPDATE SKIP LOCKED`, leased jobs with automatic rescue of workers that die mid-run, retries with exponential backoff and jitter, unique jobs, periodic jobs, scheduled jobs, multiple queues with per-queue worker limits, and transactional insert. We do not write a queue. We write handlers and an API.
2. **Client-facing state lives in our own `tasks` table**, not River's internal `river_job`. River's schema is River's private API and carries no progress field. Coupling clients to it would force a refactor the first time River changes its tables. The `tasks` row is what web, desktop, and mobile read.
3. **A job type is data plus one function.** Typed args serialized to JSONB, one worker function, one registry entry. Adding a job type needs no schema migration and no change to the core, because the payload is JSONB and the dispatch is a registry lookup. This is the extensibility guarantee.
4. **Enqueue is transactional.** The triggering mutation, the `tasks` insert, and the River insert commit in one pgx transaction or none of them do. This kills the class of bug where a job runs against a row that never committed, or a row commits but its job never enqueues.
5. **Handlers are idempotent.** A job that runs twice must produce the same result as running once. Retries and at-least-once delivery make this non-negotiable.
6. **One source of truth, and the worker writes it.** `tasks` is authoritative for status, progress, and logs. River is a pure executor whose internal state is never surfaced to a client, and there is no second umbrella table mirrored onto River by hand. Librarium's worst bugs lived in exactly that mirror. A single reconciler is the only backstop, and it only ever corrects, never leads.

Why not a home-grown queue on Postgres: it is the exact code Librarium got wrong. `SKIP LOCKED` claiming, lease timeouts, retry backoff, and dead-lettering are subtle, and every one of them is a documented River feature that is already tested against production load. We spend our effort on FireBin-specific behaviour instead.

## Two things this replaces

The processor covers two distinct moves, and the plan keeps them separate.

**Fat API, thin client.** Business logic moves server-side so every client calls one endpoint and gets identical behaviour. The single-part enrich-and-apply becomes a server service that both a synchronous endpoint and the bulk job call. The React orchestration in `updateFromProvider` gets deleted.

**Asynchronous execution.** Long, bulk, or scheduled work runs in a background worker pool, reports progress, and survives an API restart. A bulk enrich of 500 parts becomes a job you enqueue in 20 milliseconds, watch over SSE, and cancel if it misbehaves.

## Architecture

The API process runs the River client and a worker pool as part of its lifecycle. It starts after the database connects and drains gracefully on shutdown so a `SIGTERM` during a deploy finishes or requeues in-flight jobs instead of losing them.

```
HTTP handler ──enqueue(tx)──► [ tasks row + river_job row ]  (one transaction)
                                        │
                        River worker pool (in the API process)
                                        │
      handler runs ──progress()──► tasks.progress + SSE "tasks"
                                        │
             success / failure ──► tasks.status + result/error
                                        │
   GET /tasks, GET /tasks/{id}, SSE ──► web / desktop / mobile
```

Three layers, and we own two of them. River owns the queue. We own the `tasks` mirror and the job registry, and we own every handler.

## Data model: `tasks` and `job_logs`

Two tables we own. `tasks` is the client-facing record, one row per enqueued job, created in the same transaction as the River insert. `job_logs` holds the per-job log lines (schema in the logs section below). Everything else is River's.

| Column | Type | Notes |
|---|---|---|
| `id` | uuid pk | our stable id, independent of River's `bigint` job id |
| `type` | text | job type key, e.g. `bulk_enrich` |
| `status` | enum | `queued`, `running`, `retrying`, `completed`, `failed`, `cancelling`, `cancelled` (DB-enforced, one vocabulary) |
| `progress_done` | int | items processed |
| `progress_total` | int | items expected (0 if unknown) |
| `args_summary` | jsonb | redacted summary for display, never secrets |
| `result` | jsonb | handler output (counts, ids, messages) |
| `error` | text | last error on failure |
| `attempt` | int | current attempt, written by the worker from `job.Attempt` |
| `max_attempts` | int | cap before dead-letter |
| `priority` | int | lower runs first |
| `river_job_id` | bigint | link to the River job for cancel and reclaim |
| `created_by` | uuid | user who started it |
| `created_at` | timestamptz | |
| `started_at` | timestamptz | |
| `finished_at` | timestamptz | |

Indexes on `status`, `type`, and `created_at desc`. All times are UTC. River manages its own tables through its own migrations, run alongside FireBin's `golang-migrate` set.

`status` is a Postgres enum, not free `text` with a comment. Librarium stored status as `text` and ended up with two vocabularies (`processing`/`done` against `running`/`completed`) and a `normalizeStatusForJobs` function to paper over them. One enum, enforced by the database, defined before the first job runs, removes that whole class of drift.

**The worker owns the status, and `tasks` is the only source of truth.** This is the single most important lesson from Librarium. Its processor kept a hand-mirrored umbrella table next to River and let the two drift; a cancelled import updated only its own detail table, never the umbrella row, so the unified history showed the job `running` with a null `finished_at` forever. FireBin has one authoritative record, `tasks`, and River is a pure executor whose job state is never shown to a client. The worker sets `running` on entry and, through a `defer`, sets the terminal state on exit: `completed` with a result, `failed` with the error, or `retrying` when `job.Attempt < job.MaxAttempts`. The defer reads the attempt count straight off the `*river.Job`, so the worker tells a retry from a final failure without any subscriber reconciling River's events against our table.

One backstop covers the case a worker can't write itself: a hard process crash between `running` and the defer. River's lease expires, re-runs the job, sets `running` again, and the task self-heals. If repeated crashes discard the job past its attempt cap, a reconciler that runs every minute finds any `tasks` row stuck `running` or `queued` whose River job is gone and marks it `failed` with "worker lost". That reconciler is the only code that ever reads River's job state for status, and it is a backstop, not the everyday path. There is no hand-mirrored history, so there is nothing to drift.

## A job type is three things

Each type is an args struct, a worker, and a registry entry. Adding a type touches only its own file.

```go
// 1. Typed args. Kind() is the stable type key stored in tasks.type.
//    ArgsVersion lets the payload evolve without breaking queued jobs.
type BulkEnrichArgs struct {
    TaskID      uuid.UUID   `json:"task_id"`
    ArgsVersion int         `json:"args_version"`
    PartIDs     []uuid.UUID `json:"part_ids"`
}
func (BulkEnrichArgs) Kind() string { return "bulk_enrich" }

// 2. Worker. Idempotent. Reports progress. Checks for cancellation.
type BulkEnrichWorker struct{ river.WorkerDefaults[BulkEnrichArgs]; deps *Deps }
func (w *BulkEnrichWorker) Work(ctx context.Context, job *river.Job[BulkEnrichArgs]) error { ... }

// 3. Registration, at startup. No migration, no core edit.
workers := river.NewWorkers()
river.AddWorker(workers, &BulkEnrichWorker{deps: deps})
```

The registry is the single place that knows every type. A new type is one `AddWorker` call. Because args are JSONB, no column changes and no migration. `ArgsVersion` is checked on decode so a worker deployed after a queued job can read the old shape or upgrade it.

## Concurrency, queues, and the Nexar quota

Jobs run on named queues, each with its own worker limit. This is how we protect the metered Nexar allowance of roughly 100 lookups per month.

| Queue | Max workers | Job types |
|---|---|---|
| `default` | 8 | most work |
| `enrich` | 2 | `enrich_part`, `bulk_enrich` |
| `labels` | 4 | `label_batch` |
| `ingest` | 2 | `bom_ingest` |

Enrichment runs at most 2 at a time on the `enrich` queue, and Digi-Key answers first for free, so Nexar is only touched when Digi-Key has no match. On top of the queue limit, the enrich handler decrements a monthly Nexar budget counter and skips the Nexar leg once the budget is spent, logging what it dropped. A bulk refresh of 500 resistors spends zero Nexar quota because Digi-Key has every one of them.

## Retries, dead-letter, and poison jobs

River retries a failed job up to `max_attempts` with exponential backoff and jitter. On the last attempt the worker's `defer` writes `tasks.status = failed` with the error, and River moves the job to the discarded state, which is the dead-letter. It does not retry forever and it does not block the queue behind it, which is the poison-job failure Librarium hit.

Each worker sets an explicit `Timeout`, and it is neither River's 60-second default nor disabled. Librarium learned both ends of this the hard way: the 60-second default killed enrichment jobs mid-run and left "double-incremented counters and overwritten item statuses when River re-enqueued", and the fix of setting `Timeout = -1` to disable it then made stuck provider calls impossible to cancel. FireBin sets a real ceiling per job type, generous enough to finish (a bulk enrich of 500 parts gets minutes, not 60 seconds) and finite enough that a wedged job dies. Idempotent handlers cover the timeout-then-retry case so a re-run never double-counts.

Duplicate enqueue is prevented with River unique jobs. A bulk enrich is unique by `(type, sorted part_ids)` for a short window, so double-clicking "Refresh metadata" queues one job, not two. Idempotent handlers cover the at-least-once case: `SetParameter` upserts, package assignment overwrites, and datasheet assignment overwrites, so a second run changes nothing.

## Progress, logs, and cancellation

Progress, logs, and cancellation are the three things a user watches while a job runs, and Librarium got all three wrong at the start: jobs launched but their logs never showed, and cancel would hang or do nothing. This section designs each so that specific failure can't recur.

**Progress.** The handler calls `progress(ctx, done, total)`, which updates `tasks.progress_*` and publishes a `tasks` signal on the SSE broker. Updates throttle to once per 500 milliseconds or once per 25 items, whichever comes first, so a 5,000-item job writes about 200 rows, not 5,000.

**Logs are part of the worker contract, not an optional side call.** This is the precise thing Librarium got wrong. Its `AppendEvent` had zero callers for the import and enrichment workers; only the AI pipeline logged, through a second, parallel `AppendEvent` on a different repository. Two of three job kinds ran with a permanently empty timeline even though the storage, the list endpoint, and the detail view were all wired end to end. The workers simply never called the writer.

FireBin closes that by construction. There is one logger, and it arrives as a required dependency on every `Work(ctx, job)` call, bound to the task. A worker calls `log.Info(ctx, "enriched %s -> %s", mpn, pkg)`, which writes a `job_logs` row and publishes a `task:{id}` SSE event carrying the line. The base item-loop helper that jobs are built on emits a per-item line on its own, so a job author who writes no logging at all still produces a usable timeline. Clients read history with `GET /tasks/{id}/logs?after_id=N` and tail new lines over SSE, so a line shows up whether the client opened the task before or after it was written.

`job_logs` columns: `id` bigserial primary key (this is the cursor, globally monotonic and race-free), `task_id` uuid, `ts` timestamptz, `level` (`debug`, `info`, `warn`, `error`), `message` text. Index on `(task_id, id)`. Cursoring on a `bigserial` primary key sidesteps Librarium's `seq = MAX(seq)+1` subquery, which two concurrent writers to one job can compute to the same value and collide on. Writes batch on the same 500-millisecond throttle as progress so a chatty job doesn't hammer the database one line at a time, and logs prune with their task.

**Cancellation.** Cancellation propagates through the context, and that is the whole trick Librarium missed. `POST /tasks/{id}/cancel` sets `tasks.status = cancelling` and cancels the running worker's context. The API holds an in-process map of `task_id` to `context.CancelFunc` for jobs running on this node, so cancel fires immediately instead of being a flag the worker has to notice on some later loop.

Two rules make cancel actually stop, and both are where Librarium's version hung. First, every blocking call inside a handler takes the job context and a timeout: the Digi-Key HTTP call, the Nexar call, and every database query. Librarium's enrichment worker set its River `Timeout` to `-1`, disabling it, and made provider calls with no in-flight cancellation, so a book stuck on a slow provider could not be cancelled until that HTTP call returned on its own, and nothing killed it. FireBin never disables the timeout and threads the job context through every call, so cancel lands in milliseconds. Second, the item loop checks `ctx.Err()` between items and returns promptly, committing already-processed items and setting `cancelled`. Each item is its own committed unit, so a cancel never leaves half-written state.

Librarium's own AI pipeline is the proof this works: it eventually grew a `watchCancellation` goroutine that cancels the context wrapping the provider call so an admin cancel "actually kills a stuck provider call rather than waiting for the provider's client timeout". FireBin builds that behaviour into the base handler from the first job, instead of retrofitting it into one kind out of three.

River's `client.JobCancel` covers the queue side, stopping future retries and marking the job cancelled. The in-process context cancel covers the running side. Both fire from the one cancel endpoint. A single API node makes the in-process map reliable today; a multi-node future swaps it for Postgres `LISTEN/NOTIFY` and nothing else in the design changes.

## Client-facing API

Action endpoints enqueue and return `202 Accepted` with the task. They no longer do the work in the request.

```
POST /api/v1/parts/bulk/enrich   → { "task_id": "..." }   (was synchronous)
POST /api/v1/parts/{id}/enrich   → { "task_id": "..." }   (new; replaces the React chain)
POST /api/v1/projects/{id}/bom/ingest → { "task_id": "..." }

GET  /api/v1/tasks?status=&type=&limit=   list
GET  /api/v1/tasks/{id}                   detail with progress and result
GET  /api/v1/tasks/{id}/logs?after_id=N   log lines since cursor N (history, paged)
POST /api/v1/tasks/{id}/cancel
POST /api/v1/tasks/{id}/retry             enqueue a fresh job from the same args
```

The SSE stream gains a `tasks` signal for list-level changes and a `task:{id}` signal for one task's progress and log lines. A client subscribes, sees a task move `queued → running → completed`, tails its logs live, and refetches. The web app grows an Activity panel that lists running and recent tasks and opens a per-task view with progress and a live log tail; the desktop and mobile apps render the same `tasks` and `job_logs` payloads, so the feature ships on all three without per-platform logic.

## Job catalogue

Initial set, in build order.

**`bulk_enrich`**: refresh metadata for many parts from their primary MPN. Args: `part_ids`. Idempotent. Queue `enrich`. Progress unit: one part. This is the first job migrated, because it already exists synchronously and proves the whole path.

**`enrich_part`**: the single-part enrich-and-apply, moved off the React client into the shared server service. Args: `part_id`. Idempotent. Queue `enrich`. The detail-page button calls the endpoint and watches the task.

**`bom_ingest`**: parse an uploaded KiCad project or BOM, match lines to inventory by MPN then value plus footprint, and store the result. Args: `project_id`, `asset_id`. Queue `ingest`. Progress unit: one BOM line. Heavy parsing belongs off the request path.

**`label_batch`**: render a large label sheet or a full drawer's worth of labels to PDF. Args: `media_id`, `part_ids`, `used_cells`. Queue `labels`. Synchronous stays for a handful of labels; the job handles hundreds.

Roadmap set, no new plumbing.

**`reorder_digikey`**: build a Digi-Key MyList or cart from a low-stock report or a BOM shortfall. Args: `part_ids` or `board_id`. This is the reorder bridge from the Digi-Key plan.

**`stock_sweep`**: a periodic job that recomputes low-stock flags and emits notifications. River periodic job, nightly.

**`scheduled_reenrich`**: a periodic job that re-enriches parts whose cache is older than 30 days, a few per run to stay inside the Nexar budget.

## Plugins, without a refactor

The registry pattern already lets a new in-process job type land as one file. Third-party plugins get durable execution through one generic job type instead of their own machinery.

A `plugin_webhook` job carries `{ plugin_id, event, context, args_version }`, and its handler POSTs the context to the plugin's registered webhook with the same retries, backoff, and dead-lettering every other job gets. The bin-locate LED idea from the plan becomes a plugin that registers a webhook and a UI action button; pressing "Locate" enqueues a `plugin_webhook` job, the handler calls the plugin, and the plugin publishes MQTT to light the bin. The plugin writes no queue code. It registers data and receives a retried HTTP call. That is the whole integration surface, and it exists because the job type is generic and the args are versioned JSONB.

## What Librarium's job framework taught us

We read Librarium's implementation before finalizing this design. It runs River v0.34.0 with the `riverpgxv5` driver, so the engine choice is settled by precedent, and every remembered bug traces to a findable cause that shaped a decision above.

Librarium added a home-grown umbrella (`jobs`, `job_events`, `job_schedules`) on top of River in migration `000010`, so status and history lived in three places at once: River's `river_job`, the umbrella `jobs` row, and each kind's own detail table with its own status words. A `normalizeStatusForJobs` function existed only to translate `processing` and `done` into `running` and `completed` between them. FireBin keeps one authoritative table and one status enum, so there is nothing to translate and nothing to drift.

The missing logs were not a storage bug. The `AppendEvent` writer had zero callers in the import and enrichment workers; only the AI pipeline logged, and it did so through a second, parallel writer on a different repository. Two of three job kinds ran with an empty timeline while the storage, the list endpoint, and the detail view all worked. FireBin passes one logger into every `Work` call as a required dependency and emits a per-item line from the base loop, so a timeline can't come up empty.

The cancel hang had two separate causes. Enrichment set its River timeout to `-1` and never cancelled the in-flight provider call, so a slow lookup ignored cancel until it returned on its own. Import cancel wrote only the detail table and never the umbrella row, which then sat `running` with a null `finished_at` forever. FireBin cancels through the context that wraps every I/O call and writes status in one place, so both failures are structurally out of reach.

## The failure modes this design closes

This table is the answer to "Librarium had a ton of bugs." Each row is a known job-queue failure and the specific mechanism that prevents it here.

| Failure | Prevention |
|---|---|
| Job lost when a worker crashes mid-run | River leases jobs and rescues leased jobs whose worker died, requeuing them |
| Two workers run the same job | River claims with `SELECT ... FOR UPDATE SKIP LOCKED` |
| Job stuck in `running` forever | River lease timeout re-runs it, or the minute reconciler marks it failed if River discarded it |
| Poison job retries forever and blocks the queue | `max_attempts` then dead-letter; other jobs on the queue keep moving |
| Retry storm hammers a provider | exponential backoff with jitter, plus per-queue worker limits |
| Retry re-does side effects | idempotent handlers, plus unique jobs to stop duplicate enqueue |
| Job runs against an uncommitted row | transactional enqueue: mutation, `tasks` row, and River insert in one tx |
| No progress, client stares at a spinner | `progress()` helper and SSE `tasks` signal |
| Logs launch but never show (a Librarium bug) | one required logger on every `Work`, persisted `job_logs` rows on a `bigserial` cursor, paged by `after_id` and tailed over SSE |
| Cancel hangs or does nothing (a Librarium bug) | context cancellation threaded through every blocking call plus an in-process cancel map, so cancel lands in milliseconds, not on the next loop |
| Nexar quota blown by a bulk run | `enrich` queue capped at 2, Digi-Key first, monthly budget counter |
| Old queued job breaks after a deploy changes the payload | JSONB args plus `ArgsVersion` checked on decode |
| Timezone drift in scheduled work | UTC everywhere, River `ScheduledAt` and periodic jobs |
| Clients coupled to queue internals | clients read our `tasks` table, never `river_job` |

## Testing

Handlers are plain functions tested without a queue: feed args and deps, assert the database result and the idempotency of a second run. The enqueue-to-complete path is an integration test using River's test helpers against a throwaway Postgres. The failure, retry, and cancel paths each get a test, and the transactional-enqueue rollback gets one that aborts the tx and asserts no `tasks` row and no River job survive.

## Rollout

1. Add River, run its migrations, wire the client and worker pool into the API lifecycle with graceful drain on shutdown.
2. Add the `tasks` and `job_logs` tables, the one-minute reconciler, the Tasks API, and the SSE `tasks` and `task:{id}` signals.
3. Migrate `bulk_enrich` to a job, first end to end, and switch `POST /parts/bulk/enrich` to return a task.
4. Build the shared enrich-and-apply service, add `enrich_part` and its endpoint, and delete the React orchestration in `updateFromProvider`.
5. Add `bom_ingest` and `label_batch`.
6. Add the periodic jobs: `stock_sweep` and `scheduled_reenrich`.
7. Add `plugin_webhook` when the plugin framework lands.

## Open questions

- River's migrations run separately from FireBin's `golang-migrate` set. Confirm both run cleanly on a fresh database and on the existing one, in the right order.
- The `tasks` retention policy. Completed tasks accumulate; a nightly prune of tasks older than 30 days keeps the Activity list fast. Decide the window.
- Whether the Activity panel is a new top-level nav item or a header dropdown. Design decision for the web client.
