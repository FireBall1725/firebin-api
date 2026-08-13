// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package datasheets

import (
	"strings"
	"unicode"

	"github.com/ledongthuc/pdf"
)

// documentTitle reads the title a PDF declares about itself, or "" if it has
// none worth having.
//
// Isolated behind its own recover because the Info dictionary is optional, may
// be an indirect reference to an object that is missing, and is exactly the
// corner of a malformed file that nobody validates. Losing the title must never
// cost the text extraction it rides along with.
func documentTitle(r *pdf.Reader) (out string) {
	defer func() {
		if recover() != nil {
			out = ""
		}
	}()
	return CleanTitle(r.Trailer().Key("Info").Key("Title").Text())
}

// titleSuffixes are the source-document extensions that give a declared title
// away as a filename. A PDF authored in Word or FrameMaker usually carries the
// original document's name, which is what the reader is already looking at as
// the filename and is not an improvement on it.
var titleSuffixes = []string{
	".doc", ".docx", ".pdf", ".fm", ".book", ".indd", ".qxd", ".cdr",
	".ai", ".ps", ".eps", ".tex", ".odt", ".rtf", ".xls", ".xlsx", ".pptx", ".vsd",
}

// titlePlaceholders are what an authoring tool writes when nobody set a title.
var titlePlaceholders = map[string]bool{
	"untitled": true, "untitled document": true, "unknown": true,
	"document": true, "document1": true, "no title": true, "title": true,
	"microsoft word": true, "pdf document": true, "adobe acrobat": true,
	"newdoc": true, "unnamed": true, "print": true,
}

// CleanTitle decides whether a declared PDF title is worth showing.
//
// Most of this function is refusal, and that is the point. A PDF's Title field
// is unvalidated metadata that authoring tools fill in badly: it is very often
// the source filename ("Microsoft Word - esp32c6_ds_v1.5.doc"), a placeholder
// ("Untitled"), or a layout-tool artifact. Showing one of those in place of the
// filename is a downgrade, and it would be a silent one, applied across a whole
// library by a background job. So the bar is deliberately high, and "" — meaning
// keep using the filename — is the safe answer whenever there is doubt.
//
// Returns the cleaned title, or "" to keep the filename.
func CleanTitle(raw string) string {
	// Collapse whitespace first: a title pulled out of a layout tool arrives with
	// newlines and runs of spaces from wherever it was typed.
	t := strings.Join(strings.Fields(raw), " ")
	t = strings.Trim(t, " \t-–—_·|")
	if t == "" {
		return ""
	}

	// Long enough to be a name, short enough not to be a paragraph. A few PDFs
	// put an abstract in this field.
	if n := len([]rune(t)); n < 3 || n > 200 {
		return ""
	}

	lower := strings.ToLower(t)
	if titlePlaceholders[lower] {
		return ""
	}
	// "Microsoft Word - foo", "PowerPoint Presentation - bar": the tool named
	// itself. What follows is the source filename, so the whole thing goes.
	if strings.HasPrefix(lower, "microsoft ") || strings.HasPrefix(lower, "powerpoint ") {
		return ""
	}
	for _, s := range titleSuffixes {
		if strings.HasSuffix(lower, s) {
			return ""
		}
	}
	// A path is a filename with more of the same problem.
	if strings.ContainsAny(t, "/\\") {
		return ""
	}

	// Mostly words and numbers, with at least a couple of letters in it. This
	// rejects "1", "()", "-------" and "0001-2345" without needing a rule for
	// each, while "ESP32-C6 Series Datasheet" and "AN2606" both pass. Spaces are
	// left out of the ratio so a long title is not penalised for having words.
	letters, alnum, dense := 0, 0, 0
	for _, r := range t {
		if unicode.IsSpace(r) {
			continue
		}
		dense++
		switch {
		case unicode.IsLetter(r):
			letters++
			alnum++
		case unicode.IsDigit(r):
			alnum++
		}
	}
	if letters < 2 || alnum*10 < dense*7 {
		return ""
	}
	return t
}
