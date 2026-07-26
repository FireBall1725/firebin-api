// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package labels renders barcode/QR label sheets to PDF, laid out on a chosen
// label media (Avery-compatible sheet geometry). Generation is server-side so
// the same engine can back the web print flow, the future MCP print tool, and
// (later) ZPL/Brother-QL roll printers.
package labels

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/code128"
	"github.com/boombuler/barcode/qr"
	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/go-pdf/fpdf"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gobolditalic"
	"golang.org/x/image/font/gofont/goitalic"
	"golang.org/x/image/font/gofont/goregular"
)

// fontFamily is the embedded UTF-8 font used for all label text. The Go fonts
// (BSD-licensed, shipped as glyph bytes in x/image) cover Latin plus the Greek
// and maths symbols parts data needs — Ω, µ, ±, °, Δ — which the PDF core fonts
// (Latin-1 only) render as mojibake.
const fontFamily = "go"

// registerFonts embeds the UTF-8 Go fonts into a PDF instance (bytes, so no font
// file ships on disk).
func registerFonts(pdf *fpdf.Fpdf) {
	pdf.AddUTF8FontFromBytes(fontFamily, "", goregular.TTF)
	pdf.AddUTF8FontFromBytes(fontFamily, "B", gobold.TTF)
	pdf.AddUTF8FontFromBytes(fontFamily, "I", goitalic.TTF)
	pdf.AddUTF8FontFromBytes(fontFamily, "BI", gobolditalic.TTF)
}

// fontStyle builds an fpdf style string from an element's flags. B/I pick the
// font variant; U adds an underline decoration on top.
func fontStyle(e Element) string {
	s := ""
	if e.Bold {
		s += "B"
	}
	if e.Italic {
		s += "I"
	}
	if e.Underline {
		s += "U"
	}
	return s
}

// Element is one drawn item on a label. A design (built-in now, user-authored
// later) is just a slice of these, positioned in points relative to the label's
// top-left.
type Element struct {
	Type      string  `json:"type"`            // "text" | "qr" | "barcode" | "line" | "rect"
	Field     string  `json:"field,omitempty"` // template binding (ipn|name|package|mpn|qr|…); empty = literal Value
	X         float64 `json:"x"`               // offset within the label (pt)
	Y         float64 `json:"y"`
	W         float64 `json:"w"` // box size (pt); for text, W is the wrap width
	H         float64 `json:"h"`
	Value     string  `json:"value,omitempty"` // resolved/literal content
	Font      float64 `json:"font,omitempty"`  // text point size
	Bold      bool    `json:"bold,omitempty"`
	Italic    bool    `json:"italic,omitempty"`
	Underline bool    `json:"underline,omitempty"`
	Align     string  `json:"align,omitempty"`     // horizontal: "L" | "C" | "R"
	VAlign    string  `json:"valign,omitempty"`    // vertical: "T" | "M" | "B" (default T)
	Thickness float64 `json:"thickness,omitempty"` // line / rect stroke weight (pt)
	Filled    bool    `json:"filled,omitempty"`    // rect: solid fill vs outline
	Invert    bool    `json:"invert,omitempty"`    // text / qr / barcode: white-on-black
	ParamName string  `json:"paramName,omitempty"` // for field="param": which part parameter
}

// Label is a single rendered label: its elements.
type Label struct{ Elements []Element }

var pngOpts = fpdf.ImageOptions{ImageType: "png", ReadDpi: true}

// invertedImage wraps an image so its luminance is flipped (black ↔ white),
// keeping the source alpha — used to render a white QR/barcode on a black box.
type invertedImage struct{ image.Image }

func (i invertedImage) At(x, y int) color.Color {
	r, g, b, a := i.Image.At(x, y).RGBA()
	return color.RGBA{uint8(255 - (r >> 8)), uint8(255 - (g >> 8)), uint8(255 - (b >> 8)), uint8(a >> 8)}
}

