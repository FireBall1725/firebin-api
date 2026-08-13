-- What was actually sent to the model, and what came back.
--
-- FireBin already recorded that a turn happened (assistant_runs) and what was
-- said (assistant_messages). Neither answers the question you have when the
-- assistant misbehaves, which is always some version of "what did it actually
-- see?". Three real failures in one morning needed exactly this and none of
-- them could be diagnosed from the database:
--
--   * a prompt silently truncated because a provider option was missing
--   * a model emitting a malformed tool call
--   * a model running the right tools and then answering nothing
--
-- One row per PROVIDER CALL, not per turn. A turn is a loop: it calls the model,
-- runs tools, calls again. The round is the grain where things go wrong, and the
-- growth between rounds is itself the diagnosis — each round re-sends the whole
-- conversation, so a turn that ends at 10,724 input tokens got there gradually
-- and the rows show it.
--
-- request/response hold raw JSON as bytes on the wire, deliberately not the
-- neutral internal shape: the options that break a call are provider-specific
-- and never appear in the internal one.
CREATE TABLE assistant_round_logs (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    -- No FK to assistant_runs: a round is recorded as it happens, and the run
    -- row is written when the turn finishes. A turn that dies mid-round is the
    -- most interesting case there is, and an FK would throw those rows away.
    run_id          UUID,
    conversation_id UUID        REFERENCES assistant_conversations(id) ON DELETE CASCADE,
    round           INTEGER     NOT NULL,
    provider        TEXT        NOT NULL,
    model           TEXT        NOT NULL,
    url             TEXT,
    request         TEXT,
    response        TEXT,
    -- The model's reasoning, which used to be parsed off the wire and dropped.
    -- It is not shown in an answer and is often the only thing that explains a
    -- wrong one.
    thinking        TEXT,
    status          INTEGER,
    input_tokens    INTEGER     NOT NULL DEFAULT 0,
    output_tokens   INTEGER     NOT NULL DEFAULT 0,
    duration_ms     INTEGER     NOT NULL DEFAULT 0,
    error           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_assistant_round_logs_conversation ON assistant_round_logs(conversation_id, created_at DESC);
CREATE INDEX idx_assistant_round_logs_run ON assistant_round_logs(run_id);
-- Pruned by age, so the sweep has to be able to find old rows cheaply.
CREATE INDEX idx_assistant_round_logs_created ON assistant_round_logs(created_at);
