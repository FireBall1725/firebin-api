-- Project-level match memory: a manual part choice, keyed by the BOM line's
-- identity (MPN, else value+footprint), applies to every board in the project
-- and survives re-uploads/revisions.
CREATE TABLE project_matches (
    project_id UUID        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    match_key  TEXT        NOT NULL,   -- "mpn:<mpn>" or "vf:<value>|<footprint>"
    part_id    UUID        NOT NULL REFERENCES parts(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (project_id, match_key)
);
