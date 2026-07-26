// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

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
			s.barcode, s.name, s.split_from,
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
			&s.Barcode, &s.Name, &s.SplitFrom,
			&s.AddedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// StockLot is one available stock lot of a part at a location, for pick-list
// allocation.
type StockLot struct {
	ID           uuid.UUID
	PartID       uuid.UUID
	PartName     string
	LocationID   *uuid.UUID
	LocationName *string
	Quantity     float64
}

// StockForParts returns the available ('ok', qty > 0) stock lots for a set of
// parts, ordered by location name (unbinned last) so a pick list walks bins in
// order.
func (r *StockRepo) StockForParts(ctx context.Context, partIDs []uuid.UUID) ([]StockLot, error) {
	if len(partIDs) == 0 {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT s.id, s.part_id, p.name, s.location_id, l.name, s.quantity::float8
		FROM stock_items s
		JOIN parts p ON p.id = s.part_id
		LEFT JOIN storage_locations l ON l.id = s.location_id
		WHERE s.part_id = ANY($1) AND s.status = 'ok' AND s.quantity > 0
		ORDER BY l.name NULLS LAST, s.added_at`, partIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StockLot{}
	for rows.Next() {
		var s StockLot
		if err := rows.Scan(&s.ID, &s.PartID, &s.PartName, &s.LocationID, &s.LocationName, &s.Quantity); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListForLocation returns every stock lot held at a location, with part names.
func (r *StockRepo) ListForLocation(ctx context.Context, locationID uuid.UUID) ([]models.StockItem, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT s.id, s.part_id, p.name, c.name, p.image_path, s.location_id, l.name, s.supplier_part_id,
			s.quantity::float8, s.batch, s.serial, s.purchase_price::float8, s.status, s.note,
			s.added_at, s.updated_at
		FROM stock_items s
		JOIN parts p ON p.id = s.part_id
		LEFT JOIN categories c ON c.id = p.category_id
		LEFT JOIN storage_locations l ON l.id = s.location_id
		WHERE s.location_id = $1 AND s.quantity <> 0
		ORDER BY p.name`, locationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.StockItem{}
	for rows.Next() {
		var s models.StockItem
		if err := rows.Scan(&s.ID, &s.PartID, &s.PartName, &s.CategoryName, &s.ImagePath, &s.LocationID, &s.LocationName, &s.SupplierPartID,
			&s.Quantity, &s.Batch, &s.Serial, &s.PurchasePrice, &s.Status, &s.Note,
			&s.AddedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Recent returns the newest stock movements across all parts, each annotated
// with the part it affected and any from/to location names. A move writes two
// rows (a -N out of the source lot and a +N into the destination lot, sharing an
// identical created_at because Postgres now() is fixed per transaction); those are
// collapsed into a single directional row here so the feed reads as one transfer
// rather than an unrelated subtract and add.
func (r *StockRepo) Recent(ctx context.Context, limit int) ([]models.StockTransaction, error) {
	// Over-fetch: a move contributes 2+ raw rows, and collapsing must not let a
	// pair straddle the limit boundary. 2x+buffer covers the realistic worst case.
	fetch := limit*2 + 8
	rows, err := r.pool.Query(ctx, `
		SELECT t.id, t.stock_item_id, s.part_id, p.name, t.kind, t.delta::float8,
			t.resulting_quantity::float8, t.from_location_id, t.to_location_id,
			fl.name, tl.name, t.note, t.user_id, t.created_at
		FROM stock_transactions t
		JOIN stock_items s ON s.id = t.stock_item_id
		JOIN parts p ON p.id = s.part_id
		LEFT JOIN storage_locations fl ON fl.id = t.from_location_id
		LEFT JOIN storage_locations tl ON tl.id = t.to_location_id
		ORDER BY t.created_at DESC, t.id DESC
		LIMIT $1`, fetch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	raw := []models.StockTransaction{}
	for rows.Next() {
		var t models.StockTransaction
		if err := rows.Scan(&t.ID, &t.StockItemID, &t.PartID, &t.PartName, &t.Kind, &t.Delta,
			&t.ResultingQuantity, &t.FromLocationID, &t.ToLocationID,
			&t.FromLocationName, &t.ToLocationName, &t.Note, &t.UserID, &t.CreatedAt); err != nil {
			return nil, err
		}
		raw = append(raw, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return collapseMoves(raw, limit), nil
}

// collapseMoves merges the multiple rows of each move (same part + created_at) into
// one row showing the net quantity that landed at the destination, its source and
// destination names filled in from whichever side carries them. Non-move rows pass
// through untouched. Input must be ordered newest-first; output preserves that and
// is trimmed to limit.
func collapseMoves(raw []models.StockTransaction, limit int) []models.StockTransaction {
	out := []models.StockTransaction{}
	// moveKey -> index in out, so members after the first fold into the same row.
	seen := map[string]int{}
	for _, t := range raw {
		if t.Kind != "move" || t.PartID == nil {
			out = append(out, t)
			continue
		}
		key := t.PartID.String() + "|" + t.CreatedAt.Format(time.RFC3339Nano)
		if idx, ok := seen[key]; ok {
			m := &out[idx]
			// The +N side (delta > 0) is the amount that reached the destination.
			if t.Delta > 0 {
				m.Delta = t.Delta
			}
			if m.FromLocationName == nil && t.FromLocationName != nil {
				m.FromLocationID, m.FromLocationName = t.FromLocationID, t.FromLocationName
			}
			if m.ToLocationName == nil && t.ToLocationName != nil {
				m.ToLocationID, m.ToLocationName = t.ToLocationID, t.ToLocationName
			}
			continue
		}
		c := t
		c.Delta = math.Abs(t.Delta) // show the magnitude moved, not a signed +/-
		seen[key] = len(out)
		out = append(out, c)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// ListTransactions returns the recent movement log for a part (across its lots),
// with move rows collapsed the same way Recent does (see collapseMoves).
func (r *StockRepo) ListTransactions(ctx context.Context, partID uuid.UUID, limit int) ([]models.StockTransaction, error) {
	fetch := limit*2 + 8
	rows, err := r.pool.Query(ctx, `
		SELECT t.id, t.stock_item_id, s.part_id, t.kind, t.delta::float8, t.resulting_quantity::float8,
			t.from_location_id, t.to_location_id, fl.name, tl.name, t.note, t.user_id, t.created_at
		FROM stock_transactions t
		JOIN stock_items s ON s.id = t.stock_item_id
		LEFT JOIN storage_locations fl ON fl.id = t.from_location_id
		LEFT JOIN storage_locations tl ON tl.id = t.to_location_id
		WHERE s.part_id = $1
		ORDER BY t.created_at DESC, t.id DESC
		LIMIT $2`, partID, fetch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	raw := []models.StockTransaction{}
	for rows.Next() {
		var t models.StockTransaction
		if err := rows.Scan(&t.ID, &t.StockItemID, &t.PartID, &t.Kind, &t.Delta, &t.ResultingQuantity,
			&t.FromLocationID, &t.ToLocationID, &t.FromLocationName, &t.ToLocationName,
			&t.Note, &t.UserID, &t.CreatedAt); err != nil {
			return nil, err
		}
		raw = append(raw, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return collapseMoves(raw, limit), nil
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
	defer func() { _ = tx.Rollback(ctx) }()

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

	var newQty float64
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
	defer func() { _ = tx.Rollback(ctx) }()

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

// MovePartToLocation consolidates all of a part's stock into one destination
// location, recording a move transaction per relocated lot. Returns the total
// quantity moved (0 if the part had no stock elsewhere). Used by the bulk move.
func (r *StockRepo) MovePartToLocation(ctx context.Context, partID uuid.UUID, toLoc *uuid.UUID, userID *uuid.UUID) (float64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock every source lot for this part that isn't already at the destination.
	rows, err := tx.Query(ctx, `
		SELECT id, location_id, quantity::float8 FROM stock_items
		WHERE part_id = $1 AND location_id IS DISTINCT FROM $2 AND quantity > 0
		ORDER BY added_at FOR UPDATE`, partID, toLoc)
	if err != nil {
		return 0, err
	}
	type lot struct {
		id  uuid.UUID
		loc *uuid.UUID
		qty float64
	}
	var srcs []lot
	for rows.Next() {
		var l lot
		if err := rows.Scan(&l.id, &l.loc, &l.qty); err != nil {
			rows.Close()
			return 0, err
		}
		srcs = append(srcs, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(srcs) == 0 {
		return 0, tx.Commit(ctx) // nothing to move
	}

	// Find or create the destination lot.
	var destID uuid.UUID
	var destCur float64
	err = tx.QueryRow(ctx, `
		SELECT id, quantity::float8 FROM stock_items
		WHERE part_id = $1 AND location_id IS NOT DISTINCT FROM $2
		ORDER BY added_at LIMIT 1 FOR UPDATE`, partID, toLoc).Scan(&destID, &destCur)
	if errors.Is(err, pgx.ErrNoRows) {
		if err = tx.QueryRow(ctx, `INSERT INTO stock_items (part_id, location_id, quantity) VALUES ($1, $2, 0) RETURNING id`, partID, toLoc).Scan(&destID); err != nil {
			return 0, err
		}
		destCur = 0
	} else if err != nil {
		return 0, err
	}

	note := "bulk move"
	var moved float64
	for _, s := range srcs {
		if _, err = tx.Exec(ctx, `UPDATE stock_items SET quantity = 0 WHERE id = $1`, s.id); err != nil {
			return 0, err
		}
		if _, err = tx.Exec(ctx, `
			INSERT INTO stock_transactions (stock_item_id, kind, delta, resulting_quantity, from_location_id, to_location_id, note, user_id)
			VALUES ($1, 'move', $2, 0, $3, $4, $5, $6)`, s.id, -s.qty, s.loc, toLoc, &note, userID); err != nil {
			return 0, err
		}
		moved += s.qty
	}
	destCur += moved
	if _, err = tx.Exec(ctx, `UPDATE stock_items SET quantity = $2 WHERE id = $1`, destID, destCur); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO stock_transactions (stock_item_id, kind, delta, resulting_quantity, to_location_id, note, user_id)
		VALUES ($1, 'move', $2, $3, $4, $5, $6)`, destID, moved, destCur, toLoc, &note, userID); err != nil {
		return 0, err
	}

	return moved, tx.Commit(ctx)
}

func (r *StockRepo) getItem(ctx context.Context, id uuid.UUID) (*models.StockItem, error) {
	var s models.StockItem
	err := r.pool.QueryRow(ctx, `
		SELECT s.id, s.part_id, p.name, s.location_id, l.name, s.supplier_part_id,
			s.quantity::float8, s.batch, s.serial, s.purchase_price::float8, s.status, s.note,
			s.barcode, s.name, s.split_from,
			s.added_at, s.updated_at
		FROM stock_items s
		JOIN parts p ON p.id = s.part_id
		LEFT JOIN storage_locations l ON l.id = s.location_id
		WHERE s.id = $1`, id).Scan(
		&s.ID, &s.PartID, &s.PartName, &s.LocationID, &s.LocationName, &s.SupplierPartID,
		&s.Quantity, &s.Batch, &s.Serial, &s.PurchasePrice, &s.Status, &s.Note,
		&s.Barcode, &s.Name, &s.SplitFrom,
		&s.AddedAt, &s.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// GetStockItem returns a single lot by id (public wrapper).
func (r *StockRepo) GetStockItem(ctx context.Context, id uuid.UUID) (*models.StockItem, error) {
	item, err := r.getItem(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return item, err
}

// GetByBarcode resolves a lot from its scannable barcode.
func (r *StockRepo) GetByBarcode(ctx context.Context, barcode string) (*models.StockItem, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `SELECT id FROM stock_items WHERE barcode = $1`, barcode).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return r.getItem(ctx, id)
}

// SplitParams cuts a quantity off a source lot into a NEW barcoded lot (e.g. a
// mini spool). The new lot is the same part, at ToLocationID, with its own barcode.
type SplitParams struct {
	SourceID     uuid.UUID
	Quantity     float64
	ToLocationID *uuid.UUID
	Name         *string
	Barcode      *string // optional external barcode; nil = scan by the lot's id
	UserID       *uuid.UUID
}

// SplitLot decrements the source lot and creates a new lot of Quantity, linked via
// split_from. Records split transactions on both lots.
func (r *StockRepo) SplitLot(ctx context.Context, p SplitParams) (*models.StockItem, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var partID uuid.UUID
	var cur float64
	err = tx.QueryRow(ctx, `SELECT part_id, quantity::float8 FROM stock_items WHERE id = $1 FOR UPDATE`, p.SourceID).Scan(&partID, &cur)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if p.Quantity <= 0 {
		return nil, fmt.Errorf("split quantity must be positive")
	}
	if p.Quantity > cur {
		return nil, fmt.Errorf("cannot split %.4g: only %.4g in the source lot", p.Quantity, cur)
	}

	srcQty := cur - p.Quantity
	if _, err = tx.Exec(ctx, `UPDATE stock_items SET quantity = $2 WHERE id = $1`, p.SourceID, srcQty); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO stock_transactions (stock_item_id, kind, delta, resulting_quantity, note, user_id)
		VALUES ($1, 'split', $2, $3, $4, $5)`, p.SourceID, -p.Quantity, srcQty, p.Name, p.UserID); err != nil {
		return nil, err
	}

	var newID uuid.UUID
	if err = tx.QueryRow(ctx, `
		INSERT INTO stock_items (part_id, location_id, quantity, barcode, name, split_from)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		partID, p.ToLocationID, p.Quantity, p.Barcode, p.Name, p.SourceID).Scan(&newID); err != nil {
		return nil, fmt.Errorf("creating lot: %w", err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO stock_transactions (stock_item_id, kind, delta, resulting_quantity, to_location_id, note, user_id)
		VALUES ($1, 'split', $2, $2, $3, $4, $5)`, newID, p.Quantity, p.ToLocationID, p.Name, p.UserID); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.getItem(ctx, newID)
}

// MergeLot pours a lot's quantity into a target lot (same part) and deletes the
// emptied source lot — e.g. tipping a spool back into the reel.
func (r *StockRepo) MergeLot(ctx context.Context, sourceID, targetID uuid.UUID, userID *uuid.UUID) error {
	if sourceID == targetID {
		return fmt.Errorf("cannot merge a lot into itself")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var srcPart uuid.UUID
	var srcQty float64
	if err = tx.QueryRow(ctx, `SELECT part_id, quantity::float8 FROM stock_items WHERE id = $1 FOR UPDATE`, sourceID).Scan(&srcPart, &srcQty); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	var tgtPart uuid.UUID
	var tgtQty float64
	if err = tx.QueryRow(ctx, `SELECT part_id, quantity::float8 FROM stock_items WHERE id = $1 FOR UPDATE`, targetID).Scan(&tgtPart, &tgtQty); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if srcPart != tgtPart {
		return fmt.Errorf("lots are different parts")
	}
	newQty := tgtQty + srcQty
	if _, err = tx.Exec(ctx, `UPDATE stock_items SET quantity = $2 WHERE id = $1`, targetID, newQty); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM stock_items WHERE id = $1`, sourceID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO stock_transactions (stock_item_id, kind, delta, resulting_quantity, user_id)
		VALUES ($1, 'merge', $2, $3, $4)`, targetID, srcQty, newQty, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RelocateLot moves a WHOLE lot to a location, preserving its identity (barcode,
// quantity) — for carrying a spool to another bin. Unlike Move, it does not merge
// into the destination's existing lot.
func (r *StockRepo) RelocateLot(ctx context.Context, lotID uuid.UUID, toLoc *uuid.UUID, userID *uuid.UUID) (*models.StockItem, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var fromLoc *uuid.UUID
	var qty float64
	if err = tx.QueryRow(ctx, `SELECT location_id, quantity::float8 FROM stock_items WHERE id = $1 FOR UPDATE`, lotID).Scan(&fromLoc, &qty); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE stock_items SET location_id = $2 WHERE id = $1`, lotID, toLoc); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO stock_transactions (stock_item_id, kind, delta, resulting_quantity, from_location_id, to_location_id, user_id)
		VALUES ($1, 'move', 0, $2, $3, $4, $5)`, lotID, qty, fromLoc, toLoc, userID); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.getItem(ctx, lotID)
}

// AdjustLot changes a SPECIFIC lot's quantity (kind add/remove/count), unlike
// Adjust which resolves by part+location. Used for lot-precise scan actions.
func (r *StockRepo) AdjustLot(ctx context.Context, lotID uuid.UUID, kind string, qty float64, userID *uuid.UUID) (*models.StockItem, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var cur float64
	if err = tx.QueryRow(ctx, `SELECT quantity::float8 FROM stock_items WHERE id = $1 FOR UPDATE`, lotID).Scan(&cur); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	var newQty float64
	switch kind {
	case "add":
		newQty = cur + qty
	case "remove":
		newQty = cur - qty
		if newQty < 0 {
			newQty = 0
		}
	case "count":
		newQty = qty
	default:
		return nil, fmt.Errorf("unknown adjust kind %q", kind)
	}
	if _, err = tx.Exec(ctx, `UPDATE stock_items SET quantity = $2 WHERE id = $1`, lotID, newQty); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO stock_transactions (stock_item_id, kind, delta, resulting_quantity, user_id)
		VALUES ($1, $2, $3, $4, $5)`, lotID, kind, newQty-cur, newQty, userID); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.getItem(ctx, lotID)
}

// emptyLotPredicate matches stock lots that hold no inventory and carry no
// distinct physical identity: quantity 0, no barcode, and no lot name. A
// barcoded or named lot is a cut spool or tracked unit the user made on purpose,
// so it is kept even at zero (they may reorder into it).
const emptyLotPredicate = `quantity = 0 AND barcode IS NULL AND (name IS NULL OR name = '')`

// CountEmptyLots reports how many lots the cleanup would remove.
func (r *StockRepo) CountEmptyLots(ctx context.Context) (int64, error) {
	var n int64
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM stock_items WHERE `+emptyLotPredicate).Scan(&n)
	return n, err
}

// DeleteEmptyLots removes zero-quantity, non-barcoded, unnamed lots and returns
// the number deleted. Callers gate this on the opt-in stock.delete_empty_lots
// setting; it never runs automatically.
func (r *StockRepo) DeleteEmptyLots(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM stock_items WHERE `+emptyLotPredicate)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
