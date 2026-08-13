// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
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
		extract := []uuid.UUID{}

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
			extract = append(extract, d.ID)
			stored++
		})

		// One extraction job for the whole batch rather than one per file: a
		// backfill of hundreds would otherwise flood the queue with tiny jobs.
		w.h.enqueueExtraction(rctx, extract...)

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

// DatasheetExtractArgs is the payload for a text-extraction run. ArgsVersion
// lets the shape evolve without breaking jobs already queued.
type DatasheetExtractArgs struct {
	TaskID       uuid.UUID   `json:"task_id"`
	ArgsVersion  int         `json:"args_version"`
	DatasheetIDs []uuid.UUID `json:"datasheet_ids"`
}

func (DatasheetExtractArgs) Kind() string { return "datasheet_extract" }

// datasheetExtractWorker pulls the text layer out of stored PDFs and writes the
// per-page sidecar the assistant reads.
//
// On the default queue rather than ingest: extraction is CPU work on a local
// file with no network involved, so it should not sit behind a queue sized for
// slow vendor downloads.
type datasheetExtractWorker struct {
	river.WorkerDefaults[DatasheetExtractArgs]
	h    *Handler
	deps *jobs.Deps
}

// Timeout is generous because a thousand-page reference manual takes real time
// to parse, but finite so a pathological file cannot wedge the queue.
func (w *datasheetExtractWorker) Timeout(*river.Job[DatasheetExtractArgs]) time.Duration {
	return 30 * time.Minute
}

func (w *datasheetExtractWorker) Work(ctx context.Context, job *river.Job[DatasheetExtractArgs]) error {
	a := job.Args
	return w.deps.Run(ctx, a.TaskID, job.Attempt, job.MaxAttempts, func(r *jobs.Run) error {
		rctx := r.Context()
		read, scans, failed := 0, 0, 0

		err := jobs.ForEach(r, a.DatasheetIDs, func(_ int, id uuid.UUID) {
			d, err := w.h.Datasheets.Get(rctx, id)
			if err != nil || d == nil {
				r.Log("warn", "datasheet %s not found, skipped", id)
				failed++
				return
			}
			content, err := w.h.DatasheetFiles.Read(d.SHA256)
			if err != nil {
				r.Log("warn", "%s: file missing from storage", d.Filename)
				_ = w.h.Datasheets.SetExtraction(rctx, id, nil, nil, models.TextFailed)
				failed++
				return
			}

			res, err := datasheets.ExtractPages(content)
			// A parse error still leaves whatever pages were read, and the sidecar
			// is written either way so a partial document is still searchable.
			if werr := w.h.DatasheetFiles.WriteSidecar(d.SHA256, res.Pages); werr != nil {
				r.Log("warn", "%s: could not write extracted text: %v", d.Filename, werr)
			}

			// The title the document declares about itself, when it declares a
			// usable one and nobody has named it. Only ever fills a blank: a
			// title someone typed outranks metadata a converter wrote, and this
			// runs unattended across the whole library.
			if res.Title != "" && (d.Title == nil || *d.Title == "") &&
				!strings.EqualFold(res.Title, d.Filename) {
				if err := w.h.Datasheets.SetTitleIfUnset(rctx, id, res.Title); err != nil {
					r.Log("warn", "%s: could not record the title: %v", d.Filename, err)
				} else {
					r.Log("info", "%s: titled %q from the PDF", d.Filename, res.Title)
				}
			}

			pages := res.PageCount
			var pagesPtr *int
			if pages > 0 {
				pagesPtr = &pages
			}
			var lang *string
			if res.Language != "" {
				l := res.Language
				lang = &l
			}

			switch {
			case err != nil && !res.HasText:
				_ = w.h.Datasheets.SetExtraction(rctx, id, pagesPtr, lang, models.TextFailed)
				r.Log("warn", "%s: %v", d.Filename, err)
				failed++
			case !res.HasText:
				// A scan. Normal for a mechanical drawing, and not a failure: it
				// says plainly that the assistant cannot read this one.
				_ = w.h.Datasheets.SetExtraction(rctx, id, pagesPtr, lang, models.TextNoTextLayer)
				r.Log("info", "%s: %d pages, no text layer (a scan)", d.Filename, pages)
				scans++
			default:
				_ = w.h.Datasheets.SetExtraction(rctx, id, pagesPtr, lang, models.TextOK)
				r.Log("info", "%s: %d pages read%s", d.Filename, pages, langNote(res.Language))
				read++
			}
		})

		r.SetResult(map[string]int{"read": read, "scans": scans, "failed": failed})
		w.h.Bus.Publish("datasheets")
		if err != nil {
			return err // cancelled part-way
		}
		r.Log("info", "done: %d read, %d scans, %d failed", read, scans, failed)
		return nil
	})
}

