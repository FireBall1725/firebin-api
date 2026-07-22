// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package kicad

import (
	"archive/zip"
	"bytes"
	"testing"
)

// A hierarchical project split across two sheets, plus a backup that must be
// ignored, proves the zip parser merges sheets and skips backups.
func TestParseZip(t *testing.T) {
	rootSheet := `(kicad_sch (version 20231120)
      (symbol (lib_id "Device:R") (in_bom yes) (on_board yes) (dnp no)
        (property "Reference" "R1" (at 0 0 0))
        (property "Value" "10k" (at 0 0 0))
        (property "Footprint" "Resistor_SMD:R_0603_1608Metric" (at 0 0 0))))`
	subSheet := `(kicad_sch (version 20231120)
      (symbol (lib_id "Device:C") (in_bom yes) (on_board yes) (dnp no)
        (property "Reference" "C1" (at 0 0 0))
        (property "Value" "100nF" (at 0 0 0))
        (property "Footprint" "Capacitor_SMD:C_0402_1005Metric" (at 0 0 0))
        (property "MPN" "CL05B104KO5NNNC" (at 0 0 0))))`
	junk := `(kicad_sch garbage that should be skipped in backups)`

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name, body string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	write("widget/widget.kicad_sch", rootSheet)
	write("widget/power.kicad_sch", subSheet)
	write("widget/widget-backups/widget.kicad_sch", junk)
	write("widget/widget.kicad_pcb", "(kicad_pcb)")
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	lines, err := ParseZip(buf.Bytes())
	if err != nil {
		t.Fatalf("parse zip: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("want 2 merged BOM lines, got %d: %+v", len(lines), lines)
	}
	// Both sheets' parts must appear.
	var haveR, haveC bool
	for _, l := range lines {
		if l.Value == "10k" {
			haveR = true
		}
		if l.Value == "100nF" && l.MPN == "CL05B104KO5NNNC" {
			haveC = true
		}
	}
	if !haveR || !haveC {
		t.Errorf("merged BOM missing a sheet: haveR=%v haveC=%v", haveR, haveC)
	}
}
