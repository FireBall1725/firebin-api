// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type StockRepo struct{ pool *pgxpool.Pool }

func NewStockRepo(pool *pgxpool.Pool) *StockRepo { return &StockRepo{pool: pool} }

// ListForPart returns every stock lot for a part, with its location name.
func (r *StockRepo) ListForPart(ctx context.Context, partID uuid.UUID) ([]models.StockItem, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT s.id, s.part_id, s.location_id, l.name, s.supplier_part_id,
			s.quantity::float8, s.batch, s.serial, s.purchase_price::float8, s.status, s.note,
			s.added_at, s.updated_at
		FROM stock_items s
		LEFT JOIN storage_locations l ON l.id = s.location_id
		WHERE s.part_id = $1
		ORDER BY s.added_at`, partID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.StockItem{}
	for rows.Next() {
		var s models.StockItem
		if err := rows.Scan(&s.ID, &s.PartID, &s.LocationID, &s.LocationName, &s.SupplierPartID,
			&s.Quantity, &s.Batch, &s.Serial, &s.PurchasePrice, &s.Status, &s.Note,
			&s.AddedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListTransactions returns the recent movement log for a part (across its lots).
func (r *StockRepo) ListTransactions(ctx context.Context, partID uuid.UUID, limit int) ([]models.StockTransaction, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT t.id, t.stock_item_id, t.kind, t.delta::float8, t.resulting_quantity::float8,
			t.from_location_id, t.to_location_id, t.note, t.user_id, t.created_at
		FROM stock_transactions t
		JOIN stock_items s ON s.id = t.stock_item_id
		WHERE s.part_id = $1
		ORDER BY t.created_at DESC
		LIMIT $2`, partID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.StockTransaction{}
	for rows.Next() {
		var t models.StockTransaction
		if err := rows.Scan(&t.ID, &t.StockItemID, &t.Kind, &t.Delta, &t.ResultingQuantity,
			&t.FromLocationID, &t.ToLocationID, &t.Note, &t.UserID, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// AdjustParams describes a stock change.
type AdjustParams struct {
	PartID         uuid.UUID
	LocationID     *uuid.UUID
	SupplierPartID *uuid.UUID
	Kind           string  // add | remove | count | adjust
	Quantity       float64 // add/remove: magnitude; count: absolute target; adjust: signed delta
	Note           *string
	UserID         *uuid.UUID
}

// Adjust applies a stock change to the lot for (part, location), creating the
// lot if needed, and records a stock_transaction. Runs in one transaction so
// the quantity and its audit row can never diverge.
func (r *StockRepo) Adjust(ctx context.Context, p AdjustParams) (*models.StockItem, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Find (and lock) the existing lot for this part+location, or create one.
	var itemID uuid.UUID
	var cur float64
	err = tx.QueryRow(ctx, `
		SELECT id, quantity::float8 FROM stock_items
		WHERE part_id = $1 AND location_id IS NOT DISTINCT FROM $2
		ORDER BY added_at LIMIT 1 FOR UPDATE`, p.PartID, p.LocationID).Scan(&itemID, &cur)
	if errors.Is(err, pgx.ErrNoRows) {
		if err = tx.QueryRow(ctx, `
			INSERT INTO stock_items (part_id, location_id, supplier_part_id, quantity)
			VALUES ($1, $2, $3, 0) RETURNING id`,
			p.PartID, p.LocationID, p.SupplierPartID).Scan(&itemID); err != nil {
			return nil, fmt.Errorf("creating stock lot: %w", err)
		}
		cur = 0
	} else if err != nil {
		return nil, err
	}

	newQty := cur
	switch p.Kind {
	case "add":
		newQty = cur + p.Quantity
	case "remove":
		newQty = cur - p.Quantity
		if newQty < 0 {
			newQty = 0
		}
	case "count":
		newQty = p.Quantity
	case "adjust":
		newQty = cur + p.Quantity
	default:
		return nil, fmt.Errorf("unknown adjust kind %q", p.Kind)
	}
	delta := newQty - cur

	if _, err = tx.Exec(ctx, `UPDATE stock_items SET quantity = $2 WHERE id = $1`, itemID, newQty); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO stock_transactions (stock_item_id, kind, delta, resulting_quantity, note, user_id)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		itemID, p.Kind, delta, newQty, p.Note, p.UserID); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.getItem(ctx, itemID)
}

// MoveParams moves a quantity from one stock lot to a destination location.
type MoveParams struct {
	StockItemID  uuid.UUID
	ToLocationID *uuid.UUID
	Quantity     float64
	Note         *string
	UserID       *uuid.UUID
}

// Move transfers quantity from a source lot to the destination location's lot
// (creating it if needed), recording a move transaction on each side.
func (r *StockRepo) Move(ctx context.Context, p MoveParams) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var partID uuid.UUID
	var fromLoc *uuid.UUID
	var cur float64
	err = tx.QueryRow(ctx, `
		SELECT part_id, location_id, quantity::float8 FROM stock_items
		WHERE id = $1 FOR UPDATE`, p.StockItemID).Scan(&partID, &fromLoc, &cur)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if p.Quantity > cur {
		return fmt.Errorf("cannot move %.4g: only %.4g in stock", p.Quantity, cur)
	}

	// Decrement source.
	srcQty := cur - p.Quantity
	if _, err = tx.Exec(ctx, `UPDATE stock_items SET quantity = $2 WHERE id = $1`, p.StockItemID, srcQty); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO stock_transactions (stock_item_id, kind, delta, resulting_quantity, from_location_id, to_location_id, note, user_id)
		VALUES ($1, 'move', $2, $3, $4, $5, $6, $7)`,
		p.StockItemID, -p.Quantity, srcQty, fromLoc, p.ToLocationID, p.Note, p.UserID); err != nil {
		return err
	}

	// Find or create the destination lot.
	var destID uuid.UUID
	var destCur float64
	err = tx.QueryRow(ctx, `
		SELECT id, quantity::float8 FROM stock_items
		WHERE part_id = $1 AND location_id IS NOT DISTINCT FROM $2
		ORDER BY added_at LIMIT 1 FOR UPDATE`, partID, p.ToLocationID).Scan(&destID, &destCur)
	if errors.Is(err, pgx.ErrNoRows) {
		if err = tx.QueryRow(ctx, `
			INSERT INTO stock_items (part_id, location_id, quantity) VALUES ($1, $2, 0) RETURNING id`,
			partID, p.ToLocationID).Scan(&destID); err != nil {
			return err
		}
		destCur = 0
	} else if err != nil {
		return err
	}
	destQty := destCur + p.Quantity
	if _, err = tx.Exec(ctx, `UPDATE stock_items SET quantity = $2 WHERE id = $1`, destID, destQty); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO stock_transactions (stock_item_id, kind, delta, resulting_quantity, from_location_id, to_location_id, note, user_id)
		VALUES ($1, 'move', $2, $3, $4, $5, $6, $7)`,
		destID, p.Quantity, destQty, fromLoc, p.ToLocationID, p.Note, p.UserID); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (r *StockRepo) getItem(ctx context.Context, id uuid.UUID) (*models.StockItem, error) {
	var s models.StockItem
	err := r.pool.QueryRow(ctx, `
		SELECT s.id, s.part_id, s.location_id, l.name, s.supplier_part_id,
			s.quantity::float8, s.batch, s.serial, s.purchase_price::float8, s.status, s.note,
			s.added_at, s.updated_at
		FROM stock_items s
		LEFT JOIN storage_locations l ON l.id = s.location_id
		WHERE s.id = $1`, id).Scan(
		&s.ID, &s.PartID, &s.LocationID, &s.LocationName, &s.SupplierPartID,
		&s.Quantity, &s.Batch, &s.Serial, &s.PurchasePrice, &s.Status, &s.Note,
		&s.AddedAt, &s.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &s, nil
}
