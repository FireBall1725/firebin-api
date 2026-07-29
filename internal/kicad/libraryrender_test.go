// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package kicad

import (
	"math"
	"testing"
)

// deviceR is the Device:R symbol block as KiCad 10 ships it: a 2.032 x 5.08 mm
// body rectangle with a pin 1.27 mm long at each end.
const deviceR = `(symbol "R"
	(pin_numbers (hide yes))
	(pin_names (offset 0))
	(property "Reference" "R" (at 2.032 0 90))
	(property "Value" "R" (at 0 0 90))
	(symbol "R_0_1"
		(rectangle
			(start -1.016 -2.54)
			(end 1.016 2.54)
			(stroke (width 0.254) (type default))
			(fill (type none))
		)
	)
	(symbol "R_1_1"
		(pin passive line
			(at 0 3.81 270)
			(length 1.27)
			(name "" (effects (font (size 1.27 1.27))))
			(number "1" (effects (font (size 1.27 1.27))))
		)
		(pin passive line
			(at 0 -3.81 90)
			(length 1.27)
			(name "" (effects (font (size 1.27 1.27))))
			(number "2" (effects (font (size 1.27 1.27))))
		)
	)
)`

func TestRenderSymbolBody(t *testing.T) {
	d, err := RenderSymbol([]byte(deviceR))
	if err != nil {
		t.Fatalf("RenderSymbol: %v", err)
	}
	if d.Kind != "symbol" {
		t.Errorf("Kind = %q", d.Kind)
	}
	// Body rectangle plus two pins.
	if len(d.Items) != 3 {
		t.Fatalf("got %d items, want 3 (rect + 2 pins): %+v", len(d.Items), d.Items)
	}
	// The pins sit at y = ±3.81 and the body spans ±2.54, so the drawing is
	// 7.62 mm tall overall. Wrong pin direction would make it 10.16.
	if h := d.BBox.MaxY - d.BBox.MinY; math.Abs(h-7.62) > 1e-6 {
		t.Errorf("height = %v, want 7.62 (pins must run toward the body, not away)", h)
	}
	if w := d.BBox.MaxX - d.BBox.MinX; math.Abs(w-2.032) > 1e-6 {
		t.Errorf("width = %v, want 2.032", w)
	}
}

// TestPinRunsTowardBody pins down the convention that cost the most time to get
// right: a pin's (at ...) is its free connection point, and its angle points
// back toward the body. A pin at the top of a symbol therefore carries 270, and
// reading it as "direction the pin sticks out" doubles the symbol's height.
func TestPinRunsTowardBody(t *testing.T) {
	d, err := RenderSymbol([]byte(deviceR))
	if err != nil {
		t.Fatalf("RenderSymbol: %v", err)
	}
	var found bool
	for _, it := range d.Items {
		if len(it.Points) != 2 {
			continue
		}
		// Screen space, so the pin declared at y=+3.81 renders at y=-3.81.
		if math.Abs(it.Points[0][1]+3.81) < 1e-6 {
			found = true
			if math.Abs(it.Points[1][1]+2.54) > 1e-6 {
				t.Errorf("pin ends at y=%v, want -2.54 (the body edge)", it.Points[1][1])
			}
		}
	}
	if !found {
		t.Error("no pin starting at the top connection point")
	}
}

// TestSymbolIsFlippedToScreenSpace: KiCad symbol coordinates are Y-up, screens
// are Y-down. Without the flip every symbol renders mirrored vertically, which
// is invisible on a resistor and obvious on a diode.
func TestSymbolIsFlippedToScreenSpace(t *testing.T) {
	src := `(symbol "D"
		(symbol "D_0_1"
			(polyline (pts (xy 0 0) (xy 0 2)) (stroke (width 0.2)) (fill (type none)))
		)
	)`
	d, err := RenderSymbol([]byte(src))
	if err != nil {
		t.Fatalf("RenderSymbol: %v", err)
	}
	if got := d.Items[0].Points[1][1]; got != -2 {
		t.Errorf("y = %v, want -2 (Y must be negated for screen space)", got)
	}
}

// r0603 is Resistor_SMD:R_0603_1608Metric, trimmed to the parts that draw.
const r0603 = `(footprint "R_0603_1608Metric"
	(layer "F.Cu")
	(attr smd)
	(fp_line (start -1.48 -0.73) (end 1.48 -0.73)
		(stroke (width 0.05) (type solid)) (layer "F.CrtYd"))
	(fp_line (start -1.48 0.73) (end 1.48 0.73)
		(stroke (width 0.05) (type solid)) (layer "F.CrtYd"))
	(pad "1" smd roundrect (at -0.775 0) (size 0.9 0.95) (layers "F.Cu" "F.Paste" "F.Mask"))
	(pad "2" smd roundrect (at 0.775 0) (size 0.9 0.95) (layers "F.Cu" "F.Paste" "F.Mask"))
)`

