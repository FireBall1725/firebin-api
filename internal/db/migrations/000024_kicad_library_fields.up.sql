-- KiCad symbol and footprint identifiers, so a part can be served to KiCad as an
-- HTTP library entry (.kicad_httplib). Both are KiCad LIB_IDs of the form
-- "Library:Name" -- e.g. symbol "Device:R", footprint
-- "Resistor_SMD:R_0603_1608Metric" -- and both resolve against libraries already
-- installed on the machine running KiCad. We only ever store the identifier;
-- KiCad owns the geometry.
--
-- These sit on parts rather than in part_parameters for the same reason package
-- does (see 000003): they describe how the part is drawn and placed, not an
-- electrical spec, and every consumer wants them without a join.
ALTER TABLE parts
    ADD COLUMN kicad_symbol    TEXT,
    ADD COLUMN kicad_footprint TEXT;
