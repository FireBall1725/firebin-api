// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package datasheets stores datasheet PDFs on the filesystem, content-addressed
// by SHA-256, plus the per-page text sidecar the assistant reads.
//
// Its own package rather than filesystem calls inside the handlers, for the same
// reason labels and picklist are their own packages: two callers need it (the
// upload handler and the mirror job) and neither should own it. It also keeps
// the one security-relevant rule in a single place — an uploaded filename never
// contributes to a path.
//
// Bytes are on disk rather than in Postgres because a datasheet is megabytes and
// the JSON backup path base64s every BYTEA column into one document under a
// 256 MiB cap. See migration 000033 for the full argument.
package datasheets

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// pdfMagic is the first five bytes of every PDF. Checked on the way in because
// the file extension is attacker-controlled and the mirror job is fetching
// whatever a distributor URL happens to return, which is regularly an HTML error
// page served with a 200.
var pdfMagic = []byte("%PDF-")

// ErrNotPDF is returned when content does not begin with the PDF magic bytes.
var ErrNotPDF = errors.New("not a PDF")

// ErrNotFound is returned when no file exists for a hash.
var ErrNotFound = errors.New("datasheet file not found")

// Store is a content-addressed file store rooted at a single directory.
type Store struct {
	root string
}

// New returns a Store rooted at ATTACHMENT_STORAGE_PATH/datasheets. The
// directory is created lazily on first write rather than here, so a deployment
// that never uploads a datasheet does not get an empty directory it did not ask
// for, and a read-only mount does not fail startup.
func New(attachmentRoot string) *Store {
	return &Store{root: filepath.Join(attachmentRoot, "datasheets")}
}

// Root reports the directory the store writes to. Useful in diagnostics and for
// the settings page to show where files are going.
func (s *Store) Root() string { return s.root }

// pathFor builds the on-disk path for a hash. The two-character shard keeps any
// one directory from holding every file, which matters on filesystems that
// degrade with very large directories.
//
// The hash is validated rather than trusted: it comes back from the database,
// and a hex-only check means this can never walk out of the root even if a row
// were tampered with.
func (s *Store) pathFor(sha string) (string, error) {
	if len(sha) != 64 {
		return "", fmt.Errorf("bad datasheet hash length %d", len(sha))
	}
	for i := 0; i < len(sha); i++ {
		c := sha[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", errors.New("bad datasheet hash")
		}
	}
	return filepath.Join(s.root, sha[0:2], sha+".pdf"), nil
}

// sidecarFor is the derived per-page text beside the PDF. Same name, different
// extension, so a stray sidecar is obvious and deleting one is safe.
func (s *Store) sidecarFor(sha string) (string, error) {
	p, err := s.pathFor(sha)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(p, ".pdf") + ".pages.json", nil
}

// Put writes content and returns its hex SHA-256.
//
// Writing an identical file twice is a no-op that returns the same hash, which
// is what makes one PDF shared by a whole family of MPNs cost one copy on disk.
// The write goes to a temporary file and is renamed into place so a crash midway
// cannot leave a truncated file sitting at a hash that claims to be complete.
func (s *Store) Put(content []byte) (string, error) {
	if !IsPDF(content) {
		return "", ErrNotPDF
	}
	sum := sha256.Sum256(content)
	sha := hex.EncodeToString(sum[:])

	dst, err := s.pathFor(sha)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(dst); err == nil {
		return sha, nil // already have it, byte for byte
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".tmp-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once renamed
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return "", err
	}
	return sha, nil
}

// Open returns the file and its size for streaming. The caller closes it.
//
// Returns a file rather than bytes so the handler can hand it to
// http.ServeContent, which needs an io.ReadSeeker to answer the Range requests
// browsers' built-in PDF viewers make when paging through a large document.
func (s *Store) Open(sha string) (*os.File, int64, error) {
	p, err := s.pathFor(sha)
	if err != nil {
		return nil, 0, err
	}
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, err
	}
	return f, st.Size(), nil
}

// Read returns the whole file. For text extraction, which needs the bytes.
func (s *Store) Read(sha string) ([]byte, error) {
	f, _, err := s.Open(sha)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(f)
}

// Delete removes the PDF and its sidecar.
//
// Safe to call whenever the datasheets row goes away: sha256 is UNIQUE on that
// table, so a file is referenced by exactly one row. Sharing happens at the
// part-link level (many parts, one datasheet), not at the file level.
func (s *Store) Delete(sha string) error {
	p, err := s.pathFor(sha)
	if err != nil {
		return err
	}
	side, err := s.sidecarFor(sha)
	if err != nil {
		return err
	}
	_ = os.Remove(side) // derived data; absence is not an error
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Pages is the extracted text of one document, one entry per page.
type Pages struct {
	Pages []string `json:"pages"`
}

// WriteSidecar stores extracted per-page text beside the PDF.
func (s *Store) WriteSidecar(sha string, pages []string) error {
	p, err := s.sidecarFor(sha)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	body, err := json.Marshal(Pages{Pages: pages})
	if err != nil {
		return err
	}
	return os.WriteFile(p, body, 0o644)
}

// ReadSidecar returns extracted per-page text, or ErrNotFound if extraction has
// not run. Callers treat that as "not readable yet", never as a failure: the
// sidecar is a regenerable cache and deleting it is a supported way to reclaim
// disk.
func (s *Store) ReadSidecar(sha string) ([]string, error) {
	p, err := s.sidecarFor(sha)
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var out Pages
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if out.Pages == nil {
		out.Pages = []string{}
	}
	return out.Pages, nil
}

// IsPDF reports whether content starts with the PDF magic bytes.
func IsPDF(content []byte) bool {
	if len(content) < len(pdfMagic) {
		return false
	}
	for i := range pdfMagic {
		if content[i] != pdfMagic[i] {
			return false
		}
	}
	return true
}
