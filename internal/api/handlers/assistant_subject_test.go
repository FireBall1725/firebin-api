// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"os"
	"sort"
	"strings"
	"testing"
)

// The Go allow-list and the database CHECK constraint have to agree.
//
// They drifted once and it cost a live 500: making the datasheet viewer a page
// taught the web to send subject_kind = 'datasheet', the constraint from
// migration 000031 rejected it, and every question asked on a datasheet page
// failed before reaching the model. Reading the migrations here is ugly, but it
// is the only thing that ties the two together, and this test runs without a
// database so it catches the drift on any machine.
func TestKnownSubjectKindsMatchTheConstraint(t *testing.T) {
	kinds, err := subjectKindsFromMigrations("../../db/migrations")
	if err != nil {
		t.Fatalf("reading the migrations: %v", err)
	}
	if len(kinds) == 0 {
		t.Fatal("found no subject_kind CHECK in the migrations; has the column been renamed?")
	}

	inGo := make([]string, 0, len(knownSubjectKinds))
	for k := range knownSubjectKinds {
		inGo = append(inGo, k)
	}
	sort.Strings(inGo)
	sort.Strings(kinds)

	if strings.Join(inGo, ",") != strings.Join(kinds, ",") {
		t.Errorf("knownSubjectKinds and the database CHECK disagree:\n  Go:       %v\n  migration: %v\n"+
			"Adding a subject kind needs BOTH a migration and an entry in knownSubjectKinds.", inGo, kinds)
	}
}

// subjectKindsFromMigrations returns the kinds allowed by the newest migration
// that redefines the constraint, so a later ALTER wins over the original CREATE.
func subjectKindsFromMigrations(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".up.sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // numbered prefixes sort chronologically

	var latest []string
	for _, n := range names {
		body, err := os.ReadFile(dir + "/" + n)
		if err != nil {
			return nil, err
		}
		if k, ok := parseSubjectKindCheck(string(body)); ok {
			latest = k
		}
	}
	return latest, nil
}

// parseSubjectKindCheck pulls the quoted values out of the last
// `subject_kind IN ( … )` in one migration.
func parseSubjectKindCheck(sql string) ([]string, bool) {
	const marker = "subject_kind IN ("
	idx := strings.LastIndex(sql, marker)
	if idx < 0 {
		return nil, false
	}
	rest := sql[idx+len(marker):]
	end := strings.Index(rest, ")")
	if end < 0 {
		return nil, false
	}
	out := []string{}
	for _, part := range strings.Split(rest[:end], ",") {
		v := strings.TrimSpace(part)
		v = strings.Trim(v, "'")
		if v != "" {
			out = append(out, v)
		}
	}
	return out, len(out) > 0
}

// An unknown kind must cost the conversation its subject, not the user their
// answer. Guards the shape of the map rather than the handler, which needs a
// database; the pairing above is what makes an unknown kind impossible in
// practice.
func TestUnknownSubjectKindIsNotAccepted(t *testing.T) {
	for _, bad := range []string{"datasheets", "Part", "location", "", "stock"} {
		if knownSubjectKinds[bad] {
			t.Errorf("%q should not be a known subject kind", bad)
		}
	}
	for _, good := range []string{"part", "project", "board", "datasheet"} {
		if !knownSubjectKinds[good] {
			t.Errorf("%q should be a known subject kind", good)
		}
	}
}
