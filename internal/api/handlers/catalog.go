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

// @Summary     List manufacturers
// @Description Return all manufacturers in the catalog.
// @Tags        catalog
// @Security    BearerAuth
// @Produce     json
// @Success     200  {array}   map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Router      /manufacturers [get]
func (h *Handler) ListManufacturers(w http.ResponseWriter, r *http.Request) {
	m, err := h.Catalog.ListManufacturers(r.Context())
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not list manufacturers")
		return
	}
	respond.JSON(w, http.StatusOK, m)
}

// @Summary     List suppliers
// @Description Return all suppliers in the catalog.
// @Tags        catalog
// @Security    BearerAuth
// @Produce     json
// @Success     200  {array}   map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Router      /suppliers [get]
func (h *Handler) ListSuppliers(w http.ResponseWriter, r *http.Request) {
	s, err := h.Catalog.ListSuppliers(r.Context())
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not list suppliers")
		return
	}
	respond.JSON(w, http.StatusOK, s)
}

type manufacturerPartRequest struct {
	Manufacturer string  `json:"manufacturer"`
	MPN          string  `json:"mpn"`
	DatasheetURL *string `json:"datasheet_url"`
}

// CreateManufacturerPart adds an MPN (and its brand) to a part.
// @Summary     Create manufacturer part
// @Description Add an MPN and its brand to a part.
// @Tags        catalog
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       id       path      string                  true   "identifier"
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     201      {object}  map[string]interface{}
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Failure     404      {object}  map[string]interface{}
// @Router      /parts/{id}/manufacturer-parts [post]
func (h *Handler) CreateManufacturerPart(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	var req manufacturerPartRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.MPN) == "" {
		respond.Error(w, http.StatusBadRequest, "mpn is required")
		return
	}
	mp, err := h.Catalog.CreateManufacturerPart(r.Context(), id, strings.TrimSpace(req.Manufacturer), strings.TrimSpace(req.MPN), req.DatasheetURL)
	if err != nil {
		respond.Error(w, http.StatusConflict, "could not create manufacturer part (duplicate MPN?)")
		return
	}
	h.Bus.Publish("parts")
	respond.JSON(w, http.StatusCreated, mp)
}

// @Summary     Update manufacturer part
// @Description Update an existing manufacturer part's MPN, brand, or datasheet URL.
// @Tags        catalog
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       id       path      string                  true   "identifier"
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     200      {object}  map[string]interface{}
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Failure     404      {object}  map[string]interface{}
// @Router      /manufacturer-parts/{id} [patch]
func (h *Handler) UpdateManufacturerPart(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	var req manufacturerPartRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.MPN) == "" {
		respond.Error(w, http.StatusBadRequest, "mpn is required")
		return
	}
	err := h.Catalog.UpdateManufacturerPart(r.Context(), id, strings.TrimSpace(req.Manufacturer), strings.TrimSpace(req.MPN), req.DatasheetURL)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not update manufacturer part")
		return
	}
	h.Bus.Publish("parts")
	respond.JSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// @Summary     Delete manufacturer part
// @Description Remove a manufacturer part from a part.
// @Tags        catalog
// @Security    BearerAuth
// @Produce     json
// @Param       id   path      string  true   "identifier"
// @Success     200  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /manufacturer-parts/{id} [delete]
func (h *Handler) DeleteManufacturerPart(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	err := h.Catalog.DeleteManufacturerPart(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not delete")
		return
	}
	h.Bus.Publish("parts")
	respond.JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

type supplierPartRequest struct {
	SupplierID uuid.UUID           `json:"supplier_id"`
	Supplier   string              `json:"supplier"` // name; resolved/created if supplier_id absent
	SKU        string              `json:"sku"`
	Packaging  *string             `json:"packaging"`
	MOQ        *float64            `json:"moq"`
	URL        *string             `json:"url"`
	Pricing    []models.PriceBreak `json:"pricing"`
}

// CreateSupplierPart adds a vendor SKU (and price breaks) to a manufacturer part.
// Accepts either a supplier_id or a supplier name (created on demand).
// @Summary     Create supplier part
// @Description Add a vendor SKU and its price breaks to a manufacturer part.
// @Tags        catalog
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       id       path      string                  true   "identifier"
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     201      {object}  map[string]interface{}
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Failure     404      {object}  map[string]interface{}
// @Router      /manufacturer-parts/{id}/supplier-parts [post]
func (h *Handler) CreateSupplierPart(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r) // manufacturer_part id
	if !ok {
		return
	}
	var req supplierPartRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.SKU) == "" {
		respond.Error(w, http.StatusBadRequest, "sku is required")
		return
	}
	supplierID := req.SupplierID
	if supplierID == uuid.Nil {
		if strings.TrimSpace(req.Supplier) == "" {
			respond.Error(w, http.StatusBadRequest, "supplier_id or supplier name is required")
			return
		}
		var err error
		if supplierID, err = h.Catalog.GetOrCreateSupplier(r.Context(), req.Supplier); err != nil {
			respond.Error(w, http.StatusInternalServerError, "could not resolve supplier")
			return
		}
	}
	spID, err := h.Catalog.CreateSupplierPart(r.Context(), id, supplierID, strings.TrimSpace(req.SKU), req.Packaging, req.URL, req.MOQ, req.Pricing)
	if err != nil {
		respond.Error(w, http.StatusConflict, "could not create supplier part (duplicate SKU?)")
		return
	}
	h.Bus.Publish("parts")
	respond.JSON(w, http.StatusCreated, map[string]string{"id": spID.String()})
}

// @Summary     Delete supplier part
// @Description Remove a supplier part (vendor SKU) from a manufacturer part.
// @Tags        catalog
// @Security    BearerAuth
// @Produce     json
// @Param       id   path      string  true   "identifier"
// @Success     200  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /supplier-parts/{id} [delete]
func (h *Handler) DeleteSupplierPart(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	err := h.Catalog.DeleteSupplierPart(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not delete")
		return
	}
	h.Bus.Publish("parts")
	respond.JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
