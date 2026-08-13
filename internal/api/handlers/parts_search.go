// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"net/http"
	"strconv"

	"github.com/firelabsca/firebin-api/internal/api/respond"
	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/firelabsca/firebin-api/internal/repository"
	"github.com/google/uuid"
)

// SearchParts searches parts by specification rather than by name.
//
// Separate from ListParts because the two answer different questions and return
// different shapes: this one joins part_parameters and reports which parameters
// matched, which the catalog listing has no use for and should not pay for.
// @Summary     Search parts by specification
// @Description Search parts by package and parameter value, with unit-aware numeric matching. A value of "220 ohm" will not match a 100 kΩ part, and a bare "220" matches the magnitude shown on the part regardless of unit.
// @Tags        parts
// @Security    BearerAuth
// @Produce     json
// @Param       category   query     string  false  "Category id filter"
// @Param       search     query     string  false  "Free text over name, keywords, IPN, MPN and tags"
// @Param       package    query     string  false  "Package substring, e.g. 0603"
// @Param       parameter  query     string  false  "Restrict the value filter to a named parameter, e.g. Resistance"
// @Param       value      query     string  false  "Parameter value, e.g. 220, 220 ohm, 4.7uF, X7R"
// @Param       limit      query     int     false  "Maximum parts to return (default 200, max 500)"
// @Success     200  {array}   models.PartMatch
// @Failure     400  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Router      /parts/search  [get]
func (h *Handler) SearchParts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	opts := repository.ParametricOptions{
		Search:    q.Get("search"),
		Package:   q.Get("package"),
		Parameter: q.Get("parameter"),
		Value:     q.Get("value"),
	}
	if c := q.Get("category"); c != "" {
		id, err := uuid.Parse(c)
		if err != nil {
			respond.Error(w, http.StatusBadRequest, "invalid category id")
			return
		}
		opts.CategoryID = &id
	}
	if l := q.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n <= 0 {
			respond.Error(w, http.StatusBadRequest, "invalid limit")
			return
		}
		if n > 500 {
			n = 500
		}
		opts.Limit = n
	}

	// Named rather than inferred so swaggo can resolve the response type: it
	// reads the file's imports, and an annotation naming a package the file does
	// not import fails the docs build.
	var parts []models.PartMatch
	parts, err := h.Parts.SearchParametric(r.Context(), opts)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not search parts")
		return
	}
	h.attachTagsToMatches(r.Context(), parts)
	respond.JSON(w, http.StatusOK, parts)
}
