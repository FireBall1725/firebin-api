// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/firelabsca/firebin-api/internal/api/respond"
	"github.com/firelabsca/firebin-api/internal/version"
)

const backupFormatVersion = 1

// maxImportBytes caps the import request body. An export is the whole instance as
// JSON, so this is far above the default endpoint cap; it still bounds memory use.
const maxImportBytes = 256 << 20 // 256 MiB

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

// ImportData restores an export produced by ExportData (admin only). With
// ?mode=replace it wipes every durable table first and loads the export exactly
// (the correct choice for restoring a backup from another instance); otherwise it
// merges, skipping rows that already exist by primary key.
// @Summary     Import data
// @Description Restore an export produced by ExportData. mode=merge (default) skips existing rows; mode=replace wipes all data first and restores the export exactly.
// @Tags        backup
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       mode     query     string                  false  "merge (default) or replace"
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     200      {object}  map[string]interface{}
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Router      /import  [post]
func (h *Handler) ImportData(w http.ResponseWriter, r *http.Request) {
	replace := r.URL.Query().Get("mode") == "replace"
	var in exportFile
	// A full-instance export is the whole database as JSON and easily exceeds the
	// default 1 MiB body cap, so allow a much larger body for this endpoint only.
	if !respond.DecodeMax(w, r, &in, maxImportBytes) {
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
	counts, err := h.Backup.ImportAll(r.Context(), in.Tables, replace)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not import: "+err.Error())
		return
	}
	// instance_settings is in the backup, so an import replaces the assistant's
	// provider config underneath a registry that was populated once at boot.
	// Without this the restored rows are in the database and the running process
	// is still using what it read at startup: the settings page shows the URL
	// from the backup while every request goes to the old one, which reads as
	// the app ignoring its own settings.
	//
	// Logged rather than fatal, matching boot: the data restored fine, and an
	// unreadable provider config should leave the assistant unconfigured rather
	// than fail a restore that worked.
	if h.AI != nil {
		if err := h.AI.Load(r.Context()); err != nil {
			slog.Warn("import restored settings but the AI registry could not be reloaded; restart to pick them up", "error", err)
		}
	}

	var total int64
	for _, c := range counts {
		total += c
	}
	respond.JSON(w, http.StatusOK, map[string]any{"imported": total, "by_table": counts, "replaced": replace})
}
