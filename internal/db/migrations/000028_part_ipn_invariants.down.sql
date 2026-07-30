-- Back to the 000027 behaviour: IPN assigned on INSERT only, uniqueness
-- case-sensitive. Values are left alone, as in 000027's down.
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

CREATE OR REPLACE FUNCTION set_part_ipn() RETURNS trigger AS $$
BEGIN
    IF NEW.ipn IS NULL OR btrim(NEW.ipn) = '' THEN
        NEW.ipn := gen_part_ipn();
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_parts_set_ipn ON parts;
CREATE TRIGGER trg_parts_set_ipn BEFORE INSERT ON parts
    FOR EACH ROW EXECUTE FUNCTION set_part_ipn();

DROP INDEX IF EXISTS idx_parts_ipn;
CREATE UNIQUE INDEX idx_parts_ipn ON parts (ipn) WHERE ipn IS NOT NULL;
