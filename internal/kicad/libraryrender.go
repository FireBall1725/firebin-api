// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package kicad

import (
	"errors"
	"math"
	"strings"
)

// LibDrawing is a renderable outline of one library symbol or footprint,
// reduced to primitives a browser can draw with no KiCad knowledge.
//
// Everything is already in screen space: Y increases downward, arcs and
// rectangles are flattened to polylines, and the only shapes left are lines,
// circles and pads. That keeps the web renderer to a few dozen lines and means
// a fix to KiCad's odd geometry conventions lands here, once, in Go.
type LibDrawing struct {
	Kind  string     `json:"kind"` // symbol | footprint
	BBox  BBoxExtent `json:"bbox"`
	Items []DrawItem `json:"items"`

	// Pins counts a symbol's pins. ElectricalPads counts a footprint's numbered
	// pads, ignoring repeats (a split pad shares one number) and mechanical pads
	// (mounting holes carry no number). Together they let a caller sanity-check
	// a symbol/footprint pairing without re-parsing anything.
	Pins           int `json:"pins,omitempty"`
	ElectricalPads int `json:"electrical_pads,omitempty"`
}

// DrawItem is one primitive. Type is "line", "circle" or "pad".
type DrawItem struct {
	Type string `json:"type"`

	// Points is the polyline for Type "line", and the pad centre (single
	// point) for Type "pad".
	Points [][2]float64 `json:"points,omitempty"`

	Center *[2]float64 `json:"center,omitempty"` // circle
	Radius float64     `json:"r,omitempty"`      // circle
	Width  float64     `json:"w,omitempty"`      // stroke width

	Size   *[2]float64 `json:"size,omitempty"`   // pad
	Shape  string      `json:"shape,omitempty"`  // pad: rect | roundrect | circle | oval
	Number string      `json:"number,omitempty"` // pad number; empty for mechanical pads
	Angle  float64     `json:"angle,omitempty"`  // pad rotation, degrees clockwise
	Layer  string      `json:"layer,omitempty"`  // F | B for pads, silk for outlines
	Drill  float64     `json:"drill,omitempty"`

	// Fill is "none", "outline" or "background", straight from KiCad.
	Fill string `json:"fill,omitempty"`
}

const defaultStroke = 0.15

// RenderSymbol turns one (symbol "Name" ...) block from a .kicad_sym into a
// drawing.
//
// Graphics live in nested unit sub-symbols ("R_0_1", "R_1_1"), not on the
// top-level node, so this walks one level down. Properties are skipped: a
// symbol's Reference and Value text is set per placement, and drawing the
// library's placeholders would just render "R" over the middle of every part.
func RenderSymbol(source []byte) (*LibDrawing, error) {
	root, err := parseSexpr(source)
	if err != nil {
		return nil, err
	}
	sym := root
	if sym.head() != "symbol" {
		if c := root.child("symbol"); c != nil {
			sym = c
		} else {
			return nil, errors.New("no (symbol ...) node found")
		}
	}

	d := &LibDrawing{Kind: "symbol"}
	// Direct children first (some symbols draw at the top level), then each
	// unit sub-symbol.
	collectSymbolGraphics(sym, d)
	for _, kid := range sym.Children {
		if kid.head() == "symbol" {
			collectSymbolGraphics(kid, d)
		}
	}
	if len(d.Items) == 0 {
		return nil, errors.New("symbol has no drawable graphics")
	}
	d.BBox = boundsOf(d.Items)
	return d, nil
}

func collectSymbolGraphics(n *node, d *LibDrawing) {
	for _, kid := range n.Children {
		w := strokeWidth(kid)
		if w <= 0 {
			w = defaultStroke
		}
		fill := fillType(kid)

		switch kid.head() {
		case "rectangle":
			a, b := xy(kid, "start"), xy(kid, "end")
			d.Items = append(d.Items, DrawItem{
				Type:   "line",
				Points: flipY([][2]float64{a, {b[0], a[1]}, b, {a[0], b[1]}, a}),
				Width:  w, Fill: fill,
			})
		case "polyline":
			if pts := ptsFrom(kid); len(pts) >= 2 {
				d.Items = append(d.Items, DrawItem{
					Type: "line", Points: flipY(pts), Width: w, Fill: fill,
				})
			}
		case "circle":
			c := xy(kid, "center")
			r := numChild(kid, "radius")
			if r > 0 {
				ctr := [2]float64{c[0], -c[1]}
				d.Items = append(d.Items, DrawItem{
					Type: "circle", Center: &ctr, Radius: r, Width: w, Fill: fill,
				})
			}
		case "arc":
			s, m, e := xy(kid, "start"), xy(kid, "mid"), xy(kid, "end")
			if pts := flattenArc(s, m, e); len(pts) >= 2 {
				d.Items = append(d.Items, DrawItem{
					Type: "line", Points: flipY(pts), Width: w, Fill: fill,
				})
			}
		case "pin":
			// (at x y angle) is the connection point; the line runs from there
			// toward the body along `angle`, which is why a pin at the top of a
			// symbol carries 270 rather than 90.
			at := atNode(kid)
			length := numChild(kid, "length")
			if length <= 0 {
				continue
			}
			rad := at[2] * math.Pi / 180
			end := [2]float64{at[0] + length*math.Cos(rad), at[1] + length*math.Sin(rad)}
			d.Items = append(d.Items, DrawItem{
				Type:   "line",
				Points: flipY([][2]float64{{at[0], at[1]}, end}),
				Width:  defaultStroke,
			})
			d.Pins++
		}
	}
}

