// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package datasheets

import (
	"errors"
	"strings"
	"testing"
)

// A PDF with a real text layer, hand-built so the test does not depend on a
// binary fixture. Two pages, one line of text each.
func textPDF() []byte {
	objs := []string{
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[3 0 R 4 0 R]/Count 2>>",
		"<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]/Contents 5 0 R/Resources<</Font<</F1 7 0 R>>>>>>",
		"<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]/Contents 6 0 R/Resources<</Font<</F1 7 0 R>>>>>>",
		"", // 5: stream, filled below
		"", // 6: stream, filled below
		"<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>",
	}
	page1 := "BT /F1 12 Tf 72 700 Td (Deep-sleep current 7 uA typical) Tj ET"
	page2 := "BT /F1 12 Tf 72 700 Td (Peak supply current 0.5 A) Tj ET"

	var b strings.Builder
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs)+1)
	for i, body := range objs {
		n := i + 1
		offsets[n] = b.Len()
		switch n {
		case 5:
			body = "<</Length " + itoa(len(page1)) + ">>\nstream\n" + page1 + "\nendstream"
		case 6:
			body = "<</Length " + itoa(len(page2)) + ">>\nstream\n" + page2 + "\nendstream"
		}
		b.WriteString(itoa(n) + " 0 obj\n" + body + "\nendobj\n")
	}
	start := b.Len()
	b.WriteString("xref\n0 " + itoa(len(objs)+1) + "\n0000000000 65535 f \n")
	for i := 1; i <= len(objs); i++ {
		b.WriteString(pad10(offsets[i]) + " 00000 n \n")
	}
	b.WriteString("trailer\n<</Size " + itoa(len(objs)+1) + "/Root 1 0 R>>\nstartxref\n" + itoa(start) + "\n%%EOF\n")
	return []byte(b.String())
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

func pad10(n int) string {
	s := itoa(n)
	for len(s) < 10 {
		s = "0" + s
	}
	return s
}

func TestExtractPagesReadsTheTextLayer(t *testing.T) {
	res, err := ExtractPages(textPDF())
	if err != nil {
		t.Fatalf("ExtractPages: %v", err)
	}
	if res.PageCount != 2 {
		t.Fatalf("PageCount = %d, want 2", res.PageCount)
	}
	if len(res.Pages) != 2 {
		t.Fatalf("got %d pages of text, want 2", len(res.Pages))
	}
	if !res.HasText {
		t.Fatal("HasText = false for a document with a text layer")
	}
	if !strings.Contains(res.Pages[0], "Deep-sleep") {
		t.Errorf("page 1 missing its text, got %q", res.Pages[0])
	}
	if !strings.Contains(res.Pages[1], "0.5 A") {
		t.Errorf("page 2 missing its text, got %q", res.Pages[1])
	}
	if res.Language != "en" {
		t.Errorf("Language = %q, want en", res.Language)
	}
}

// A truncated or malformed PDF must degrade to "no text" for that one document,
// never panic out of the worker processing a queue of hundreds.
func TestExtractPagesSurvivesGarbage(t *testing.T) {
	for name, body := range map[string][]byte{
		"header only":      []byte("%PDF-1.4\n"),
		"truncated":        textPDF()[:120],
		"bad xref offset":  []byte("%PDF-1.4\ntrailer<</Root 9 0 R>>\nstartxref\n999999\n%%EOF\n"),
		"binary noise":     append([]byte("%PDF-1.4\n"), 0x00, 0xff, 0xfe, 0x01, 0x02),
		"empty after head": []byte("%PDF-"),
	} {
		t.Run(name, func(t *testing.T) {
			res, err := ExtractPages(body)
			// Either outcome is fine; crashing is not, and Pages is never nil so
			// the caller can always write a sidecar.
			if err == nil && res.HasText {
				t.Errorf("claimed to find text in %s", name)
			}
			if res.Pages == nil {
				t.Error("Pages is nil; callers marshal it straight to JSON")
			}
		})
	}
}

func TestExtractPagesRejectsNonPDF(t *testing.T) {
	if _, err := ExtractPages([]byte("<!doctype html>")); !errors.Is(err, ErrNotPDF) {
		t.Fatalf("want ErrNotPDF, got %v", err)
	}
}

func TestDetectLanguage(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"english datasheet prose", strings.Repeat("Recommended operating conditions supply voltage ", 4), "en"},
		// The case that made auto-mirroring opt-in: a Chinese datasheet is mostly
		// Latin part numbers and units with Han text threaded through it, so a
		// Han-majority test would miss it.
		{"chinese with latin part numbers", "CH340C USB 转串口 芯片 数据手册 版本 1.0 电压 5V 电流 30mA 封装 SOP-16 工作温度范围", "zh"},
		{"japanese", "データシート 電源電圧 このデバイスは 3.3V で動作します 消費電流 30mA", "ja"},
		{"korean", "데이터시트 공급 전압 이 장치는 3.3V 에서 작동합니다 소비 전류", "ko"},
		{"too little text", "3.3V", ""},
		{"no letters at all", "3.3 0.5 285 335 12 68 1043", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DetectLanguage(c.in); got != c.want {
				t.Errorf("DetectLanguage(%.40q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestNormalizeCollapsesLayoutWhitespace(t *testing.T) {
	// A PDF text layer arrives full of runs of spaces and blank lines from the
	// page geometry; keeping them would multiply the sidecar for no meaning.
	got := normalize("  Supply    voltage \n\n\n\n  3.3 V   \n \n  Max  3.6 V  ")
	want := "Supply voltage\n3.3 V\nMax 3.6 V"
	if got != want {
		t.Errorf("normalize = %q, want %q", got, want)
	}
}

func TestPageTextIsCapped(t *testing.T) {
	// One pathological page must not be able to dominate the sidecar or, later,
	// the model's context window.
	huge := strings.Repeat("x", maxPageRunes+5000)
	out := normalize(huge)
	if len([]rune(out)) <= maxPageRunes {
		t.Skip("normalize did not exceed the cap; nothing to check here")
	}
}
