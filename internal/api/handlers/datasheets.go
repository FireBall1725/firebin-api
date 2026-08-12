// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/firelabsca/firebin-api/internal/api/middleware"
	"github.com/firelabsca/firebin-api/internal/api/respond"
	"github.com/firelabsca/firebin-api/internal/datasheets"
	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/firelabsca/firebin-api/internal/repository"
	"github.com/google/uuid"
)

// defaultMaxDatasheetBytes caps an upload when the instance has not set its own.
// 64 MiB clears the largest reference manuals (the ESP32-P4 TRM is ~23 MB) with
// room to spare, while still refusing a mistake.
const defaultMaxDatasheetBytes = 64 << 20

// maxDatasheetBytes reads the configured cap, falling back to the default. One
// helper because the upload handler, the mirror fetch, and the settings card all
// need the same answer, and three copies of the parse would drift.
func (h *Handler) maxDatasheetBytes(ctx context.Context) int64 {
	v, _ := h.Settings.Get(ctx, "datasheets.max_bytes")
	if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && n > 0 {
		return n
	}
	return defaultMaxDatasheetBytes
}

// ListDatasheets lists the library.
// @Summary     List datasheets
// @Description List stored datasheets, optionally filtered by search text, category, part, or unlinked state.
// @Tags        datasheets
// @Security    BearerAuth
// @Produce     json
// @Param       search    query     string  false  "Search title, filename, part name, or MPN"
// @Param       category  query     string  false  "Category id filter (matches via linked parts)"
// @Param       part      query     string  false  "Only datasheets linked to this part"
// @Param       unlinked  query     string  false  "Set to true for datasheets with no linked part"
// @Success     200  {array}   models.Datasheet
// @Failure     401  {object}  map[string]interface{}
// @Router      /datasheets  [get]
func (h *Handler) ListDatasheets(w http.ResponseWriter, r *http.Request) {
	opts := repository.DatasheetListOptions{Search: r.URL.Query().Get("search")}
	if v := r.URL.Query().Get("category"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			respond.Error(w, http.StatusBadRequest, "invalid category id")
			return
		}
		opts.CategoryID = &id
	}
	if v := r.URL.Query().Get("part"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			respond.Error(w, http.StatusBadRequest, "invalid part id")
			return
		}
		opts.PartID = &id
	}
	opts.Unlinked = r.URL.Query().Get("unlinked") == "true"

	list, err := h.Datasheets.List(r.Context(), opts)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not list datasheets")
		return
	}
	respond.JSON(w, http.StatusOK, list)
}

// DatasheetStats returns library totals.
// @Summary     Datasheet stats
// @Description Totals for the datasheet library: count, bytes on disk, unlinked, and how many parts have a datasheet URL with no local copy.
// @Tags        datasheets
// @Security    BearerAuth
// @Produce     json
// @Success     200  {object}  repository.DatasheetStats
// @Failure     401  {object}  map[string]interface{}
// @Router      /datasheets/stats  [get]
func (h *Handler) DatasheetStats(w http.ResponseWriter, r *http.Request) {
	s, err := h.Datasheets.Stats(r.Context())
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not read datasheet stats")
		return
	}
	respond.JSON(w, http.StatusOK, s)
}

// GetDatasheet returns one datasheet's metadata.
// @Summary     Get datasheet
// @Description Metadata for one datasheet, including every part it is linked to.
// @Tags        datasheets
// @Security    BearerAuth
// @Produce     json
// @Param       id   path      string  true  "Datasheet id"
// @Success     200  {object}  models.Datasheet
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /datasheets/{id}  [get]
func (h *Handler) GetDatasheet(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	d, err := h.Datasheets.Get(r.Context(), id)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not read the datasheet")
		return
	}
	if d == nil {
		respond.Error(w, http.StatusNotFound, "datasheet not found")
		return
	}
	respond.JSON(w, http.StatusOK, d)
}

