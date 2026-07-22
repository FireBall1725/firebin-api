// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import (
	"context"

	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CatalogRepo struct{ pool *pgxpool.Pool }

func NewCatalogRepo(pool *pgxpool.Pool) *CatalogRepo { return &CatalogRepo{pool: pool} }

// ── Manufacturers & suppliers ────────────────────────────────────────────────

func (r *CatalogRepo) ListManufacturers(ctx context.Context) ([]models.Manufacturer, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, name, website FROM manufacturers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Manufacturer{}
	for rows.Next() {
		var m models.Manufacturer
		if err := rows.Scan(&m.ID, &m.Name, &m.Website); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// getOrCreateManufacturer resolves a manufacturer by name, creating it if new.
func (r *CatalogRepo) getOrCreateManufacturer(ctx context.Context, name string) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `
		INSERT INTO manufacturers (name) VALUES ($1)
		ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
		RETURNING id`, name).Scan(&id)
	return id, err
}

func (r *CatalogRepo) ListSuppliers(ctx context.Context) ([]models.Supplier, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, key, name, website, is_distributor FROM suppliers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Supplier{}
	for rows.Next() {
		var s models.Supplier
		if err := rows.Scan(&s.ID, &s.Key, &s.Name, &s.Website, &s.IsDistributor); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ── Manufacturer parts ───────────────────────────────────────────────────────

// ListManufacturerParts returns a part's MPNs, each with nested supplier parts
// and price breaks — the full commercial tree for the part-detail view.
func (r *CatalogRepo) ListManufacturerParts(ctx context.Context, partID uuid.UUID) ([]models.ManufacturerPart, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT mp.id, mp.part_id, mp.manufacturer_id, m.name, mp.mpn, mp.description, mp.datasheet_url, mp.created_at
		FROM manufacturer_parts mp
		LEFT JOIN manufacturers m ON m.id = mp.manufacturer_id
		WHERE mp.part_id = $1
		ORDER BY mp.created_at`, partID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.ManufacturerPart{}
	for rows.Next() {
		var mp models.ManufacturerPart
		if err := rows.Scan(&mp.ID, &mp.PartID, &mp.ManufacturerID, &mp.ManufacturerName,
			&mp.MPN, &mp.Description, &mp.DatasheetURL, &mp.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, mp)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		sps, err := r.listSupplierParts(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].SupplierParts = sps
	}
	return out, nil
}

func (r *CatalogRepo) CreateManufacturerPart(ctx context.Context, partID uuid.UUID, manufacturerName, mpn string, datasheet *string) (*models.ManufacturerPart, error) {
	var mfgID *uuid.UUID
	if manufacturerName != "" {
		id, err := r.getOrCreateManufacturer(ctx, manufacturerName)
		if err != nil {
			return nil, err
		}
		mfgID = &id
	}
	var mp models.ManufacturerPart
	err := r.pool.QueryRow(ctx, `
		INSERT INTO manufacturer_parts (part_id, manufacturer_id, mpn, datasheet_url)
		VALUES ($1, $2, $3, $4)
		RETURNING id, part_id, manufacturer_id, mpn, description, datasheet_url, created_at`,
		partID, mfgID, mpn, datasheet,
	).Scan(&mp.ID, &mp.PartID, &mp.ManufacturerID, &mp.MPN, &mp.Description, &mp.DatasheetURL, &mp.CreatedAt)
	if err != nil {
		return nil, err
	}
	if manufacturerName != "" {
		mp.ManufacturerName = &manufacturerName
	}
	mp.SupplierParts = []models.SupplierPart{}
	return &mp, nil
}

func (r *CatalogRepo) DeleteManufacturerPart(ctx context.Context, id uuid.UUID) error {
	ct, err := r.pool.Exec(ctx, `DELETE FROM manufacturer_parts WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ── Supplier parts + pricing ─────────────────────────────────────────────────

func (r *CatalogRepo) listSupplierParts(ctx context.Context, mfgPartID uuid.UUID) ([]models.SupplierPart, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT sp.id, sp.manufacturer_part_id, sp.supplier_id, s.name, sp.sku, sp.packaging, sp.moq::float8, sp.url
		FROM supplier_parts sp
		JOIN suppliers s ON s.id = sp.supplier_id
		WHERE sp.manufacturer_part_id = $1
		ORDER BY s.name`, mfgPartID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.SupplierPart{}
	for rows.Next() {
		var sp models.SupplierPart
		if err := rows.Scan(&sp.ID, &sp.ManufacturerPartID, &sp.SupplierID, &sp.SupplierName,
			&sp.SKU, &sp.Packaging, &sp.MOQ, &sp.URL); err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		breaks, err := r.listPriceBreaks(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Pricing = breaks
	}
	return out, nil
}

func (r *CatalogRepo) listPriceBreaks(ctx context.Context, supplierPartID uuid.UUID) ([]models.PriceBreak, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, quantity::float8, price::float8, currency
		FROM supplier_part_pricing WHERE supplier_part_id = $1
		ORDER BY quantity`, supplierPartID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.PriceBreak{}
	for rows.Next() {
		var b models.PriceBreak
		if err := rows.Scan(&b.ID, &b.Quantity, &b.Price, &b.Currency); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// CreateSupplierPart adds a vendor SKU and its price breaks in one transaction.
func (r *CatalogRepo) CreateSupplierPart(ctx context.Context, mfgPartID, supplierID uuid.UUID, sku string, packaging, url *string, moq *float64, breaks []models.PriceBreak) (uuid.UUID, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)

	var id uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO supplier_parts (manufacturer_part_id, supplier_id, sku, packaging, moq, url)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		mfgPartID, supplierID, sku, packaging, moq, url).Scan(&id); err != nil {
		return uuid.Nil, err
	}
	for _, b := range breaks {
		if _, err := tx.Exec(ctx, `
			INSERT INTO supplier_part_pricing (supplier_part_id, quantity, price, currency)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (supplier_part_id, quantity, currency) DO UPDATE SET price = EXCLUDED.price`,
			id, b.Quantity, b.Price, defaultCurrency(b.Currency)); err != nil {
			return uuid.Nil, err
		}
	}
	return id, tx.Commit(ctx)
}

func (r *CatalogRepo) DeleteSupplierPart(ctx context.Context, id uuid.UUID) error {
	ct, err := r.pool.Exec(ctx, `DELETE FROM supplier_parts WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func defaultCurrency(c string) string {
	if c == "" {
		return "USD"
	}
	return c
}
