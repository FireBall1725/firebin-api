-- Drops the vocabulary and every part's links to it. Nothing else holds a tag,
-- so rolling back loses them; parts.keywords is a separate column and is
-- untouched either way.
DROP TABLE IF EXISTS part_tags;
DROP TABLE IF EXISTS tags;
