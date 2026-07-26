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

type categoryRequest struct {
	ParentID    *uuid.UUID `json:"parent_id"`
	Name        string     `json:"name"`
	Description *string    `json:"description"`
}

// @Summary     List categories
// @Description Return all categories.
// @Tags        categories
// @Security    BearerAuth
// @Produce     json
// @Success     200  {array}   models.Category
// @Failure     401  {object}  map[string]interface{}
// @Router      /categories [get]
func (h *Handler) ListCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := h.Categories.List(r.Context())
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not list categories")
		return
	}
	respond.JSON(w, http.StatusOK, cats)
}

// @Summary     Create category
// @Description Create a new category, optionally nested under a parent.
// @Tags        categories
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     201      {object}  models.Category
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Router      /categories [post]
func (h *Handler) CreateCategory(w http.ResponseWriter, r *http.Request) {
	var req categoryRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		respond.Error(w, http.StatusBadRequest, "name is required")
		return
	}
	c := &models.Category{ParentID: req.ParentID, Name: strings.TrimSpace(req.Name), Description: req.Description}
	if err := h.Categories.Create(r.Context(), c); err != nil {
		respond.Error(w, http.StatusConflict, "could not create category (duplicate name under parent?)")
		return
	}
	h.Bus.Publish("categories")
	respond.JSON(w, http.StatusCreated, c)
}

// @Summary     Update category
// @Description Update an existing category's name, parent, or description.
// @Tags        categories
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       id       path      string                  true   "identifier"
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     200      {object}  models.Category
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Failure     404      {object}  map[string]interface{}
// @Router      /categories/{id} [patch]
func (h *Handler) UpdateCategory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	var req categoryRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	c := &models.Category{ID: id, ParentID: req.ParentID, Name: strings.TrimSpace(req.Name), Description: req.Description}
	err := h.Categories.Update(r.Context(), c)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "category not found")
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not update category")
		return
	}
	h.Bus.Publish("categories")
	respond.JSON(w, http.StatusOK, c)
}

// @Summary     Delete category
// @Description Delete a category that has no parts assigned to it.
// @Tags        categories
// @Security    BearerAuth
// @Produce     json
// @Param       id   path      string  true   "identifier"
// @Success     200  {object}  map[string]string
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /categories/{id} [delete]
func (h *Handler) DeleteCategory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	err := h.Categories.Delete(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "category not found")
		return
	}
	if errors.Is(err, repository.ErrCategoryNotEmpty) {
		respond.Error(w, http.StatusConflict, "category still has parts — move or remove them first")
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not delete category")
		return
	}
	h.Bus.Publish("categories")
	respond.JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// pathUUID parses the {id} path value as a UUID, writing a 400 on failure.
func pathUUID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid id")
		return uuid.Nil, false
	}
	return id, true
}
