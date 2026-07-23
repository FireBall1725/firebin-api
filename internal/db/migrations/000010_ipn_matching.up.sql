-- FireBin internal part number (IPN) and richer BOM-line match keys.

-- The part's FireBin part number: a short, human-assigned internal id used as
-- the highest-priority BOM match key. Nullable and unique when present.
ALTER TABLE parts ADD COLUMN ipn TEXT;
CREATE UNIQUE INDEX idx_parts_ipn ON parts (ipn) WHERE ipn IS NOT NULL;

-- BOM lines carry the supplier SKU (LCSC/Digi-Key…) and any FireBin PN from the
-- source BOM, so they can be matched and re-matched without re-parsing.
ALTER TABLE board_bom_lines ADD COLUMN supplier_sku TEXT NOT NULL DEFAULT '';
ALTER TABLE board_bom_lines ADD COLUMN ipn          TEXT NOT NULL DEFAULT '';
