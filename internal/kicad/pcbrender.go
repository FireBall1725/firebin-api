// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package kicad

import (
	"math"
	"strconv"
	"strings"
)

// PcbData mirrors the subset of an Interactive HTML BOM's `pcbdata` that the web
// renderer consumes, so a board generated from a .kicad_pcb renders identically
// to a real iBOM. Cut #1: board outline + pads + footprint boxes (no silk yet).
type PcbData struct {
	EdgesBBox  BBoxExtent        `json:"edges_bbox"`
	Edges      []any             `json:"edges"`
	Drawings   PcbDrawings       `json:"drawings"`
	Footprints []PcbFootprint    `json:"footprints"`
	Metadata   map[string]string `json:"metadata"`
}
type BBoxExtent struct {
	MinX float64 `json:"minx"`
	MinY float64 `json:"miny"`
	MaxX float64 `json:"maxx"`
	MaxY float64 `json:"maxy"`
}
type PcbDrawings struct {
	Silkscreen SilkSides `json:"silkscreen"`
}
type SilkSides struct {
	F []any `json:"F"`
	B []any `json:"B"`
}
type PcbFootprint struct {
	Ref   string   `json:"ref"`
	Layer string   `json:"layer"`
	Bbox  PcbBbox  `json:"bbox"`
	Pads  []PcbPad `json:"pads"`
}
type PcbBbox struct {
	Pos    [2]float64 `json:"pos"`
	Relpos [2]float64 `json:"relpos"`
	Size   [2]float64 `json:"size"`
	Angle  float64    `json:"angle"`
}
type PcbPad struct {
	Pos        [2]float64 `json:"pos"`
	Size       [2]float64 `json:"size"`
	Angle      float64    `json:"angle"`
	Shape      string     `json:"shape"`
	Radius     float64    `json:"radius,omitempty"`
	Type       string     `json:"type"`
	DrillShape string     `json:"drillshape,omitempty"`
	DrillSize  []float64  `json:"drillsize,omitempty"`
	Layers     []string   `json:"layers"`
}
type segEdge struct {
	Type  string     `json:"type"`
	Start [2]float64 `json:"start"`
	End   [2]float64 `json:"end"`
	Width float64    `json:"width"`
}
type circleEdge struct {
	Type   string     `json:"type"`
	Start  [2]float64 `json:"start"`
	Radius float64    `json:"radius"`
	Width  float64    `json:"width"`
}

// GeneratePcbData builds renderable board data from a .kicad_pcb.
func GeneratePcbData(data []byte) (*PcbData, error) {
	root, err := parseSexpr(data)
	if err != nil {
		return nil, err
	}
	pcb := &PcbData{
		Edges:    []any{},
		Drawings: PcbDrawings{Silkscreen: SilkSides{F: []any{}, B: []any{}}},
		Metadata: map[string]string{},
	}
	minx, miny := math.Inf(1), math.Inf(1)
	maxx, maxy := math.Inf(-1), math.Inf(-1)
	acc := func(x, y float64) {
		minx, miny = math.Min(minx, x), math.Min(miny, y)
		maxx, maxy = math.Max(maxx, x), math.Max(maxy, y)
	}

	var identity [3]float64
	for _, ch := range root.Children {
		switch ch.head() {
		case "gr_line", "gr_arc", "gr_circle", "gr_poly", "gr_rect", "gr_text":
			layer := layerOf(ch)
			switch {
			case layer == "Edge.Cuts":
				for _, e := range edgesFrom(ch) {
					pcb.Edges = append(pcb.Edges, e)
					if s, ok := e.(segEdge); ok {
						acc(s.Start[0], s.Start[1])
						acc(s.End[0], s.End[1])
					}
					if c, ok := e.(circleEdge); ok {
						acc(c.Start[0]-c.Radius, c.Start[1]-c.Radius)
						acc(c.Start[0]+c.Radius, c.Start[1]+c.Radius)
					}
				}
			case strings.HasPrefix(layer, "F.SilkS"):
				pcb.Drawings.Silkscreen.F = append(pcb.Drawings.Silkscreen.F, silkElements(ch, identity)...)
			case strings.HasPrefix(layer, "B.SilkS"):
				pcb.Drawings.Silkscreen.B = append(pcb.Drawings.Silkscreen.B, silkElements(ch, identity)...)
			}
		case "footprint", "module":
			// Every footprint renders its pads and silk — mounting holes,
			// fiducials and other board-only parts carry real copper and holes.
			fp, sf, sb, _ := footprintRender(ch)
			pcb.Drawings.Silkscreen.F = append(pcb.Drawings.Silkscreen.F, sf...)
			pcb.Drawings.Silkscreen.B = append(pcb.Drawings.Silkscreen.B, sb...)
			pcb.Footprints = append(pcb.Footprints, fp)
			for _, p := range fp.Pads {
				acc(p.Pos[0], p.Pos[1])
			}
		}
	}

	if math.IsInf(minx, 1) {
		minx, miny, maxx, maxy = 0, 0, 1, 1
	}
	pcb.EdgesBBox = BBoxExtent{MinX: minx, MinY: miny, MaxX: maxx, MaxY: maxy}
	return pcb, nil
}

