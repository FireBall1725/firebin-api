// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package kicad

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"path"
	"sort"
	"strconv"
	"strings"
)

// ParseBOMXLSX reads a BOM from an .xlsx (the first worksheet). EasyEDA / JLCPCB
// / LCSC export BOMs as xlsx; the column mapping is shared with the CSV path.
func ParseBOMXLSX(data []byte) ([]BOMLine, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	shared := readSharedStrings(zr)
	sheet := firstWorksheet(zr)
	if sheet == nil {
		return nil, nil
	}
	rows := parseWorksheet(sheet, shared)
	return rowsToLines(rows), nil
}

func readSharedStrings(zr *zip.Reader) []string {
	f := zipEntry(zr, "xl/sharedStrings.xml")
	if f == nil {
		return nil
	}
	b, err := readZipFile(f)
	if err != nil {
		return nil
	}
	var doc struct {
		SI []struct {
			T string `xml:"t"`
			R []struct {
				T string `xml:"t"`
			} `xml:"r"`
		} `xml:"si"`
	}
	if xml.Unmarshal(b, &doc) != nil {
		return nil
	}
	out := make([]string, len(doc.SI))
	for i, si := range doc.SI {
		s := si.T
		for _, r := range si.R {
			s += r.T
		}
		out[i] = s
	}
	return out
}

func firstWorksheet(zr *zip.Reader) *zip.File {
	if f := zipEntry(zr, "xl/worksheets/sheet1.xml"); f != nil {
		return f
	}
	var names []string
	for _, f := range zr.File {
		n := strings.ToLower(f.Name)
		if strings.HasPrefix(n, "xl/worksheets/") && strings.HasSuffix(n, ".xml") {
			names = append(names, f.Name)
		}
	}
	sort.Strings(names)
	if len(names) > 0 {
		return zipEntry(zr, names[0])
	}
	return nil
}

func zipEntry(zr *zip.Reader, name string) *zip.File {
	for _, f := range zr.File {
		if path.Clean(f.Name) == name {
			return f
		}
	}
	return nil
}

func parseWorksheet(f *zip.File, shared []string) [][]string {
	b, err := readZipFile(f)
	if err != nil {
		return nil
	}
	var doc struct {
		Rows []struct {
			Cells []struct {
				R  string `xml:"r,attr"`
				T  string `xml:"t,attr"`
				V  string `xml:"v"`
				Is struct {
					T string `xml:"t"`
				} `xml:"is"`
			} `xml:"c"`
		} `xml:"sheetData>row"`
	}
	if xml.Unmarshal(b, &doc) != nil {
		return nil
	}
	var rows [][]string
	for _, row := range doc.Rows {
		cells := []string{}
		for _, c := range row.Cells {
			col := colIndex(c.R)
			for len(cells) <= col {
				cells = append(cells, "")
			}
			var val string
			switch c.T {
			case "s":
				if i, err := strconv.Atoi(c.V); err == nil && i >= 0 && i < len(shared) {
					val = shared[i]
				}
			case "inlineStr":
				val = c.Is.T
			default:
				val = c.V
			}
			cells[col] = strings.TrimSpace(val)
		}
		rows = append(rows, cells)
	}
	return rows
}

// colIndex converts a cell ref ("C5") to a 0-based column index.
func colIndex(ref string) int {
	n := 0
	for _, r := range ref {
		if r >= 'A' && r <= 'Z' {
			n = n*26 + int(r-'A'+1)
		} else if r >= 'a' && r <= 'z' {
			n = n*26 + int(r-'a'+1)
		} else {
			break
		}
	}
	if n > 0 {
		return n - 1
	}
	return 0
}
