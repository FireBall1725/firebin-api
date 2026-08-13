// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package datasheets

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

// ExtractResult is what one extraction run learned about a document.
type ExtractResult struct {
	// Pages is the plain text of each page, index 0 = page 1. Always non-nil.
	Pages []string
	// PageCount is what the PDF itself reports, which can exceed len(Pages) if
	// some pages failed to parse.
	PageCount int
	// Language is a script-based guess, empty when nothing was legible.
	Language string
	// HasText is false for a document with pages but no text layer at all: a
	// scan. That is a normal outcome, not a failure.
	HasText bool
	// Title is what the PDF says it is called, once it has survived DocumentTitle.
	// Empty when it declares nothing usable, which is most of the time.
	Title string
}

// maxPageRunes caps how much text is kept per page.
//
// A datasheet page is normally well under this. The cap exists for the
// pathological ones (a 400-row parameter table rendered as one page) so that a
// single page cannot dominate the sidecar or, later, the model's context.
const maxPageRunes = 20000

// ExtractPages pulls the text layer out of a PDF, one string per page.
//
// Never returns an error for a document that simply has no text: that is a scan,
// reported as HasText false, and the caller records no_text_layer rather than a
// failure. An error here means the file could not be parsed as a PDF at all.
//
// Recovers from panics on purpose. The parser indexes into structures it assumes
// are well-formed, and real datasheets include enough malformed and
// non-conforming files that a panic is a question of when. A crashed extraction
// must degrade to "no text" for one document, never take down the worker
// processing a queue of hundreds.
func ExtractPages(content []byte) (res ExtractResult, err error) {
	res.Pages = []string{}
	defer func() {
		if r := recover(); r != nil {
			// Keep whatever pages were read before the panic; they are still useful.
			err = fmt.Errorf("pdf parser failed: %v", r)
		}
	}()

	if !IsPDF(content) {
		return res, ErrNotPDF
	}

	r, err := pdf.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return res, err
	}
	res.PageCount = r.NumPage()
	if res.PageCount < 0 {
		res.PageCount = 0
	}
	res.Title = documentTitle(r)

	// Fonts are cached across pages: parsing a charmap is the expensive part and
	// a datasheet reuses the same handful of fonts throughout.
	fonts := make(map[string]*pdf.Font)
	var all strings.Builder

	for i := 1; i <= res.PageCount; i++ {
		text := pageText(r, i, fonts)
		text = normalize(text)
		if len([]rune(text)) > maxPageRunes {
			text = string([]rune(text)[:maxPageRunes]) + "\n[page truncated]"
		}
		res.Pages = append(res.Pages, text)
		if text != "" {
			res.HasText = true
			if all.Len() < 40000 { // enough of a sample to judge the script
				all.WriteString(text)
			}
		}
	}

	res.Language = DetectLanguage(all.String())
	return res, nil
}

// pageText reads one page, isolating a per-page panic so one bad page does not
// cost the rest of the document.
func pageText(r *pdf.Reader, i int, fonts map[string]*pdf.Font) (out string) {
	defer func() {
		if recover() != nil {
			out = ""
		}
	}()
	p := r.Page(i)
	if p.V.IsNull() {
		return ""
	}
	for _, name := range p.Fonts() {
		if _, ok := fonts[name]; !ok {
			f := p.Font(name)
			fonts[name] = &f
		}
	}
	text, err := p.GetPlainText(fonts)
	if err != nil {
		return ""
	}
	return text
}

// normalize collapses the whitespace a PDF text layer is full of. Extracted text
// arrives with runs of spaces and blank lines from the page layout, which carry
// no meaning once the geometry is gone and would otherwise triple the sidecar.
func normalize(s string) string {
	// A byte slice rather than a strings.Builder because a newline has to be able
	// to drop the space already written before it; a Builder cannot un-write.
	b := make([]byte, 0, len(s))
	lastSpace, lastNewline := false, true
	for _, r := range s {
		switch {
		case r == '\n' || r == '\r':
			if lastSpace {
				b = b[:len(b)-1] // no trailing space before a line break
				lastSpace = false
			}
			if !lastNewline {
				b = append(b, '\n')
				lastNewline = true
			}
		case unicode.IsSpace(r):
			if !lastSpace && !lastNewline {
				b = append(b, ' ')
				lastSpace = true
			}
		default:
			b = utf8.AppendRune(b, r)
			lastSpace, lastNewline = false, false
		}
	}
	return strings.TrimSpace(string(b))
}

// DetectLanguage guesses a document's language from the script it is written in.
//
// Script, not language, and named for what the caller wants rather than what it
// measures: it cannot tell Simplified from Traditional Chinese, or English from
// German. That is enough for the job it has, which is warning that a mirrored
// datasheet is not in a language you read — the case that made auto-mirroring
// opt-in. Latin script returns "en" as the useful default rather than a claim.
//
// Returns "" when there is too little text to judge.
func DetectLanguage(s string) string {
	var han, kana, hangul, cyrillic, greek, latin, total int
	for _, r := range s {
		if !unicode.IsLetter(r) {
			continue
		}
		total++
		switch {
		case unicode.Is(unicode.Han, r):
			han++
		case unicode.Is(unicode.Hiragana, r), unicode.Is(unicode.Katakana, r):
			kana++
		case unicode.Is(unicode.Hangul, r):
			hangul++
		case unicode.Is(unicode.Cyrillic, r):
			cyrillic++
		case unicode.Is(unicode.Greek, r):
			greek++
		case r < unicode.MaxASCII || unicode.Is(unicode.Latin, r):
			latin++
		}
		if total > 20000 {
			break // a sample this size has already settled it
		}
	}
	if total < 20 {
		return ""
	}
	// Kana before Han: Japanese technical text is mostly Han characters, so
	// testing Han first would label every Japanese datasheet Chinese. Any
	// meaningful kana presence settles it.
	if kana*50 > total {
		return "ja"
	}
	if hangul*10 > total*2 {
		return "ko"
	}
	// A low bar on purpose. A Chinese datasheet carries a lot of Latin text
	// (part numbers, units, pin names), so requiring a Han majority would miss
	// exactly the documents this exists to flag.
	if han*10 > total {
		return "zh"
	}
	if cyrillic*10 > total*3 {
		return "ru"
	}
	if greek*10 > total*3 {
		return "el"
	}
	if latin*10 > total*5 {
		return "en"
	}
	return ""
}