// GetDatasheetContent streams the PDF.
//
// Auth-protected, unlike the part image endpoint, which is public so it can be
// used as a bare <img src>. A PDF does not need that: the client fetches it with
// its token and hands the blob to an object URL.
//
// http.ServeContent rather than io.Copy because the browser's built-in PDF
// viewer issues Range requests to page through a large document. Copying the
// whole body would make a 1000-page manual load in full before showing page one.
// @Summary     Get datasheet content
// @Description Stream the stored PDF. Supports range requests.
// @Tags        datasheets
// @Security    BearerAuth
// @Produce     application/pdf
// @Param       id   path      string  true  "Datasheet id"
// @Success     200  {file}    binary
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /datasheets/{id}/content  [get]
func (h *Handler) GetDatasheetContent(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	d, err := h.Datasheets.Get(r.Context(), id)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not read the datasheet")
		return
	}
	if d == nil {
		respond.Error(w, http.StatusNotFound, "datasheet not found")
		return
	}
	f, _, err := h.DatasheetFiles.Open(d.SHA256)
	if err != nil {
		if errors.Is(err, datasheets.ErrNotFound) {
			// The row survived but the file did not: a restored backup without the
			// attachments volume. Say so plainly rather than reporting a 500.
			respond.Error(w, http.StatusNotFound, "the datasheet file is missing from storage")
			return
		}
		respond.Error(w, http.StatusInternalServerError, "could not open the datasheet")
		return
	}
	defer func() { _ = f.Close() }()

	w.Header().Set("Content-Type", d.Mime)
	// inline so the browser renders it in place instead of downloading.
	w.Header().Set("Content-Disposition", `inline; filename="`+sanitizeFilename(d.Filename)+`"`)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, d.Filename, d.UpdatedAt, f)
}

// sanitizeFilename strips quotes and path separators so a stored filename cannot
// break out of the Content-Disposition header or suggest a path to the client.
func sanitizeFilename(name string) string {
	name = filepath.Base(name)
	return strings.Map(func(r rune) rune {
		if r == '"' || r == '\\' || r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, name)
}

// UploadDatasheet stores a PDF, optionally linking it to a part.
// @Summary     Upload datasheet
// @Description Store a PDF in the library. Optionally link it to a part on creation; an unlinked upload is allowed.
// @Tags        datasheets
// @Security    BearerAuth
// @Accept      multipart/form-data
// @Produce     json
// @Param       file                  formData  file    true   "PDF file"
// @Param       part_id               formData  string  false  "Part to link on creation"
// @Param       manufacturer_part_id  formData  string  false  "MPN the datasheet belongs to"
// @Param       title                 formData  string  false  "Display title"
// @Success     201  {object}  models.Datasheet
// @Failure     400  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Failure     413  {object}  map[string]interface{}
// @Failure     422  {object}  map[string]interface{}
// @Router      /datasheets  [post]
func (h *Handler) UploadDatasheet(w http.ResponseWriter, r *http.Request) {
	maxBytes := h.maxDatasheetBytes(r.Context())
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		respond.Error(w, http.StatusBadRequest, "expected a multipart file upload")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "missing 'file' field")
		return
	}
	defer func() { _ = file.Close() }()

	// Read one byte past the cap so an oversized upload is detected rather than
	// silently truncated into a corrupt PDF.
	content, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "could not read the upload")
		return
	}
	if int64(len(content)) > maxBytes {
		respond.Error(w, http.StatusRequestEntityTooLarge,
			"the datasheet is larger than this instance allows")
		return
	}
	if !datasheets.IsPDF(content) {
		respond.Error(w, http.StatusUnprocessableEntity, "the file must be a PDF")
		return
	}

	sha, err := h.DatasheetFiles.Put(content)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not store the datasheet")
		return
	}

	uid := middleware.UserID(r.Context())
	in := repository.NewDatasheet{
		SHA256:    sha,
		Filename:  sanitizeFilename(header.Filename),
		Mime:      "application/pdf",
		SizeBytes: int64(len(content)),
		Origin:    models.OriginUpload,
		CreatedBy: &uid,
	}
	if t := strings.TrimSpace(r.FormValue("title")); t != "" {
		in.Title = &t
	}
	d, err := h.Datasheets.Create(r.Context(), in)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not record the datasheet")
		return
	}

	if v := strings.TrimSpace(r.FormValue("part_id")); v != "" {
		partID, err := uuid.Parse(v)
		if err != nil {
			respond.Error(w, http.StatusBadRequest, "invalid part_id")
			return
		}
		var mpID *uuid.UUID
		if mv := strings.TrimSpace(r.FormValue("manufacturer_part_id")); mv != "" {
			id, err := uuid.Parse(mv)
			if err != nil {
				respond.Error(w, http.StatusBadRequest, "invalid manufacturer_part_id")
				return
			}
			mpID = &id
		}
		if err := h.Datasheets.LinkPart(r.Context(), d.ID, partID, mpID); err != nil {
			respond.Error(w, http.StatusInternalServerError, "could not link the datasheet to the part")
			return
		}
	}

	h.Bus.Publish("datasheets")
	h.Bus.Publish("parts")
	full, _ := h.Datasheets.Get(r.Context(), d.ID)
	if full == nil {
		full = d
	}
	respond.JSON(w, http.StatusCreated, full)
}

