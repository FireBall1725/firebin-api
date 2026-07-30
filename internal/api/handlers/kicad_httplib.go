// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/firelabsca/firebin-api/internal/api/middleware"
	"github.com/firelabsca/firebin-api/internal/api/respond"
	"github.com/firelabsca/firebin-api/internal/auth"
	"github.com/firelabsca/firebin-api/internal/repository"
)

// Settings keys for the KiCad library server. Dotted, matching the enrichment
// provider keys already in instance_settings.
const (
	kicadLibEnabledKey = "kicad_library.enabled"
	kicadLibRootURLKey = "kicad_library.root_url"
)

// KicadLibRoutePrefix is where the KiCad-facing routes are mounted, and the path
// that has to be appended to the site URL to form root_url.
//
// It cannot live under /api/v1: KiCad inserts its own version segment, so the
// full path is <prefix>/v1/..., and /api/v1/kicad is already taken by the
// unrelated library-index endpoints. It must stay under /api, because that is
// the prefix firebin-web's nginx proxies to this service; anything else would be
// answered by the SPA fallback with a 200 and an HTML body, which KiCad would
// try to parse as JSON.
const KicadLibRoutePrefix = "/api/kicad-lib"

// kicadUnmappedMarker prefixes the name of a part with no symbol mapping. KiCad
// renders a flagged placeholder for those anyway; the marker makes the reason
// legible in the chooser instead of looking like a broken library.
const kicadUnmappedMarker = "(no symbol) "

// kicadSnapshotTTL is how often the catalogue is rebuilt. Stock is the field most
// likely to be stale, and being a few minutes behind on a count is not worth
// rebuilding on every request.
const kicadSnapshotTTL = 5 * time.Minute

// KicadLibraryEnabled reports whether the feature is switched on. Read per
// request by the KiCad middleware so the toggle takes effect immediately.
func (h *Handler) KicadLibraryEnabled(ctx context.Context) bool {
	v, _ := h.Settings.Get(ctx, kicadLibEnabledKey)
	return v == "true"
}

type kicadLibrarySettings struct {
	Enabled bool `json:"enabled"`
	// RootURL is what goes in a generated .kicad_httplib. Stored rather than
	// derived: the machine running KiCad may reach FireBin by a different name
	// than the browser did, and behind a reverse proxy this service cannot see
	// its own external scheme (nginx overwrites X-Forwarded-Proto with the plain
	// scheme it listens on).
	RootURL string `json:"root_url"`
	// RoutePath is advisory, so the UI can suggest "<origin>" + this rather than
	// hardcoding the prefix in TypeScript and silently producing broken files if
	// it ever moves.
	RoutePath string `json:"route_path"`
}

// GetKicadLibrarySettings returns the toggle and the configured root URL.
// @Summary     Get KiCad library server settings
// @Description Whether the KiCad HTTP library is enabled, and the root URL written into generated .kicad_httplib files.
// @Tags        settings
// @Security    BearerAuth
// @Produce     json
// @Success     200  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Failure     403  {object}  map[string]interface{}
// @Router      /settings/kicad-library  [get]
func (h *Handler) GetKicadLibrarySettings(w http.ResponseWriter, r *http.Request) {
	rootURL, _ := h.Settings.Get(r.Context(), kicadLibRootURLKey)
	respond.JSON(w, http.StatusOK, kicadLibrarySettings{
		Enabled:   h.KicadLibraryEnabled(r.Context()),
		RootURL:   rootURL,
		RoutePath: KicadLibRoutePrefix,
	})
}

type updateKicadLibraryRequest struct {
	Enabled *bool   `json:"enabled"`
	RootURL *string `json:"root_url"`
}

// UpdateKicadLibrarySettings saves the toggle and root URL.
// @Summary     Update KiCad library server settings
// @Description Enable or disable the KiCad HTTP library and set the root URL used in generated config files.
// @Tags        settings
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       request  body      map[string]interface{}  true  "request body"
// @Success     200      {object}  map[string]interface{}
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Failure     403      {object}  map[string]interface{}
// @Router      /settings/kicad-library  [put]
func (h *Handler) UpdateKicadLibrarySettings(w http.ResponseWriter, r *http.Request) {
	var req updateKicadLibraryRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	ctx := r.Context()

	if req.RootURL != nil {
		clean, err := normalizeKicadRootURL(*req.RootURL)
		if err != nil {
			respond.Error(w, http.StatusBadRequest, err.Error())
			return
		}
		_ = h.Settings.Set(ctx, kicadLibRootURLKey, clean)
	}

	if req.Enabled != nil {
		v := "true"
		if !*req.Enabled {
			v = "false"
		}
		_ = h.Settings.Set(ctx, kicadLibEnabledKey, v)
	}

	h.GetKicadLibrarySettings(w, r)
}

// normalizeKicadRootURL validates and tidies a root URL.
//
// The trailing slash matters more than it looks: KiCad builds every request as
// root_url + "/" + api_version + "/" + resource, so a trailing slash produces a
// double slash and, depending on the proxy in front, a 404 that presents as the
// whole library failing to load.
func normalizeKicadRootURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil // clearing it is allowed; the UI then re-suggests one
	}
	s = strings.TrimRight(s, "/")

	u, err := url.Parse(s)
	if err != nil {
		return "", errors.New("that is not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("the URL must start with http:// or https://")
	}
	if u.Host == "" {
		return "", errors.New("the URL needs a host, for example https://firebin.example/api/kicad-lib")
	}
	return s, nil
}

