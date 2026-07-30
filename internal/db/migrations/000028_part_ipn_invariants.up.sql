-- Harden the internal part number added in 000027.
--
-- 000027 only guaranteed an IPN at INSERT. UPDATE had no guard, and the part
-- editor sends `ipn: ipn.trim() || null`, so clearing the field in the UI set
-- the column back to NULL and the part lost its number again. Verified against
-- a running API: PATCH with ipn=null returned 200 and the column went NULL.
--
-- It also left uniqueness case-sensitive while the rest of the stack is not:
-- 'FB-ABCD1234' and 'fb-abcd1234' could both exist, yet the label deep-link
-- resolver lowercases both sides before comparing, so a scan could resolve to
-- either one.

-- Clearing the field now keeps the existing number rather than minting a new
-- one. Regenerating would orphan any label already printed and stuck on a bin,
-- and would break stored BOM lines that recorded the old code. A number is
-- therefore overridable but never removable.
CREATE OR REPLACE FUNCTION set_part_ipn() RETURNS trigger AS $$
BEGIN
    IF NEW.ipn IS NULL OR btrim(NEW.ipn) = '' THEN
        IF TG_OP = 'UPDATE' AND OLD.ipn IS NOT NULL AND btrim(OLD.ipn) <> '' THEN
            NEW.ipn := OLD.ipn;
        ELSE
            NEW.ipn := gen_part_ipn();
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_parts_set_ipn ON parts;
CREATE TRIGGER trg_parts_set_ipn BEFORE INSERT OR UPDATE ON parts
    FOR EACH ROW EXECUTE FUNCTION set_part_ipn();

-- Resolve any existing case-insensitive collisions before the new index goes on,
-- otherwise creating it fails and the API will not boot. Generated codes are all
-- uppercase and distinct, so this is defensive: it only bites a database where
-- someone hand-entered a case variant.
DO $$
DECLARE
    r record;
BEGIN
    FOR r IN
        SELECT id FROM (
            SELECT id, row_number() OVER (PARTITION BY upper(ipn) ORDER BY created_at, id) AS n
            FROM parts WHERE ipn IS NOT NULL
        ) ranked WHERE n > 1
    LOOP
        UPDATE parts SET ipn = gen_part_ipn() WHERE id = r.id;
    END LOOP;
END $$;

-- Case-insensitive uniqueness, without rewriting what anyone typed. The
-- expression index also serves the case-insensitive lookup in
-- CatalogRepo.FindPartByIPN.
DROP INDEX IF EXISTS idx_parts_ipn;
CREATE UNIQUE INDEX idx_parts_ipn ON parts (upper(ipn)) WHERE ipn IS NOT NULL;

-- gen_part_ipn's own collision check has to agree with the index, or it can
-- hand back a candidate the index then rejects.
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
        EXIT WHEN NOT EXISTS (SELECT 1 FROM parts WHERE upper(ipn) = candidate);
    END LOOP;
    RETURN candidate;
END;
$$ LANGUAGE plpgsql;
