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

// ErrorBody is the wire shape for every error response.
type ErrorBody struct {
	Error string `json:"error"`
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

// Error writes an error envelope with the given status code and message.
func Error(w http.ResponseWriter, status int, msg string) {
	JSON(w, status, ErrorBody{Error: msg})
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