// RenderFootprint turns a .kicad_mod into a drawing, reusing the pad and
// silkscreen logic that already backs the board preview.
func RenderFootprint(source []byte) (*LibDrawing, error) {
	root, err := parseSexpr(source)
	if err != nil {
		return nil, err
	}
	fp := root
	if fp.head() != "footprint" && fp.head() != "module" {
		c := root.child("footprint")
		if c == nil {
			c = root.child("module")
		}
		if c == nil {
			return nil, errors.New("no (footprint ...) node found")
		}
		fp = c
	}

	d := &LibDrawing{Kind: "footprint"}

	// A library footprint sits at the origin with no placement transform, so
	// pads are read directly rather than through the board's rotation math.
	for _, kid := range fp.Children {
		switch kid.head() {
		case "pad":
			if it, ok := padItem(kid); ok {
				d.Items = append(d.Items, it)
			}
		case "fp_line":
			a, b := xy(kid, "start"), xy(kid, "end")
			d.Items = append(d.Items, DrawItem{
				Type: "line", Points: [][2]float64{a, b},
				Width: strokeOr(kid), Layer: layerOf(kid),
			})
		case "fp_rect":
			a, b := xy(kid, "start"), xy(kid, "end")
			d.Items = append(d.Items, DrawItem{
				Type:   "line",
				Points: [][2]float64{a, {b[0], a[1]}, b, {a[0], b[1]}, a},
				Width:  strokeOr(kid), Layer: layerOf(kid), Fill: fillType(kid),
			})
		case "fp_circle":
			c, e := xy(kid, "center"), xy(kid, "end")
			r := math.Hypot(e[0]-c[0], e[1]-c[1])
			if r > 0 {
				ctr := c
				d.Items = append(d.Items, DrawItem{
					Type: "circle", Center: &ctr, Radius: r,
					Width: strokeOr(kid), Layer: layerOf(kid), Fill: fillType(kid),
				})
			}
		case "fp_arc":
			s, m, e := xy(kid, "start"), xy(kid, "mid"), xy(kid, "end")
			if pts := flattenArc(s, m, e); len(pts) >= 2 {
				d.Items = append(d.Items, DrawItem{
					Type: "line", Points: pts,
					Width: strokeOr(kid), Layer: layerOf(kid),
				})
			}
		case "fp_poly":
			if pts := ptsFrom(kid); len(pts) >= 2 {
				d.Items = append(d.Items, DrawItem{
					Type: "line", Points: append(pts, pts[0]),
					Width: strokeOr(kid), Layer: layerOf(kid), Fill: fillType(kid),
				})
			}
		}
	}
	if len(d.Items) == 0 {
		return nil, errors.New("footprint has no drawable graphics")
	}
	// Distinct pad numbers, not pad count: a split thermal pad or a connector
	// shell repeats one number across several shapes, and counting shapes would
	// report a two-terminal part as having six terminals.
	nums := map[string]bool{}
	for _, it := range d.Items {
		if it.Type == "pad" && it.Number != "" {
			nums[it.Number] = true
		}
	}
	d.ElectricalPads = len(nums)
	d.BBox = boundsOf(d.Items)
	return d, nil
}

func padItem(pad *node) (DrawItem, bool) {
	at := atNode(pad)
	size := xy(pad, "size")
	if size[0] <= 0 && size[1] <= 0 {
		return DrawItem{}, false
	}
	shape := pad.atom(3)
	if shape == "" {
		shape = "rect"
	}
	// (pad "1" smd roundrect ...) — atom(1) is the pad number. Mechanical pads
	// (mounting holes, fiducials) carry "" and are not electrical connections.
	number := pad.atom(1)
	// Layer set decides which copper side the pad renders on; through-hole
	// pads list both, and "*.Cu" is the wildcard for all copper.
	layer := "F"
	if ls := pad.child("layers"); ls != nil {
		joined := ""
		for i := range ls.Children {
			joined += " " + ls.atom(i)
		}
		if !strings.Contains(joined, "F.Cu") && !strings.Contains(joined, "*.Cu") &&
			strings.Contains(joined, "B.Cu") {
			layer = "B"
		}
	}
	ctr := [2]float64{at[0], at[1]}
	sz := [2]float64{size[0], size[1]}
	it := DrawItem{
		Type: "pad", Points: [][2]float64{ctr}, Size: &sz,
		Shape: shape, Number: number, Angle: at[2], Layer: layer,
	}
	if dr := pad.child("drill"); dr != nil {
		it.Drill = atof(dr.atom(1))
	}
	return it, true
}

