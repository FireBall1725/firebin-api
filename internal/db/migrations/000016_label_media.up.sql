-- Label media: the physical sheet geometry for a label product (e.g. Avery
-- 5163 → 2"x4", 2 columns x 5 rows on US Letter). Seeded from the open gLabels
-- template database (facts are not copyrightable; template files are MIT). Codes
-- are stored as vendor-compatible references, not an endorsement.
--
-- All dimensions are in PDF points (1pt = 1/72 inch) so the renderer can place
-- labels at absolute coordinates without unit conversion. US Letter = 612x792.
CREATE TABLE label_media (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    brand         TEXT        NOT NULL DEFAULT 'Avery',
    code          TEXT        NOT NULL,             -- e.g. '5163'
    name          TEXT        NOT NULL,             -- human description
    page_w        REAL        NOT NULL,             -- sheet width  (pt)
    page_h        REAL        NOT NULL,             -- sheet height (pt)
    label_w       REAL        NOT NULL,             -- one label width  (pt)
    label_h       REAL        NOT NULL,             -- one label height (pt)
    corner_radius REAL        NOT NULL DEFAULT 0,   -- rounded corner (pt)
    cols          INT         NOT NULL,             -- labels across
    rows          INT         NOT NULL,             -- labels down
    x0            REAL        NOT NULL,             -- top-left of first label from page top-left (pt)
    y0            REAL        NOT NULL,
    pitch_x       REAL        NOT NULL,             -- centre-to-centre spacing across (pt)
    pitch_y       REAL        NOT NULL,             -- centre-to-centre spacing down (pt)
    builtin       BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (brand, code)
);

CREATE TRIGGER trg_label_media_updated_at BEFORE UPDATE ON label_media
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Seed the common US Letter Avery sheets (points). 5260/8160 == 5160,
-- 8163 == 5163 (same geometry, different product code the user may own).
INSERT INTO label_media
    (brand, code, name, page_w, page_h, label_w, label_h, corner_radius, cols, rows, x0, y0, pitch_x, pitch_y, builtin)
VALUES
    ('Avery', '5160', 'Address labels 1" x 2-5/8"',        612, 792, 189,  72,  4.5, 3, 10, 11.25, 36, 200.25,  72, TRUE),
    ('Avery', '5260', 'Address labels 1" x 2-5/8"',        612, 792, 189,  72,  4.5, 3, 10, 11.25, 36, 200.25,  72, TRUE),
    ('Avery', '8160', 'Address labels 1" x 2-5/8"',        612, 792, 189,  72,  4.5, 3, 10, 11.25, 36, 200.25,  72, TRUE),
    ('Avery', '5161', 'Address labels 1" x 4"',            612, 792, 288,  72,  4.5, 2, 10, 11.25, 36, 301.5,   72, TRUE),
    ('Avery', '5162', 'Address labels 1-1/3" x 4"',        612, 792, 288,  96,  4.5, 2,  7, 11.25, 60, 301.5,   96, TRUE),
    ('Avery', '5163', 'Shipping labels 2" x 4"',           612, 792, 288, 144,  9.0, 2,  5, 11.7,  36, 301.5,  144, TRUE),
    ('Avery', '8163', 'Shipping labels 2" x 4"',           612, 792, 288, 144,  9.0, 2,  5, 11.7,  36, 301.5,  144, TRUE),
    ('Avery', '5164', 'Shipping labels 3-1/3" x 4"',       612, 792, 288, 240,  9.0, 2,  3, 11.25, 36, 301.5,  240, TRUE),
    ('Avery', '5167', 'Return address labels 1/2" x 1-3/4"', 612, 792, 126, 36, 4.5, 4, 20, 20.25, 36, 148.5,   36, TRUE);
