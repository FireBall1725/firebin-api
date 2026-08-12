// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/firelabsca/firebin-api/internal/api/middleware"
	"github.com/firelabsca/firebin-api/internal/api/respond"
	"github.com/firelabsca/firebin-api/internal/datasheets"
	"github.com/firelabsca/firebin-api/internal/jobs"
	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/firelabsca/firebin-api/internal/repository"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

// MirrorTarget is one MPN to fetch a datasheet for.
type MirrorTarget struct {
	ManufacturerPartID uuid.UUID `json:"manufacturer_part_id"`
	PartID             uuid.UUID `json:"part_id"`
	MPN                string    `json:"mpn"`
	URL                string    `json:"url"`
}

// DatasheetMirrorArgs is the payload for a mirror run. ArgsVersion lets the
// shape evolve without breaking jobs already queued.
type DatasheetMirrorArgs struct {
	TaskID      uuid.UUID      `json:"task_id"`
	ArgsVersion int            `json:"args_version"`
	Targets     []MirrorTarget `json:"targets"`
}

func (DatasheetMirrorArgs) Kind() string { return "datasheet_mirror" }

// datasheetMirrorWorker downloads each target's datasheet and stores it.
//
// Idempotent by construction: the file is content-addressed, the row upserts on
// sha256, and the part link upserts on its primary key. A retry after a partial
// run re-fetches but changes nothing, which matters because this job is the one
// most likely to be interrupted (large files, slow manufacturer sites).
type datasheetMirrorWorker struct {
	river.WorkerDefaults[DatasheetMirrorArgs]
	h    *Handler
	deps *jobs.Deps
}

// Timeout is a real ceiling rather than River's 60s default or disabled. A
// backfill of several hundred PDFs over slow vendor sites needs room; a wedged
// run still dies.
func (w *datasheetMirrorWorker) Timeout(*river.Job[DatasheetMirrorArgs]) time.Duration {
	return 60 * time.Minute
}

func (w *datasheetMirrorWorker) Work(ctx context.Context, job *river.Job[DatasheetMirrorArgs]) error {
	a := job.Args
	return w.deps.Run(ctx, a.TaskID, job.Attempt, job.MaxAttempts, func(r *jobs.Run) error {
		rctx := r.Context()
		stored, skipped := 0, 0

		err := jobs.ForEach(r, a.Targets, func(_ int, t MirrorTarget) {
			content, err := w.h.fetchDatasheet(rctx, t.URL)
			if err != nil {
				r.Log("warn", "%s: %v", t.MPN, err)
				skipped++
				return
			}
			sha, err := w.h.DatasheetFiles.Put(content)
			if err != nil {
				r.Log("warn", "%s: could not store: %v", t.MPN, err)
				skipped++
				return
			}
			url := t.URL
			name := datasheetFilename(t.URL, t.MPN)
			d, err := w.h.Datasheets.Create(rctx, repository.NewDatasheet{
				SHA256:    sha,
				Filename:  name,
				Mime:      "application/pdf",
				SizeBytes: int64(len(content)),
				SourceURL: &url,
				Origin:    models.OriginMirror,
			})
			if err != nil {
				r.Log("warn", "%s: could not record: %v", t.MPN, err)
				skipped++
				return
			}
			mpID := t.ManufacturerPartID
			if err := w.h.Datasheets.LinkPart(rctx, d.ID, t.PartID, &mpID); err != nil {
				r.Log("warn", "%s: stored but could not link: %v", t.MPN, err)
				skipped++
				return
			}
			r.Log("info", "%s: %s (%d KB)", t.MPN, name, len(content)/1024)
			stored++
		})

		r.SetResult(map[string]int{"stored": stored, "skipped": skipped})
		w.h.Bus.Publish("datasheets")
		w.h.Bus.Publish("parts")
		if err != nil {
			return err // cancelled part-way
		}
		r.Log("info", "done: %d stored, %d skipped", stored, skipped)
		return nil
	})
}

// fetchDatasheet downloads a URL and verifies it is really a PDF.
//
// The content check is not paranoia. A rotted distributor link regularly answers
// 200 with an HTML "product not found" page, and storing that would produce a
// library full of documents that open to nothing. Failing here instead leaves
// the vendor URL in place and the part honestly marked as un-mirrored.
func (h *Handler) fetchDatasheet(ctx context.Context, url string) ([]byte, error) {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("unsupported datasheet URL")
	}
	maxBytes := h.maxDatasheetBytes(ctx)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// Some manufacturer CDNs serve a challenge page to an unrecognised client.
	req.Header.Set("User-Agent", "FireBin/1.0 (+https://github.com/FireBall1725/firebin)")
	req.Header.Set("Accept", "application/pdf,*/*")

	client := &http.Client{Timeout: datasheetHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("datasheet URL returned %d", resp.StatusCode)
	}

	content, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > maxBytes {
		return nil, fmt.Errorf("datasheet is larger than the configured limit")
	}
	if !datasheets.IsPDF(content) {
		return nil, fmt.Errorf("the URL did not return a PDF (likely a dead link serving an error page)")
	}
	return content, nil
}

