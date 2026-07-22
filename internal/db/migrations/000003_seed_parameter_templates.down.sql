-- Only remove seeded templates that were never used by a part, so a rollback
-- never destroys real parameter values.
DELETE FROM parameter_templates pt
WHERE pt.name IN (
    'Resistance','Capacitance','Inductance','Tolerance','Voltage Rating',
    'Current Rating','Power Rating','Temperature Coefficient','Operating Temperature',
    'Dielectric','ESR','Frequency','Pin Count','Mounting Type','Polarity','Color',
    'Forward Voltage','Logic Family','Interface','RoHS'
)
AND NOT EXISTS (SELECT 1 FROM part_parameters pp WHERE pp.template_id = pt.id);
