// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"net/http"

	"github.com/firelabsca/firebin-api/internal/api/respond"
	"github.com/firelabsca/firebin-api/internal/version"
)

// Health is an unauthenticated liveness probe.
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	respond.JSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"service": "firebin-api",
		"version": version.Version,
	})
}
