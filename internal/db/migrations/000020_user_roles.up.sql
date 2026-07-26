-- Roles for household / small-team instances. Three levels, no permission matrix:
--   admin  — everything, plus user management, instance settings, tokens.
--   member — full CRUD on inventory (parts, stock, locations, projects, labels).
--   viewer — read and export only; every mutation is refused.
-- Backfill from the existing is_instance_admin flag, which stays in sync with
-- role='admin' so the current admin badge and JWT keep working.
ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'member'
    CHECK (role IN ('admin', 'member', 'viewer'));

UPDATE users SET role = 'admin' WHERE is_instance_admin;
