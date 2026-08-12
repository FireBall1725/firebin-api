-- Make a datasheet something FireBin owns rather than a link it hopes still works.
--
-- Until now the entire datasheet feature was manufacturer_parts.datasheet_url, a
-- nullable TEXT filled by whichever enrichment provider answered first. That has
-- three failure modes and this migration exists to fix all three. Distributor and
-- manufacturer URLs rot, so a part catalogued a year ago quietly loses its
-- documentation. A remote PDF cannot be embedded anyway, because the origin sets
-- X-Frame-Options, or CORS blocks the fetch, or the page is served over plain
-- HTTP and the browser refuses the mixed content. And the assistant has nothing
-- to read, because no copy of anything exists locally.
--
-- datasheet_url is deliberately LEFT IN PLACE and untouched. It is still the
-- right thing to record when no copy has been taken, it is what the KiCad HTTP
-- library emits as the `datasheet` field, and mirroring is opt-in, so most rows
-- will have a URL and no file for a long time yet. The two coexist: the URL says
-- where it came from, this table says what we have.

-- ── Datasheets ───────────────────────────────────────────────────────────────
-- Bytes live on the filesystem under ATTACHMENT_STORAGE_PATH, not in a BYTEA
-- column. part_images and project_assets both went the BYTEA route and that was
-- correct for them, because a part photo is tens of kilobytes. A datasheet is
-- single-digit to tens of megabytes, and ExportAll base64s every BYTEA into one
-- JSON document that ImportData reads back under a 256 MiB body cap. A few
-- hundred datasheets in BYTEA would silently make backups unrestorable, which is
-- the worst possible time to discover a size limit.
--
-- sha256 is the primary identity and doubles as the on-disk name. Two reasons.
-- One PDF routinely covers a whole family (a single ESP32-C6 datasheet serves
-- every module variant), so content addressing stores it once no matter how many
-- MPNs point at it. And the uploaded filename is then never used to build a
-- path, which removes directory traversal as a category of bug rather than
-- guarding against it.
CREATE TABLE datasheets (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    sha256       TEXT        NOT NULL UNIQUE,
    filename     TEXT        NOT NULL,
    title        TEXT,
    mime         TEXT        NOT NULL DEFAULT 'application/pdf',
    size_bytes   BIGINT      NOT NULL,
    page_count   INTEGER,
    -- Where it was mirrored from. NULL for a hand-uploaded file, which is also
    -- how the UI tells "Upload" apart from a distributor source without a second
    -- column to keep in step.
    source_url   TEXT,
    origin       TEXT        NOT NULL DEFAULT 'upload'
                             CHECK (origin IN ('upload', 'mirror')),
    -- Detected on extraction, NULL until then. Exists because enrichment happily
    -- returns a Chinese datasheet for a part with an English one available, and
    -- seeing that before spending disk is the whole reason mirroring is opt-in.
    language     TEXT,
    -- Extraction is a derived cache, so its state has to be legible rather than
    -- inferred from whether the sidecar file happens to exist. no_text_layer is a
    -- real, common answer (mechanical drawings are scans) and is not a failure.
    text_status  TEXT        NOT NULL DEFAULT 'pending'
                             CHECK (text_status IN ('pending', 'ok', 'no_text_layer', 'failed')),
    extracted_at TIMESTAMPTZ,
    created_by   UUID        REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_datasheets_text_status ON datasheets(text_status);
CREATE TRIGGER trg_datasheets_updated_at BEFORE UPDATE ON datasheets
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ── Datasheet ↔ part links ───────────────────────────────────────────────────
-- Many-to-many, and both directions are real. One PDF covers many parts (the
-- family case above). One part has many PDFs (datasheet, errata, application
-- note). The old attachments table modelled neither and could not express either.
--
-- A datasheet with NO rows here is not an error state. It is the "saw a part
-- online, saved the PDF for later" case, and it is deliberately allowed: the old
-- attachments table forbade it with CHECK (part_id IS NOT NULL OR
-- manufacturer_part_id IS NOT NULL), which is one of the reasons it is being
-- dropped below rather than adopted.
--
-- manufacturer_part_id is optional extra precision: it records WHICH MPN of the
-- part the sheet came from, when that is known. ON DELETE SET NULL rather than
-- CASCADE, because removing one MPN should not silently unlink a datasheet that
-- still describes the part.
CREATE TABLE datasheet_parts (
    datasheet_id         UUID NOT NULL REFERENCES datasheets(id) ON DELETE CASCADE,
    part_id              UUID NOT NULL REFERENCES parts(id) ON DELETE CASCADE,
    manufacturer_part_id UUID REFERENCES manufacturer_parts(id) ON DELETE SET NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (datasheet_id, part_id)
);
-- The parts list asks "does this part have a datasheet?" for every visible row,
-- so the part_id direction needs its own index; the primary key only serves the
-- datasheet_id direction.
CREATE INDEX idx_datasheet_parts_part ON datasheet_parts(part_id);

-- ── Remove the abandoned attachments table ───────────────────────────────────
-- Written in migration 000002 and never wired to anything: no repository, no
-- model, no handler, and not one INSERT anywhere in the codebase. Its only
-- appearance in Go is the table name in the backup allow-list, so every
-- deployment's copy is empty. Its path-on-disk design was the right instinct and
-- is what the datasheets table above finally implements, but its shape cannot be
-- reused (one row per part-or-MPN, no many-to-many, loose files forbidden).
--
-- Dropped rather than left alone because a dead table sitting beside a live one
-- that does the same job is how the next person loses an afternoon.
DROP TABLE IF EXISTS attachments;
