// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package auth

import "testing"

// The prefixes are the boundary between a credential that can do anything the
// account can and one that can only read the parts catalogue. These tests exist
// so that boundary cannot be blurred by a later edit.

func TestKicadTokenAndPATAreDistinguishable(t *testing.T) {
	pat, _, _, err := GeneratePAT()
	if err != nil {
		t.Fatalf("GeneratePAT: %v", err)
	}
	kicad, _, _, err := GenerateKicadToken()
	if err != nil {
		t.Fatalf("GenerateKicadToken: %v", err)
	}

	if !IsPAT(pat) {
		t.Errorf("IsPAT(%q) = false, want true", pat)
	}
	if !IsKicadToken(kicad) {
		t.Errorf("IsKicadToken(%q) = false, want true", kicad)
	}

	// The load-bearing direction. A KiCad token must never satisfy IsPAT: the
	// bearer path would then look it up in api_tokens and, on a hash collision
	// or a future shared table, grant account authority to a credential that
	// lives in plaintext on a workstation.
	if IsPAT(kicad) {
		t.Errorf("IsPAT(%q) = true; a KiCad token must not read as a PAT", kicad)
	}
	if IsKicadToken(pat) {
		t.Errorf("IsKicadToken(%q) = true; a PAT must not read as a KiCad token", pat)
	}
}

func TestKicadTokensAreUniqueAndHashStably(t *testing.T) {
	a, hashA, suffixA, err := GenerateKicadToken()
	if err != nil {
		t.Fatalf("GenerateKicadToken: %v", err)
	}
	b, hashB, _, err := GenerateKicadToken()
	if err != nil {
		t.Fatalf("GenerateKicadToken: %v", err)
	}

	if a == b || hashA == hashB {
		t.Error("two generated tokens collided")
	}
	if got := HashToken(a); got != hashA {
		t.Errorf("HashToken is not stable: %q != %q", got, hashA)
	}
	// The stored suffix is what the UI shows to tell devices apart, so it has to
	// be the tail of the real token.
	if want := a[len(a)-4:]; suffixA != want {
		t.Errorf("suffix = %q, want %q", suffixA, want)
	}
	// The hash must not be the token. Obvious, and worth pinning: it is the only
	// thing standing between a database read and every workstation credential.
	if hashA == a {
		t.Error("stored hash equals the raw token")
	}
}
