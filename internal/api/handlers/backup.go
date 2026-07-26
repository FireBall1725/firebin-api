// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/firelabsca/firebin-api/internal/api/respond"
	"github.com/firelabsca/firebin-api/internal/version"
)

const backupFormatVersion = 1

type exportFile struct {
	Format  int                        `json:"format"`
	App     string                     `json:"app"`
	Version string                     `json:"version"`
	Tables  map[string]json.RawMessage `json:"tables"`
}

// ExportData streams the whole instance as a portable JSON backup (admin only).
// This is an application-level export, separate from a Postgres dump; the deployer
// still owns their own database backup strategy.
// @Summary     Export data
// @Description Stream the whole instance as a portable JSON backup file.
// @Tags        backup
// @Security    BearerAuth
// @Produce     json
// @Success     200  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Router      /export  [get]
func (h *Handler) ExportData(w http.ResponseWriter, r *http.Request) {
	tables, err := h.Backup.ExportAll(r.Context())
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not export: "+err.Error())
		return
	}
	out := exportFile{Format: backupFormatVersion, App: "firebin", Version: version.Version, Tables: tables}
	body, err := json.Marshal(out)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not encode export")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="firebin-export.json"`)
	_, _ = w.Write(body)
}

// ImportData restores an export produced by ExportData (admin only). Existing rows
// (by primary key) are left untouched, so importing into a populated instance only
// fills gaps; importing into an empty one is a full restore.
// @Summary     Import data
// @Description Restore an export produced by ExportData; existing rows are left untouched.
// @Tags        backup
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     200      {object}  map[string]interface{}
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Router      /import  [post]
func (h *Handler) ImportData(w http.ResponseWriter, r *http.Request) {
	var in exportFile
	if !respond.Decode(w, r, &in) {
		return
	}
	if in.App != "firebin" || in.Format != backupFormatVersion {
		respond.Error(w, http.StatusBadRequest, fmt.Sprintf("not a FireBin export (format %d)", backupFormatVersion))
		return
	}
	if len(in.Tables) == 0 {
		respond.Error(w, http.StatusBadRequest, "export contains no tables")
		return
	}
	counts, err := h.Backup.ImportAll(r.Context(), in.Tables)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not import: "+err.Error())
		return
	}
	var total int64
	for _, c := range counts {
		total += c
	}
	respond.JSON(w, http.StatusOK, map[string]any{"imported": total, "by_table": counts})
}
