-- Seed a starter catalog of common electronics parameter types so the UI can
-- offer autocomplete. These are only suggestions — parameters are fully
-- dynamic (parameter_templates grows as users enter new ones), and any of
-- these can be ignored. Footprint/package is intentionally NOT here: it is a
-- first-class column on parts.
INSERT INTO parameter_templates (name, units) VALUES
    ('Resistance',            'Ω'),
    ('Capacitance',           'F'),
    ('Inductance',            'H'),
    ('Tolerance',             '%'),
    ('Voltage Rating',        'V'),
    ('Current Rating',        'A'),
    ('Power Rating',          'W'),
    ('Temperature Coefficient','ppm/°C'),
    ('Operating Temperature', '°C'),
    ('Dielectric',            NULL),
    ('ESR',                   'Ω'),
    ('Frequency',             'Hz'),
    ('Pin Count',             NULL),
    ('Mounting Type',         NULL),
    ('Polarity',              NULL),
    ('Color',                 NULL),
    ('Forward Voltage',       'V'),
    ('Logic Family',          NULL),
    ('Interface',             NULL),
    ('RoHS',                  NULL)
ON CONFLICT (name) DO NOTHING;