// ── Edges ────────────────────────────────────────────────────────────────────

func edgesFrom(n *node) []any {
	width := strokeWidth(n)
	switch n.head() {
	case "gr_line":
		s := xy(n, "start")
		e := xy(n, "end")
		return []any{segEdge{Type: "segment", Start: s, End: e, Width: width}}
	case "gr_rect":
		s := xy(n, "start")
		e := xy(n, "end")
		return rectSegs(s, e, width)
	case "gr_circle":
		c := xy(n, "center")
		e := xy(n, "end")
		r := dist(c, e)
		return []any{circleEdge{Type: "circle", Start: c, Radius: r, Width: width}}
	case "gr_arc":
		return arcSegs(xy(n, "start"), xy(n, "mid"), xy(n, "end"), width)
	case "gr_poly":
		return polySegs(n, width)
	}
	return nil
}

func rectSegs(a, b [2]float64, w float64) []any {
	p := [][2]float64{{a[0], a[1]}, {b[0], a[1]}, {b[0], b[1]}, {a[0], b[1]}}
	out := []any{}
	for i := 0; i < 4; i++ {
		out = append(out, segEdge{Type: "segment", Start: p[i], End: p[(i+1)%4], Width: w})
	}
	return out
}

func polySegs(n *node, w float64) []any {
	pts := ptsFrom(n)
	out := []any{}
	for i := 0; i+1 < len(pts); i++ {
		out = append(out, segEdge{Type: "segment", Start: pts[i], End: pts[i+1], Width: w})
	}
	if len(pts) > 2 {
		out = append(out, segEdge{Type: "segment", Start: pts[len(pts)-1], End: pts[0], Width: w})
	}
	return out
}

// arcSegs tessellates a 3-point KiCad arc into segments, sidestepping arc
// sweep-direction ambiguity.
func arcSegs(s, m, e [2]float64, w float64) []any {
	cx, cy, ok := circumcenter(s, m, e)
	if !ok {
		return []any{segEdge{Type: "segment", Start: s, End: e, Width: w}}
	}
	r := math.Hypot(s[0]-cx, s[1]-cy)
	a0 := math.Atan2(s[1]-cy, s[0]-cx)
	am := math.Atan2(m[1]-cy, m[0]-cx)
	a1 := math.Atan2(e[1]-cy, e[0]-cx)
	// Total sweep from a0 to a1 passing through am.
	d1 := norm(am - a0)
	d2 := norm(a1 - am)
	sweep := d1 + d2
	if d1 > math.Pi { // mid is on the clockwise side
		sweep = -(norm(a0-am) + norm(am-a1))
	}
	const steps = 24
	out := []any{}
	prev := s
	for i := 1; i <= steps; i++ {
		t := float64(i) / steps
		ang := a0 + sweep*t
		p := [2]float64{cx + r*math.Cos(ang), cy + r*math.Sin(ang)}
		out = append(out, segEdge{Type: "segment", Start: prev, End: p, Width: w})
		prev = p
	}
	return out
}

