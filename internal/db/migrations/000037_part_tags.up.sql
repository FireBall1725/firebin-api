-- ── Tags: a shared vocabulary for the names you actually use ─────────────────
-- A part is findable today by name, keywords, IPN and MPN. None of those hold
-- the word a community uses for a thing: a JST SH 1.0 mm 4-pin header is "the
-- Qwiic connector" or "STEMMA QT" to everyone who reaches for one, and the only
-- way to find it in FireBin is to already know the part number, which is the
-- thing you opened the search box to look up.
--
-- Why a table and not a text column: parts.keywords already exists and is the
-- wrong home. It is whitespace-tokenized into the KiCad chooser blob by
-- keywordsFor(), so a two-word name like "STEMMA QT" cannot survive in it as one
-- unit, and it is the same field the enrichment providers write. keywords stays
-- the raw provider blob; this is the user-owned layer beside it.
--
-- Why shared rows and not an array per part: "Qwiic" is one thing that many
-- parts point at. A shared row gives a usage count, a rename that propagates to
-- every part at once, a merge for when two spellings escape into the wild, and
-- a browse page. projects.tags TEXT[] (migration 000012) is the older, weaker
-- pattern and is deliberately left alone; this table is named `tags`, not
-- `part_tags`, so a project_tags join can adopt the same vocabulary later
-- without a rename.
CREATE TABLE tags (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 64),
    -- Identity fold, computed in Go: lowercased with everything but [a-z0-9]
    -- dropped. "STEMMA QT", "stemma-qt" and "StemmaQT" are one tag, so the
    -- vocabulary does not fragment into three rows that each find a third of
    -- your parts. `name` keeps whatever spelling was typed first and is what
    -- gets displayed; `slug` is only ever compared, never shown.
    slug        TEXT        NOT NULL,
    -- A palette slot name ('slate', 'red', 'amber', 'green', 'teal', 'blue',
    -- 'violet', 'pink'), not a hex value. The web app resolves it to CSS
    -- variables so a tag stays legible in every theme; a stored hex would be
    -- unreadable in half of them. NULL means the default chip colour.
    colour      TEXT,
    description TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX idx_tags_slug ON tags(slug);
-- Search matches tag names with the same unanchored ILIKE the rest of part
-- search uses, so the trigram index earns its place the same way
-- idx_parts_keywords_trgm does.
CREATE INDEX idx_tags_name_trgm ON tags USING gin (name gin_trgm_ops);
CREATE TRIGGER trg_tags_updated_at BEFORE UPDATE ON tags
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ── Part ↔ tag links ─────────────────────────────────────────────────────────
-- Both FKs are declared DEFERRABLE explicitly. The one-time DO $$ loop in
-- migration 000023 made every FK that existed at the time deferrable, and it
-- does not reach tables created after it; ImportAll relies on SET CONSTRAINTS
-- ALL DEFERRED to restore tables in any order. datasheet_parts (000033) missed
-- this and is a latent restore bug, so do not copy its DDL.
CREATE TABLE part_tags (
    part_id    UUID NOT NULL REFERENCES parts(id) ON DELETE CASCADE
                 DEFERRABLE INITIALLY IMMEDIATE,
    tag_id     UUID NOT NULL REFERENCES tags(id) ON DELETE CASCADE
                 DEFERRABLE INITIALLY IMMEDIATE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (part_id, tag_id)
);
-- The primary key serves "what tags does this part have?", which is the parts
-- list asking once per visible row. Browsing "everything tagged Qwiic" reads
-- the other direction and needs its own index.
CREATE INDEX idx_part_tags_tag ON part_tags(tag_id);
