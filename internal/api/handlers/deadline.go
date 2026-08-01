// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"log/slog"
	"net/http"
	"time"
)

// clearWriteDeadline removes the server-wide write deadline for one request.
//
// http.Server.WriteTimeout is set once when a request begins and is never
// extended, so any handler that legitimately takes longer than it has its
// connection cut before it can answer. The client sees the socket close with no
// status at all, which is worse than a timeout: there is nothing to report and
// nothing in the response to explain it.
//
// Used only where the wait is expected and bounded by something else: an
// assistant turn is several provider calls, bounded by the provider's own
// timeout and by the request context, which still carries the client's
// cancellation.
//
// Failure is logged rather than returned. The handler can still do its work;
// it may just get cut off, which is exactly what happened before this existed.
func clearWriteDeadline(w http.ResponseWriter) {
	if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
		slog.Warn("could not clear the write deadline; a long request may be cut off", "error", err)
	}
}
