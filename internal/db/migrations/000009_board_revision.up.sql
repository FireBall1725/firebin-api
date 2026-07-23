-- A board carries a revision label (e.g. "A", "1"), pre-filled from the KiCad
-- title block on upload.
ALTER TABLE project_boards ADD COLUMN revision TEXT NOT NULL DEFAULT '';
