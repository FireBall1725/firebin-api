// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package picklist computes what to pull from stock to build a board.
//
// Its own package because two callers need it and neither should own it: the
// HTTP handler that answers /boards/{id}/pick-list, and the assistant tool that
// answers "can I build this". A second implementation would drift from the
// first, and the two answers disagreeing about whether you can build a board is
// worse than either being wrong.
package picklist

import (
	"context"
	"sort"

	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/firelabsca/firebin-api/internal/repository"
	"github.com/google/uuid"
)

// Boards is the board lookup this needs.
type Boards interface {
	GetBoard(ctx context.Context, id uuid.UUID) (*models.Board, error)
}

// Stock is the stock lookup this needs. Lots come back in bin order, which is
// the order they are allocated in, so the pick list reads as a walk.
type Stock interface {
	StockForParts(ctx context.Context, partIDs []uuid.UUID) ([]repository.StockLot, error)
}

// ErrBoardNotFound is returned when the board id does not exist.
type ErrBoardNotFound struct{ ID uuid.UUID }

func (e ErrBoardNotFound) Error() string { return "board not found: " + e.ID.String() }

// Compute builds the pick list for qty copies of a board.
//
// Required per part is qty x per-board quantity x panel copies, aggregated
// across BOM lines. Stock is allocated bin by bin. Parts short of stock and BOM
// lines with no matched part are reported rather than silently dropped: a pick
// list that quietly omits what you do not have is a pick list that says you can
// build something you cannot.
func Compute(ctx context.Context, boards Boards, stock Stock, boardID uuid.UUID, qty int) (*models.PickList, error) {
	if qty < 1 {
		qty = 1
	}
	board, err := boards.GetBoard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	if board == nil {
		return nil, ErrBoardNotFound{ID: boardID}
	}
	copies := board.Copies
	if copies < 1 {
		copies = 1
	}

	// Aggregate required quantity per matched part; collect unmatched lines.
	required := map[uuid.UUID]float64{}
	names := map[uuid.UUID]string{}
	order := []uuid.UUID{}
	unmatched := []models.PickUnmatched{}
	for _, l := range board.Lines {
		need := float64(l.Quantity * copies * qty)
		if l.PartID == nil {
			unmatched = append(unmatched, models.PickUnmatched{
				Refs: l.Refs, Value: l.Value, Quantity: l.Quantity * copies * qty,
				Footprint: l.Footprint, MPN: l.MPN, Manufacturer: l.Manufacturer,
			})
			continue
		}
		if _, seen := required[*l.PartID]; !seen {
			order = append(order, *l.PartID)
			names[*l.PartID] = l.PartName
		}
		required[*l.PartID] += need
	}

	lots, err := stock.StockForParts(ctx, order)
	if err != nil {
		return nil, err
	}
	byPart := map[uuid.UUID][]int{} // part -> indexes into lots (preserves bin order)
	available := map[uuid.UUID]float64{}
	for i, lot := range lots {
		byPart[lot.PartID] = append(byPart[lot.PartID], i)
		available[lot.PartID] += lot.Quantity
	}

	entries := []models.PickEntry{}
	shortfalls := []models.PickShortfall{}
	var total float64
	for _, pid := range order {
		need := required[pid]
		remaining := need
		for _, i := range byPart[pid] {
			if remaining <= 0 {
				break
			}
			lot := lots[i]
			pull := remaining
			if lot.Quantity < pull {
				pull = lot.Quantity
			}
			loc := ""
			if lot.LocationName != nil {
				loc = *lot.LocationName
			}
			entries = append(entries, models.PickEntry{
				StockItemID: lot.ID, PartID: pid, PartName: lot.PartName,
				LocationID: lot.LocationID, LocationName: loc, Quantity: pull,
			})
			remaining -= pull
			total += pull
		}
		if avail := available[pid]; avail < need {
			shortfalls = append(shortfalls, models.PickShortfall{
				PartID: pid, PartName: names[pid], Required: need, Available: avail, Short: need - avail,
			})
		}
	}

	// Walk order: by location name (unbinned last), then part name.
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if (a.LocationName == "") != (b.LocationName == "") {
			return a.LocationName != "" // named bins first
		}
		if a.LocationName != b.LocationName {
			return a.LocationName < b.LocationName
		}
		return a.PartName < b.PartName
	})

	return &models.PickList{
		BoardID: board.ID, BoardName: board.Name, Quantity: qty, Copies: copies,
		TotalUnits: total, Entries: entries, Shortfalls: shortfalls, Unmatched: unmatched,
	}, nil
}
