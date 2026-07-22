-- FireBin inventory domain.
--
-- Model (InvenTree-style, plus a template/variant layer):
--   category ─┐
--             part (template "1k resistor" → variant "1k 0603 1%")
--               ├─ part_parameters (typed attributes: tolerance, power, …)
--               ├─ manufacturer_part (brand + MPN, the enrichment key)
--               │     └─ supplier_part (vendor SKU) ─ supplier_part_pricing (price breaks)
--               └─ stock_item (quantity at a storage_location) ─ stock_transaction (movement log)

-- ── Categories (hierarchical) ────────────────────────────────────────────────
CREATE TABLE categories (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_id   UUID        REFERENCES categories(id) ON DELETE SET NULL,
    name        TEXT        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 128),
    description TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (parent_id, name)
);
CREATE INDEX idx_categories_parent ON categories(parent_id);
CREATE TRIGGER trg_categories_updated_at BEFORE UPDATE ON categories
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ── Manufacturers (brands) ───────────────────────────────────────────────────
CREATE TABLE manufacturers (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT        NOT NULL UNIQUE CHECK (char_length(name) BETWEEN 1 AND 128),
    website     TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TRIGGER trg_manufacturers_updated_at BEFORE UPDATE ON manufacturers
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ── Suppliers / distributors ─────────────────────────────────────────────────
-- `key` is a stable slug the enrichment providers match on (digikey, mouser…).
CREATE TABLE suppliers (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    key            TEXT        NOT NULL UNIQUE,
    name           TEXT        NOT NULL,
    website        TEXT,
    is_distributor BOOLEAN     NOT NULL DEFAULT true,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TRIGGER trg_suppliers_updated_at BEFORE UPDATE ON suppliers
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ── Parts (abstract/usable; template ↔ variant) ──────────────────────────────
CREATE TABLE parts (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    category_id    UUID        REFERENCES categories(id) ON DELETE SET NULL,
    variant_of     UUID        REFERENCES parts(id) ON DELETE CASCADE,
    name           TEXT        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 256),
    description    TEXT,
    -- Convenience column: the physical package/footprint (e.g. 0603, SOT-23).
    -- Richer attributes live in part_parameters.
    package        TEXT,
    keywords       TEXT,
    barcode        TEXT        UNIQUE,   -- internal scannable id ("ID Anything")
    image_path     TEXT,
    is_template    BOOLEAN     NOT NULL DEFAULT false,
    is_component   BOOLEAN     NOT NULL DEFAULT true,
    is_assembly    BOOLEAN     NOT NULL DEFAULT false,
    is_purchaseable BOOLEAN    NOT NULL DEFAULT true,
    is_trackable   BOOLEAN     NOT NULL DEFAULT false,
    minimum_stock  NUMERIC(18,4) NOT NULL DEFAULT 0,
    default_location_id UUID,            -- FK added after storage_locations exists
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_parts_category ON parts(category_id);
CREATE INDEX idx_parts_variant_of ON parts(variant_of);
-- Full-text-ish search over name/description/keywords.
CREATE INDEX idx_parts_name_trgm ON parts USING gin (name gin_trgm_ops);
CREATE INDEX idx_parts_keywords_trgm ON parts USING gin (keywords gin_trgm_ops);
CREATE TRIGGER trg_parts_updated_at BEFORE UPDATE ON parts
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ── Parameter templates + values ─────────────────────────────────────────────
CREATE TABLE parameter_templates (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT        NOT NULL UNIQUE CHECK (char_length(name) BETWEEN 1 AND 64),
    units       TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE part_parameters (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    part_id     UUID        NOT NULL REFERENCES parts(id) ON DELETE CASCADE,
    template_id UUID        NOT NULL REFERENCES parameter_templates(id) ON DELETE CASCADE,
    value       TEXT        NOT NULL,
    UNIQUE (part_id, template_id)
);
CREATE INDEX idx_part_parameters_part ON part_parameters(part_id);

-- ── Manufacturer parts (brand + MPN) ─────────────────────────────────────────
CREATE TABLE manufacturer_parts (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    part_id         UUID        NOT NULL REFERENCES parts(id) ON DELETE CASCADE,
    manufacturer_id UUID        REFERENCES manufacturers(id) ON DELETE SET NULL,
    mpn             TEXT        NOT NULL,
    description     TEXT,
    datasheet_url   TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (part_id, manufacturer_id, mpn)
);
CREATE INDEX idx_manufacturer_parts_part ON manufacturer_parts(part_id);
CREATE INDEX idx_manufacturer_parts_mpn_trgm ON manufacturer_parts USING gin (mpn gin_trgm_ops);
CREATE TRIGGER trg_manufacturer_parts_updated_at BEFORE UPDATE ON manufacturer_parts
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ── Supplier parts (vendor SKU) + price breaks ───────────────────────────────
CREATE TABLE supplier_parts (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    manufacturer_part_id UUID       NOT NULL REFERENCES manufacturer_parts(id) ON DELETE CASCADE,
    supplier_id         UUID        NOT NULL REFERENCES suppliers(id) ON DELETE CASCADE,
    sku                 TEXT        NOT NULL,
    packaging           TEXT,
    moq                 NUMERIC(18,4),
    url                 TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (supplier_id, sku)
);
CREATE INDEX idx_supplier_parts_mfg_part ON supplier_parts(manufacturer_part_id);
CREATE TRIGGER trg_supplier_parts_updated_at BEFORE UPDATE ON supplier_parts
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE supplier_part_pricing (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    supplier_part_id UUID       NOT NULL REFERENCES supplier_parts(id) ON DELETE CASCADE,
    quantity        NUMERIC(18,4) NOT NULL,
    price           NUMERIC(18,6) NOT NULL,
    currency        TEXT        NOT NULL DEFAULT 'USD',
    UNIQUE (supplier_part_id, quantity, currency)
);
CREATE INDEX idx_supplier_part_pricing_part ON supplier_part_pricing(supplier_part_id);

-- ── Storage locations (hierarchical bins) ────────────────────────────────────
CREATE TABLE storage_locations (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_id   UUID        REFERENCES storage_locations(id) ON DELETE SET NULL,
    name        TEXT        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 128),
    description TEXT,
    barcode     TEXT        UNIQUE,   -- scan a bin to list its contents
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (parent_id, name)
);
CREATE INDEX idx_storage_locations_parent ON storage_locations(parent_id);
CREATE TRIGGER trg_storage_locations_updated_at BEFORE UPDATE ON storage_locations
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Now that storage_locations exists, wire the parts default-location FK.
ALTER TABLE parts
    ADD CONSTRAINT fk_parts_default_location
    FOREIGN KEY (default_location_id) REFERENCES storage_locations(id) ON DELETE SET NULL;

-- ── Stock items (physical quantity at a location) ────────────────────────────
CREATE TABLE stock_items (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    part_id          UUID        NOT NULL REFERENCES parts(id) ON DELETE CASCADE,
    location_id      UUID        REFERENCES storage_locations(id) ON DELETE SET NULL,
    supplier_part_id UUID        REFERENCES supplier_parts(id) ON DELETE SET NULL,
    quantity         NUMERIC(18,4) NOT NULL DEFAULT 0,
    batch            TEXT,
    serial           TEXT,
    purchase_price   NUMERIC(18,6),
    purchase_currency TEXT       DEFAULT 'USD',
    status           TEXT        NOT NULL DEFAULT 'ok',
    note             TEXT,
    added_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_stock_items_part ON stock_items(part_id);
CREATE INDEX idx_stock_items_location ON stock_items(location_id);
CREATE TRIGGER trg_stock_items_updated_at BEFORE UPDATE ON stock_items
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ── Stock transactions (append-only movement log) ────────────────────────────
CREATE TABLE stock_transactions (
    id                 UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    stock_item_id      UUID        NOT NULL REFERENCES stock_items(id) ON DELETE CASCADE,
    kind               TEXT        NOT NULL,  -- add | remove | move | count | adjust
    delta              NUMERIC(18,4) NOT NULL,
    resulting_quantity NUMERIC(18,4) NOT NULL,
    from_location_id   UUID        REFERENCES storage_locations(id) ON DELETE SET NULL,
    to_location_id     UUID        REFERENCES storage_locations(id) ON DELETE SET NULL,
    note               TEXT,
    user_id            UUID        REFERENCES users(id) ON DELETE SET NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_stock_transactions_item ON stock_transactions(stock_item_id);

-- ── Attachments (datasheets, images, STEP) ───────────────────────────────────
-- Path references only — files live on the BYO filesystem, not in the DB.
CREATE TABLE attachments (
    id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    part_id              UUID        REFERENCES parts(id) ON DELETE CASCADE,
    manufacturer_part_id UUID        REFERENCES manufacturer_parts(id) ON DELETE CASCADE,
    kind                 TEXT        NOT NULL DEFAULT 'file', -- datasheet | image | model | file
    filename             TEXT        NOT NULL,
    path                 TEXT        NOT NULL,
    mime_type            TEXT,
    size_bytes           BIGINT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (part_id IS NOT NULL OR manufacturer_part_id IS NOT NULL)
);
CREATE INDEX idx_attachments_part ON attachments(part_id);
CREATE INDEX idx_attachments_mfg_part ON attachments(manufacturer_part_id);

-- ── Seed the distributors enrichment will target ─────────────────────────────
INSERT INTO suppliers (key, name, website) VALUES
    ('digikey', 'Digi-Key',       'https://www.digikey.com'),
    ('mouser',  'Mouser',         'https://www.mouser.com'),
    ('lcsc',    'LCSC',           'https://www.lcsc.com'),
    ('nexar',   'Octopart / Nexar','https://nexar.com')
ON CONFLICT (key) DO NOTHING;
