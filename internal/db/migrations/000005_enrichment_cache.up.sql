-- Cache enrichment lookups by MPN so re-scanning the same part (or retries)
-- never spends another provider query — important for metered free tiers.
CREATE TABLE enrichment_cache (
    mpn        TEXT        PRIMARY KEY,
    data       JSONB       NOT NULL,
    source     TEXT,
    fetched_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
