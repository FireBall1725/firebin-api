-- Units belong to the individual part's parameter, not the shared template. A
-- single "Capacitance" template can't be both nF and µF, so storing units on the
-- template froze the first-seen unit and dropped every later part's ("100 F").
-- Move units onto part_parameters; parameter_templates.units stays as a typeahead
-- default only.
ALTER TABLE part_parameters ADD COLUMN units TEXT NOT NULL DEFAULT '';