// ListKicadLibraryTokens lists the per-workstation tokens.
// @Summary     List KiCad library tokens
// @Description List the per-workstation tokens issued for the KiCad HTTP library. Secrets are never returned.
// @Tags        settings
// @Security    BearerAuth
// @Produce     json
// @Success     200  {array}   map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Failure     403  {object}  map[string]interface{}
// @Router      /settings/kicad-library/tokens  [get]
func (h *Handler) ListKicadLibraryTokens(w http.ResponseWriter, r *http.Request) {
	out, err := h.KicadHTTPTokens.List(r.Context())
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not list tokens")
		return
	}
	respond.JSON(w, http.StatusOK, out)
}

type createKicadTokenRequest struct {
	Name string `json:"name"`
}

// CreateKicadLibraryToken mints a token for one workstation.
// @Summary     Create a KiCad library token
// @Description Issue a token for one KiCad workstation and return it with a ready-made .kicad_httplib body. The secret is shown once.
// @Tags        settings
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       request  body      map[string]interface{}  true  "request body"
// @Success     201      {object}  map[string]interface{}
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Failure     403      {object}  map[string]interface{}
// @Router      /settings/kicad-library/tokens  [post]
func (h *Handler) CreateKicadLibraryToken(w http.ResponseWriter, r *http.Request) {
	var req createKicadTokenRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		respond.Error(w, http.StatusBadRequest, "name the device this is for, so it can be revoked on its own later")
		return
	}
	if len(name) > 64 {
		respond.Error(w, http.StatusBadRequest, "that name is too long (64 characters max)")
		return
	}

	raw, hash, suffix, err := auth.GenerateKicadToken()
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not generate a token")
		return
	}

	var createdBy *uuid.UUID
	if uid := middleware.UserID(r.Context()); uid != uuid.Nil {
		createdBy = &uid
	}

	rec, err := h.KicadHTTPTokens.Create(r.Context(), name, hash, suffix, createdBy)
	if err != nil {
		if errors.Is(err, repository.ErrConflict) {
			respond.Error(w, http.StatusConflict, "that token already exists; try again")
			return
		}
		respond.Error(w, http.StatusInternalServerError, "could not create the token")
		return
	}

	rootURL, _ := h.Settings.Get(r.Context(), kicadLibRootURLKey)

	respond.JSON(w, http.StatusCreated, map[string]any{
		// Shown once and never recoverable: only the hash is stored.
		"token":      raw,
		"meta":       rec,
		"route_path": KicadLibRoutePrefix,
		// The finished file as text, for the client to write byte for byte.
		//
		// Not a JSON object. Handing over structure and letting the browser
		// re-serialise it loses meta.version: it decodes to a JavaScript number
		// and comes back out as 1 rather than 1.0, because JavaScript cannot tell
		// the two apart. Rendering here is the only way the bytes KiCad receives
		// are the bytes this code intended.
		"config_file": kicadHTTPLibFile(rootURL, raw),
	})
}

// kicadHTTPLibConfig builds the .kicad_httplib body.
//
// Assembled here rather than in the browser so the parts fixed by KiCad's
// contract have exactly one home. Note meta.version is a JSON number: the
// everything-must-be-a-string rule applies to the library responses, not to this
// file, and quoting it here makes KiCad reject the library.
//
// timeout_categories_seconds is 240 despite its name gating the
// parts-per-category cache rather than the category list, because nginx caps the
// request at proxy_read_timeout 300s and a larger number here would be a promise
// nothing can keep.
// Written as a template rather than marshalled from a map for two reasons:
// meta.version has to read 1.0 and Go renders float64(1.0) as "1", and key order
// is preserved, which makes the file legible to whoever opens it next.
//
// Both string values interpolated here are constrained: root_url is validated on
// save and the token is generated base64url, so neither can carry a quote or a
// backslash that would break the JSON.
func kicadHTTPLibFile(rootURL, token string) string {
	return `{
  "meta": {
    "version": 1.0
  },
  "name": "FireBin",
  "description": "Parts in FireBin inventory",
  "source": {
    "type": "REST_API",
    "api_version": "v1",
    "root_url": ` + strconv.Quote(rootURL) + `,
    "token": ` + strconv.Quote(token) + `,
    "timeout_parts_seconds": 60,
    "timeout_categories_seconds": 240
  }
}
`
}

// RevokeKicadLibraryToken cuts off one workstation.
// @Summary     Revoke a KiCad library token
// @Description Revoke one workstation's token. Other workstations are unaffected.
// @Tags        settings
// @Security    BearerAuth
// @Produce     json
// @Param       id   path      string  true  "Token id"
// @Success     200  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Failure     403  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /settings/kicad-library/tokens/{id}  [delete]
func (h *Handler) RevokeKicadLibraryToken(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	err := h.KicadHTTPTokens.Revoke(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "token not found")
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not revoke the token")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}
