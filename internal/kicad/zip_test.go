// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package kicad

import (
	"archive/zip"
	"bytes"
	"testing"
)

// A project with a root schematic that references one sub-sheet, plus an orphan
// schematic (a reusable sub-circuit not instantiated in the design) and a
// backup — both of which must be excluded so parts don't double-count.
func TestParseZipRootAndOrphan(t *testing.T) {
	root := `(kicad_sch (version 20231120)
      (symbol (lib_id "Device:R") (in_bom yes) (on_board yes) (dnp no)
        (property "Reference" "R1" (at 0 0 0))
        (property "Value" "10k" (at 0 0 0))
        (property "Footprint" "Resistor_SMD:R_0603_1608Metric" (at 0 0 0)))
      (sheet (at 50 50)
        (property "Sheetname" "power" (at 0 0 0))
        (property "Sheetfile" "power.kicad_sch" (at 0 0 0))))`
	sub := `(kicad_sch (version 20231120)
      (symbol (lib_id "Device:C") (in_bom yes) (on_board yes) (dnp no)
        (property "Reference" "C1" (at 0 0 0))
        (property "Value" "100nF" (at 0 0 0))
        (property "Footprint" "Capacitor_SMD:C_0402_1005Metric" (at 0 0 0))))`
	// Orphan sub-circuit sitting in the project folder but not referenced.
	orphan := `(kicad_sch (version 20231120)
      (symbol (lib_id "Device:C") (in_bom yes) (on_board yes) (dnp no)
        (property "Reference" "C1" (at 0 0 0))
        (property "Value" "100nF" (at 0 0 0))
        (property "Footprint" "Capacitor_SMD:C_0402_1005Metric" (at 0 0 0))))`

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
	write("widget/widget.kicad_pro", `{"meta":{}}`)
	write("widget/widget.kicad_sch", root)
	write("widget/power.kicad_sch", sub)
	write("widget/misc_reusable_block.kicad_sch", orphan)
	write("widget/widget-backups/widget.kicad_sch", "(kicad_sch junk)")
	write("widget/widget.kicad_pcb", "(kicad_pcb)")
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	lines, err := ParseZip(buf.Bytes())
	if err != nil {
		t.Fatalf("parse zip: %v", err)
	}
	// Root R1 + referenced sub C1 = 2 lines. Orphan C1 must NOT inflate the cap.
	if len(lines) != 2 {
		t.Fatalf("want 2 lines (root + referenced sub, orphan excluded), got %d: %+v", len(lines), lines)
	}
	for _, l := range lines {
		if l.Value == "100nF" && l.Quantity != 1 {
			t.Errorf("cap qty = %d, want 1 (orphan double-count leaked in)", l.Quantity)
		}
	}
}