func TestRenderFootprintPads(t *testing.T) {
	d, err := RenderFootprint([]byte(r0603))
	if err != nil {
		t.Fatalf("RenderFootprint: %v", err)
	}
	if d.Kind != "footprint" {
		t.Errorf("Kind = %q", d.Kind)
	}
	var pads int
	for _, it := range d.Items {
		if it.Type != "pad" {
			continue
		}
		pads++
		if it.Shape != "roundrect" {
			t.Errorf("pad shape = %q, want roundrect", it.Shape)
		}
		if it.Size == nil || it.Size[0] != 0.9 || it.Size[1] != 0.95 {
			t.Errorf("pad size = %v, want [0.9 0.95]", it.Size)
		}
		if it.Layer != "F" {
			t.Errorf("pad layer = %q, want F", it.Layer)
		}
	}
	if pads != 2 {
		t.Errorf("got %d pads, want 2", pads)
	}
	// Courtyard is 2.96 mm wide; the pads sit inside it.
	if w := d.BBox.MaxX - d.BBox.MinX; math.Abs(w-2.96) > 1e-6 {
		t.Errorf("width = %v, want 2.96", w)
	}
}

// TestFootprintIsNotFlipped: PCB coordinates are already Y-down, so applying the
// symbol flip here would mirror every footprint.
func TestFootprintIsNotFlipped(t *testing.T) {
	d, err := RenderFootprint([]byte(r0603))
	if err != nil {
		t.Fatalf("RenderFootprint: %v", err)
	}
	for _, it := range d.Items {
		if it.Type == "line" && len(it.Points) == 2 && it.Points[0][1] == -0.73 {
			return // the courtyard line declared at -0.73 stayed at -0.73
		}
	}
	t.Error("footprint geometry appears flipped; PCB space is already Y-down")
}

// TestBackSidePadDetected covers a pad that lives only on the bottom copper, so
// the renderer can colour the two sides differently.
func TestBackSidePadDetected(t *testing.T) {
	src := `(footprint "X"
		(pad "1" smd rect (at 0 0) (size 1 1) (layers "B.Cu" "B.Paste" "B.Mask"))
	)`
	d, err := RenderFootprint([]byte(src))
	if err != nil {
		t.Fatalf("RenderFootprint: %v", err)
	}
	if d.Items[0].Layer != "B" {
		t.Errorf("layer = %q, want B", d.Items[0].Layer)
	}
}

// TestArcFlattensThroughItsMidPoint guards the sweep direction. Taking the
// short way from start to end ignoring the mid point draws the arc on the wrong
// side of the circle, which turns a rounded connector shell inside out.
func TestArcFlattensThroughItsMidPoint(t *testing.T) {
	// Half circle of radius 1 centred at the origin, bulging upward.
	pts := flattenArc([2]float64{-1, 0}, [2]float64{0, 1}, [2]float64{1, 0})
	if len(pts) < 3 {
		t.Fatalf("got %d points", len(pts))
	}
	var maxY float64
	for _, p := range pts {
		maxY = math.Max(maxY, p[1])
		if r := math.Hypot(p[0], p[1]); math.Abs(r-1) > 1e-6 {
			t.Fatalf("point %v is not on the unit circle (r=%v)", p, r)
		}
	}
	if math.Abs(maxY-1) > 1e-6 {
		t.Errorf("arc peaks at y=%v, want 1; it swept the wrong way", maxY)
	}
}

func TestRenderRejectsEmptyGraphics(t *testing.T) {
	if _, err := RenderSymbol([]byte(`(symbol "Empty")`)); err == nil {
		t.Error("want an error for a symbol with nothing to draw")
	}
	if _, err := RenderFootprint([]byte(`(footprint "Empty")`)); err == nil {
		t.Error("want an error for a footprint with nothing to draw")
	}
}

