DROP INDEX IF EXISTS idx_stock_items_barcode;
ALTER TABLE stock_items DROP COLUMN IF EXISTS split_from;
ALTER TABLE stock_items DROP COLUMN IF EXISTS name;
ALTER TABLE stock_items DROP COLUMN IF EXISTS barcode;
