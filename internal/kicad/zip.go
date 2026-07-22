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

// ParseZip reads a zipped KiCad project into one BOM. It finds the project's
// root schematic (the `.kicad_sch` matching the `.kicad_pro`), then follows the
// sheet hierarchy — only sub-sheets the design actually references are included,
// with correct multiplicity for reused sheets. Orphan `.kicad_sch` files in the
// project folder (reusable sub-circuits not instantiated in the design) are
// ignored, so they don't double-count. Falls back to a single schematic, then a
// BOM CSV, when there's no project file.
func ParseZip(data []byte) ([]BOMLine, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}

	files := map[string]*zip.File{}
	var proKeys, schKeys []string
	var csvData []byte

	for _, f := range zr.File {
		if f.FileInfo().IsDir() || skipZipEntry(f.Name) {
			continue
		}
		key := path.Clean(f.Name)
		files[key] = f
		base := path.Base(key)
		lower := strings.ToLower(base)

		if strings.HasSuffix(lower, ".kicad_pro") && !strings.HasPrefix(base, ".") {
			proKeys = append(proKeys, key)
		}
		if strings.HasSuffix(lower, ".kicad_sch") {
			schKeys = append(schKeys, key)
		}
		if csvData == nil && (strings.HasSuffix(lower, ".csv") || strings.HasSuffix(lower, ".tsv")) {
			if b, err := readZipFile(f); err == nil {
				csvData = b
			}
		}
	}

	// Pick the root schematic. A project can hold several .kicad_pro (e.g. a
	// panel/ sub-project with a PCB but no schematic), so choose the one whose
	// sibling <name>.kicad_sch actually exists, preferring the shallowest path
	// (the main project over a nested panel).
	rootKey := ""
	for _, pk := range proKeys {
		base := path.Base(pk)
		cand := path.Clean(path.Join(path.Dir(pk), base[:len(base)-len(".kicad_pro")]+".kicad_sch"))
		if _, ok := files[cand]; !ok {
			continue
		}
		if rootKey == "" || strings.Count(cand, "/") < strings.Count(rootKey, "/") {
			rootKey = cand
		}
	}
	if rootKey == "" && len(schKeys) == 1 {
		rootKey = schKeys[0]
	}

	if rootKey != "" {
		comps := collectSheet(rootKey, files, map[string]bool{})
		return GroupComponents(comps), nil
	}
	// No identifiable root: prefer a CSV BOM if present, else merge every
	// schematic as a best effort (may double-count in odd project layouts).
	if csvData != nil {
		return ParseBOMCSV(csvData)
	}
	if len(schKeys) > 0 {
		var comps []Component
		for _, k := range schKeys {
			if b, err := readZipFile(files[k]); err == nil {
				if c, err := componentsFromSchematic(b); err == nil {
					comps = append(comps, c...)
				}
			}
		}
		return GroupComponents(comps), nil
	}
	return nil, errors.New("no .kicad_sch or BOM .csv found in the zip")
}

// collectSheet parses one schematic and recurses into the sub-sheets it
// references (resolved relative to its own directory in the zip), accumulating
// components. `stack` guards against reference cycles.
func collectSheet(key string, files map[string]*zip.File, stack map[string]bool) []Component {
	if stack[key] {
		return nil
	}
	f := files[key]
	if f == nil {
		return nil
	}
	data, err := readZipFile(f)
	if err != nil {
		return nil
	}
	root, err := parseSexpr(data)
	if err != nil {
		return nil
	}
	stack[key] = true
	defer delete(stack, key)

	dir := path.Dir(key)
	var comps []Component
	for _, child := range root.Children {
		switch child.head() {
		case "symbol":
			if c, ok := componentFromSymbol(child); ok {
				comps = append(comps, c)
			}
		case "sheet":
			sf := sheetProperty(child, "Sheetfile")
			if sf == "" {
				continue
			}
			childKey := path.Clean(path.Join(dir, sf))
			comps = append(comps, collectSheet(childKey, files, stack)...)
		}
	}
	return comps
}

// sheetProperty reads a named property ("Sheetfile", "Sheetname") off a (sheet …)
// node.
func sheetProperty(sheet *node, name string) string {
	for _, ch := range sheet.Children {
		if ch.head() == "property" && strings.EqualFold(ch.atom(1), name) {
			return ch.atom(2)
		}
	}
	return ""
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
