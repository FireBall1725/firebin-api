// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package kicad

import (
	"archive/zip"
	"bytes"
	"path"
	"strings"
)

// Asset is a renderable file pulled from a project zip.
type Asset struct {
	Name    string // display name (the file's base name)
	Kind    string // "ibom" | "image"
	Mime    string
	Content []byte
}

const (
	maxAssetBytes = 16 << 20 // 16 MB per asset
	maxAssets     = 40       // cap how many we keep
)

var imageMimes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".svg":  "image/svg+xml",
	".bmp":  "image/bmp",
}

// ExtractAssets pulls the renderable files out of a project zip: the interactive
// BOM (iBOM HTML) and image renders. STEP/PCB/Gerber files are skipped — they're
// not viewable in a browser without a dedicated viewer. Backups and macOS
// metadata are ignored. iBOM files are returned first.
func ExtractAssets(data []byte) ([]Asset, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	var ibom, images []Asset
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || skipZipEntry(f.Name) {
			continue
		}
		if f.UncompressedSize64 > maxAssetBytes {
			continue
		}
		key := path.Clean(f.Name)
		base := path.Base(key)
		lower := strings.ToLower(base)
		ext := strings.ToLower(path.Ext(lower))

		switch {
		case ext == ".html" || ext == ".htm":
			b, err := readZipFile(f)
			if err != nil || !LooksLikeIBOM(b) {
				continue
			}
			ibom = append(ibom, Asset{Name: base, Kind: "ibom", Mime: "text/html; charset=utf-8", Content: b})
		case imageMimes[ext] != "":
			b, err := readZipFile(f)
			if err != nil {
				continue
			}
			images = append(images, Asset{Name: base, Kind: "image", Mime: imageMimes[ext], Content: b})
		}
	}

	out := append(ibom, images...)
	if len(out) > maxAssets {
		out = out[:maxAssets]
	}
	return out, nil
}

// LooksLikeIBOM recognizes an Interactive HTML BOM by its generator signatures.
// It scans the whole file (not just the head): a real iBOM inlines a large CSS
// block first, so the "pcbdata"/"ibom" markers can sit hundreds of KB in, and
// the title is "Interactive BOM for KiCAD".
func LooksLikeIBOM(b []byte) bool {
	s := strings.ToLower(string(b))
	return strings.Contains(s, "pcbdata") ||
		strings.Contains(s, "interactive bom") ||
		strings.Contains(s, "interactive html bom") ||
		strings.Contains(s, "var config") && strings.Contains(s, "ibom")
}
