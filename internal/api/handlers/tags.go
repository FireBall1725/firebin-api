// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/firelabsca/firebin-api/internal/api/respond"
	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/firelabsca/firebin-api/internal/repository"
	"github.com/firelabsca/firebin-api/internal/tags"
	"github.com/google/uuid"
)

type tagRequest struct {
	Name        *string `json:"name"`
	Colour      *string `json:"colour"`
	Description *string `json:"description"`
}

type tagMergeRequest struct {
	Into uuid.UUID `json:"into"`
}

type partTagsRequest struct {
	Tags []string `json:"tags"`
}

// attachTags fills in Tags on a slice of parts in one round trip.
//
// Called on the list endpoints as well as on a single read. The command palette
// filters client-side over GET /parts, so a tag that only appeared on
// GET /parts/{id} would be invisible to the one search users reach for most.
//
// A failure here is deliberately not fatal: tags are an aid to finding a part,
// and returning no parts at all because their labels could not be loaded is the
// worse outcome. The caller's parts go out untagged.
func (h *Handler) attachTags(ctx context.Context, parts []models.Part) {
	if len(parts) == 0 {
		return
	}
	ids := make([]uuid.UUID, len(parts))
	for i := range parts {
		ids[i] = parts[i].ID
	}
	byPart, err := h.Tags.TagsForParts(ctx, ids)
	if err != nil {
		return
	}
	for i := range parts {
		parts[i].Tags = byPart[parts[i].ID]
	}
}

// attachTagsToMatches is attachTags for the parametric search's result shape,
// which embeds Part rather than being one.
func (h *Handler) attachTagsToMatches(ctx context.Context, matches []models.PartMatch) {
	if len(matches) == 0 {
		return
	}
	ids := make([]uuid.UUID, len(matches))
	for i := range matches {
		ids[i] = matches[i].ID
	}
	byPart, err := h.Tags.TagsForParts(ctx, ids)
	if err != nil {
		return
	}
	for i := range matches {
		matches[i].Tags = byPart[matches[i].ID]
	}
}

// @Summary     List tags
// @Description Return every tag with the number of parts carrying it.
// @Tags        tags
// @Security    BearerAuth
// @Produce     json
// @Success     200  {array}   models.Tag
// @Failure     401  {object}  map[string]interface{}
// @Router      /tags [get]
func (h *Handler) ListTags(w http.ResponseWriter, r *http.Request) {
	tags, err := h.Tags.List(r.Context())
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not list tags")
		return
	}
	respond.JSON(w, http.StatusOK, tags)
}

// @Summary     Create tag
// @Description Create a tag. A name that folds onto an existing tag is a conflict, not a second spelling.
// @Tags        tags
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     201      {object}  models.Tag
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Failure     409      {object}  map[string]interface{}
// @Router      /tags [post]
func (h *Handler) CreateTag(w http.ResponseWriter, r *http.Request) {
	var req tagRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	if req.Name == nil || strings.TrimSpace(*req.Name) == "" {
		respond.Error(w, http.StatusBadRequest, "name is required")
		return
	}
	if !tagColourOK(w, req.Colour) {
		return
	}
	t, err := h.Tags.Create(r.Context(), *req.Name, req.Colour, req.Description)
	switch {
	case errors.Is(err, repository.ErrInvalid):
		respond.Error(w, http.StatusBadRequest, "that name has no letters or digits in it")
		return
	case errors.Is(err, repository.ErrConflict):
		respond.Error(w, http.StatusConflict, "a tag with that name already exists")
		return
	case err != nil:
		respond.Error(w, http.StatusInternalServerError, "could not create tag")
		return
	}
	h.Bus.Publish("tags")
	respond.JSON(w, http.StatusCreated, t)
}

// @Summary     Update tag
// @Description Rename or recolour a tag. A rename onto another tag's name is a conflict; use merge instead.
// @Tags        tags
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       id       path      string                  true   "identifier"
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     200      {object}  models.Tag
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Failure     404      {object}  map[string]interface{}
// @Failure     409      {object}  map[string]interface{}
// @Router      /tags/{id} [patch]
func (h *Handler) UpdateTag(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	var req tagRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	if !tagColourOK(w, req.Colour) {
		return
	}
	t, err := h.Tags.Update(r.Context(), id, req.Name, req.Colour, req.Description)
	switch {
	case errors.Is(err, repository.ErrInvalid):
		respond.Error(w, http.StatusBadRequest, "that name has no letters or digits in it")
		return
	case errors.Is(err, repository.ErrConflict):
		respond.Error(w, http.StatusConflict, "another tag already has that name; merge them instead")
		return
	case errors.Is(err, repository.ErrNotFound):
		respond.Error(w, http.StatusNotFound, "tag not found")
		return
	case err != nil:
		respond.Error(w, http.StatusInternalServerError, "could not update tag")
		return
	}
	h.Bus.Publish("tags")
	respond.JSON(w, http.StatusOK, t)
}

