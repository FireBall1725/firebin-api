-- Dropping the table revokes every workstation, which is the honest outcome of
-- removing the feature: the tokens are meaningless without the routes that
-- accept them, and only their hashes are stored so nothing is recoverable
-- anyway.
DROP INDEX IF EXISTS idx_kicad_library_tokens_hash_active;
DROP TABLE IF EXISTS kicad_library_tokens;
