// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"net/http"

	"github.com/firelabsca/firebin-api/internal/api/middleware"
	"github.com/firelabsca/firebin-api/internal/api/respond"
)

// Me returns the authenticated user's profile.
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	u, err := h.Users.GetByID(r.Context(), middleware.UserID(r.Context()))
	if err != nil {
		respond.Error(w, http.StatusNotFound, "user not found")
		return
	}
	respond.JSON(w, http.StatusOK, u)
}
