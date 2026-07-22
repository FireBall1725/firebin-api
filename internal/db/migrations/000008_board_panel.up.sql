-- A board can be a normal board or a panel (the same board arrayed N-up). The
-- panel stores the per-board BOM plus a copy count; effective quantity is
-- quantity * copies.
ALTER TABLE project_boards ADD COLUMN kind   TEXT    NOT NULL DEFAULT 'board';   -- board | panel
ALTER TABLE project_boards ADD COLUMN copies INTEGER NOT NULL DEFAULT 1 CHECK (copies >= 1);
