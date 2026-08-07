// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Events streams change signals to the caller over Server-Sent Events. Every
// mutation elsewhere publishes to the broker; open views subscribe here and
// refetch on a matching resource. A heartbeat comment keeps proxies from
// idling the connection out.
// @Summary     Event stream
// @Description Stream change signals to the caller over Server-Sent Events.
// @Tags        system
// @Security    BearerAuth
// @Produce     text/event-stream
// @Success     200  {object}  map[string]interface{}
// @Failure     401  {object}  map[string]interface{}
// @Router      /events  [get]
func (h *Handler) Events(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Disable proxy buffering (nginx / Traefik) so events flush immediately.
	w.Header().Set("X-Accel-Buffering", "no")

	rc := http.NewResponseController(w)
	ch, unsubscribe := h.Bus.Subscribe()
	defer unsubscribe()

	// Open the stream so the client's onopen fires promptly. A write error
	// here surfaces through the Flush below, which is the one that decides
	// whether this connection can stream at all.
	_, _ = fmt.Fprint(w, ": connected\n\n")
	if err := rc.Flush(); err != nil {
		return // ResponseWriter can't flush — cannot stream
	}

	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			payload, _ := json.Marshal(ev)
			if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		}
	}
}