// @Summary     Merge tags
// @Description Move every part carrying this tag onto another, then delete this one.
// @Tags        tags
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       id       path      string                  true   "identifier"
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     200      {object}  map[string]interface{}
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Failure     404      {object}  map[string]interface{}
// @Router      /tags/{id}/merge [post]
func (h *Handler) MergeTag(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	var req tagMergeRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	err := h.Tags.Merge(r.Context(), id, req.Into)
	switch {
	case errors.Is(err, repository.ErrInvalid):
		respond.Error(w, http.StatusBadRequest, "a tag cannot be merged into itself")
		return
	case errors.Is(err, repository.ErrNotFound):
		respond.Error(w, http.StatusNotFound, "tag not found")
		return
	case err != nil:
		respond.Error(w, http.StatusInternalServerError, "could not merge tags")
		return
	}
	h.Bus.Publish("tags")
	h.Bus.Publish("parts")
	respond.JSON(w, http.StatusOK, map[string]string{"status": "merged"})
}

// @Summary     Delete tag
// @Description Delete a tag and remove it from every part.
// @Tags        tags
// @Security    BearerAuth
// @Produce     json
// @Param       id   path      string                  true  "identifier"
// @Success     200  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /tags/{id} [delete]
func (h *Handler) DeleteTag(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	err := h.Tags.Delete(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "tag not found")
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not delete tag")
		return
	}
	h.Bus.Publish("tags")
	h.Bus.Publish("parts")
	respond.JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// SetPartTags replaces a part's tags with the names given, creating any that do
// not exist yet.
//
// Its own endpoint rather than a field on the part PATCH. That request struct is
// mirrored in three codebases (this one, the MCP server, the web client) and is
// decoded with DisallowUnknownFields, so a field added to one and missed in
// another silently writes its zero value; the header comment on UpdatePart
// records that happening twice already. Tags cannot join that arrangement.
// @Summary     Set part tags
// @Description Replace a part's tags with exactly these names, creating any that are new.
// @Tags        parts
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       id       path      string                  true   "identifier"
// @Param       request  body      map[string]interface{}  true   "request body"
// @Success     200      {array}   models.Tag
// @Failure     400      {object}  map[string]interface{}
// @Failure     401      {object}  map[string]interface{}
// @Failure     404      {object}  map[string]interface{}
// @Router      /parts/{id}/tags [put]
func (h *Handler) SetPartTags(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	var req partTagsRequest
	if !respond.Decode(w, r, &req) {
		return
	}
	if _, err := h.Parts.Get(r.Context(), id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			respond.Error(w, http.StatusNotFound, "part not found")
			return
		}
		respond.Error(w, http.StatusInternalServerError, "could not load part")
		return
	}
	tags, err := h.Tags.SetPartTags(r.Context(), id, req.Tags)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not set tags")
		return
	}
	h.Bus.Publish("tags")
	h.Bus.Publish("parts")
	respond.JSON(w, http.StatusOK, tags)
}

// SuggestPartTags offers the well-known nicknames for a part it recognises.
//
// This is the half of the feature that answers the original complaint. A tag you
// have to know to type does not help you find the connector whose name you
// cannot remember; being told "this is the Qwiic one" does. It suggests only,
// never writes — the part comes back with the same tags it went in with.
// @Summary     Suggest tags for a part
// @Description Well-known nicknames for a recognised part, minus any it already carries. Suggestions only; nothing is applied.
// @Tags        parts
// @Security    BearerAuth
// @Produce     json
// @Param       id   path      string                  true  "identifier"
// @Success     200  {array}   tags.Suggestion
// @Failure     401  {object}  map[string]interface{}
// @Failure     404  {object}  map[string]interface{}
// @Router      /parts/{id}/tag-suggestions [get]
func (h *Handler) SuggestPartTags(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	p, err := h.Parts.Get(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "part not found")
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not load part")
		return
	}

	in := tags.Part{
		Name:        p.Name,
		Description: deref(p.Description),
		Package:     deref(p.Package),
	}
	// MPNs are the reliable signal, so it is worth the extra read: the part's
	// own name is whatever someone typed, and "4 pin connector" recognises
	// nothing.
	if mps, err := h.Catalog.ListManufacturerParts(r.Context(), id); err == nil {
		for _, mp := range mps {
			in.MPNs = append(in.MPNs, mp.MPN)
		}
	}
	if have, err := h.Tags.TagsForPart(r.Context(), id); err == nil {
		for _, t := range have {
			in.Existing = append(in.Existing, t.Name)
		}
	}

	respond.JSON(w, http.StatusOK, tags.Suggest(in))
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// tagColourOK rejects a colour that is not a palette slot, so a hex value
// entered by hand fails loudly here rather than rendering as nothing in half
// the themes. An empty string is allowed and means "back to the default chip".
func tagColourOK(w http.ResponseWriter, colour *string) bool {
	if colour == nil || *colour == "" || repository.ValidTagColour(*colour) {
		return true
	}
	respond.Error(w, http.StatusBadRequest,
		"colour must be one of: "+strings.Join(repository.TagColours, ", "))
	return false
}
