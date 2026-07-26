// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"
	"time"

	"github.com/firelabsca/firebin-api/internal/api/respond"
	"github.com/firelabsca/firebin-api/internal/auth"
	"github.com/firelabsca/firebin-api/internal/models"
)

type authRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Email    string `json:"email,omitempty"`
}

type tokenResponse struct {
	AccessToken  string       `json:"access_token"`
	RefreshToken string       `json:"refresh_token"`
	User         *models.User `json:"user"`
}

// Register creates a new user. The first user to register becomes the instance
// admin; registration can be disabled for everyone else via config.
// @Summary     Register a new user
// @Description Create a new account; the first registrant becomes the instance admin.
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     201      {object}  map[string]interface{}
// @Failure     400      {object}  map[string]interface{}
// @Router      /auth/register  [post]
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || len(req.Password) < 8 {
		respond.Error(w, http.StatusBadRequest, "username required and password must be at least 8 characters")
		return
	}

	count, err := h.Users.Count(r.Context())
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not check registration state")
		return
	}
	// First user always allowed (bootstraps the instance admin); after that,
	// honour the registration toggle.
	isFirst := count == 0
	if !isFirst && !h.Cfg.RegistrationEnabled {
		respond.Error(w, http.StatusForbidden, "registration is disabled")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not hash password")
		return
	}

	// First user bootstraps as admin; anyone added after (only when registration
	// is enabled) starts as a member. Admins can change roles afterward.
	role := "member"
	if isFirst {
		role = "admin"
	}
	u := &models.User{
		Username:        req.Username,
		PasswordHash:    hash,
		Role:            role,
		IsInstanceAdmin: isFirst,
	}
	if req.Email != "" {
		u.Email = &req.Email
	}
	if err := h.Users.Create(r.Context(), u); err != nil {
		respond.Error(w, http.StatusConflict, "username or email already taken")
		return
	}

	h.issueTokens(w, r, u)
}

// Login authenticates a username/password and issues a token pair.
// @Summary     Log in
// @Description Authenticate a username and password and issue an access + refresh token pair.
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     200      {object}  map[string]interface{}
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Router      /auth/login  [post]
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if !respond.Decode(w, r, &req) {
		return
	}

	u, err := h.Users.GetByUsername(r.Context(), strings.TrimSpace(req.Username))
	if err != nil {
		// Uniform message so we don't leak which usernames exist.
		respond.Error(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if !u.IsActive || auth.VerifyPassword(u.PasswordHash, req.Password) != nil {
		respond.Error(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	h.issueTokens(w, r, u)
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// Refresh exchanges a valid refresh token for a new access + refresh pair,
// rotating the old refresh token.
// @Summary     Refresh tokens
// @Description Exchange a valid refresh token for a new access + refresh pair, rotating the old one.
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     200      {object}  map[string]interface{}
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Router      /auth/refresh  [post]
func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	hash := auth.HashToken(req.RefreshToken)
	userID, err := h.Tokens.LookupRefreshToken(r.Context(), hash)
	if err != nil {
		respond.Error(w, http.StatusUnauthorized, "invalid or expired refresh token")
		return
	}
	// Rotate: revoke the presented token so it can't be replayed.
	_ = h.Tokens.RevokeRefreshToken(r.Context(), hash)

	u, err := h.Users.GetByID(r.Context(), userID)
	if err != nil || !u.IsActive {
		respond.Error(w, http.StatusUnauthorized, "account not found or disabled")
		return
	}
	h.issueTokens(w, r, u)
}

// Logout revokes the presented refresh token.
// @Summary     Log out
// @Description Revoke the presented refresh token.
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     200      {object}  map[string]interface{}
// @Failure     400      {object}  map[string]interface{}
// @Router      /auth/logout  [post]
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	if req.RefreshToken != "" {
		_ = h.Tokens.RevokeRefreshToken(r.Context(), auth.HashToken(req.RefreshToken))
	}
	respond.JSON(w, http.StatusOK, map[string]string{"status": "logged out"})
}

// issueTokens generates an access token and a rotating refresh token, persists
// the refresh token hash, and writes the token pair.
func (h *Handler) issueTokens(w http.ResponseWriter, r *http.Request, u *models.User) {
	access, err := h.JWT.Generate(u.ID, u.IsInstanceAdmin)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not issue access token")
		return
	}

	refresh, err := randomToken()
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not issue refresh token")
		return
	}
	expiresAt := time.Now().Add(h.Cfg.JWTRefreshTTL)
	if err := h.Tokens.CreateRefreshToken(r.Context(), u.ID, auth.HashToken(refresh), expiresAt); err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not persist refresh token")
		return
	}

	respond.JSON(w, http.StatusOK, tokenResponse{
		AccessToken:  access,
		RefreshToken: refresh,
		User:         u,
	})
}

// randomToken returns a URL-safe random opaque string for refresh tokens.
func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
