// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package ai

import (
	"context"
	"encoding/json"
	"sync"
)

// Recording one provider call, for the assistant's debug log.
//
// Captured at the HTTP layer rather than from the neutral ChatRequest, because
// the neutral shape is exactly what does NOT explain most failures. The bug that
// prompted this was Ollama truncating a prompt because `num_ctx` was missing —
// a provider-specific option that never appears in a ChatRequest. What is on the
// wire is the thing worth keeping.
//
// Carried on the context so every provider gets it without a signature change
// and without a global, and so a call with no recorder costs one nil check.

type recorderKey struct{}

// RoundRecord is the request and response of a single provider call.
//
// Request and Response hold the raw JSON as sent and received. They can be
// large — a tool-calling round re-sends the whole conversation — which is why
// the rows they end up in are pruned.
type RoundRecord struct {
	mu sync.Mutex

	URL      string
	Request  json.RawMessage
	Response json.RawMessage
	Status   int
	// Err is the transport or decode failure, when the call did not return a
	// usable body. A provider-level error inside a 200 lands in Response.
	Err string
}

// NewRoundRecord returns a recorder ready to be attached to a context.
func NewRoundRecord() *RoundRecord { return &RoundRecord{} }

// WithRecorder attaches a recorder so the next provider call fills it in.
func WithRecorder(ctx context.Context, r *RoundRecord) context.Context {
	if r == nil {
		return ctx
	}
	return context.WithValue(ctx, recorderKey{}, r)
}

// recorderFrom returns the recorder on a context, or nil when nothing is
// listening. Nil is the normal case and must stay cheap.
func recorderFrom(ctx context.Context) *RoundRecord {
	r, _ := ctx.Value(recorderKey{}).(*RoundRecord)
	return r
}

// request records what was sent.
func (r *RoundRecord) request(url string, body []byte) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.URL = url
	r.Request = append(json.RawMessage(nil), body...)
}

// response records what came back.
//
// Stored whatever the status, because a non-2xx body is usually the most
// informative thing in the whole exchange.
func (r *RoundRecord) response(status int, body []byte) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Status = status
	r.Response = append(json.RawMessage(nil), body...)
}

// fail records a call that produced no usable body at all.
func (r *RoundRecord) fail(err error) {
	if r == nil || err == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Err = err.Error()
}

// appendResponse accumulates a streamed response, which arrives as frames
// rather than one body.
func (r *RoundRecord) appendResponse(line string) {
	if r == nil || line == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.Response) > 0 {
		r.Response = append(r.Response, '\n')
	}
	r.Response = append(r.Response, line...)
}

// Snapshot returns a copy safe to read after the call, since a streamed
// response is still being written while the caller reads.
func (r *RoundRecord) Snapshot() (url string, req, resp json.RawMessage, status int, errText string) {
	if r == nil {
		return "", nil, nil, 0, ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.URL,
		append(json.RawMessage(nil), r.Request...),
		append(json.RawMessage(nil), r.Response...),
		r.Status, r.Err
}
