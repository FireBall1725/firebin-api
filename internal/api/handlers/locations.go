// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/firelabsca/firebin-api/internal/api/respond"
	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/firelabsca/firebin-api/internal/repository"
	"github.com/google/uuid"
)

type locationRequest struct {
	ParentID    *uuid.UUID `json:"parent_id"`
	Name        string     `json:"name"`
	Description *string    `json:"description"`
	Barcode     *string    `json:"barcode"`
}

// @Summary     List locations
// @Description List all storage locations.
// @Tags        locations
// @Security    BearerAuth
// @Produce     json
// @Success     200  {array}   models.StorageLocation
// @Failure     401  {object}  map[string]interface{}
// @Router      /locations  [get]
func (h *Handler) ListLocations(w http.ResponseWriter, r *http.Request) {
	locs, err := h.Locations.List(r.Context())
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not list locations")
		return
	}
	respond.JSON(w, http.StatusOK, locs)
}

// @Summary     Get location
// @Description Return one storage location by id.
// @Tags        locations
// @Security    BearerAuth
// @Produce     json
// @Param       id   path      string                  true   "identifier"
// @Success     200  {object}  models.StorageLocation
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /locations/{id}  [get]
func (h *Handler) GetLocation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	l, err := h.Locations.Get(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "location not found")
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not load location")
		return
	}
	respond.JSON(w, http.StatusOK, l)
}

// ScanLocation resolves a bin by its barcode — "scan a bin to list contents".
// The contents themselves come from the stock listing; this returns the bin.
// @Summary     Scan location
// @Description Resolve a storage location from its barcode.
// @Tags        locations
// @Security    BearerAuth
// @Produce     json
// @Param       barcode  query     string                  true   "barcode"
// @Success     200      {object}  models.StorageLocation
// @Failure     401      {object}  map[string]interface{}
// @Router      /locations/scan  [get]
func (h *Handler) ScanLocation(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("barcode"))
	if code == "" {
		respond.Error(w, http.StatusBadRequest, "barcode query param is required")
		return
	}
	l, err := h.Locations.GetByBarcode(r.Context(), code)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "no location with that barcode")
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not resolve barcode")
		return
	}
	respond.JSON(w, http.StatusOK, l)
}

// @Summary     Create location
// @Description Create a storage location.
// @Tags        locations
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     201      {object}  models.StorageLocation
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Router      /locations  [post]
func (h *Handler) CreateLocation(w http.ResponseWriter, r *http.Request) {
	var req locationRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		respond.Error(w, http.StatusBadRequest, "name is required")
		return
	}
	l := &models.StorageLocation{ParentID: req.ParentID, Name: strings.TrimSpace(req.Name), Description: req.Description, Barcode: req.Barcode}
	if err := h.Locations.Create(r.Context(), l); err != nil {
		respond.Error(w, http.StatusConflict, "could not create location (duplicate name or barcode?)")
		return
	}
	h.Bus.Publish("locations")
	respond.JSON(w, http.StatusCreated, l)
}

// @Summary     Update location
// @Description Update a storage location.
// @Tags        locations
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       id       path      string                  true   "identifier"
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     200      {object}  models.StorageLocation
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Failure     404      {object}  map[string]interface{}
// @Router      /locations/{id}  [patch]
func (h *Handler) UpdateLocation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	var req locationRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	l := &models.StorageLocation{ID: id, ParentID: req.ParentID, Name: strings.TrimSpace(req.Name), Description: req.Description, Barcode: req.Barcode}
	err := h.Locations.Update(r.Context(), l)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "location not found")
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not update location")
		return
	}
	h.Bus.Publish("locations")
	respond.JSON(w, http.StatusOK, l)
}

// @Summary     Delete location
// @Description Delete a storage location.
// @Tags        locations
// @Security    BearerAuth
// @Produce     json
// @Param       id   path      string                  true   "identifier"
// @Success     200  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /locations/{id}  [delete]
func (h *Handler) DeleteLocation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	err := h.Locations.Delete(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "location not found")
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not delete location")
		return
	}
	h.Bus.Publish("locations")
	respond.JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
