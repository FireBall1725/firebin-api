// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// StreamingChatProvider is a provider that can emit an answer as it is written.
//
// Optional on purpose. A provider that does not implement it still works; the
// caller falls back to waiting for the whole reply, which is what every
// provider did before. That keeps adding a provider from becoming a two-part
// job where the second part is easy to forget.
type StreamingChatProvider interface {
	ChatProvider
	// ChatStream calls onText with each fragment of the answer as it arrives
	// and returns the same ChatResponse Chat would have.
	//
	// Tool calls are not streamed to the caller. Their arguments arrive in
	// fragments and a tool cannot be run on half a JSON object, so they are
	// accumulated and returned whole.
	ChatStream(ctx context.Context, req ChatRequest, onText func(string)) (*ChatResponse, error)
}

// streamLines reads a streamed response body a line at a time, calling onLine
// for each. Used for both wire formats: server-sent events and newline-
// delimited JSON differ only in how each line is framed.
func streamLines(ctx context.Context, url string, body any, setHeaders func(http.Header), onLine func(string) error) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if setHeaders != nil {
		setHeaders(req.Header)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxProviderBody))
		return fmt.Errorf("%s", providerError(resp.StatusCode, raw))
	}

	sc := bufio.NewScanner(resp.Body)
	// A single event can carry a whole tool-call argument object, which is far
	// larger than the default 64 KiB line budget.
	sc.Buffer(make([]byte, 0, 64*1024), maxProviderBody)
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := onLine(sc.Text()); err != nil {
			return err
		}
	}
	return sc.Err()
}

// sseData strips the "data: " prefix from a server-sent event line, reporting
// whether the line carried a payload at all. Blank lines and "event:" lines
// carry none.
func sseData(line string) (string, bool) {
	if !strings.HasPrefix(line, "data:") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(line, "data:")), true
}
