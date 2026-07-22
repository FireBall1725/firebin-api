-- FireBin initial schema: users, authentication, and shared helpers.
-- Domain (inventory) tables live in 000002.

CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- Shared trigger function: stamp updated_at on every UPDATE.
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- ── Users ────────────────────────────────────────────────────────────────────
CREATE TABLE users (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    username          TEXT        NOT NULL UNIQUE CHECK (char_length(username) BETWEEN 1 AND 64),
    email             TEXT        UNIQUE,
    password_hash     TEXT        NOT NULL,
    display_name      TEXT,
    is_instance_admin BOOLEAN     NOT NULL DEFAULT false,
    is_active         BOOLEAN     NOT NULL DEFAULT true,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TRIGGER trg_users_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ── Refresh tokens ───────────────────────────────────────────────────────────
-- Long-lived tokens are stored hashed; the raw value never touches the DB.
CREATE TABLE refresh_tokens (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  TEXT        NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    revoked_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_refresh_tokens_user_id ON refresh_tokens(user_id);
CREATE INDEX idx_refresh_tokens_hash_active ON refresh_tokens(token_hash)
    WHERE revoked_at IS NULL;

-- ── Access-token revocation list ─────────────────────────────────────────────
-- A short-lived access token can be killed before expiry by denylisting its jti.
CREATE TABLE revoked_access_tokens (
    jti         TEXT        PRIMARY KEY,
    expires_at  TIMESTAMPTZ NOT NULL,
    revoked_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ── Personal access tokens (PATs) ────────────────────────────────────────────
-- Each row is one `fbin_pat_<random>` credential. The raw value is shown once
-- at creation; only the sha256 hash and last-4 suffix are stored. An empty
-- scopes array means "inherit the user's full permissions".
CREATE TABLE api_tokens (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name          TEXT        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 64),
    token_hash    TEXT        NOT NULL UNIQUE,
    token_suffix  TEXT        NOT NULL CHECK (char_length(token_suffix) = 4),
    scopes        TEXT[]      NOT NULL DEFAULT '{}',
    last_used_at  TIMESTAMPTZ,
    expires_at    TIMESTAMPTZ,
    revoked_at    TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_api_tokens_user_id ON api_tokens(user_id);
CREATE INDEX idx_api_tokens_hash_active ON api_tokens(token_hash)
    WHERE revoked_at IS NULL;
