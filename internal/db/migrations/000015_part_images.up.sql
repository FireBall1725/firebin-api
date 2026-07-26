-- A custom uploaded image for a part (one per part). Bundled symbols are just a
-- /symbols/*.svg path in parts.image_path and need no storage; this table backs
-- the "upload your own picture" option, served at /api/v1/parts/{id}/image.
CREATE TABLE part_images (
    part_id    UUID PRIMARY KEY REFERENCES parts(id) ON DELETE CASCADE,
    mime       TEXT NOT NULL,
    size       BIGINT NOT NULL DEFAULT 0,
    content    BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
