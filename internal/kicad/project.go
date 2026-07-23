// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package kicad

import (
	"archive/zip"
	"bytes"
	"path"
	"strings"
)

// RootPCB returns the root project's .kicad_pcb bytes from a zip (the board
// matching the shallowest .kicad_pro), or nil if there isn't one.
func RootPCB(data []byte) []byte {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil
	}
	files := map[string]*zip.File{}
	var proKeys []string
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || skipZipEntry(f.Name) {
			continue
		}
		key := path.Clean(f.Name)
		files[key] = f
		if strings.HasSuffix(strings.ToLower(key), ".kicad_pro") && !strings.HasPrefix(path.Base(key), ".") {
			proKeys = append(proKeys, key)
		}
	}
	best, bestDepth := "", 1<<30
	for _, pk := range proKeys {
		base := path.Base(pk)
		stem := base[:len(base)-len(".kicad_pro")]
		cand := path.Clean(path.Join(path.Dir(pk), stem+".kicad_pcb"))
		if _, ok := files[cand]; !ok {
			continue
		}
		if d := strings.Count(cand, "/"); d < bestDepth {
			bestDepth, best = d, cand
		}
	}
	if best == "" {
		return nil
	}
	b, err := readZipFile(files[best])
	if err != nil {
		return nil
	}
	return b
}

// SchematicRevision reads the title-block revision from a .kicad_sch:
// `(title_block (rev "A") …)`. Returns "" if absent.
func SchematicRevision(data []byte) string {
	root, err := parseSexpr(data)
	if err != nil {
		return ""
	}
	for _, child := range root.Children {
		if child.head() != "title_block" {
			continue
		}
		for _, c := range child.Children {
			if c.head() == "rev" {
				return strings.TrimSpace(c.atom(1))
			}
		}
	}
	return ""
}

// ProjectInfo returns a zipped project's board name (the root project) and its
// title-block revision, by locating the root schematic the same way ParseZip
// does (the .kicad_sch matching the shallowest .kicad_pro). Empty strings when
// there's no identifiable project.
func ProjectInfo(data []byte) (name, revision string) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", ""
	}
	files := map[string]*zip.File{}
	var proKeys []string
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || skipZipEntry(f.Name) {
			continue
		}
		key := path.Clean(f.Name)
		files[key] = f
		if strings.HasSuffix(strings.ToLower(key), ".kicad_pro") && !strings.HasPrefix(path.Base(key), ".") {
			proKeys = append(proKeys, key)
		}
	}
	bestDepth := 1 << 30
	for _, pk := range proKeys {
		base := path.Base(pk)
		stem := base[:len(base)-len(".kicad_pro")]
		cand := path.Clean(path.Join(path.Dir(pk), stem+".kicad_sch"))
		f, ok := files[cand]
		if !ok {
			continue
		}
		if d := strings.Count(cand, "/"); d < bestDepth {
			bestDepth = d
			name = stem
			revision = ""
			if b, err := readZipFile(f); err == nil {
				revision = SchematicRevision(b)
			}
		}
	}
	return name, revision
}
