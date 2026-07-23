// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package kicad

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"path"
	"strings"
)

// IsZip reports whether data starts with the ZIP local-file magic ("PK\x03\x04").
func IsZip(data []byte) bool {
	return len(data) >= 4 && data[0] == 'P' && data[1] == 'K' && data[2] == 0x03 && data[3] == 0x04
}

// ParseZip reads a zipped KiCad project. It merges the components from every
// schematic (`.kicad_sch`) in the archive — so a hierarchical design's sub-sheet
// files are all included — then groups them into one BOM. If the zip has no
// schematics it falls back to the first BOM CSV it finds.
//
// Note: a sheet instantiated more than once is counted once (its file appears
// once in the project). Designs that reuse a sub-sheet will undercount those
// parts; single-instance hierarchies (the common case) are exact.
func ParseZip(data []byte) ([]BOMLine, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}

	var comps []Component
	var csvData []byte
	schematics := 0

	for _, f := range zr.File {
		name := f.Name
		if f.FileInfo().IsDir() || skipZipEntry(name) {
			continue
		}
		lower := strings.ToLower(name)
		switch {
		case strings.HasSuffix(lower, ".kicad_sch"):
			b, err := readZipFile(f)
			if err != nil {
				return nil, err
			}
			c, err := componentsFromSchematic(b)
			if err != nil {
				// A malformed sub-sheet shouldn't sink the whole upload.
				continue
			}
			comps = append(comps, c...)
			schematics++
		case csvData == nil && (strings.HasSuffix(lower, ".csv") || strings.HasSuffix(lower, ".tsv")):
			if b, err := readZipFile(f); err == nil {
				csvData = b
			}
		}
	}

	if schematics > 0 {
		return GroupComponents(comps), nil
	}
	if csvData != nil {
		return ParseBOMCSV(csvData)
	}
	return nil, errors.New("no .kicad_sch or BOM .csv found in the zip")
}

// skipZipEntry drops files we never want to parse: macOS metadata, KiCad backup
// copies, and anything inside a backups directory.
func skipZipEntry(name string) bool {
	lower := strings.ToLower(name)
	base := path.Base(lower)
	if strings.HasPrefix(base, "._") || strings.Contains(lower, "__macosx/") {
		return true
	}
	if strings.Contains(lower, "-backups/") || strings.Contains(lower, "/backups/") {
		return true
	}
	if strings.HasSuffix(lower, "-bak") || strings.HasSuffix(lower, ".bak") {
		return true
	}
	return false
}

func readZipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	// Cap per-file reads so a zip bomb can't exhaust memory.
	return io.ReadAll(io.LimitReader(rc, 32<<20))
}
