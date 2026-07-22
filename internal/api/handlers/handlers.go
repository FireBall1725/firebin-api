// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package handlers holds the HTTP handlers for the FireBin API.
package handlers

import (
	"github.com/firelabsca/firebin-api/internal/auth"
	"github.com/firelabsca/firebin-api/internal/config"
	"github.com/firelabsca/firebin-api/internal/events"
	"github.com/firelabsca/firebin-api/internal/repository"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Handler bundles the dependencies shared by every endpoint.
type Handler struct {
	Cfg    *config.Config
	JWT    *auth.JWTService
	Users  *repository.UserRepo
	Tokens *repository.TokenRepo

	Categories *repository.CategoryRepo
	Parts      *repository.PartRepo
	Locations  *repository.LocationRepo
	Stock      *repository.StockRepo
	Stats      *repository.StatsRepo
	Bus        *events.Broker
}

// New builds the handler and all its repositories from the connection pool.
func New(cfg *config.Config, pool *pgxpool.Pool, jwt *auth.JWTService) *Handler {
	return &Handler{
		Cfg:        cfg,
		JWT:        jwt,
		Users:      repository.NewUserRepo(pool),
		Tokens:     repository.NewTokenRepo(pool),
		Categories: repository.NewCategoryRepo(pool),
		Parts:      repository.NewPartRepo(pool),
		Locations:  repository.NewLocationRepo(pool),
		Stock:      repository.NewStockRepo(pool),
		Stats:      repository.NewStatsRepo(pool),
		Bus:        events.NewBroker(),
	}
}
