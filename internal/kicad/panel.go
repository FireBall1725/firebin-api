// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package kicad

import (
	"archive/zip"
	"bytes"
	"path"
	"strings"
)

// Panel is a panelized copy of the design found in a project zip: the same board
// arrayed Copies-up (for fabrication), with no schematic of its own.
type Panel struct {
	Name   string
	Copies int
}

// DetectPanels finds panel PCBs in a project zip and estimates how many copies
// of the board each holds. A panel is a `.kicad_pcb` that isn't the project's
// main board (typically under a panel/ folder). Copies are estimated from the
// footprint-count ratio against the main board's PCB, which is exact except for
// the panel frame's few extra footprints (fiducials, mouse-bites), so it rounds
// cleanly. Returns nil when there's no main PCB to compare against.
func DetectPanels(data []byte) ([]Panel, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}

	files := map[string]*zip.File{}
	var proKeys, pcbKeys []string
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || skipZipEntry(f.Name) {
			continue
		}
		key := path.Clean(f.Name)
		files[key] = f
		lower := strings.ToLower(path.Base(key))
		if strings.HasSuffix(lower, ".kicad_pro") && !strings.HasPrefix(lower, ".") {
			proKeys = append(proKeys, key)
		}
		if strings.HasSuffix(lower, ".kicad_pcb") {
			pcbKeys = append(pcbKeys, key)
		}
	}

	// The main PCB is the one matching the shallowest project (.kicad_pro).
	mainPCB := ""
	for _, pk := range proKeys {
		base := path.Base(pk)
		cand := path.Clean(path.Join(path.Dir(pk), base[:len(base)-len(".kicad_pro")]+".kicad_pcb"))
		if _, ok := files[cand]; !ok {
			continue
		}
		if mainPCB == "" || strings.Count(cand, "/") < strings.Count(mainPCB, "/") {
			mainPCB = cand
		}
	}
	if mainPCB == "" {
		return nil, nil
	}
	mainCount := footprintCount(files[mainPCB])
	if mainCount == 0 {
		return nil, nil
	}

	var panels []Panel
	for _, pk := range pcbKeys {
		if pk == mainPCB {
			continue
		}
		b, err := readZipFile(files[pk])
		if err != nil {
			continue
		}
		s := string(b)
		total := strings.Count(s, "(footprint ")

		// KiKit is the reliable signal. It stamps its frame parts with refs
		// prefixed "KiKit_" (fiducials KiKit_FID_*, tooling KiKit_TO_*, …), and
		// keeps each board copy's original refdes. Excluding those frame parts,
		// the remaining footprints are exactly copies * (main board footprints),
		// so the copy count is exact — no rounding fudge.
		kikitFrame := strings.Count(s, `(property "Reference" "KiKit`)
		isKiKit := kikitFrame > 0 || strings.Contains(strings.ToLower(s), "kikit")
		boardFP := total - kikitFrame
		if boardFP < 0 {
			boardFP = total
		}
		copies := (boardFP + mainCount/2) / mainCount // exact for KiKit, rounded otherwise

		// A second PCB is a panel when it's KiKit-panelized or clearly holds more
		// than one copy of the design — never mislabel a distinct 1-up board.
		if !isKiKit && copies < 2 {
			continue
		}
		if copies < 1 {
			copies = 1
		}
		name := strings.TrimSuffix(path.Base(pk), path.Ext(path.Base(pk)))
		panels = append(panels, Panel{Name: name, Copies: copies})
	}
	return panels, nil
}

// footprintCount counts `(footprint …)` entries in a .kicad_pcb — one per placed
// component. A plain substring count is robust for the KiCad PCB format.
func footprintCount(f *zip.File) int {
	if f == nil {
		return 0
	}
	b, err := readZipFile(f)
	if err != nil {
		return 0
	}
	return strings.Count(string(b), "(footprint ")
}
