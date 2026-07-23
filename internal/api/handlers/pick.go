// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"net/http"
	"sort"
	"strconv"

	"github.com/firelabsca/firebin-api/internal/api/respond"
	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/google/uuid"
)

// BoardPickList computes what to pull from stock to build N of a board. Required
// quantity per matched part = N x per-board qty x panel copies, aggregated across
// BOM lines. Stock is allocated bin by bin (ordered for walking); parts short of
// stock and BOM lines with no inventory match are flagged. Query: ?quantity=N.
func (h *Handler) BoardPickList(w http.ResponseWriter, r *http.Request) {
	boardID, ok := pathUUID(w, r)
	if !ok {
		return
	}
	qty := 1
	if q, err := strconv.Atoi(r.URL.Query().Get("quantity")); err == nil && q > 0 {
		qty = q
	}

	board, err := h.Projects.GetBoard(r.Context(), boardID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not load board")
		return
	}
	if board == nil {
		respond.Error(w, http.StatusNotFound, "board not found")
		return
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
			unmatched = append(unmatched, models.PickUnmatched{Refs: l.Refs, Value: l.Value, Quantity: l.Quantity * copies * qty})
			continue
		}
		if _, seen := required[*l.PartID]; !seen {
			order = append(order, *l.PartID)
			names[*l.PartID] = l.PartName
		}
		required[*l.PartID] += need
	}

	lots, err := h.Stock.StockForParts(r.Context(), order)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not read stock")
		return
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

	respond.JSON(w, http.StatusOK, models.PickList{
		BoardID: board.ID, BoardName: board.Name, Quantity: qty, Copies: copies,
		TotalUnits: total, Entries: entries, Shortfalls: shortfalls, Unmatched: unmatched,
	})
}
