// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package tags_test

import (
	"slices"
	"testing"

	"github.com/firelabsca/firebin-api/internal/tags"
)

// TestDictionaryParses guards the embedded JSON. It is read once at first use
// and a parse failure degrades silently to no suggestions, which would be a
// feature quietly doing nothing rather than an error anyone notices.
func TestDictionaryParses(t *testing.T) {
	d := tags.Dictionary()
	if len(d) == 0 {
		t.Fatal("dictionary is empty; did dictionary.json fail to parse?")
	}
	for _, e := range d {
		if len(e.Tags) == 0 {
			t.Errorf("entry with no tags: %+v", e)
		}
		if e.Why == "" {
			t.Errorf("entry %v has no explanation; a suggestion that cannot say why is a guess", e.Tags)
		}
		if len(e.MPNPrefixes) == 0 && len(e.AllOf) == 0 {
			t.Errorf("entry %v matches nothing", e.Tags)
		}
	}
}

func names(sug []tags.Suggestion) []string {
	out := []string{}
	for _, s := range sug {
		out = append(out, s.Tags...)
	}
	return out
}

func has(sug []tags.Suggestion, want string) bool {
	return slices.Contains(names(sug), want)
}

// TestSuggestsFromMPN covers the reliable signal. A part's MPN is a fact about
// it; its name is whatever someone typed.
func TestSuggestsFromMPN(t *testing.T) {
	got := tags.Suggest(tags.Part{
		Name: "4 pin connector",
		MPNs: []string{"SM04B-SRSS-TB(LF)(SN)"},
	})
	if !has(got, "Qwiic") || !has(got, "STEMMA QT") {
		t.Errorf("JST SH header suggested %v, want both Qwiic and STEMMA QT", names(got))
	}

	got = tags.Suggest(tags.Part{Name: "Addressable LED", MPNs: []string{"WS2812B-V5"}})
	if !has(got, "NeoPixel") {
		t.Errorf("WS2812B suggested %v, want NeoPixel", names(got))
	}
}

// TestSuggestsNothingForAnUnrelatedPart is the case that matters most. A
// dictionary that fires on ordinary parts trains you to dismiss it, and then it
// is worse than not existing.
func TestSuggestsNothingForAnUnrelatedPart(t *testing.T) {
	for _, p := range []tags.Part{
		{Name: "10k Resistor", Package: "0603", MPNs: []string{"RC0603FR-0710KL"}},
		{Name: "CH340C", Package: "SOP-16", MPNs: []string{"CH340C"}},
		{Name: "100nF Capacitor", Package: "0402", MPNs: []string{"CL05B104KO5NNNC"}},
		// A connector, but not one of the recognised ones. "header" alone must
		// not be enough or every pin strip in the drawer gets a suggestion.
		{Name: "IDC header", Package: "2x5", Description: "shrouded box header"},
	} {
		if got := tags.Suggest(p); len(got) != 0 {
			t.Errorf("%q suggested %v, want nothing", p.Name, names(got))
		}
	}
}

// TestAllOfNeedsEveryTerm pins the AND. A single loose term would make the text
// matcher fire constantly.
func TestAllOfNeedsEveryTerm(t *testing.T) {
	if got := tags.Suggest(tags.Part{Name: "2.54mm pin header, 40 way"}); !has(got, "DuPont") {
		t.Errorf("2.54 header suggested %v, want DuPont", names(got))
	}
	if got := tags.Suggest(tags.Part{Name: "2.54mm screw terminal"}); len(got) != 0 {
		t.Errorf("2.54 without 'header' suggested %v, want nothing", names(got))
	}
	if got := tags.Suggest(tags.Part{Name: "1.27mm header"}); len(got) != 0 {
		t.Errorf("header without 2.54 suggested %v, want nothing", names(got))
	}
}

// TestExistingTagsAreNotSuggestedAgain covers the fold: a part tagged
// "stemma-qt" must not be offered "STEMMA QT".
func TestExistingTagsAreNotSuggestedAgain(t *testing.T) {
	got := tags.Suggest(tags.Part{
		MPNs:     []string{"SM04B-SRSS-TB"},
		Existing: []string{"stemma-qt"},
	})
	if has(got, "STEMMA QT") {
		t.Errorf("suggested a tag the part already carries in another spelling: %v", names(got))
	}
	if !has(got, "Qwiic") {
		t.Errorf("dropped the tag the part does not have: %v", names(got))
	}

	// Every tag already present means no suggestion at all, not an empty one.
	got = tags.Suggest(tags.Part{
		MPNs:     []string{"SM04B-SRSS-TB"},
		Existing: []string{"Qwiic", "STEMMA QT"},
	})
	if len(got) != 0 {
		t.Errorf("fully tagged part suggested %v, want nothing", names(got))
	}
}

// TestSlug pins the fold shared with the repository layer. TagSlug delegates
// here, so these are the cases both sides obey.
func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Qwiic":     "qwiic",
		"  Qwiic  ": "qwiic",
		"STEMMA QT": "stemmaqt",
		"stemma-qt": "stemmaqt",
		"StemmaQT":  "stemmaqt",
		"WS2812B":   "ws2812b",
		"3.3V":      "33v",
		"Größe":     "größe",
		"---":       "",
		"":          "",
	}
	for in, want := range cases {
		if got := tags.Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}
