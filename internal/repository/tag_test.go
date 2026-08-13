// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/firelabsca/firebin-api/internal/db"
	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/firelabsca/firebin-api/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestTagSlugFolds pins the identity fold. Everything else about tags rests on
// it: two spellings that fold together are one tag, and two that do not are two
// tags that each find half your parts.
func TestTagSlugFolds(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Qwiic", "qwiic"},
		{"qwiic", "qwiic"},
		{"  Qwiic  ", "qwiic"},
		{"STEMMA QT", "stemmaqt"},
		{"stemma-qt", "stemmaqt"},
		{"StemmaQT", "stemmaqt"},
		{"stemma_qt", "stemmaqt"},
		{"WS2812B", "ws2812b"},
		{"3.3V", "33v"},
		// Non-ASCII letters fold rather than being stripped, so a name is not
		// gutted down to its ASCII skeleton.
		{"Größe", "größe"},
		// Nothing survives, so it is not a usable tag.
		{"---", ""},
		{"", ""},
		{"   ", ""},
	}
	for _, c := range cases {
		if got := repository.TagSlug(c.in); got != c.want {
			t.Errorf("TagSlug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// tagTestPool brings up a migrated pool and truncates the tables these tests
// touch, both before and after. Needs a disposable Postgres via DATABASE_URL;
// skips when unset. CI provides one; do not point it at real data.
func tagTestPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	url := dbURL(t)
	if err := db.Migrate(url); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)

	truncate := func() {
		if _, err := pool.Exec(ctx, `TRUNCATE parts, tags CASCADE`); err != nil {
			t.Fatalf("truncate: %v", err)
		}
	}
	truncate()
	t.Cleanup(truncate)
	return pool
}

// TestResolveCollapsesSpellings covers the get-or-create path: three spellings
// of one name produce one tag, and asking again does not produce a second.
func TestResolveCollapsesSpellings(t *testing.T) {
	ctx := context.Background()
	repo := repository.NewTagRepo(tagTestPool(t, ctx))

	tags, err := repo.Resolve(ctx, []string{"STEMMA QT", "stemma-qt", "StemmaQT"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(tags) != 1 {
		t.Fatalf("got %d tags, want 1: %+v", len(tags), tags)
	}
	// The first spelling seen is the one that gets displayed.
	if tags[0].Name != "STEMMA QT" {
		t.Errorf("Name = %q, want %q", tags[0].Name, "STEMMA QT")
	}
	if tags[0].Slug != "stemmaqt" {
		t.Errorf("Slug = %q, want %q", tags[0].Slug, "stemmaqt")
	}

	again, err := repo.Resolve(ctx, []string{"stemma qt"})
	if err != nil {
		t.Fatalf("Resolve again: %v", err)
	}
	if len(again) != 1 || again[0].ID != tags[0].ID {
		t.Errorf("second Resolve made a new tag: %+v", again)
	}

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("vocabulary has %d tags, want 1: %+v", len(all), all)
	}

	// Punctuation-only names are not tags and must not create empty rows.
	none, err := repo.Resolve(ctx, []string{"---", "  "})
	if err != nil {
		t.Fatalf("Resolve punctuation: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("got %d tags for unusable names, want 0", len(none))
	}
}

// TestCreateRejectsASecondSpelling covers the explicit create path, which
// reports a conflict rather than quietly adding a near-duplicate.
func TestCreateRejectsASecondSpelling(t *testing.T) {
	ctx := context.Background()
	repo := repository.NewTagRepo(tagTestPool(t, ctx))

	if _, err := repo.Create(ctx, "Qwiic", nil, nil); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := repo.Create(ctx, "qwiic", nil, nil); !errors.Is(err, repository.ErrConflict) {
		t.Errorf("Create duplicate: err = %v, want ErrConflict", err)
	}
	if _, err := repo.Create(ctx, "!!!", nil, nil); !errors.Is(err, repository.ErrInvalid) {
		t.Errorf("Create unusable: err = %v, want ErrInvalid", err)
	}
}

// TestBothSearchPathsFindATaggedPart is the regression this feature exists to
// avoid.
//
// GET /parts and GET /parts/search are two different queries, and they carried
// byte-identical copies of the free-text predicate until partSearchClause pulled
// them together. If they drift, "qwiic" finds the part from the parts page and
// returns nothing the moment a package filter is typed, which reads as the part
// having vanished.
func TestBothSearchPathsFindATaggedPart(t *testing.T) {
	ctx := context.Background()
	pool := tagTestPool(t, ctx)

	const connID = "cccccccc-0000-0000-0000-0000000000e1"
	const otherID = "cccccccc-0000-0000-0000-0000000000e2"
	mustExec(t, pool, ctx,
		`INSERT INTO parts (id, name, package) VALUES ($1, 'SM04B-SRSS-TB', 'SH 1.0mm'), ($2, 'CH340C', 'SOP-16')`,
		connID, otherID)

	tags := repository.NewTagRepo(pool)
	if _, err := tags.SetPartTags(ctx, uuid.MustParse(connID), []string{"Qwiic", "STEMMA QT"}); err != nil {
		t.Fatalf("SetPartTags: %v", err)
	}

	parts := repository.NewPartRepo(pool)

	list, err := parts.List(ctx, repository.ListOptions{TopLevel: true, Search: "qwiic"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID.String() != connID {
		t.Errorf("List(search=qwiic) returned %d parts, want just the connector: %+v", len(list), list)
	}

	matches, err := parts.SearchParametric(ctx, repository.ParametricOptions{Search: "qwiic"})
	if err != nil {
		t.Fatalf("SearchParametric: %v", err)
	}
	if len(matches) != 1 || matches[0].ID.String() != connID {
		t.Errorf("SearchParametric(search=qwiic) returned %d parts, want just the connector", len(matches))
	}

	// The tag is a way in, not an identity: it must not have touched the name.
	if list[0].Name != "SM04B-SRSS-TB" {
		t.Errorf("Name = %q, want the real part number", list[0].Name)
	}

	// A tag on one part does not drag in the others.
	none, err := parts.List(ctx, repository.ListOptions{TopLevel: true, Search: "stemma"})
	if err != nil {
		t.Fatalf("List stemma: %v", err)
	}
	if len(none) != 1 {
		t.Errorf("List(search=stemma) returned %d parts, want 1", len(none))
	}
}

// TestSetAddRemovePartTags covers the three write shapes: replace, add without
// disturbing what is there, and remove without deleting the tag itself.
func TestSetAddRemovePartTags(t *testing.T) {
	ctx := context.Background()
	pool := tagTestPool(t, ctx)

	const partID = "cccccccc-0000-0000-0000-0000000000e3"
	mustExec(t, pool, ctx, `INSERT INTO parts (id, name) VALUES ($1, 'SM04B-SRSS-TB')`, partID)
	id := uuid.MustParse(partID)
	repo := repository.NewTagRepo(pool)

	names := func(tags []models.Tag) []string {
		out := make([]string, len(tags))
		for i, t := range tags {
			out[i] = t.Name
		}
		return out
	}
	same := func(got []models.Tag, want ...string) bool {
		if len(got) != len(want) {
			return false
		}
		for i, w := range want {
			if got[i].Name != w {
				return false
			}
		}
		return true
	}

	got, err := repo.SetPartTags(ctx, id, []string{"Qwiic", "i2c"})
	if err != nil {
		t.Fatalf("SetPartTags: %v", err)
	}
	if !same(got, "i2c", "Qwiic") { // ordered by name
		t.Fatalf("after set: %v", names(got))
	}

	got, err = repo.AddPartTags(ctx, id, []string{"STEMMA QT"})
	if err != nil {
		t.Fatalf("AddPartTags: %v", err)
	}
	if !same(got, "i2c", "Qwiic", "STEMMA QT") {
		t.Errorf("add dropped an existing tag: %v", names(got))
	}

	got, err = repo.RemovePartTags(ctx, id, []string{"i2c", "never-applied"})
	if err != nil {
		t.Fatalf("RemovePartTags: %v", err)
	}
	if !same(got, "Qwiic", "STEMMA QT") {
		t.Errorf("after remove: %v", names(got))
	}

	// Removing a tag from a part leaves it in the vocabulary for other parts.
	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("vocabulary has %d tags, want 3: %v", len(all), names(all))
	}

	// Replacing with the empty set clears the part without touching the words.
	got, err = repo.SetPartTags(ctx, id, nil)
	if err != nil {
		t.Fatalf("SetPartTags empty: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("after clearing: %v", names(got))
	}
}

// TestMergeMovesEveryPart covers reconciling two spellings that both escaped
// into use, including a part that already carried both.
func TestMergeMovesEveryPart(t *testing.T) {
	ctx := context.Background()
	pool := tagTestPool(t, ctx)

	const aID = "cccccccc-0000-0000-0000-0000000000e4"
	const bID = "cccccccc-0000-0000-0000-0000000000e5"
	mustExec(t, pool, ctx,
		`INSERT INTO parts (id, name) VALUES ($1, 'SM04B-SRSS-TB'), ($2, 'JST SH cable')`, aID, bID)
	repo := repository.NewTagRepo(pool)

	// Two genuinely different words that mean the same connector.
	stemma, err := repo.Create(ctx, "STEMMA QT", nil, nil)
	if err != nil {
		t.Fatalf("Create stemma: %v", err)
	}
	qwiic, err := repo.Create(ctx, "Qwiic", nil, nil)
	if err != nil {
		t.Fatalf("Create qwiic: %v", err)
	}
	if _, err := repo.SetPartTags(ctx, uuid.MustParse(aID), []string{"STEMMA QT", "Qwiic"}); err != nil {
		t.Fatalf("SetPartTags a: %v", err)
	}
	if _, err := repo.SetPartTags(ctx, uuid.MustParse(bID), []string{"STEMMA QT"}); err != nil {
		t.Fatalf("SetPartTags b: %v", err)
	}

	if err := repo.Merge(ctx, stemma.ID, qwiic.ID); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	// The part that carried both survives the move rather than failing it.
	for _, id := range []string{aID, bID} {
		got, err := repo.TagsForPart(ctx, uuid.MustParse(id))
		if err != nil {
			t.Fatalf("TagsForPart %s: %v", id, err)
		}
		if len(got) != 1 || got[0].ID != qwiic.ID {
			t.Errorf("part %s has %d tags, want just Qwiic", id, len(got))
		}
	}
	if _, err := repo.Get(ctx, stemma.ID); !errors.Is(err, repository.ErrNotFound) {
		t.Errorf("merged-away tag still exists: err = %v", err)
	}
	if err := repo.Merge(ctx, qwiic.ID, qwiic.ID); !errors.Is(err, repository.ErrInvalid) {
		t.Errorf("self-merge: err = %v, want ErrInvalid", err)
	}
}

// TestRenameOntoAnotherTagConflicts pins that a rename never merges by accident.
// Collapsing two tags silently would move every part on one of them with no way
// back, so it has to be the separate, deliberate call.
func TestRenameOntoAnotherTagConflicts(t *testing.T) {
	ctx := context.Background()
	repo := repository.NewTagRepo(tagTestPool(t, ctx))

	if _, err := repo.Create(ctx, "Qwiic", nil, nil); err != nil {
		t.Fatalf("Create: %v", err)
	}
	stemma, err := repo.Create(ctx, "STEMMA QT", nil, nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	name := "qwiic"
	if _, err := repo.Update(ctx, stemma.ID, &name, nil, nil); !errors.Is(err, repository.ErrConflict) {
		t.Errorf("rename onto an existing tag: err = %v, want ErrConflict", err)
	}

	// A rename that does not collide still works, and keeps the id.
	renamed := "Stemma QT"
	got, err := repo.Update(ctx, stemma.ID, &renamed, nil, nil)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.ID != stemma.ID || got.Name != renamed || got.Slug != "stemmaqt" {
		t.Errorf("after rename: %+v", got)
	}
}

// TestTagsSurviveABackupRoundTrip is the guard for the backup allow-list.
//
// backupTables is hand-maintained, and a table missing from it is not an error
// anywhere: the export simply does not contain it, so tags would come back from
// a restore silently gone. Feeding part_tags in before parts also proves the
// explicit DEFERRABLE on its foreign keys, which tables created after migration
// 000023 do not get for free.
func TestTagsSurviveABackupRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := tagTestPool(t, ctx)

	const partID = "cccccccc-0000-0000-0000-0000000000e7"
	mustExec(t, pool, ctx, `INSERT INTO parts (id, name) VALUES ($1, 'SM04B-SRSS-TB')`, partID)
	repo := repository.NewTagRepo(pool)
	if _, err := repo.SetPartTags(ctx, uuid.MustParse(partID), []string{"Qwiic", "STEMMA QT"}); err != nil {
		t.Fatalf("SetPartTags: %v", err)
	}

	backup := repository.NewBackupRepo(pool)
	full, err := backup.ExportAll(ctx)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	for _, table := range []string{"tags", "part_tags"} {
		if len(full[table]) == 0 || string(full[table]) == "[]" {
			t.Fatalf("export has no %s rows; is it missing from backupTables?", table)
		}
	}

	mustExec(t, pool, ctx, `TRUNCATE parts, tags CASCADE`)

	// Child before parent, so the restore leans on deferred checks.
	if _, err := backup.ImportAll(ctx, map[string]json.RawMessage{
		"part_tags": full["part_tags"],
		"tags":      full["tags"],
		"parts":     full["parts"],
	}, false); err != nil {
		t.Fatalf("import: %v", err)
	}

	got, err := repo.TagsForPart(ctx, uuid.MustParse(partID))
	if err != nil {
		t.Fatalf("TagsForPart: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("after restore the part has %d tags, want 2", len(got))
	}
	if got[0].Name != "Qwiic" || got[1].Name != "STEMMA QT" {
		t.Errorf("after restore: %+v", got)
	}
}

// TestDeletingAPartDropsItsTagLinks covers the cascade. The words stay in the
// vocabulary; only this part's claim on them goes.
func TestDeletingAPartDropsItsTagLinks(t *testing.T) {
	ctx := context.Background()
	pool := tagTestPool(t, ctx)

	const partID = "cccccccc-0000-0000-0000-0000000000e6"
	mustExec(t, pool, ctx, `INSERT INTO parts (id, name) VALUES ($1, 'SM04B-SRSS-TB')`, partID)
	repo := repository.NewTagRepo(pool)
	if _, err := repo.SetPartTags(ctx, uuid.MustParse(partID), []string{"Qwiic"}); err != nil {
		t.Fatalf("SetPartTags: %v", err)
	}

	mustExec(t, pool, ctx, `DELETE FROM parts WHERE id = $1`, partID)

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("got %d tags, want the word to survive", len(all))
	}
	if all[0].PartCount != 0 {
		t.Errorf("PartCount = %d, want 0 after the part was deleted", all[0].PartCount)
	}
}
