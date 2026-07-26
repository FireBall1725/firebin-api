// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/firelabsca/firebin-api/internal/api/middleware"
	"github.com/firelabsca/firebin-api/internal/api/respond"
	"github.com/firelabsca/firebin-api/internal/auth"
	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/firelabsca/firebin-api/internal/repository"
	"github.com/google/uuid"
)

type createPATRequest struct {
	Name      string     `json:"name"`
	Scopes    []string   `json:"scopes,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type createPATResponse struct {
	Token string          `json:"token"` // shown exactly once
	Meta  models.APIToken `json:"meta"`
}

// CreatePAT mints a new personal access token for the authenticated user.
// @Summary     Create a personal access token
// @Description Mint a new personal access token for the authenticated user; the raw token is shown once.
// @Tags        tokens
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     201      {object}  map[string]interface{}
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Router      /tokens  [post]
func (h *Handler) CreatePAT(w http.ResponseWriter, r *http.Request) {
	var req createPATRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 64 {
		respond.Error(w, http.StatusBadRequest, "name is required (1-64 chars)")
		return
	}

	raw, hash, suffix, err := auth.GeneratePAT()
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not generate token")
		return
	}
	if req.Scopes == nil {
		req.Scopes = []string{}
	}

	t := &models.APIToken{
		UserID:      middleware.UserID(r.Context()),
		Name:        req.Name,
		TokenSuffix: suffix,
		Scopes:      req.Scopes,
		ExpiresAt:   req.ExpiresAt,
	}
	if err := h.Tokens.CreatePAT(r.Context(), t, hash); err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not store token")
		return
	}

	respond.JSON(w, http.StatusCreated, createPATResponse{Token: raw, Meta: *t})
}

// ListPATs returns the caller's tokens (metadata only, never the raw values).
// @Summary     List personal access tokens
// @Description Return the caller's tokens as metadata only, never the raw values.
// @Tags        tokens
// @Security    BearerAuth
// @Produce     json
// @Success     200  {array}   map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Router      /tokens  [get]
func (h *Handler) ListPATs(w http.ResponseWriter, r *http.Request) {
	tokens, err := h.Tokens.ListPATs(r.Context(), middleware.UserID(r.Context()))
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not list tokens")
		return
	}
	if tokens == nil {
		tokens = []models.APIToken{}
	}
	respond.JSON(w, http.StatusOK, tokens)
}

// RevokePAT revokes one of the caller's tokens by id.
// @Summary     Revoke a personal access token
// @Description Revoke one of the caller's tokens by id.
// @Tags        tokens
// @Security    BearerAuth
// @Produce     json
// @Param       id   path      string                  true   "token ID"
// @Success     200  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /tokens/{id}  [delete]
func (h *Handler) RevokePAT(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid token id")
		return
	}
	err = h.Tokens.RevokePAT(r.Context(), middleware.UserID(r.Context()), id)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "token not found")
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not revoke token")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}
