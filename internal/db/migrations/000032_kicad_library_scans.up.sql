-- One row per import, so a library can say where it came from.
--
-- kicad_library_index_meta holds a single row for the whole index (its primary
-- key is CHECK (id = 1)), which answers "when was this last scanned" and
-- nothing else. Since importing became additive, an index is the accumulation
-- of several imports: a full pass over a KiCad install, then a folder someone
-- downloaded. Those are the two cases a person needs told apart, and with one
-- meta row there was no way to tell them apart at all.
--
-- A full install is 438 libraries. Finding the three footprints you added
-- yesterday by scrolling that alphabetically is not a search, it is luck.
CREATE TABLE IF NOT EXISTS kicad_library_scans (
    scan_id       uuid PRIMARY KEY,
    source        text NOT NULL,
    kicad_version text,
    -- Null means the import happened before imports were recorded. Every import
    -- from here on sets it; nothing back-dates an older one, because the time
    -- was never stored and a plausible-looking guess is worse than an admitted
    -- gap. Reads sort nulls first, so an unknown surfaces rather than hides.
    imported_at   timestamptz
);

-- Sorting libraries by when they arrived is the whole point, so the ordering
-- column is indexed rather than sorted on every page load.
CREATE INDEX IF NOT EXISTS idx_kicad_library_scans_imported
    ON kicad_library_scans (imported_at DESC NULLS FIRST);

-- Backfill from what was actually written down. Every item already carries the
-- scan_id that wrote it, so each past import gets a row; only the newest one
-- left a timestamp anywhere, and the rest keep a null.
INSERT INTO kicad_library_scans (scan_id, source, kicad_version, imported_at)
SELECT DISTINCT
    i.scan_id,
    COALESCE(m.source, 'imported before this was recorded'),
    m.kicad_version,
    m.scanned_at
FROM kicad_library_items i
LEFT JOIN kicad_library_index_meta m ON m.scan_id = i.scan_id
ON CONFLICT (scan_id) DO NOTHING;
