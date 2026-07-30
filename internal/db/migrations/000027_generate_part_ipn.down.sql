-- Removes the automation only. The generated numbers stay in parts.ipn, because
-- a generated code cannot be told apart from one typed in by hand, and they are
-- referenced by printed labels and by stored BOM lines. Dropping them would
-- break scans of labels already on bins.
DROP TRIGGER IF EXISTS trg_parts_set_ipn ON parts;
DROP FUNCTION IF EXISTS set_part_ipn();
DROP FUNCTION IF EXISTS gen_part_ipn();
