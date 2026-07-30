-- Give every part an internal part number.
--
-- parts.ipn has existed since 000010 with a partial unique index, and the whole
-- codebase already reads it: label QR codes resolve firebin://p/<code> against
-- it, the command palette and part picker search it, and the KiCad BOM importer
-- treats it as the highest-confidence match (bom.go reads an `fbpn` schematic
-- field into it, then CatalogRepo.FindPartByIPN resolves it to a part id).
-- Nothing ever populated it, so all of that fell back to MPN matching and the
-- firebin-kicad library server emitted no fbpn field at all.
--
-- Generated here rather than in Go because parts are inserted from several
-- paths (the create handler, JSON import, the MCP add_part_by_mpn tool) and a
-- trigger covers them all. A column DEFAULT would not: the API passes ipn
-- explicitly, and a DEFAULT does not fire on an explicit NULL.

-- Crockford base32, which drops I, L, O and U so a code read off a printed
-- label cannot be mistyped as 1/0. 32^8 is about 1.1e12, so a collision is
-- already implausible; the loop plus the unique index make it impossible.
CREATE OR REPLACE FUNCTION gen_part_ipn() RETURNS text AS $$
DECLARE
    alphabet CONSTANT text := '0123456789ABCDEFGHJKMNPQRSTVWXYZ';
    candidate text;
    i int;
BEGIN
    LOOP
        candidate := 'FB-';
        FOR i IN 1..8 LOOP
            candidate := candidate || substr(alphabet, 1 + floor(random() * 32)::int, 1);
        END LOOP;
        EXIT WHEN NOT EXISTS (SELECT 1 FROM parts WHERE ipn = candidate);
    END LOOP;
    RETURN candidate;
END;
$$ LANGUAGE plpgsql;

-- An explicitly supplied ipn always wins, so restoring a JSON export keeps the
-- numbers it was taken with. Empty string is treated as absent: it is not NULL,
-- so the partial unique index would let the first '' through and then reject
-- every later one.
CREATE OR REPLACE FUNCTION set_part_ipn() RETURNS trigger AS $$
BEGIN
    IF NEW.ipn IS NULL OR btrim(NEW.ipn) = '' THEN
        NEW.ipn := gen_part_ipn();
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_parts_set_ipn BEFORE INSERT ON parts
    FOR EACH ROW EXECUTE FUNCTION set_part_ipn();

-- Backfill. One statement per row on purpose: inside a single UPDATE every
-- gen_part_ipn() call reads the same snapshot, so it cannot see codes assigned
-- to earlier rows of the same statement.
--
-- This bumps parts.updated_at for every backfilled row, via the existing
-- trg_parts_updated_at. Suppressing that would mean disabling a trigger during
-- a migration that runs on boot, which is a worse trade than one shifted
-- timestamp.
DO $$
DECLARE
    r record;
BEGIN
    FOR r IN SELECT id FROM parts WHERE ipn IS NULL OR btrim(ipn) = '' LOOP
        UPDATE parts SET ipn = gen_part_ipn() WHERE id = r.id;
    END LOOP;
END $$;
