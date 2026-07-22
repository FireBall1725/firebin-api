// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package handlers holds the HTTP handlers for the FireBin API.
package handlers

import (
	"github.com/firelabsca/firebin-api/internal/auth"
	"github.com/firelabsca/firebin-api/internal/config"
	"github.com/firelabsca/firebin-api/internal/repository"
)

// Handler bundles the dependencies shared by every endpoint.
type Handler struct {
	Cfg    *config.Config
	JWT    *auth.JWTService
	Users  *repository.UserRepo
	Tokens *repository.TokenRepo
}

func New(cfg *config.Config, jwt *auth.JWTService, users *repository.UserRepo, tokens *repository.TokenRepo) *Handler {
	return &Handler{Cfg: cfg, JWT: jwt, Users: users, Tokens: tokens}
}
