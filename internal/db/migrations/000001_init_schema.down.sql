DROP TABLE IF EXISTS api_tokens;
DROP TABLE IF EXISTS revoked_access_tokens;
DROP TABLE IF EXISTS refresh_tokens;
DROP TRIGGER IF EXISTS trg_users_updated_at ON users;
DROP TABLE IF EXISTS users;
DROP FUNCTION IF EXISTS set_updated_at();