// norm maps an angle to [0, 2π).
func norm(a float64) float64 {
	for a < 0 {
		a += 2 * math.Pi
	}
	for a >= 2*math.Pi {
		a -= 2 * math.Pi
	}
	return a
}

func circumcenter(a, b, c [2]float64) (float64, float64, bool) {
	d := 2 * (a[0]*(b[1]-c[1]) + b[0]*(c[1]-a[1]) + c[0]*(a[1]-b[1]))
	if math.Abs(d) < 1e-9 {
		return 0, 0, false
	}
	a2 := a[0]*a[0] + a[1]*a[1]
	b2 := b[0]*b[0] + b[1]*b[1]
	c2 := c[0]*c[0] + c[1]*c[1]
	ux := (a2*(b[1]-c[1]) + b2*(c[1]-a[1]) + c2*(a[1]-b[1])) / d
	uy := (a2*(c[0]-b[0]) + b2*(a[0]-c[0]) + c2*(b[0]-a[0])) / d
	return ux, uy, true
}

// ── Footprints ───────────────────────────────────────────────────────────────

func footprintRender(fp *node) (PcbFootprint, []any, []any, bool) {
	var out PcbFootprint
	out.Layer = "F"
	if l := layerOf(fp); strings.HasPrefix(l, "B") {
		out.Layer = "B"
	}
	fat := atNode(fp) // [x, y, angle]
	out.Bbox.Pos = [2]float64{fat[0], fat[1]}
	out.Bbox.Angle = fat[2]

	silkF, silkB := []any{}, []any{}
	addSilk := func(layer string, els ...any) {
		if strings.HasPrefix(layer, "F.SilkS") {
			silkF = append(silkF, els...)
		} else if strings.HasPrefix(layer, "B.SilkS") {
			silkB = append(silkB, els...)
		}
	}
	for _, ch := range fp.Children {
		switch ch.head() {
		case "property":
			// Only the reference designator is a real silk label; other
			// properties (Value, custom KiLib_* metadata) sit on silk layers too
			// but shouldn't be drawn.
			if strings.EqualFold(ch.atom(1), "reference") {
				out.Ref = ch.atom(2)
				if l := layerOf(ch); strings.Contains(l, "SilkS") && ch.atom(2) != "" && !hidden(ch) {
					addSilk(l, textElement(ch, fat, ch.atom(2)))
				}
			}
		case "fp_line", "fp_arc", "fp_circle", "fp_poly", "fp_rect":
			addSilk(layerOf(ch), silkElements(ch, fat)...)
		case "fp_text":
			if !hidden(ch) {
				addSilk(layerOf(ch), silkElements(ch, fat)...)
			}
		}
	}
	// Pads → absolute geometry + a local-frame bbox for highlighting. Rendered
	// for every footprint, even those excluded from the BOM (mounting holes,
	// fiducials, board_only parts) — they carry real copper and drilled holes.
	lminx, lminy := math.Inf(1), math.Inf(1)
	lmaxx, lmaxy := math.Inf(-1), math.Inf(-1)
	for _, ch := range fp.Children {
		if ch.head() != "pad" {
			continue
		}
		p, lx, ly, hx, hy := padRender(ch, fat)
		out.Pads = append(out.Pads, p)
		lminx, lminy = math.Min(lminx, lx), math.Min(lminy, ly)
		lmaxx, lmaxy = math.Max(lmaxx, hx), math.Max(lmaxy, hy)
	}
	if math.IsInf(lminx, 1) {
		lminx, lminy, lmaxx, lmaxy = -0.5, -0.5, 0.5, 0.5
	}
	out.Bbox.Relpos = [2]float64{lminx, lminy}
	out.Bbox.Size = [2]float64{lmaxx - lminx, lmaxy - lminy}
	return out, silkF, silkB, true
}

// xform maps a point in a footprint's local (unrotated) frame to absolute board
// coordinates. Identity (fat all zero) for board-level graphics.
func xform(p [2]float64, fat [3]float64) [2]float64 {
	rad := -fat[2] * math.Pi / 180
	cos, sin := math.Cos(rad), math.Sin(rad)
	return [2]float64{fat[0] + p[0]*cos - p[1]*sin, fat[1] + p[0]*sin + p[1]*cos}
}

