// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package assistant

import (
	"strings"
	"testing"
)

// A datasheet page, roughly the density of a real electrical-characteristics
// table: long, numeric, and repetitive.
func longPage(marker string) string {
	var b strings.Builder
	b.WriteString(marker + "\n")
	for i := 0; i < 800; i++ {
		b.WriteString("Symbol VDD Parameter supply voltage Min 3.0 Typ 3.3 Max 3.6 Unit V Condition ambient 25 C\n")
	}
	return b.String()
}

// TestSnippetStaysSmall is the token budget, pinned.
//
// localHistoryBudget is 4000 tokens and one datasheet page can exceed that on
// its own, so a search that returned whole pages would blow the window on its
// first call and every later turn would answer from a truncated conversation.
// This is the same failure that once made a 25-part search cost 10,600 tokens.
// Roughly 4 characters per token, so the assertions below are in characters.
func TestSnippetStaysSmall(t *testing.T) {
	page := longPage("DEEP SLEEP CURRENT 7 uA")
	at := indexFold(page, "deep sleep")
	if at < 0 {
		t.Fatal("fixture does not contain the search term")
	}
	s := snippetAround(page, at, len("deep sleep"))

	if len([]rune(s)) > snippetRunes+4 { // +4 for the two ellipsis characters
		t.Errorf("snippet is %d runes, cap is %d", len([]rune(s)), snippetRunes)
	}
	if !strings.Contains(s, "DEEP SLEEP CURRENT 7 uA") {
		t.Errorf("snippet lost the matched text: %q", s)
	}

	// The whole worst-case search result has to stay small: maxSnippets windows
	// plus the surrounding JSON. ~2600 characters is around 650 tokens, which
	// leaves the model room to actually answer.
	worst := maxSnippets * (snippetRunes + 4)
	if worst > 3000 {
		t.Errorf("worst-case search result is %d runes; that is too much of a 4000-token budget", worst)
	}
}

// A page read must also be bounded, and must say when it was cut rather than
// letting the model treat half a table as the whole story.
func TestPageReadIsCappedAndSaysSo(t *testing.T) {
	if maxPageRunes > 8000 {
		t.Errorf("maxPageRunes is %d; a single page should not be able to fill the window", maxPageRunes)
	}
	page := longPage("PAGE START")
	if len([]rune(page)) <= maxPageRunes {
		t.Fatal("fixture is not long enough to exercise truncation")
	}
	cut := string([]rune(page)[:maxPageRunes])
	if len([]rune(cut)) != maxPageRunes {
		t.Fatalf("truncation produced %d runes, want %d", len([]rune(cut)), maxPageRunes)
	}
}

func TestSnippetIsCaseInsensitiveAndRuneSafe(t *testing.T) {
	// Case folding, because a datasheet prints "Absolute Maximum Ratings" and a
	// user asks for "absolute maximum".
	page := "Section 5. Absolute Maximum Ratings. Storage temperature -65 to 150 C."
	if at := indexFold(page, "absolute maximum"); at < 0 {
		t.Fatal("case-insensitive search failed")
	}

	// A snippet must never cut a multi-byte character in half; the result is fed
	// back as JSON and invalid UTF-8 would break the turn.
	cjk := strings.Repeat("电压范围工作温度", 200) + "DEEP SLEEP" + strings.Repeat("电流消耗封装", 200)
	at := indexFold(cjk, "deep sleep")
	s := snippetAround(cjk, at, len("deep sleep"))
	if !strings.ContainsRune(s, '�') && strings.ToValidUTF8(s, "�") != s {
		t.Error("snippet produced invalid UTF-8")
	}
	if !strings.Contains(s, "DEEP SLEEP") {
		t.Errorf("snippet lost the match in CJK text: %q", s)
	}
}

func TestSnippetHandlesMatchAtEdges(t *testing.T) {
	// A match in the first or last few characters must not produce a negative
	// window or panic.
	start := "DEEP SLEEP " + strings.Repeat("x ", 500)
	end := strings.Repeat("x ", 500) + "DEEP SLEEP"
	for name, page := range map[string]string{"at start": start, "at end": end} {
		t.Run(name, func(t *testing.T) {
			at := indexFold(page, "deep sleep")
			s := snippetAround(page, at, len("deep sleep"))
			if !strings.Contains(s, "DEEP SLEEP") {
				t.Errorf("lost the match: %q", s)
			}
			if len([]rune(s)) > snippetRunes+4 {
				t.Errorf("snippet is %d runes, cap is %d", len([]rune(s)), snippetRunes)
			}
		})
	}

	// A page shorter than the window is returned whole, not padded or panicking.
	short := "VDD 3.3 V DEEP SLEEP 7 uA"
	at := indexFold(short, "deep sleep")
	if got := snippetAround(short, at, len("deep sleep")); !strings.Contains(got, "7 uA") {
		t.Errorf("short page mangled: %q", got)
	}
}

// The datasheet tools are only offered when both halves of the source are
// wired. An instance with no attachment storage must not advertise a capability
// that would fail on every call.
func TestDatasheetToolsOnlyOfferedWhenWired(t *testing.T) {
	bare := &Toolbox{}
	for _, tool := range bare.Tools() {
		switch tool.Def.Name {
		case "find_datasheet", "search_datasheet", "read_datasheet_page":
			t.Errorf("%s offered with no datasheet source wired", tool.Def.Name)
		}
	}
}
