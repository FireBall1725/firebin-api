-- User-designed label layouts for the drag-and-drop builder. The renderer is
-- already element-based (labels.Element), so a template is just a named list of
-- positioned elements bound to a label size. Elements are stored as JSON:
-- [{type,field,x,y,w,h,value,font,bold,align}, …].
CREATE TABLE label_templates (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name           TEXT        NOT NULL,
    label_media_id UUID        REFERENCES label_media(id) ON DELETE SET NULL,
    elements       JSONB       NOT NULL DEFAULT '[]',
    created_by     UUID        REFERENCES users(id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_label_templates_media ON label_templates(label_media_id);
