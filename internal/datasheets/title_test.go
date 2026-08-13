// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package datasheets

import "testing"

func TestCleanTitleKeeps(t *testing.T) {
	cases := map[string]string{
		"ESP32-C6 Series Datasheet":            "ESP32-C6 Series Datasheet",
		"  ESP32-C6\n Series   Datasheet  ":    "ESP32-C6 Series Datasheet",
		"AN2606 Application Note":              "AN2606 Application Note",
		"AN2606":                               "AN2606",
		"STM32F407xx":                          "STM32F407xx",
		"— LM358 Dual Operational Amplifier —": "LM358 Dual Operational Amplifier",
		"数据手册 ESP32-C6":                        "数据手册 ESP32-C6",
	}
	for in, want := range cases {
		if got := CleanTitle(in); got != want {
			t.Errorf("CleanTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

// Everything here has to come back empty, meaning "keep the filename". These
// are not hypothetical: a PDF's Title field is unvalidated metadata and this is
// what authoring tools actually put in it.
func TestCleanTitleRejects(t *testing.T) {
	cases := []string{
		"",
		"   ",
		"Untitled",
		"untitled document",
		"Document1",
		"Microsoft Word - esp32c6_ds_en.doc",
		"Microsoft Word - Document1",
		"PowerPoint Presentation",
		"esp32-c6_datasheet_en.pdf",
		"LM358.fm",
		"chapter1.book",
		"C:\\docs\\lm358.indd",
		"/home/eng/lm358",
		"1",
		"--",
		"-------",
		"0001-2345",
		"()",
		"[ ]",
	}
	for _, in := range cases {
		if got := CleanTitle(in); got != "" {
			t.Errorf("CleanTitle(%q) = %q, want it rejected", in, got)
		}
	}
}

// A title long enough to be an abstract is not a title.
func TestCleanTitleLength(t *testing.T) {
	long := ""
	for len(long) < 250 {
		long += "datasheet "
	}
	if got := CleanTitle(long); got != "" {
		t.Errorf("a %d-character title was kept: %q", len(long), got)
	}
}
