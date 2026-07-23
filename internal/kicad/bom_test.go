// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package kicad

import "testing"

// A trimmed KiCad 7/8 schematic: two 10k 0603 resistors, one 100nF cap with an
// MPN, a power symbol and a DNP part that must be excluded.
const sampleSch = `(kicad_sch (version 20231120) (generator eeschema)
  (lib_symbols
    (symbol "Device:R" (property "Reference" "R" (at 0 0 0)))
  )
  (symbol (lib_id "Device:R") (at 100 100 0) (unit 1) (in_bom yes) (on_board yes) (dnp no)
    (property "Reference" "R2" (at 0 0 0))
    (property "Value" "10k" (at 0 0 0))
    (property "Footprint" "Resistor_SMD:R_0603_1608Metric" (at 0 0 0))
  )
  (symbol (lib_id "Device:R") (at 120 100 0) (unit 1) (in_bom yes) (on_board yes) (dnp no)
    (property "Reference" "R1" (at 0 0 0))
    (property "Value" "10k" (at 0 0 0))
    (property "Footprint" "Resistor_SMD:R_0603_1608Metric" (at 0 0 0))
  )
  (symbol (lib_id "Device:C") (at 140 100 0) (unit 1) (in_bom yes) (on_board yes) (dnp no)
    (property "Reference" "C1" (at 0 0 0))
    (property "Value" "100nF" (at 0 0 0))
    (property "Footprint" "Capacitor_SMD:C_0402_1005Metric" (at 0 0 0))
    (property "MPN" "CL05B104KO5NNNC" (at 0 0 0))
    (property "Manufacturer" "Samsung" (at 0 0 0))
  )
  (symbol (lib_id "power:GND") (at 160 100 0) (unit 1) (in_bom yes) (on_board yes)
    (property "Reference" "#PWR01" (at 0 0 0))
    (property "Value" "GND" (at 0 0 0))
  )
  (symbol (lib_id "Device:R") (at 180 100 0) (unit 1) (in_bom yes) (on_board yes) (dnp yes)
    (property "Reference" "R99" (at 0 0 0))
    (property "Value" "DNP" (at 0 0 0))
  )
)`

func TestParseSchematic(t *testing.T) {
	lines, err := ParseSchematic([]byte(sampleSch))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("want 2 BOM lines, got %d: %+v", len(lines), lines)
	}

	// BOM sorts by refdes prefix, so the capacitor (C1) comes before R1/R2.
	c := lines[0]
	if c.Value != "100nF" || c.MPN != "CL05B104KO5NNNC" || c.Manufacturer != "Samsung" {
		t.Errorf("cap line = %+v, want 100nF with MPN+manufacturer", c)
	}
	if c.Quantity != 1 || len(c.Refs) != 1 || c.Refs[0] != "C1" {
		t.Errorf("cap line refs/qty = %v/%d, want [C1]/1", c.Refs, c.Quantity)
	}

	r := lines[1]
	if r.Value != "10k" || r.Quantity != 2 {
		t.Errorf("resistor line = %+v, want value 10k qty 2", r)
	}
	if len(r.Refs) != 2 || r.Refs[0] != "R1" || r.Refs[1] != "R2" {
		t.Errorf("resistor refs = %v, want [R1 R2] sorted", r.Refs)
	}
}

func TestParseBOMCSV(t *testing.T) {
	csvData := `Reference,Value,Footprint,Quantity,MPN,Manufacturer
"R1, R2",10k,R_0603,2,,
C1,100nF,C_0402,1,CL05B104KO5NNNC,Samsung
`
	lines, err := ParseBOMCSV([]byte(csvData))
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d", len(lines))
	}
	if lines[0].Quantity != 2 || len(lines[0].Refs) != 2 {
		t.Errorf("row 0 = %+v, want qty 2 with 2 refs", lines[0])
	}
	if lines[1].MPN != "CL05B104KO5NNNC" {
		t.Errorf("row 1 MPN = %q", lines[1].MPN)
	}
}
