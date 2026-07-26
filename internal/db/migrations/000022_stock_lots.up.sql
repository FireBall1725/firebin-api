-- Stock lots: a stock_item can be a distinct, barcoded physical unit — e.g. a
-- mini spool cut off a reel. Same part (no separate parameters), its own barcode
-- and quantity, and it rolls up into the part's total like any other stock. The
-- barcode is the scan identity behind firebin://s/<barcode-or-id>.
ALTER TABLE stock_items ADD COLUMN barcode    TEXT;
ALTER TABLE stock_items ADD COLUMN name       TEXT;
ALTER TABLE stock_items ADD COLUMN split_from UUID REFERENCES stock_items(id) ON DELETE SET NULL;

-- One barcode maps to one lot.
CREATE UNIQUE INDEX idx_stock_items_barcode ON stock_items (barcode) WHERE barcode IS NOT NULL;