// silkElements converts one graphic/text node to renderer drawings, in absolute
// coordinates via the footprint transform.
func silkElements(n *node, fat [3]float64) []any {
	w := strokeWidth(n)
	switch n.head() {
	case "fp_line", "gr_line":
		return []any{segEdge{Type: "segment", Start: xform(xy(n, "start"), fat), End: xform(xy(n, "end"), fat), Width: w}}
	case "fp_rect", "gr_rect":
		a := xy(n, "start")
		b := xy(n, "end")
		corners := [][2]float64{{a[0], a[1]}, {b[0], a[1]}, {b[0], b[1]}, {a[0], b[1]}}
		out := []any{}
		for i := 0; i < 4; i++ {
			out = append(out, segEdge{Type: "segment", Start: xform(corners[i], fat), End: xform(corners[(i+1)%4], fat), Width: w})
		}
		return out
	case "fp_circle", "gr_circle":
		c := xform(xy(n, "center"), fat)
		e := xform(xy(n, "end"), fat)
		return []any{circleEdge{Type: "circle", Start: c, Radius: dist(c, e), Width: w}}
	case "fp_arc", "gr_arc":
		return arcSegs(xform(xy(n, "start"), fat), xform(xy(n, "mid"), fat), xform(xy(n, "end"), fat), w)
	case "fp_poly", "gr_poly":
		pts := ptsFrom(n)
		poly := make([][2]float64, len(pts))
		for i, p := range pts {
			poly[i] = xform(p, fat)
		}
		return []any{map[string]any{"type": "polygon", "pos": []float64{0, 0}, "angle": 0.0, "polygons": [][][2]float64{poly}}}
	case "fp_text":
		// (fp_text reference|value|user "text" …) — atom(1)=kind, atom(2)=text.
		return []any{textElement(n, fat, n.atom(2))}
	case "gr_text":
		// (gr_text "text" …) — atom(1)=text.
		return []any{textElement(n, fat, n.atom(1))}
	}
	return nil
}

// textElement builds a canvas-rendered text drawing at absolute coordinates.
func textElement(n *node, fat [3]float64, text string) any {
	at := atNode(n)
	pos := xform([2]float64{at[0], at[1]}, fat)
	size := 1.0
	thick := 0.1
	if fnt := effectsFont(n); fnt != nil {
		if s := fnt.child("size"); s != nil {
			size = atof(s.atom(2))
		}
		if t := fnt.child("thickness"); t != nil {
			thick = atof(t.atom(1))
		}
	}
	hj, vj := "center", "center"
	for _, j := range justifyOf(n) {
		switch j {
		case "left", "right":
			hj = j
		case "top", "bottom":
			vj = j
		}
	}
	return map[string]any{
		"type": "text", "text": text,
		"pos": []float64{pos[0], pos[1]}, "angle": at[2],
		"size": size, "thickness": thick, "justify": hj, "vjustify": vj,
	}
}

// hidden reports whether a text node is marked hidden ((hide yes) or a bare
// "hide" atom in older files).
func hidden(n *node) bool {
	if h := n.child("hide"); h != nil {
		return !strings.EqualFold(h.atom(1), "no")
	}
	for _, c := range n.Children {
		if !c.IsList && c.Value == "hide" {
			return true
		}
	}
	return false
}

func effectsFont(n *node) *node {
	if e := n.child("effects"); e != nil {
		return e.child("font")
	}
	return nil
}

func justifyOf(n *node) []string {
	if e := n.child("effects"); e != nil {
		if j := e.child("justify"); j != nil {
			var out []string
			for _, a := range j.Children[1:] {
				out = append(out, strings.ToLower(a.Value))
			}
			return out
		}
	}
	return nil
}

