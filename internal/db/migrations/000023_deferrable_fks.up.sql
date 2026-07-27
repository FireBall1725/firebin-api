-- Make every foreign key DEFERRABLE so a bulk restore can defer FK checks to
-- COMMIT with `SET CONSTRAINTS ALL DEFERRED` (which any role may run) rather than
-- `SET session_replication_role = replica`, which is superuser-only and therefore
-- unavailable to the non-superuser app role that CloudNativePG (and most managed
-- Postgres) create. INITIALLY IMMEDIATE keeps normal per-statement checking at
-- runtime; only the import transaction opts into deferral.
DO $$
DECLARE r record;
BEGIN
  FOR r IN
    SELECT conrelid::regclass AS tbl, conname
    FROM pg_constraint
    WHERE contype = 'f'
      AND connamespace = 'public'::regnamespace
      AND NOT condeferrable
  LOOP
    EXECUTE format('ALTER TABLE %s ALTER CONSTRAINT %I DEFERRABLE INITIALLY IMMEDIATE', r.tbl, r.conname);
  END LOOP;
END $$;
