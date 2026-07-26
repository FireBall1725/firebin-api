// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"net/http"
	"strings"

	"github.com/firelabsca/firebin-api/internal/api/respond"
	"github.com/firelabsca/firebin-api/internal/barcode/eigp114"
	"github.com/firelabsca/firebin-api/internal/barcode/lcsc"
	"github.com/google/uuid"
)

type scanRequest struct {
	Code string `json:"code"` // raw decoded barcode string
}

type scanMatch struct {
	PartID   uuid.UUID `json:"part_id"`
	PartName string    `json:"part_name"`
}

type scanResponse struct {
	Parsed  *eigp114.Parsed `json:"parsed"`
	IsEIGP  bool            `json:"is_eigp"`
	Match   *scanMatch      `json:"match,omitempty"` // existing part with this MPN, if any
	RawCode string          `json:"raw_code"`
}

// Scan parses a decoded distributor barcode (ECIA EIGP 114 Data Matrix) and,
// if its MPN already exists in the catalog, returns the matching part so the
// client can jump straight to "add stock". Otherwise the client offers to
// create a new part prefilled from the parsed fields.
// @Summary     Scan barcode
// @Description Parse a decoded distributor barcode and return a matching part when its MPN already exists.
// @Tags        scan
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       request  body      map[string]interface{}  true  "Scan request"
// @Success     200  {object}  map[string]interface{}
// @Failure     400  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Router      /scan  [post]
func (h *Handler) Scan(w http.ResponseWriter, r *http.Request) {
	var req scanRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	code := strings.TrimSpace(req.Code)
	if code == "" {
		respond.Error(w, http.StatusBadRequest, "code is required")
		return
	}

	// Pick the parser by label shape: LCSC's {key:value} QR, ECIA EIGP 114, or a
	// bare MPN typed/scanned on its own.
	isLCSC := lcsc.IsLCSC(code)
	isEIGP := eigp114.IsEIGP(code)
	var parsed *eigp114.Parsed
	if isLCSC {
		parsed = lcsc.Parse(code)
	} else {
		parsed = eigp114.Parse(code)
	}
	resp := scanResponse{
		Parsed:  parsed,
		IsEIGP:  isEIGP,
		RawCode: code,
	}

	// A bare scan (no envelope) is treated as an MPN itself.
	mpn := parsed.MPN
	if mpn == "" && !isEIGP && !isLCSC {
		mpn = code
		resp.Parsed.MPN = code
	}

	if mpn != "" {
		if id, name, found, err := h.Catalog.FindPartByMPN(r.Context(), mpn); err == nil && found {
			resp.Match = &scanMatch{PartID: id, PartName: name}
		}
	}

	respond.JSON(w, http.StatusOK, resp)
}
