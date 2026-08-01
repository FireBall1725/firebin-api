-- Dropping the column loses which parts were only recorded rather than owned.
-- There is nowhere to recover that from, since the whole point of the column is
-- that the distinction cannot be derived from stock rows. The parts themselves
-- are untouched; they simply all read as owned again.
ALTER TABLE parts DROP COLUMN IF EXISTS reference_only;