// RenderSheet lays labels onto media, filling free cells left-to-right,
// top-to-bottom. usedCells marks cells already peeled off the FIRST sheet (a
// partially-used sheet); overflow spills onto fresh full sheets. outline draws a
// faint cut guide per cell (preview only).
func RenderSheet(media models.LabelMedia, items []Label, usedCells map[int]bool, outline bool) ([]byte, error) {
	pdf := fpdf.NewCustom(&fpdf.InitType{
		UnitStr: "pt",
		Size:    fpdf.SizeType{Wd: media.PageW, Ht: media.PageH},
	})
	pdf.SetMargins(0, 0, 0)
	pdf.SetAutoPageBreak(false, 0)
	registerFonts(pdf)
	pdf.AddPage()

	perSheet := media.PerSheet()
	if perSheet <= 0 {
		return nil, fmt.Errorf("media %s has no cells", media.Code)
	}
	cell := 0
	firstSheet := true
	imgN := 0

	for _, lb := range items {
		if firstSheet {
			for cell < perSheet && usedCells[cell] {
				cell++
			}
		}
		if cell >= perSheet {
			pdf.AddPage()
			cell = 0
			firstSheet = false
		}
		col := cell % media.Cols
		row := cell / media.Cols
		lx := media.X0 + float64(col)*media.PitchX
		ly := media.Y0 + float64(row)*media.PitchY
		if outline {
			pdf.SetDrawColor(205, 208, 214)
			pdf.SetLineWidth(0.5)
			pdf.RoundedRect(lx, ly, media.LabelW, media.LabelH, media.CornerRadius, "1234", "D")
		}
		// Clip to the label so nothing an element draws can spill onto a neighbour.
		pdf.ClipRect(lx, ly, media.LabelW, media.LabelH, false)
		err := drawLabel(pdf, lx, ly, lb, &imgN)
		pdf.ClipEnd()
		if err != nil {
			return nil, err
		}
		cell++
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// RenderLabel renders ONE label to a one-page PDF sized exactly to the label,
// for the designer's live preview. A faint border shows the label bounds.
func RenderLabel(media models.LabelMedia, lb Label) ([]byte, error) {
	pdf := fpdf.NewCustom(&fpdf.InitType{
		UnitStr: "pt",
		Size:    fpdf.SizeType{Wd: media.LabelW, Ht: media.LabelH},
	})
	pdf.SetMargins(0, 0, 0)
	pdf.SetAutoPageBreak(false, 0)
	registerFonts(pdf)
	pdf.AddPage()
	pdf.SetDrawColor(205, 208, 214)
	pdf.SetLineWidth(0.5)
	if media.CornerRadius > 0 {
		pdf.RoundedRect(0.25, 0.25, media.LabelW-0.5, media.LabelH-0.5, media.CornerRadius, "1234", "D")
	} else {
		pdf.Rect(0.25, 0.25, media.LabelW-0.5, media.LabelH-0.5, "D")
	}
	imgN := 0
	pdf.ClipRect(0, 0, media.LabelW, media.LabelH, false)
	err := drawLabel(pdf, 0, 0, lb, &imgN)
	pdf.ClipEnd()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func drawLabel(pdf *fpdf.Fpdf, lx, ly float64, lb Label, imgN *int) error {
	for _, e := range lb.Elements {
		switch e.Type {
		case "text":
			if e.Value == "" {
				continue
			}
			if e.Invert {
				// White text on a solid black box.
				pdf.SetFillColor(0, 0, 0)
				pdf.Rect(lx+e.X, ly+e.Y, e.W, e.H, "F")
				pdf.SetTextColor(255, 255, 255)
			} else {
				pdf.SetTextColor(20, 20, 20)
			}
			pdf.SetFont(fontFamily, fontStyle(e), e.Font)
			align := e.Align
			if align == "" {
				align = "L"
			}
			// Wrap to the box width and CLIP to its height so text never spills
			// past the box the user drew; the last visible line gets an ellipsis
			// when truncated. (MultiCell alone wraps but ignores height.)
			lineH := e.Font * 1.18
			maxLines := max(int(e.H/lineH), 1)
			lines := pdf.SplitText(e.Value, e.W)
			if len(lines) > maxLines {
				lines = lines[:maxLines]
				r := []rune(strings.TrimRight(lines[maxLines-1], " "))
				for len(r) > 0 && pdf.GetStringWidth(string(r)+"…") > e.W {
					r = r[:len(r)-1]
				}
				lines[maxLines-1] = string(r) + "…"
			}
			// Vertical alignment inside the box: offset the text block by the slack
			// between the box height and the block height (default top).
			yOff := 0.0
			if slack := e.H - float64(len(lines))*lineH; slack > 0 {
				switch e.VAlign {
				case "M":
					yOff = slack / 2
				case "B":
					yOff = slack
				}
			}
			// Render each already-wrapped line as its own single-line cell. Using
			// MultiCell here would RE-WRAP the text against e.W and could produce
			// more lines than we clipped to, spilling past the box; a cell per line
			// renders exactly len(lines) lines.
			for i, ln := range lines {
				pdf.SetXY(lx+e.X, ly+e.Y+yOff+float64(i)*lineH)
				pdf.CellFormat(e.W, lineH, ln, "", 0, align, false, 0, "")
			}
		case "qr":
			if e.Value == "" {
				continue
			}
			bc, err := qr.Encode(e.Value, qr.M, qr.Auto)
			if err != nil {
				return err
			}
			if err := placeImage(pdf, bc, lx+e.X, ly+e.Y, e.W, e.H, int(e.W*4), int(e.H*4), e.Invert, imgN); err != nil {
				return err
			}
		case "barcode":
			if e.Value == "" {
				continue
			}
			bc, err := code128.Encode(e.Value)
			if err != nil {
				return err
			}
			if err := placeImage(pdf, bc, lx+e.X, ly+e.Y, e.W, e.H, int(e.W*4), 1, e.Invert, imgN); err != nil {
				return err
			}
		case "line":
			// A rule drawn across the box: horizontal when wider than tall, else
			// vertical, centred, at the given thickness.
			t := e.Thickness
			if t <= 0 {
				t = 1
			}
			pdf.SetDrawColor(0, 0, 0)
			pdf.SetLineWidth(t)
			if e.W >= e.H {
				cy := ly + e.Y + e.H/2
				pdf.Line(lx+e.X, cy, lx+e.X+e.W, cy)
			} else {
				cx := lx + e.X + e.W/2
				pdf.Line(cx, ly+e.Y, cx, ly+e.Y+e.H)
			}
		case "rect":
			if e.Filled {
				pdf.SetFillColor(0, 0, 0)
				pdf.Rect(lx+e.X, ly+e.Y, e.W, e.H, "F")
			} else {
				t := e.Thickness
				if t <= 0 {
					t = 1
				}
				pdf.SetDrawColor(0, 0, 0)
				pdf.SetLineWidth(t)
				// Inset by half the stroke so the outline stays inside the box.
				pdf.Rect(lx+e.X+t/2, ly+e.Y+t/2, e.W-t, e.H-t, "D")
			}
		}
	}
	return nil
}

// placeImage scales a barcode to the given pixel dims, encodes PNG, and draws it
// into the PDF at the point rectangle. scaleH of 1 lets Scale keep the code's
// native bar pattern height (1-D codes); pass the real pixel height for QR.
// invert swaps black and white (white code on a black box).
func placeImage(pdf *fpdf.Fpdf, bc barcode.Barcode, x, y, w, h float64, pxW, pxH int, invert bool, imgN *int) error {
	pxW = max(pxW, 1)
	pxH = max(pxH, 1)
	scaled, err := barcode.Scale(bc, pxW, pxH)
	if err != nil {
		return err
	}
	var img image.Image = scaled
	if invert {
		img = invertedImage{scaled}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return err
	}
	name := fmt.Sprintf("img%d", *imgN)
	*imgN++
	pdf.RegisterImageOptionsReader(name, pngOpts, bytes.NewReader(buf.Bytes()))
	pdf.ImageOptions(name, x, y, w, h, false, pngOpts, 0, "")
	return nil
}

// BuildPartLabel produces the built-in part label: a QR that deep-links to the
// part page on the left, and the part name plus an IPN Code128 barcode on the
// right. It adapts to the media's label size.
func BuildPartLabel(media models.LabelMedia, name, ipn, url string) Label {
	lw, lh := media.LabelW, media.LabelH
	p := clamp(lh*0.08, 3, 7)
	qrSide := lh - 2*p
	if qrSide > lw*0.5 {
		qrSide = lw * 0.5
	}
	rx := p + qrSide + 8
	rw := lw - rx - p
	if rw < 8 {
		rw = 8
	}

	els := []Element{
		{Type: "qr", X: p, Y: p, W: qrSide, H: qrSide, Value: url},
	}

	nameFont := clamp(lh*0.13, 7, 12)
	els = append(els, Element{
		Type: "text", X: rx, Y: p, W: rw, Font: nameFont, Bold: true, Align: "L",
		Value: truncateToLines(name, rw, nameFont, 2),
	})

	if ipn != "" {
		bcH := clamp(lh*0.28, 12, 40)
		bcY := lh - p - bcH
		els = append(els,
			Element{Type: "text", X: rx, Y: bcY - 9, W: rw, Font: 6.5, Align: "L", Value: ipn},
			Element{Type: "barcode", X: rx, Y: bcY, W: rw, H: bcH, Value: ipn},
		)
	}
	return Label{Elements: els}
}

// BuildLocationLabel is the built-in location label: a QR deep link, the location
// name, and its barcode. Same adaptive layout as the part label.
func BuildLocationLabel(media models.LabelMedia, name, code, url string) Label {
	return BuildPartLabel(media, name, code, url)
}

// BuildStockLabel is the built-in lot label (e.g. a mini spool): a QR deep link,
// the part name, and a sub line (its quantity / lot name).
func BuildStockLabel(media models.LabelMedia, name, sub, url string) Label {
	lw, lh := media.LabelW, media.LabelH
	p := clamp(lh*0.08, 3, 7)
	qrSide := lh - 2*p
	if qrSide > lw*0.5 {
		qrSide = lw * 0.5
	}
	rx := p + qrSide + 8
	rw := lw - rx - p
	if rw < 8 {
		rw = 8
	}
	nameFont := clamp(lh*0.13, 7, 12)
	els := []Element{
		{Type: "qr", X: p, Y: p, W: qrSide, H: qrSide, Value: url},
		{Type: "text", X: rx, Y: p, W: rw, Font: nameFont, Bold: true, Align: "L", Value: truncateToLines(name, rw, nameFont, 2)},
	}
	if sub != "" {
		els = append(els, Element{Type: "text", X: rx, Y: p + nameFont*2.5, W: rw, Font: clamp(lh*0.11, 6, 9), Align: "L", Value: sub})
	}
	return Label{Elements: els}
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// truncateToLines caps a string to roughly `lines` lines at the given wrap width
// and font size (Helvetica averages ~0.52em per glyph), so a long name can't
// overrun into the barcode area.
func truncateToLines(s string, width, font float64, lines int) string {
	s = strings.TrimSpace(s)
	perLine := max(int(width/(font*0.52)), 1)
	max := perLine * lines
	if len(s) <= max {
		return s
	}
	if max < 2 {
		return s[:max]
	}
	return strings.TrimSpace(s[:max-1]) + "…"
}
