// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"testing"

	"github.com/firelabsca/firebin-api/internal/models"
)

func tag(name string, count int) models.Tag {
	return models.Tag{Name: name, Slug: slugOf(name), PartCount: count}
}

// slugOf mirrors what the repository stores, so the fixtures look like rows.
func slugOf(name string) string {
	out := []rune{}
	for _, r := range name {
		switch {
		case r >= 'A' && r <= 'Z':
			out = append(out, r+32)
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			out = append(out, r)
		}
	}
	return string(out)
}

func namesOf(tags []models.Tag) []string {
	out := []string{}
	for _, t := range tags {
		out = append(out, t.Name)
	}
	return out
}

func eq(a []string, b ...string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestTagsNamedByMatchesWholeWords is the guard on the loosest rung in
// resolveMatch. Tags sit below value+footprint precisely because they are the
// fuzzy one, and a tag that matched as a substring would claim most of a BOM.
func TestTagsNamedByMatchesWholeWords(t *testing.T) {
	vocab := []models.Tag{tag("Qwiic", 1), tag("i2c", 3), tag("QT", 1)}

	got := namesOf(tagsNamedBy("Qwiic connector", "", vocab))
	if !eq(got, "Qwiic") {
		t.Errorf("\"Qwiic connector\" named %v, want just Qwiic", got)
	}

	// Case and punctuation fold, the same way they do everywhere else.
	if got := namesOf(tagsNamedBy("QWIIC", "", vocab)); !eq(got, "Qwiic") {
		t.Errorf("uppercase named %v, want Qwiic", got)
	}

	// A tag must not match inside a longer word.
	if got := namesOf(tagsNamedBy("Qwiicish widget", "", vocab)); len(got) != 0 {
		t.Errorf("substring named %v, want nothing", got)
	}
	if got := namesOf(tagsNamedBy("SQT-110-01-L-D", "", vocab)); len(got) != 0 {
		t.Errorf("a header part number named %v, want nothing", got)
	}

	if got := namesOf(tagsNamedBy("", "", vocab)); len(got) != 0 {
		t.Errorf("an empty line named %v, want nothing", got)
	}
}

// TestTagsNamedByMatchesAPhrase covers the multi-word case. "STEMMA QT" has to
// be matched as a phrase, or the "QT" half alone would claim it.
func TestTagsNamedByMatchesAPhrase(t *testing.T) {
	vocab := []models.Tag{tag("STEMMA QT", 1), tag("Qwiic", 1)}

	if got := namesOf(tagsNamedBy("STEMMA QT connector", "", vocab)); !eq(got, "STEMMA QT") {
		t.Errorf("phrase named %v, want STEMMA QT", got)
	}
	// The spelling a BOM actually carries varies; the fold handles it.
	if got := namesOf(tagsNamedBy("stemma-qt", "", vocab)); !eq(got, "STEMMA QT") {
		t.Errorf("hyphenated named %v, want STEMMA QT", got)
	}
	// Half the phrase is not the phrase.
	if got := namesOf(tagsNamedBy("STEMMA cable", "", vocab)); len(got) != 0 {
		t.Errorf("half a phrase named %v, want nothing", got)
	}
}

// TestTagsNamedByReportsEveryHit is what makes the ambiguity check possible.
// tagMatch declines unless exactly one tag is named, so this has to return all
// of them rather than picking one.
func TestTagsNamedByReportsEveryHit(t *testing.T) {
	vocab := []models.Tag{tag("Qwiic", 1), tag("STEMMA QT", 1)}
	got := namesOf(tagsNamedBy("Qwiic / STEMMA QT connector", "", vocab))
	if !eq(got, "Qwiic", "STEMMA QT") {
		t.Errorf("named %v, want both so the caller can decline", got)
	}
}

// TestTagsNamedByReadsTheDescription covers the second field. A KiCad BOM often
// carries the human wording in Description rather than in Value.
func TestTagsNamedByReadsTheDescription(t *testing.T) {
	vocab := []models.Tag{tag("Qwiic", 1)}
	if got := namesOf(tagsNamedBy("CONN", "Qwiic breakout header", vocab)); !eq(got, "Qwiic") {
		t.Errorf("description named %v, want Qwiic", got)
	}
}