// datasheetFilename picks a display name from the URL, falling back to the MPN.
func datasheetFilename(rawURL, mpn string) string {
	name := rawURL
	if i := strings.IndexAny(name, "?#"); i >= 0 {
		name = name[:i]
	}
	name = path.Base(name)
	if name == "" || name == "." || name == "/" || !strings.HasSuffix(strings.ToLower(name), ".pdf") {
		if mpn != "" {
			return sanitizeFilename(mpn) + ".pdf"
		}
		return "datasheet.pdf"
	}
	return sanitizeFilename(name)
}

// autoMirrorDatasheet queues a mirror after enrichment, when the instance has
// opted in.
//
// Off by default, and that default is the feature rather than caution. Providers
// regularly return a datasheet in a language the user cannot read, and a silent
// download of every one of those fills a volume with documents nobody asked for.
// With the setting off, enrichment still records the URL and the part shows a
// "Save a copy" button, so the choice stays with the person looking at it.
//
// Failures here are swallowed on purpose: enrichment succeeded, and a datasheet
// that could not be queued must not fail the part update that did work.
func (h *Handler) autoMirrorDatasheet(ctx context.Context, partID, mpID uuid.UUID, mpn, url string) {
	if v, _ := h.Settings.Get(ctx, "datasheets.auto_mirror"); v != "true" {
		return
	}
	if strings.TrimSpace(url) == "" {
		return
	}
	// Already have something for this part; a family PDF covers the siblings.
	existing, err := h.Datasheets.List(ctx, repository.DatasheetListOptions{PartID: &partID, Limit: 1})
	if err != nil || len(existing) > 0 {
		return
	}
	taskID := uuid.New()
	args := DatasheetMirrorArgs{TaskID: taskID, Targets: []MirrorTarget{{
		ManufacturerPartID: mpID, PartID: partID, MPN: mpn, URL: url,
	}}}
	summary, _ := json.Marshal(args)
	_ = h.Jobs.Enqueue(ctx, taskID, args, jobs.EnqueueMeta{
		Type: "datasheet_mirror", Queue: jobs.QueueIngest, MaxAttempts: 3,
		ArgsSummary: summary, ProgressTotal: 1,
	})
}

// MirrorDatasheet saves a local copy of one MPN's datasheet.
// @Summary     Mirror an MPN's datasheet
// @Description Download the datasheet at this manufacturer part's URL and store it locally.
// @Tags        datasheets
// @Security    BearerAuth
// @Produce     json
// @Param       id   path      string  true  "Manufacturer part id"
// @Success     202  {object}  map[string]interface{}
// @Failure     400  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /manufacturer-parts/{id}/datasheet/mirror  [post]
func (h *Handler) MirrorDatasheet(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	mp, err := h.Catalog.GetManufacturerPart(r.Context(), id)
	if err != nil || mp == nil {
		respond.Error(w, http.StatusNotFound, "manufacturer part not found")
		return
	}
	if mp.DatasheetURL == nil || strings.TrimSpace(*mp.DatasheetURL) == "" {
		respond.Error(w, http.StatusBadRequest, "this MPN has no datasheet URL to save")
		return
	}

	uid := middleware.UserID(r.Context())
	taskID := uuid.New()
	args := DatasheetMirrorArgs{TaskID: taskID, Targets: []MirrorTarget{{
		ManufacturerPartID: mp.ID, PartID: mp.PartID, MPN: mp.MPN, URL: *mp.DatasheetURL,
	}}}
	summary, _ := json.Marshal(args)
	if err := h.Jobs.Enqueue(r.Context(), taskID, args, jobs.EnqueueMeta{
		Type: "datasheet_mirror", Queue: jobs.QueueIngest, MaxAttempts: 3,
		CreatedBy: &uid, ArgsSummary: summary, ProgressTotal: 1,
	}); err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not start the download")
		return
	}
	respond.JSON(w, http.StatusAccepted, map[string]any{"task_id": taskID})
}

// BulkMirrorDatasheets backfills every part that has a datasheet URL but no copy.
// @Summary     Mirror missing datasheets
// @Description Download a local copy for every part that has a datasheet URL and no stored file.
// @Tags        datasheets
// @Security    BearerAuth
// @Produce     json
// @Success     202  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Router      /datasheets/bulk/mirror  [post]
func (h *Handler) BulkMirrorDatasheets(w http.ResponseWriter, r *http.Request) {
	cands, err := h.Datasheets.MirrorCandidates(r.Context(), 1000)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not list what needs downloading")
		return
	}
	if len(cands) == 0 {
		// Not an error: every part with a URL already has its copy.
		respond.JSON(w, http.StatusOK, map[string]any{"task_id": nil, "targets": 0})
		return
	}
	targets := make([]MirrorTarget, 0, len(cands))
	for _, c := range cands {
		targets = append(targets, MirrorTarget{
			ManufacturerPartID: c.ManufacturerPartID, PartID: c.PartID, MPN: c.MPN, URL: c.DatasheetURL,
		})
	}

	uid := middleware.UserID(r.Context())
	taskID := uuid.New()
	args := DatasheetMirrorArgs{TaskID: taskID, Targets: targets}
	summary, _ := json.Marshal(args)
	if err := h.Jobs.Enqueue(r.Context(), taskID, args, jobs.EnqueueMeta{
		Type: "datasheet_mirror", Queue: jobs.QueueIngest, MaxAttempts: 3,
		CreatedBy: &uid, ArgsSummary: summary, ProgressTotal: len(targets),
	}); err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not start the download")
		return
	}
	respond.JSON(w, http.StatusAccepted, map[string]any{"task_id": taskID, "targets": len(targets)})
}
