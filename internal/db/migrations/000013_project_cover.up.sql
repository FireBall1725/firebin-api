-- An optional uploaded cover image for a project. When null, the project card
-- falls back to the first board's render.
ALTER TABLE projects ADD COLUMN cover_image_id UUID REFERENCES project_assets(id) ON DELETE SET NULL;
