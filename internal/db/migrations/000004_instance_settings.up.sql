-- Instance-wide key/value settings (e.g. enrichment provider credentials).
-- Values are stored as-is; treat secret keys as sensitive.
CREATE TABLE instance_settings (
    key        TEXT        PRIMARY KEY,
    value      TEXT        NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