// flattenArc approximates a three-point arc as a polyline. Doing it here rather
// than emitting an SVG arc keeps the browser renderer free of sweep-flag and
// large-arc bookkeeping, which is where arc rendering usually goes wrong.
func flattenArc(s, m, e [2]float64) [][2]float64 {
	cx, cy, ok := circumcenter(s, m, e)
	if !ok {
		return [][2]float64{s, m, e}
	}
	r := math.Hypot(s[0]-cx, s[1]-cy)
	a0 := math.Atan2(s[1]-cy, s[0]-cx)
	am := math.Atan2(m[1]-cy, m[0]-cx)
	a1 := math.Atan2(e[1]-cy, e[0]-cx)

	// Walk start → mid → end the short way round each leg, so the sweep passes
	// through the mid point rather than around the other side of the circle.
	sweep := func(from, to float64) float64 {
		d := to - from
		for d > math.Pi {
			d -= 2 * math.Pi
		}
		for d < -math.Pi {
			d += 2 * math.Pi
		}
		return d
	}
	total := sweep(a0, am) + sweep(am, a1)

	const steps = 24
	out := make([][2]float64, 0, steps+1)
	for i := 0; i <= steps; i++ {
		a := a0 + total*float64(i)/steps
		out = append(out, [2]float64{cx + r*math.Cos(a), cy + r*math.Sin(a)})
	}
	return out
}

// flipY converts KiCad's symbol space (Y up) to screen space (Y down).
// Footprints are already Y-down and skip this.
func flipY(pts [][2]float64) [][2]float64 {
	out := make([][2]float64, len(pts))
	for i, p := range pts {
		out[i] = [2]float64{p[0], -p[1]}
	}
	return out
}

func boundsOf(items []DrawItem) BBoxExtent {
	b := BBoxExtent{MinX: math.Inf(1), MinY: math.Inf(1), MaxX: math.Inf(-1), MaxY: math.Inf(-1)}
	add := func(x, y float64) {
		b.MinX = math.Min(b.MinX, x)
		b.MinY = math.Min(b.MinY, y)
		b.MaxX = math.Max(b.MaxX, x)
		b.MaxY = math.Max(b.MaxY, y)
	}
	for _, it := range items {
		switch it.Type {
		case "circle":
			if it.Center != nil {
				add(it.Center[0]-it.Radius, it.Center[1]-it.Radius)
				add(it.Center[0]+it.Radius, it.Center[1]+it.Radius)
			}
		case "pad":
			if len(it.Points) == 1 && it.Size != nil {
				// Rotation is ignored here on purpose: a pad's bounding box only
				// sets the viewport, and half a millimetre of slack costs nothing.
				hw, hh := it.Size[0]/2, it.Size[1]/2
				r := math.Max(hw, hh)
				add(it.Points[0][0]-r, it.Points[0][1]-r)
				add(it.Points[0][0]+r, it.Points[0][1]+r)
			}
		default:
			for _, p := range it.Points {
				add(p[0], p[1])
			}
		}
	}
	if math.IsInf(b.MinX, 1) {
		return BBoxExtent{}
	}
	return b
}

func fillType(n *node) string {
	if f := n.child("fill"); f != nil {
		if t := f.child("type"); t != nil {
			// (type none): atom(0) is the head token itself, the value is atom(1).
			return t.atom(1)
		}
	}
	return ""
}

func strokeOr(n *node) float64 {
	if w := strokeWidth(n); w > 0 {
		return w
	}
	return defaultStroke
}

// numChild reads the numeric argument of a single-value node such as
// (length 1.27) or (radius 0.5). atom(0) is the head token, so the value is
// atom(1); reading atom(0) yields 0 and silently drops every pin.
func numChild(n *node, head string) float64 {
	if c := n.child(head); c != nil {
		return atof(c.atom(1))
	}
	return 0
}

// SymbolExtends returns the base symbol name when this symbol is derived, or ""
// when it draws its own graphics.
//
// KiCad's libraries lean on inheritance heavily: 54% of the symbols in a stock
// KiCad 10 install are `extends` variants that carry only properties, so a
// renderer that ignores this draws nothing for more than half the library.
// AMS1117-3.3, for instance, is graphically an AP1117-15.
func SymbolExtends(source []byte) (string, error) {
	root, err := parseSexpr(source)
	if err != nil {
		return "", err
	}
	sym := root
	if sym.head() != "symbol" {
		if c := root.child("symbol"); c != nil {
			sym = c
		} else {
			return "", errors.New("no (symbol ...) node found")
		}
	}
	if e := sym.child("extends"); e != nil {
		return e.atom(1), nil
	}
	return "", nil
}
