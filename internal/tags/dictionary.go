// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package tags holds the built-in suggestion dictionary: the small set of
// well-known nicknames FireBin can recognise on your behalf.
//
// It exists because a user-maintained vocabulary only helps after you have
// already done the lookup once. The complaint that produced tags was "I forget
// which JST is the Qwiic one", and a tag you have to know to type does not
// answer that. A dictionary does.
//
// It only ever suggests. Nothing here writes a tag, and no caller may make it
// write one: a wrong suggestion you have to click past costs a second, and a
// wrong fact silently added to your inventory costs however long it takes to
// notice. The entries are kept few and boring for the same reason — this is a
// list of things that are true, not a list of things that might be.
package tags

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"
	"unicode"
)

//go:embed dictionary.json
var dictionaryJSON []byte

// Entry maps a recognisable part to the names people actually call it.
type Entry struct {
	// Tags are suggested together: a JST SH header is both "Qwiic" and
	// "STEMMA QT", and offering one without the other is half an answer.
	Tags []string `json:"tags"`
	// Why is shown to the user. A suggestion that cannot explain itself is
	// indistinguishable from a guess.
	Why string `json:"why"`
	// MPNPrefixes match case-insensitively against the part's manufacturer part
	// numbers. This is the reliable signal: an MPN is a fact about the part,
	// where its name is whatever someone typed.
	MPNPrefixes []string `json:"mpn_prefixes"`
	// AllOf requires every string to appear somewhere in the part's name,
	// package or description. The weaker signal, for parts whose identity lives
	// in prose rather than in an MPN. All of them, not any: a single loose term
	// like "header" would suggest DuPont for every connector in the drawer.
	AllOf []string `json:"all_of"`
}

var (
	once    sync.Once
	entries []Entry
)

// Dictionary returns the built-in entries, parsed once.
func Dictionary() []Entry {
	once.Do(func() {
		if err := json.Unmarshal(dictionaryJSON, &entries); err != nil {
			// The file is embedded and covered by a test, so a parse failure is
			// a build-time mistake, not a runtime condition. Degrade to no
			// suggestions rather than taking the process down over a hint.
			entries = nil
		}
	})
	return entries
}

// Part is what a suggestion is computed from.
type Part struct {
	Name        string
	Description string
	Package     string
	MPNs        []string
	// Existing are the tags the part already carries, in any spelling.
	Existing []string
}

// Suggestion is one dictionary hit, reduced to the tags the part does not have.
type Suggestion struct {
	Tags []string `json:"tags"`
	Why  string   `json:"why"`
}

// Suggest returns the dictionary entries matching a part, with tags it already
// carries removed. An entry whose tags are all already present is dropped
// entirely rather than returned empty.
//
// Folding uses the same rule as the tag slug (lowercase, letters and digits
// only), so a part tagged "stemma-qt" is not offered "STEMMA QT" again.
func Suggest(p Part) []Suggestion {
	have := map[string]bool{}
	for _, e := range p.Existing {
		have[fold(e)] = true
	}

	hay := strings.ToLower(strings.Join([]string{p.Name, p.Description, p.Package}, " "))
	mpns := make([]string, 0, len(p.MPNs))
	for _, m := range p.MPNs {
		mpns = append(mpns, strings.ToLower(strings.TrimSpace(m)))
	}

	out := []Suggestion{}
	for _, e := range Dictionary() {
		if !matches(e, hay, mpns) {
			continue
		}
		fresh := []string{}
		for _, t := range e.Tags {
			if !have[fold(t)] {
				fresh = append(fresh, t)
			}
		}
		if len(fresh) == 0 {
			continue
		}
		out = append(out, Suggestion{Tags: fresh, Why: e.Why})
	}
	return out
}

// matches reports whether an entry recognises this part. Either signal is
// enough on its own; they describe the same part from two directions.
func matches(e Entry, hay string, mpns []string) bool {
	for _, prefix := range e.MPNPrefixes {
		p := strings.ToLower(prefix)
		for _, m := range mpns {
			if strings.HasPrefix(m, p) {
				return true
			}
		}
	}
	if len(e.AllOf) == 0 {
		return false
	}
	for _, term := range e.AllOf {
		if !strings.Contains(hay, strings.ToLower(term)) {
			return false
		}
	}
	return true
}

// Slug folds a tag name to its identity: lowercased, with everything that is
// not a letter or a digit removed. "STEMMA QT", "stemma-qt" and "StemmaQT" all
// fold to "stemmaqt" and are therefore one tag, which is the whole point — a
// vocabulary that splits into three spellings finds a third of your parts each.
//
// Letters and digits are tested by Unicode class rather than by an ASCII range,
// so an accented or non-Latin name folds instead of being gutted: "Größe" keeps
// its letters rather than becoming "grse". The cost is that "café" and "cafe"
// stay distinct, which is the better failure — mangling a name is worse than
// declining to merge two that genuinely differ.
//
// Returns "" when nothing survives the fold (a name of only punctuation), which
// callers treat as "not a usable tag".
//
// This lives here, in the leaf package, so the repository and the dictionary
// share one definition. Two copies would drift, and the day they disagree is
// the day a suggestion offers a tag the part already carries.
func Slug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func fold(s string) string { return Slug(s) }
