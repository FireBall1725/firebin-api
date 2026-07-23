-- Free-form tags on a project (e.g. "wip", "client-x", "revB").
ALTER TABLE projects ADD COLUMN tags TEXT[] NOT NULL DEFAULT '{}';