// TestSymbolExtendsIsDetected covers the inheritance that most of a KiCad
// library relies on. 54% of the symbols in a stock KiCad 10 install are
// `extends` variants carrying only properties, so a renderer that does not
// follow the chain silently draws nothing for more than half of them.
func TestSymbolExtendsIsDetected(t *testing.T) {
	derived := `(symbol "AMS1117-3.3"
		(extends "AP1117-15")
		(property "Reference" "U" (at -3.81 3.175 0))
		(property "Value" "AMS1117-3.3" (at 0 3.175 0))
	)`
	base, err := SymbolExtends([]byte(derived))
	if err != nil {
		t.Fatalf("SymbolExtends: %v", err)
	}
	if base != "AP1117-15" {
		t.Errorf("base = %q, want AP1117-15", base)
	}

	// A symbol drawing its own graphics reports no parent.
	base, err = SymbolExtends([]byte(deviceR))
	if err != nil {
		t.Fatalf("SymbolExtends: %v", err)
	}
	if base != "" {
		t.Errorf("base = %q, want empty for a self-contained symbol", base)
	}

	// And it renders as nothing on its own, which is why the caller must follow
	// the chain before rendering rather than after failing.
	if _, err := RenderSymbol([]byte(derived)); err == nil {
		t.Error("a derived symbol alone should not render")
	}
}

// TestPinAndPadCounts backs the sanity check that stops a two-terminal symbol
// being paired with a four-pad footprint. Counting distinct pad NUMBERS rather
// than pad shapes matters: a split thermal pad or a connector shell repeats one
// number across several shapes.
func TestPinAndPadCounts(t *testing.T) {
	sym, err := RenderSymbol([]byte(deviceR))
	if err != nil {
		t.Fatalf("RenderSymbol: %v", err)
	}
	if sym.Pins != 2 {
		t.Errorf("Device:R pins = %d, want 2", sym.Pins)
	}

	fp, err := RenderFootprint([]byte(r0603))
	if err != nil {
		t.Fatalf("RenderFootprint: %v", err)
	}
	if fp.ElectricalPads != 2 {
		t.Errorf("R_0603 electrical pads = %d, want 2", fp.ElectricalPads)
	}
}

// TestMechanicalPadsAreNotElectrical: mounting holes and fiducials carry no pad
// number. Counting them would make a 2-pin part look like a 4-pin one and would
// trip the pairing check on perfectly good footprints.
func TestMechanicalPadsAreNotElectrical(t *testing.T) {
	src := `(footprint "X"
		(pad "1" smd rect (at -1 0) (size 1 1) (layers "F.Cu"))
		(pad "2" smd rect (at 1 0) (size 1 1) (layers "F.Cu"))
		(pad "" np_thru_hole circle (at 0 3) (size 2 2) (drill 2) (layers "*.Cu"))
	)`
	d, err := RenderFootprint([]byte(src))
	if err != nil {
		t.Fatalf("RenderFootprint: %v", err)
	}
	if d.ElectricalPads != 2 {
		t.Errorf("electrical pads = %d, want 2 (the unnumbered hole is mechanical)", d.ElectricalPads)
	}
}

// TestRepeatedPadNumberCountsOnce covers a split pad, where one terminal is
// drawn as several shapes sharing a number.
func TestRepeatedPadNumberCountsOnce(t *testing.T) {
	src := `(footprint "X"
		(pad "1" smd rect (at -1 0) (size 1 1) (layers "F.Cu"))
		(pad "2" smd rect (at 1 0) (size 1 1) (layers "F.Cu"))
		(pad "2" smd rect (at 1 1) (size 1 1) (layers "F.Cu"))
		(pad "2" smd rect (at 1 2) (size 1 1) (layers "F.Cu"))
	)`
	d, err := RenderFootprint([]byte(src))
	if err != nil {
		t.Fatalf("RenderFootprint: %v", err)
	}
	if d.ElectricalPads != 2 {
		t.Errorf("electrical pads = %d, want 2 (pad 2 is split across three shapes)", d.ElectricalPads)
	}
}

// TestFourPadPackageIsNotTwoTerminal is the case that motivated the guard:
// SP0503BAHTG sits in SOT-143, and its FireBin category ("Zener Diodes")
// suggests a 2-pin Device:D_Zener. The footprint says otherwise.
func TestFourPadPackageIsNotTwoTerminal(t *testing.T) {
	sot143 := `(footprint "SOT-143"
		(pad "1" smd rect (at -0.9 -1.1) (size 0.8 0.7) (layers "F.Cu"))
		(pad "2" smd rect (at 0.9 -1.1) (size 1.4 0.7) (layers "F.Cu"))
		(pad "3" smd rect (at 0.9 1.1) (size 0.8 0.7) (layers "F.Cu"))
		(pad "4" smd rect (at -0.9 1.1) (size 0.8 0.7) (layers "F.Cu"))
	)`
	d, err := RenderFootprint([]byte(sot143))
	if err != nil {
		t.Fatalf("RenderFootprint: %v", err)
	}
	if d.ElectricalPads <= 2 {
		t.Fatalf("electrical pads = %d, want more than 2 so the guard fires", d.ElectricalPads)
	}
}
