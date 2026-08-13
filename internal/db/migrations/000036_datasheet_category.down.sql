-- Drops the column and, with it, every hand-set category on a loose datasheet.
-- Nothing else holds that assignment, so rolling back loses it.
DROP INDEX IF EXISTS idx_datasheets_category;
ALTER TABLE datasheets DROP COLUMN IF EXISTS category_id;
