-- FireBin projects: a design broken into one or more boards, each with its own
-- BOM parsed from an uploaded KiCad file.
--
--   project ─┐
--            board (e.g. "Main board", "Panel")   ← one uploaded KiCad file each
--              └─ bom_line (grouped refdes: value + footprint + optional MPN)
--                    └─ optional match to an inventory part

-- ── Projects ─────────────────────────────────────────────────────────────────
CREATE TABLE projects (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 200),
    description TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TRIGGER trg_projects_updated_at BEFORE UPDATE ON projects
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ── Boards ───────────────────────────────────────────────────────────────────
-- Each board is one uploaded design source (a KiCad schematic, or a BOM export).
-- `position` orders boards within a project (main board first, panel next, …).
CREATE TABLE project_boards (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id      UUID        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name            TEXT        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 200),
    description     TEXT,
    source_filename TEXT,
    source_format   TEXT        NOT NULL DEFAULT 'kicad_sch',
    position        INTEGER     NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_project_boards_project ON project_boards(project_id);
CREATE TRIGGER trg_project_boards_updated_at BEFORE UPDATE ON project_boards
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ── BOM lines ────────────────────────────────────────────────────────────────
-- One grouped BOM row: components sharing value + footprint (+ MPN) collapse to
-- a single line. `refs` is the comma-joined reference designators, `quantity`
-- is the count per board. `part_id`/`match_kind` record the inventory match.
CREATE TABLE board_bom_lines (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    board_id     UUID        NOT NULL REFERENCES project_boards(id) ON DELETE CASCADE,
    refs         TEXT        NOT NULL DEFAULT '',
    quantity     INTEGER     NOT NULL DEFAULT 1 CHECK (quantity >= 0),
    value        TEXT        NOT NULL DEFAULT '',
    footprint    TEXT        NOT NULL DEFAULT '',
    mpn          TEXT        NOT NULL DEFAULT '',
    manufacturer TEXT        NOT NULL DEFAULT '',
    description  TEXT        NOT NULL DEFAULT '',
    part_id      UUID        REFERENCES parts(id) ON DELETE SET NULL,
    match_kind   TEXT        NOT NULL DEFAULT 'none',
    position     INTEGER     NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_board_bom_lines_board ON board_bom_lines(board_id);
CREATE INDEX idx_board_bom_lines_part ON board_bom_lines(part_id);
