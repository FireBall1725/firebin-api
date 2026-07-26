-- Merge duplicate categories (same case-insensitive name under the same parent):
-- keep the oldest, repoint its parts and any child categories, delete the rest.
-- Duplicates crept in because Create used to INSERT unconditionally; it is now
-- get-or-create, and the unique index below stops it at the database level too.

WITH keepers AS (
    SELECT id, first_value(id) OVER (
        PARTITION BY lower(name), parent_id ORDER BY created_at, id
    ) AS keep_id
    FROM categories
)
UPDATE parts p SET category_id = k.keep_id
FROM keepers k
WHERE p.category_id = k.id AND k.id <> k.keep_id;

WITH keepers AS (
    SELECT id, first_value(id) OVER (
        PARTITION BY lower(name), parent_id ORDER BY created_at, id
    ) AS keep_id
    FROM categories
)
UPDATE categories c SET parent_id = k.keep_id
FROM keepers k
WHERE c.parent_id = k.id AND k.id <> k.keep_id;

WITH keepers AS (
    SELECT id, first_value(id) OVER (
        PARTITION BY lower(name), parent_id ORDER BY created_at, id
    ) AS keep_id
    FROM categories
)
DELETE FROM categories c
USING keepers k
WHERE c.id = k.id AND k.id <> k.keep_id;

-- Enforce uniqueness. COALESCE gives NULL-parent categories a single group so
-- top-level names can't duplicate either.
CREATE UNIQUE INDEX idx_categories_name_parent
    ON categories (lower(name), COALESCE(parent_id, '00000000-0000-0000-0000-000000000000'::uuid));
