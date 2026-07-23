-- Renderable files pulled from an uploaded project zip: an interactive BOM
-- (iBOM HTML) and image renders. Stored inline so the app can serve them back.
CREATE TABLE project_assets (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    board_id   UUID        REFERENCES project_boards(id) ON DELETE CASCADE,
    name       TEXT        NOT NULL,
    kind       TEXT        NOT NULL DEFAULT 'other',  -- ibom | image | other
    mime       TEXT        NOT NULL DEFAULT 'application/octet-stream',
    size       BIGINT      NOT NULL DEFAULT 0,
    content    BYTEA       NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_project_assets_project ON project_assets(project_id);
CREATE INDEX idx_project_assets_board ON project_assets(board_id);
