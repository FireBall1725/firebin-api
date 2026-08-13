-- Give a datasheet a category of its own.
--
-- Until now a datasheet's category was entirely borrowed: the library's rail
-- filtered on the categories of the parts a document was linked to. That works
-- for a mirrored datasheet, which arrives attached to a part by definition, and
-- leaves the loose pile with nowhere to go. An unlinked upload — the "saw a
-- part online, saved the PDF for later" case the Unlinked bucket exists for —
-- matched no category filter at all, so the only way to sort it was to first
-- create a part for something you may not own.
--
-- This is an addition, not a replacement. The borrowed category still works and
-- is still right for a linked document; the filter matches either. A datasheet
-- with its own category and links to parts in a different one shows up under
-- both, which is the honest answer: it belongs to both.
--
-- ON DELETE SET NULL, matching parts.category_id: deleting a category should
-- leave its documents uncategorised, never delete them.
ALTER TABLE datasheets
    ADD COLUMN category_id UUID REFERENCES categories(id) ON DELETE SET NULL;

-- The library page filters on this on every rail click, and the table is small
-- enough that the index is cheap either way.
CREATE INDEX idx_datasheets_category ON datasheets(category_id);
