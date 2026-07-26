// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"net/http"
	"strings"

	"github.com/firelabsca/firebin-api/internal/api/middleware"
	"github.com/firelabsca/firebin-api/internal/api/respond"
	"github.com/firelabsca/firebin-api/internal/auth"
	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/google/uuid"
)

// Me returns the authenticated user's profile.
// @Summary     Get my profile
// @Description Return the authenticated user's profile.
// @Tags        account
// @Security    BearerAuth
// @Produce     json
// @Success     200  {object}  models.User
// @Failure     401  {object}  map[string]interface{}
// @Router      /me  [get]
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	u, err := h.Users.GetByID(r.Context(), middleware.UserID(r.Context()))
	if err != nil {
		respond.Error(w, http.StatusNotFound, "user not found")
		return
	}
	respond.JSON(w, http.StatusOK, u)
}

func validRole(role string) bool {
	return role == "admin" || role == "member" || role == "viewer"
}

// ListUsers returns every user for the admin user-management screen.
// @Summary     List users
// @Description Return every user for the admin user-management screen.
// @Tags        users
// @Security    BearerAuth
// @Produce     json
// @Success     200  {array}   models.User
// @Failure     401  {object}  map[string]interface{}
// @Router      /users  [get]
func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.Users.List(r.Context())
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not list users")
		return
	}
	respond.JSON(w, http.StatusOK, users)
}

type createUserRequest struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	Role        string `json:"role"`
	Email       string `json:"email,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

// CreateUser adds a user with a role (admin only). No email invite; the admin
// sets an initial password and shares it out of band.
// @Summary     Create a user
// @Description Add a user with a role; the admin sets an initial password and shares it out of band.
// @Tags        users
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     201      {object}  models.User
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Router      /users  [post]
func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || len(req.Password) < 8 {
		respond.Error(w, http.StatusBadRequest, "username required and password must be at least 8 characters")
		return
	}
	if req.Role == "" {
		req.Role = "member"
	}
	if !validRole(req.Role) {
		respond.Error(w, http.StatusBadRequest, "invalid role")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not hash password")
		return
	}
	u := &models.User{Username: req.Username, PasswordHash: hash, Role: req.Role, IsInstanceAdmin: req.Role == "admin"}
	if req.Email != "" {
		u.Email = &req.Email
	}
	if req.DisplayName != "" {
		u.DisplayName = &req.DisplayName
	}
	if err := h.Users.Create(r.Context(), u); err != nil {
		respond.Error(w, http.StatusConflict, "username or email already taken")
		return
	}
	respond.JSON(w, http.StatusCreated, u)
}

type updateUserRequest struct {
	Role        string  `json:"role"`
	IsActive    bool    `json:"is_active"`
	DisplayName *string `json:"display_name"`
}

// UpdateUser sets a user's role, active flag, and display name (admin only),
// refusing any change that would remove the last active admin.
// @Summary     Update a user
// @Description Set a user's role, active flag, and display name, refusing any change that removes the last active admin.
// @Tags        users
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       id       path      string                  true   "user ID"
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     200      {object}  models.User
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Failure     404      {object}  map[string]interface{}
// @Router      /users/{id}  [patch]
func (h *Handler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req updateUserRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	if !validRole(req.Role) {
		respond.Error(w, http.StatusBadRequest, "invalid role")
		return
	}
	target, err := h.Users.GetByID(r.Context(), id)
	if err != nil {
		respond.Error(w, http.StatusNotFound, "user not found")
		return
	}
	if target.Role == "admin" && (req.Role != "admin" || !req.IsActive) && h.lastAdmin(r) {
		respond.Error(w, http.StatusBadRequest, "cannot demote or disable the last admin")
		return
	}
	u, err := h.Users.Update(r.Context(), id, req.Role, req.IsActive, req.DisplayName)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not update user")
		return
	}
	respond.JSON(w, http.StatusOK, u)
}

type passwordRequest struct {
	Password string `json:"password"`
}

// ResetUserPassword sets a new password for a user (admin only).
// @Summary     Reset a user's password
// @Description Set a new password for a user.
// @Tags        users
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       id       path      string                  true   "user ID"
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     200      {object}  map[string]string
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Failure     404      {object}  map[string]interface{}
// @Router      /users/{id}/reset-password  [post]
func (h *Handler) ResetUserPassword(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req passwordRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	if len(req.Password) < 8 {
		respond.Error(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not hash password")
		return
	}
	if err := h.Users.SetPassword(r.Context(), id, hash); err != nil {
		respond.Error(w, http.StatusNotFound, "user not found")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// DeleteUser removes a user (admin only). You cannot delete yourself or the last admin.
// @Summary     Delete a user
// @Description Remove a user; you cannot delete yourself or the last admin.
// @Tags        users
// @Security    BearerAuth
// @Produce     json
// @Param       id   path      string                  true   "user ID"
// @Success     200  {object}  map[string]string
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /users/{id}  [delete]
func (h *Handler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid id")
		return
	}
	if id == middleware.UserID(r.Context()) {
		respond.Error(w, http.StatusBadRequest, "you cannot delete your own account")
		return
	}
	target, err := h.Users.GetByID(r.Context(), id)
	if err != nil {
		respond.Error(w, http.StatusNotFound, "user not found")
		return
	}
	if target.Role == "admin" && h.lastAdmin(r) {
		respond.Error(w, http.StatusBadRequest, "cannot delete the last admin")
		return
	}
	if err := h.Users.Delete(r.Context(), id); err != nil {
		respond.Error(w, http.StatusNotFound, "user not found")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// ChangeMyPassword lets any signed-in user (any role) change their own password
// after confirming the current one.
// @Summary     Change my password
// @Description Let any signed-in user change their own password after confirming the current one.
// @Tags        account
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     200      {object}  map[string]string
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Router      /users/me/password  [patch]
func (h *Handler) ChangeMyPassword(w http.ResponseWriter, r *http.Request) {
	var req changePasswordRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	if len(req.NewPassword) < 8 {
		respond.Error(w, http.StatusBadRequest, "new password must be at least 8 characters")
		return
	}
	id := middleware.UserID(r.Context())
	u, err := h.Users.GetByID(r.Context(), id)
	if err != nil {
		respond.Error(w, http.StatusUnauthorized, "not found")
		return
	}
	if auth.VerifyPassword(u.PasswordHash, req.CurrentPassword) != nil {
		respond.Error(w, http.StatusBadRequest, "current password is incorrect")
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not hash password")
		return
	}
	if err := h.Users.SetPassword(r.Context(), id, hash); err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not update password")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// lastAdmin reports whether one or fewer active admins remain.
func (h *Handler) lastAdmin(r *http.Request) bool {
	n, err := h.Users.CountAdmins(r.Context())
	return err != nil || n <= 1
}
