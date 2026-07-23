// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package respond centralizes JSON response and error writing so every handler
// returns the same envelope shape.
package respond

import (
	"encoding/json"
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

// Decode reads and validates a JSON request body into dst. It rejects unknown
// fields and bodies larger than 1 MiB. Returns false and writes a 400 on error.
func Decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return false
	}
	return true
}