func langNote(lang string) string {
	if lang == "" || lang == "en" {
		return ""
	}
	return " (" + lang + ")"
}

// enqueueExtraction queues text extraction for freshly stored datasheets, when
// the instance has not turned it off.
//
// Errors are swallowed: the file is stored and linked, which is the part the
// user asked for. Extraction is a cache that can always be rebuilt later.
func (h *Handler) enqueueExtraction(ctx context.Context, ids ...uuid.UUID) {
	if len(ids) == 0 {
		return
	}
	if v, _ := h.Settings.Get(ctx, "datasheets.extract_text"); v == "false" {
		return
	}
	taskID := uuid.New()
	args := DatasheetExtractArgs{TaskID: taskID, DatasheetIDs: ids}
	summary, _ := json.Marshal(args)
	_ = h.Jobs.Enqueue(ctx, taskID, args, jobs.EnqueueMeta{
		Type: "datasheet_extract", Queue: jobs.QueueDefault, MaxAttempts: 2,
		ArgsSummary: summary, ProgressTotal: len(ids),
	})
}

// ExtractDatasheetText re-runs text extraction.
//
// Exists because the sidecar is a rebuildable cache: deleting it to reclaim disk,
// or upgrading the parser, both need a way to ask for the text again. With no id
// it sweeps everything still pending.
// @Summary     Extract datasheet text
// @Description Re-read the text layer of one datasheet, or of every datasheet still pending.
// @Tags        datasheets
// @Security    BearerAuth
// @Produce     json
// @Param       id   path      string  false  "Datasheet id; omit to sweep everything pending"
// @Success     202  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Router      /datasheets/{id}/extract  [post]
func (h *Handler) ExtractDatasheetText(w http.ResponseWriter, r *http.Request) {
	var ids []uuid.UUID
	if raw := r.PathValue("id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			respond.Error(w, http.StatusBadRequest, "invalid id")
			return
		}
		ids = []uuid.UUID{id}
	} else {
		pending, err := h.Datasheets.PendingExtraction(r.Context(), 500)
		if err != nil {
			respond.Error(w, http.StatusInternalServerError, "could not list pending datasheets")
			return
		}
		for _, d := range pending {
			ids = append(ids, d.ID)
		}
	}
	if len(ids) == 0 {
		respond.JSON(w, http.StatusOK, map[string]any{"task_id": nil, "datasheets": 0})
		return
	}

	uid := middleware.UserID(r.Context())
	taskID := uuid.New()
	args := DatasheetExtractArgs{TaskID: taskID, DatasheetIDs: ids}
	summary, _ := json.Marshal(args)
	if err := h.Jobs.Enqueue(r.Context(), taskID, args, jobs.EnqueueMeta{
		Type: "datasheet_extract", Queue: jobs.QueueDefault, MaxAttempts: 2,
		CreatedBy: &uid, ArgsSummary: summary, ProgressTotal: len(ids),
	}); err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not start extraction")
		return
	}
	respond.JSON(w, http.StatusAccepted, map[string]any{"task_id": taskID, "datasheets": len(ids)})
}

