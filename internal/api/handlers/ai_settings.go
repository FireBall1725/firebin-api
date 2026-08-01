// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"net/http"

	"github.com/firelabsca/firebin-api/internal/ai"
	"github.com/firelabsca/firebin-api/internal/api/respond"
)

// aiSettingsResponse is the whole AI settings section in one request, so the
// page renders in one round trip instead of one per provider.
type aiSettingsResponse struct {
	Enabled   bool                `json:"enabled"`
	Active    string              `json:"active_provider"`
	Providers []ai.ProviderStatus `json:"providers"`
}

// GetAISettings returns the assistant's configuration with every secret masked.
// @Summary     Get AI settings
// @Description Get the assistant's enabled state, the active provider, and every provider's configuration with secrets masked.
// @Tags        settings
// @Security    BearerAuth
// @Produce     json
// @Success     200  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Failure     403  {object}  map[string]interface{}
// @Router      /settings/ai  [get]
func (h *Handler) GetAISettings(w http.ResponseWriter, r *http.Request) {
	if h.AI == nil {
		respond.Error(w, http.StatusServiceUnavailable, "the assistant is not available on this instance")
		return
	}
	enabled, err := h.AI.FeatureEnabled(r.Context())
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not read AI settings")
		return
	}
	providers, err := h.AI.Status(r.Context())
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not read AI settings")
		return
	}
	respond.JSON(w, http.StatusOK, aiSettingsResponse{
		Enabled:   enabled,
		Active:    h.AI.Registry().ActiveName(),
		Providers: providers,
	})
}

type aiSettingsRequest struct {
	// Pointers so an absent field means "leave it alone". Sending false and
	// sending nothing are different instructions, and a settings page that
	// saves one section must not switch the feature off by omission.
	Enabled  *bool              `json:"enabled"`
	Active   *string            `json:"active_provider"`
	Provider *string            `json:"provider"`
	Config   *map[string]string `json:"config"`
}

// UpdateAISettings saves any combination of the enable toggle, the active
// provider, and one provider's config.
// @Summary     Update AI settings
// @Description Update the enable toggle, the active provider, or one provider's configuration. Omitted fields are left unchanged. A secret submitted as the mask value is ignored rather than saved.
// @Tags        settings
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       request  body      map[string]interface{}  true  "Settings to change"
// @Success     200  {object}  map[string]interface{}
// @Failure     400  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Failure     403  {object}  map[string]interface{}
// @Router      /settings/ai  [put]
func (h *Handler) UpdateAISettings(w http.ResponseWriter, r *http.Request) {
	if h.AI == nil {
		respond.Error(w, http.StatusServiceUnavailable, "the assistant is not available on this instance")
		return
	}
	var req aiSettingsRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	if (req.Provider == nil) != (req.Config == nil) {
		respond.Error(w, http.StatusBadRequest, "provider and config must be sent together")
		return
	}

	ctx := r.Context()
	if req.Provider != nil {
		if err := h.AI.Configure(ctx, *req.Provider, *req.Config); err != nil {
			respond.Error(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if req.Active != nil {
		if err := h.AI.SetActive(ctx, *req.Active); err != nil {
			respond.Error(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if req.Enabled != nil {
		if err := h.AI.SetFeatureEnabled(ctx, *req.Enabled); err != nil {
			respond.Error(w, http.StatusInternalServerError, "could not save AI settings")
			return
		}
	}
	h.GetAISettings(w, r)
}

// TestAIProvider makes one probe call to a provider.
//
// Returns HTTP 200 even when the provider rejects the call, with the failure in
// the body. A 502 here would be the API reporting its own health for someone
// else's credentials, and the settings page needs the provider's own wording to
// be any use ("invalid x-api-key" is actionable; "bad gateway" is not).
// @Summary     Test an AI provider
// @Description Send one probe request to a provider to check its credentials, model, and whether the model calls tools. Returns 200 with ok=false when the provider rejects the call.
// @Tags        settings
// @Security    BearerAuth
// @Produce     json
// @Param       name  path      string  true  "Provider name"
// @Success     200  {object}  map[string]interface{}
// @Failure     400  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Failure     403  {object}  map[string]interface{}
// @Router      /settings/ai/{name}/test  [post]
func (h *Handler) TestAIProvider(w http.ResponseWriter, r *http.Request) {
	if h.AI == nil {
		respond.Error(w, http.StatusServiceUnavailable, "the assistant is not available on this instance")
		return
	}
	res, err := h.AI.Test(r.Context(), r.PathValue("name"))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	respond.JSON(w, http.StatusOK, res)
}

// ListAIModels asks a provider which models its host has installed.
//
// An unreachable host is not an error either: the settings page degrades to a
// free-text field, which is exactly what it does for a provider that cannot
// enumerate at all. Failing the request would leave a page that cannot save.
// @Summary     List a provider's models
// @Description Ask a provider which models its host has available. Returns an empty list when the provider cannot enumerate or its host is unreachable.
// @Tags        settings
// @Security    BearerAuth
// @Produce     json
// @Param       name  path      string  true  "Provider name"
// @Success     200  {object}  map[string]interface{}
// @Failure     400  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Failure     403  {object}  map[string]interface{}
// @Router      /settings/ai/{name}/models  [get]
func (h *Handler) ListAIModels(w http.ResponseWriter, r *http.Request) {
	if h.AI == nil {
		respond.Error(w, http.StatusServiceUnavailable, "the assistant is not available on this instance")
		return
	}
	models, err := h.AI.ListModels(r.Context(), r.PathValue("name"))
	if err != nil {
		respond.JSON(w, http.StatusOK, map[string]any{"models": []string{}, "error": err.Error()})
		return
	}
	if models == nil {
		models = []string{}
	}
	respond.JSON(w, http.StatusOK, map[string]any{"models": models})
}