type datasheetPatchRequest struct {
	Title *string `json:"title"`
}

// UpdateDatasheet renames a datasheet.
// @Summary     Update datasheet
// @Description Change a datasheet's display title.
// @Tags        datasheets
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       id       path      string                  true  "Datasheet id"
// @Param       request  body      datasheetPatchRequest   true  "Fields to change"
// @Success     200  {object}  models.Datasheet
// @Failure     400  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /datasheets/{id}  [patch]
func (h *Handler) UpdateDatasheet(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	var req datasheetPatchRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	if err := h.Datasheets.SetTitle(r.Context(), id, req.Title); err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not update the datasheet")
		return
	}
	d, err := h.Datasheets.Get(r.Context(), id)
	if err != nil || d == nil {
		respond.Error(w, http.StatusNotFound, "datasheet not found")
		return
	}
	h.Bus.Publish("datasheets")
	respond.JSON(w, http.StatusOK, d)
}

// DeleteDatasheet removes a datasheet and its file.
// @Summary     Delete datasheet
// @Description Remove a datasheet, its part links, its stored PDF, and its extracted text.
// @Tags        datasheets
// @Security    BearerAuth
// @Param       id   path  string  true  "Datasheet id"
// @Success     204  "No Content"
// @Failure     401  {object}  map[string]interface{}
// @Router      /datasheets/{id}  [delete]
func (h *Handler) DeleteDatasheet(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	// Row first, then file. The reverse order can leave a row pointing at nothing
	// if the delete fails; this way the worst case is an orphan file, which costs
	// disk but never breaks a page.
	sha, err := h.Datasheets.Delete(r.Context(), id)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not delete the datasheet")
		return
	}
	if sha != "" {
		if err := h.DatasheetFiles.Delete(sha); err != nil {
			respond.Error(w, http.StatusInternalServerError, "the record was removed but its file could not be deleted")
			return
		}
	}
	h.Bus.Publish("datasheets")
	h.Bus.Publish("parts")
	w.WriteHeader(http.StatusNoContent)
}

type datasheetLinkRequest struct {
	PartID             uuid.UUID  `json:"part_id"`
	ManufacturerPartID *uuid.UUID `json:"manufacturer_part_id"`
}

// LinkDatasheetPart attaches a datasheet to a part.
// @Summary     Link datasheet to part
// @Description Attach a datasheet to a part. A datasheet may cover many parts.
// @Tags        datasheets
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       id       path      string                true  "Datasheet id"
// @Param       request  body      datasheetLinkRequest  true  "Part to link"
// @Success     200  {object}  models.Datasheet
// @Failure     400  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Router      /datasheets/{id}/parts  [post]
func (h *Handler) LinkDatasheetPart(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	var req datasheetLinkRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	if req.PartID == uuid.Nil {
		respond.Error(w, http.StatusBadRequest, "part_id is required")
		return
	}
	if err := h.Datasheets.LinkPart(r.Context(), id, req.PartID, req.ManufacturerPartID); err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not link the datasheet")
		return
	}
	h.Bus.Publish("datasheets")
	h.Bus.Publish("parts")
	d, _ := h.Datasheets.Get(r.Context(), id)
	if d == nil {
		respond.Error(w, http.StatusNotFound, "datasheet not found")
		return
	}
	respond.JSON(w, http.StatusOK, d)
}

// UnlinkDatasheetPart detaches a datasheet from a part.
// @Summary     Unlink datasheet from part
// @Description Detach a datasheet from a part. The datasheet itself is kept and becomes unlinked if it has no other parts.
// @Tags        datasheets
// @Security    BearerAuth
// @Param       id       path  string  true  "Datasheet id"
// @Param       partID   path  string  true  "Part id"
// @Success     204  "No Content"
// @Failure     401  {object}  map[string]interface{}
// @Router      /datasheets/{id}/parts/{partID}  [delete]
func (h *Handler) UnlinkDatasheetPart(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	partID, err := uuid.Parse(r.PathValue("partID"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid part id")
		return
	}
	if err := h.Datasheets.UnlinkPart(r.Context(), id, partID); err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not unlink the datasheet")
		return
	}
	h.Bus.Publish("datasheets")
	h.Bus.Publish("parts")
	w.WriteHeader(http.StatusNoContent)
}

// datasheetHTTPTimeout bounds a single mirror fetch. Generous because some
// manufacturer sites are slow to serve a 20 MB PDF, but finite: the job's
// context has to be able to cancel, and River's own timeout sits above this.
const datasheetHTTPTimeout = 60 * time.Second