// fetchDatasheet downloads a URL and verifies it is really a PDF.
//
// The content check is not paranoia. A rotted distributor link regularly answers
// 200 with an HTML "product not found" page, and storing that would produce a
// library full of documents that open to nothing. Failing here instead leaves
// the vendor URL in place and the part honestly marked as un-mirrored.
func (h *Handler) fetchDatasheet(ctx context.Context, rawURL string) ([]byte, error) {
	url, err := normalizeDatasheetURL(rawURL)
	if err != nil {
		return nil, err
	}
	maxBytes := h.maxDatasheetBytes(ctx)

	// One retry, for transient failures only. Manufacturer CDNs drop
	// connections and reset HTTP/2 streams often enough that a single blip
	// should not permanently mark a part as un-mirrorable, but a 403 or a 404
	// means the same thing however many times you ask.
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(2 * time.Second):
			}
		}
		content, err := h.fetchDatasheetOnce(ctx, url, maxBytes)
		if err == nil {
			return content, nil
		}
		lastErr = err
		if !isRetriableFetch(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

// fetchDatasheetOnce is a single attempt.
func (h *Handler) fetchDatasheetOnce(ctx context.Context, url string, maxBytes int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// An honest User-Agent, on purpose. Some manufacturer CDNs refuse anything
	// that is not a browser, and the answer to that is to report the refusal
	// clearly (see the 403 case below) rather than to claim to be Chrome. The
	// vendor link still works in a browser, and the part keeps it.
	req.Header.Set("User-Agent", "FireBin/1.0 (+https://github.com/FireBall1725/firebin)")
	req.Header.Set("Accept", "application/pdf,*/*")

	client := &http.Client{Timeout: datasheetHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		// Nothing to do; carry on to reading the body below. (Not Go's
		// `fallthrough`, which would run the 403 case next.)
	case http.StatusForbidden, http.StatusUnauthorized:
		// Worth its own message: nothing is broken and retrying will not help.
		// The vendor is refusing automated downloads, and the datasheet link on
		// the part still opens fine in a browser.
		return nil, fmt.Errorf("the vendor refused an automated download (%d); the datasheet link still works in a browser, so open it and upload the PDF if you want a copy", resp.StatusCode)
	case http.StatusNotFound, http.StatusGone:
		return nil, fmt.Errorf("the datasheet URL is dead (%d)", resp.StatusCode)
	default:
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

// normalizeDatasheetURL accepts the URL shapes providers actually return.
//
// A protocol-relative URL ("//host/path") is the common one: it is legal in a
// page where the scheme is inherited, and several distributors store their
// datasheet links that way. Rejecting it lost real datasheets for no reason.
func normalizeDatasheetURL(raw string) (string, error) {
	u := strings.TrimSpace(raw)
	if u == "" {
		return "", fmt.Errorf("no datasheet URL")
	}
	if strings.HasPrefix(u, "//") {
		u = "https:" + u
	}
	parsed, err := neturl.Parse(u)
	if err != nil {
		return "", fmt.Errorf("could not read the datasheet URL: %v", err)
	}
	switch parsed.Scheme {
	case "http", "https":
	case "":
		return "", fmt.Errorf("the datasheet URL has no scheme, so it cannot be fetched: %q", raw)
	default:
		// ftp:// still shows up on older manufacturer sites. Say which scheme
		// rather than a bare "unsupported", so the cause is obvious.
		return "", fmt.Errorf("cannot download over %q; only http and https are supported", parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("the datasheet URL has no host: %q", raw)
	}
	return parsed.String(), nil
}

// isRetriableFetch reports whether an error is worth one more attempt.
//
// Transport-level failures only. Every status-code case above is already a
// settled answer, and retrying a 403 just asks a CDN to refuse twice.
func isRetriableFetch(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var urlErr *neturl.Error
	if errors.As(err, &urlErr) {
		return true
	}
	// http2 stream resets surface as a plain error string with no typed form to
	// match on, and Molex served exactly this: "stream error: stream ID 1;
	// INTERNAL_ERROR; received from peer".
	s := err.Error()
	for _, frag := range []string{"stream error", "connection reset", "unexpected EOF", "EOF", "broken pipe", "no such host", "i/o timeout"} {
		if strings.Contains(s, frag) {
			return true
		}
	}
	return false
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
