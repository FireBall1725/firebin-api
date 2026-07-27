// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package respond_test

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/firelabsca/firebin-api/internal/api/respond"
)

type payload struct {
	Blob string `json:"blob"`
}

// bodyOfSize builds a JSON object whose serialized form is at least n bytes.
func bodyOfSize(n int) string {
	return fmt.Sprintf(`{"blob":%q}`, strings.Repeat("x", n))
}

func TestDecode_RejectsOverDefault(t *testing.T) {
	body := bodyOfSize(2 << 20) // ~2 MiB, over the 1 MiB default
	r := httptest.NewRequest("POST", "/", strings.NewReader(body))
	w := httptest.NewRecorder()

	var dst payload
	if respond.Decode(w, r, &dst) {
		t.Fatal("Decode accepted a >1 MiB body; expected rejection")
	}
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestDecodeMax_AcceptsLargeBody(t *testing.T) {
	const size = 4 << 20 // 4 MiB, like a real export
	body := bodyOfSize(size)
	r := httptest.NewRequest("POST", "/", strings.NewReader(body))
	w := httptest.NewRecorder()

	var dst payload
	if !respond.DecodeMax(w, r, &dst, 256<<20) {
		t.Fatalf("DecodeMax rejected a %d-byte body under a 256 MiB cap; recorder: %d %s", len(body), w.Code, w.Body.String())
	}
	if len(dst.Blob) != size {
		t.Fatalf("decoded blob length = %d, want %d", len(dst.Blob), size)
	}
}
