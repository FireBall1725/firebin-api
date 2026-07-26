-- cut_guides: whether to draw cut outlines for this paper. A property of the
-- media (Avery pre-cut sheets need none; generic full-page label stock does),
-- not a per-print choice. kind: 'sheet' now; roll/label-printer media later.
ALTER TABLE label_media ADD COLUMN cut_guides BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE label_media ADD COLUMN kind       TEXT    NOT NULL DEFAULT 'sheet';
