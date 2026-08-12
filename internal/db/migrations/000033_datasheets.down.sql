-- Dropping these loses the record of which datasheets were held and what they
-- were linked to. The FILES are not touched: they stay under
-- ATTACHMENT_STORAGE_PATH/datasheets/ as content-addressed blobs with no rows
-- pointing at them. That is deliberate. A down migration should not delete the
-- user's documents, and re-applying the up migration then re-uploading the same
-- PDF lands on the same sha256 path, so nothing is duplicated.
DROP TABLE IF EXISTS datasheet_parts;
DROP TABLE IF EXISTS datasheets;

-- Recreate attachments exactly as migration 000002 left it, so a down-then-up
-- round trip is a no-op against the schema a pre-000033 build expects. It was
-- empty when 000033 dropped it and it is empty again here.
CREATE TABLE attachments (
    id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    part_id              UUID        REFERENCES parts(id) ON DELETE CASCADE,
    manufacturer_part_id UUID        REFERENCES manufacturer_parts(id) ON DELETE CASCADE,
    kind                 TEXT        NOT NULL DEFAULT 'file', -- datasheet | image | model | file
    filename             TEXT        NOT NULL,
    path                 TEXT        NOT NULL,
    mime_type            TEXT,
    size_bytes           BIGINT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (part_id IS NOT NULL OR manufacturer_part_id IS NOT NULL)
);
CREATE INDEX idx_attachments_part ON attachments(part_id);
CREATE INDEX idx_attachments_mfg_part ON attachments(manufacturer_part_id);
