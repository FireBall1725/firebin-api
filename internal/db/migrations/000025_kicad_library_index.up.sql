-- A copy of the KiCad symbol and footprint libraries available on the machines
-- this instance designs on, uploaded by an indexer that runs where KiCad is
-- installed.
--
-- Two jobs. It powers typeahead when mapping a part (KiCad IDs like
-- "Resistor_SMD:R_0603_1608Metric" are not typed from memory), and it holds the
-- source so FireBin can draw a preview. It is not authoritative: KiCad resolves
-- IDs locally at placement time, so a part may reference something absent here
-- and the UI warns rather than rejecting.
--
-- Everything is stored, not just the items parts reference. Stock libraries are
-- 400 MB raw but 26 MB gzipped, and a user's own footprints are the ones that
-- matter most: those are not reproducible from a KiCad install, so this doubles
-- as their backup. 3D models are deliberately excluded -- 482 MB that adds
-- nothing to a 2D preview.
CREATE TABLE kicad_library_items (
    id      BIGSERIAL PRIMARY KEY,
    -- Every upload tags its rows with one scan id. Finishing a scan deletes
    -- rows carrying any other id, which is how a library the user uninstalled
    -- disappears. Batching plus this marker means a 38,000-item upload can be
    -- retried or resumed without a half-written index ever being visible.
    scan_id UUID NOT NULL,
    kind    TEXT NOT NULL CHECK (kind IN ('symbol', 'footprint')),
    -- Library nickname as it appears in KiCad's library table, e.g. "Device".
    lib     TEXT NOT NULL,
    -- Symbol or footprint name within that library, e.g. "R".
    name    TEXT NOT NULL,
    -- gzipped S-expression: a single (symbol ...) block, or a whole .kicad_mod.
    -- Nullable so an index-only scan stays possible.
    source  BYTEA,
    -- Parsed render data, derived from source and filled in on first view.
    -- Cached rather than computed per request, and safe to clear: a renderer
    -- change only needs this column emptied, never a re-scan.
    drawing JSONB,
    UNIQUE (kind, lib, name)
);

-- Searches match the full "Lib:Name" identifier, because that is what a user
-- pastes and what is stored on parts.kicad_symbol / parts.kicad_footprint.
CREATE INDEX idx_kicad_library_items_libid_trgm
    ON kicad_library_items USING gin ((lib || ':' || name) gin_trgm_ops);

CREATE INDEX idx_kicad_library_items_kind_lib ON kicad_library_items (kind, lib);
CREATE INDEX idx_kicad_library_items_scan ON kicad_library_items (scan_id);

-- Provenance for the index as a whole. Single row: there is one index, and
-- knowing which machine and KiCad version produced it is what distinguishes
-- "that library is not installed" from "you scanned from the wrong laptop".
CREATE TABLE kicad_library_index_meta (
    id              SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    scan_id         UUID NOT NULL,
    source          TEXT NOT NULL,
    kicad_version   TEXT,
    scanned_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    symbol_count    INT NOT NULL DEFAULT 0,
    footprint_count INT NOT NULL DEFAULT 0,
    bytes_stored    BIGINT NOT NULL DEFAULT 0
);