// padRender returns the pad in absolute coordinates plus its local-frame
// bounding extent (for the footprint bbox).
func padRender(pad *node, fat [3]float64) (PcbPad, float64, float64, float64, float64) {
	var p PcbPad
	p.Type = "smd"
	if t := strings.ToLower(pad.atom(2)); strings.Contains(t, "thru") {
		p.Type = "th"
	}
	p.Shape = strings.ToLower(pad.atom(3))
	pat := atNode(pad) // local [px, py, pangle]
	sz := xy(pad, "size")
	p.Size = sz

	// Rotate the local pad position by -footprint-angle, then translate.
	rad := -fat[2] * math.Pi / 180
	cos, sin := math.Cos(rad), math.Sin(rad)
	p.Pos = [2]float64{
		fat[0] + pat[0]*cos - pat[1]*sin,
		fat[1] + pat[0]*sin + pat[1]*cos,
	}
	// The pad's `at` angle is already absolute (footprint rotation baked in).
	p.Angle = pat[2]

	// Layers → F / B (through-hole pads use *.Cu = both sides).
	for _, a := range childAtoms(pad, "layers") {
		switch {
		case strings.HasPrefix(a, "*."):
			p.Layers = []string{"F", "B"}
		case strings.HasPrefix(a, "F."):
			if !contains(p.Layers, "F") {
				p.Layers = append(p.Layers, "F")
			}
		case strings.HasPrefix(a, "B."):
			if !contains(p.Layers, "B") {
				p.Layers = append(p.Layers, "B")
			}
		}
	}
	if p.Shape == "roundrect" {
		if r := childFloat(pad, "roundrect_rratio"); r > 0 {
			p.Radius = r * math.Min(sz[0], sz[1])
		}
	}
	if d := pad.child("drill"); d != nil && p.Type == "th" {
		if strings.EqualFold(d.atom(1), "oval") {
			p.DrillShape = "oval"
			p.DrillSize = []float64{atof(d.atom(2)), atof(d.atom(3))}
		} else {
			dd := atof(d.atom(1))
			p.DrillSize = []float64{dd, dd}
		}
	}

	// Local-frame extent (ignore pad rotation for a slightly loose box).
	lx, ly := pat[0]-sz[0]/2, pat[1]-sz[1]/2
	hx, hy := pat[0]+sz[0]/2, pat[1]+sz[1]/2
	return p, lx, ly, hx, hy
}

// ── s-expr helpers ───────────────────────────────────────────────────────────

func (n *node) child(head string) *node {
	for _, c := range n.Children {
		if c.head() == head {
			return c
		}
	}
	return nil
}

func atNode(n *node) [3]float64 {
	c := n.child("at")
	if c == nil {
		return [3]float64{}
	}
	return [3]float64{atof(c.atom(1)), atof(c.atom(2)), atof(c.atom(3))}
}

func xy(n *node, head string) [2]float64 {
	c := n.child(head)
	if c == nil {
		return [2]float64{}
	}
	return [2]float64{atof(c.atom(1)), atof(c.atom(2))}
}

func strokeWidth(n *node) float64 {
	if s := n.child("stroke"); s != nil {
		return childFloat(s, "width")
	}
	if w := childFloat(n, "width"); w > 0 {
		return w
	}
	return 0.15
}

func layerOf(n *node) string {
	if c := n.child("layer"); c != nil {
		return strings.Trim(c.atom(1), "\"")
	}
	return ""
}

func ptsFrom(n *node) [][2]float64 {
	p := n.child("pts")
	if p == nil {
		return nil
	}
	var out [][2]float64
	for _, c := range p.Children {
		if c.head() == "xy" {
			out = append(out, [2]float64{atof(c.atom(1)), atof(c.atom(2))})
		}
	}
	return out
}

func childAtoms(n *node, head string) []string {
	c := n.child(head)
	if c == nil {
		return nil
	}
	var out []string
	for _, a := range c.Children[1:] {
		out = append(out, a.Value)
	}
	return out
}

func childFloat(n *node, head string) float64 {
	if c := n.child(head); c != nil {
		return atof(c.atom(1))
	}
	return 0
}

func dist(a, b [2]float64) float64 { return math.Hypot(a[0]-b[0], a[1]-b[1]) }

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func atof(s string) float64 {
	f, _ := strconv.ParseFloat(strings.Trim(s, "\""), 64)
	return f
}
