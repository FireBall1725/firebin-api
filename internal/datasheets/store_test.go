// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package datasheets

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pdf builds a minimal byte slice that passes the magic-byte check, with a
// distinguishing tail so two calls produce different hashes.
func fakePDF(tail string) []byte { return []byte("%PDF-1.7\n" + tail) }

func TestPutIsContentAddressedAndDeduplicates(t *testing.T) {
	s := New(t.TempDir())

	a, err := s.Put(fakePDF("esp32-c6"))
	if err != nil {
		t.Fatalf("first Put: %v", err)
	}
	b, err := s.Put(fakePDF("esp32-c6"))
	if err != nil {
		t.Fatalf("second Put: %v", err)
	}
	if a != b {
		t.Fatalf("same content gave different hashes: %s vs %s", a, b)
	}

	// The dedup has to be real on disk, not just an equal return value: this is
	// what makes one family datasheet shared by many MPNs cost one copy.
	var files int
	_ = filepath.Walk(s.Root(), func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			files++
		}
		return nil
	})
	if files != 1 {
		t.Fatalf("want 1 file on disk after storing the same PDF twice, got %d", files)
	}

	c, err := s.Put(fakePDF("xvf3800"))
	if err != nil {
		t.Fatalf("third Put: %v", err)
	}
	if c == a {
		t.Fatal("different content produced the same hash")
	}
}

func TestPutRejectsNonPDF(t *testing.T) {
	s := New(t.TempDir())
	// The mirror job fetches whatever a distributor URL returns, and an HTML
	// error page served with a 200 is the common case this guards.
	if _, err := s.Put([]byte("<!doctype html><title>404</title>")); !errors.Is(err, ErrNotPDF) {
		t.Fatalf("want ErrNotPDF for an HTML body, got %v", err)
	}
	if _, err := s.Put(nil); !errors.Is(err, ErrNotPDF) {
		t.Fatalf("want ErrNotPDF for empty content, got %v", err)
	}
}

// A tampered or malformed hash must never build a path outside the root. The
// uploaded filename never reaches pathFor at all, so this is the only input that
// could traverse.
func TestPathForRejectsAnythingButHex(t *testing.T) {
	s := New(t.TempDir())
	valid := strings.Repeat("a", 64)
	for _, bad := range []string{
		"",
		"abc",
		strings.Repeat("a", 63),
		strings.Repeat("a", 65),
		"../../../../etc/passwd" + strings.Repeat("a", 42),
		strings.Repeat("A", 64), // uppercase is not what hex.EncodeToString emits
		strings.Repeat("g", 64),
	} {
		if _, err := s.pathFor(bad); err == nil {
			t.Fatalf("pathFor(%q) should have failed", bad)
		}
	}
	got, err := s.pathFor(valid)
	if err != nil {
		t.Fatalf("pathFor on a valid hash: %v", err)
	}
	if !strings.HasPrefix(got, s.Root()) {
		t.Fatalf("path %q escaped root %q", got, s.Root())
	}
}

func TestOpenAndReadRoundTrip(t *testing.T) {
	s := New(t.TempDir())
	want := fakePDF("round trip")
	sha, err := s.Put(want)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	f, size, err := s.Open(sha)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_ = f.Close()
	if size != int64(len(want)) {
		t.Fatalf("size %d, want %d", size, len(want))
	}

	got, err := s.Read(sha)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(got) != string(want) {
		t.Fatal("content did not round trip")
	}

	if _, _, err := s.Open(strings.Repeat("b", 64)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound for an absent hash, got %v", err)
	}
}

func TestSidecarRoundTripAndAbsence(t *testing.T) {
	s := New(t.TempDir())
	sha, err := s.Put(fakePDF("with text"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Absent extraction is "not readable yet", not a failure.
	if _, err := s.ReadSidecar(sha); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound before extraction, got %v", err)
	}

	pages := []string{"page one text", "page two text"}
	if err := s.WriteSidecar(sha, pages); err != nil {
		t.Fatalf("WriteSidecar: %v", err)
	}
	got, err := s.ReadSidecar(sha)
	if err != nil {
		t.Fatalf("ReadSidecar: %v", err)
	}
	if len(got) != 2 || got[0] != pages[0] || got[1] != pages[1] {
		t.Fatalf("sidecar did not round trip: %#v", got)
	}
}

func TestDeleteRemovesBothFiles(t *testing.T) {
	s := New(t.TempDir())
	sha, err := s.Put(fakePDF("delete me"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.WriteSidecar(sha, []string{"text"}); err != nil {
		t.Fatalf("WriteSidecar: %v", err)
	}
	if err := s.Delete(sha); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := s.Open(sha); !errors.Is(err, ErrNotFound) {
		t.Fatalf("PDF survived delete: %v", err)
	}
	if _, err := s.ReadSidecar(sha); !errors.Is(err, ErrNotFound) {
		t.Fatalf("sidecar survived delete: %v", err)
	}
	// Deleting twice is not an error; the mirror job can retry a cleanup.
	if err := s.Delete(sha); err != nil {
		t.Fatalf("second Delete should be a no-op, got %v", err)
	}
}
