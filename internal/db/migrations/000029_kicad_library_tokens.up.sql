-- Tokens for the KiCad HTTP library, one per workstation.
--
-- A separate table from api_tokens rather than a type column on it, matching the
-- existing shape where each token concept owns its table (api_tokens,
-- refresh_tokens, revoked_access_tokens).
--
-- The separation is the security property. These are the only credentials the
-- KiCad route group accepts, and that group serves four read-only endpoints, so
-- a token here is read-only by construction. Reusing api_tokens would put a
-- credential carrying its owner's full account authority into a .kicad_httplib
-- file, which sits in plaintext on a workstation and gets copied into project
-- folders — and PAT scopes are recorded but never enforced, so "scope it" is not
-- an available answer.
--
-- One per device rather than one shared secret: the file is already per-machine,
-- revoking one workstation must not break the others, and last_used_at is the
-- only way to tell which machines are still connected.
CREATE TABLE kicad_library_tokens (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    -- The device this was issued for, e.g. "Workshop iMac".
    name          TEXT        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 64),
    token_hash    TEXT        NOT NULL UNIQUE,
    token_suffix  TEXT        NOT NULL CHECK (char_length(token_suffix) = 4),
    -- Who issued it. ON DELETE SET NULL, deliberately unlike api_tokens.user_id
    -- which cascades: this is instance-level configuration behind an admin
    -- toggle, not a personal credential, so removing an admin account must not
    -- silently stop a workstation from resolving parts.
    created_by    UUID        REFERENCES users(id) ON DELETE SET NULL,
    last_used_at  TIMESTAMPTZ,
    revoked_at    TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Every request KiCad makes is a lookup by hash restricted to live tokens.
CREATE INDEX idx_kicad_library_tokens_hash_active
    ON kicad_library_tokens (token_hash) WHERE revoked_at IS NULL;
