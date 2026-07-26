// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package respond centralizes JSON response and error writing so every handler
// returns the same envelope shape.
package respond

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
)

// ErrorBody is the wire shape for every error response. Error is a human-readable
// English message (a sensible default the client can show as-is). Code is an
// optional stable identifier the client can map to a localized message; when
// absent the client falls back to Error.
type ErrorBody struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

// JSON writes v as JSON with the given status code.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encoding JSON response", "error", err)
	}
}

// Error writes an error envelope with the given status code and message (no
// stable code — the client shows the message as-is).
func Error(w http.ResponseWriter, status int, msg string) {
	JSON(w, status, ErrorBody{Error: msg})
}

// ErrorCode writes an error envelope with a stable code the client can localize,
// plus an English fallback message. Prefer this for new user-facing errors so
// they're translatable; code is a dotted identifier, e.g. "board.parse_failed".
func ErrorCode(w http.ResponseWriter, status int, code, msg string) {
	JSON(w, status, ErrorBody{Error: msg, Code: code})
}

// DefaultMaxBody is the request-body cap for JSON endpoints: enough for any
// normal payload, small enough to be a sane guard against oversized bodies.
const DefaultMaxBody = 1 << 20 // 1 MiB

// Decode reads and validates a JSON request body into dst. It rejects unknown
// fields and bodies larger than DefaultMaxBody. Returns false and writes a 400
// on error.
func Decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	return DecodeMax(w, r, dst, DefaultMaxBody)
}

// DecodeMax is Decode with an explicit maximum body size. Use it for the few
// endpoints that legitimately accept large payloads, such as a full-instance
// import, rather than raising the default for every endpoint.
func DecodeMax(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return false
	}
	return true
}

// DecodeAllowEmpty is like Decode but treats an empty body as success, leaving
// dst zero-valued. Use for endpoints where the body is optional.
func DecodeAllowEmpty(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return true // empty body — leave dst zero-valued
		}
		Error(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return false
	}
	return true
}
