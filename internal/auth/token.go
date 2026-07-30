// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// PATPrefix is the human-visible prefix on every personal access token. Makes
// leaked tokens greppable in logs and repos (like GitHub's ghp_).
const PATPrefix = "fbin_pat_"

// KicadTokenPrefix marks a token issued for a KiCad workstation. Distinct from
// PATPrefix on purpose: these arrive under the `Token` scheme on the KiCad
// library routes and are resolved against a different table, so a leaked one
// grants nothing but read access to the catalogue. A PAT in the same place would
// carry its owner's full account authority, because PAT scopes are recorded and
// never enforced.
const KicadTokenPrefix = "fbin_kicad_"

// GeneratePAT mints a new personal access token. It returns the raw token
// (shown to the user exactly once), its sha256 hash (stored for comparison),
// and the last four characters (stored so the UI can disambiguate tokens in a
// list without exposing them).
func GeneratePAT() (raw, hash, suffix string, err error) {
	return generateToken(PATPrefix)
}

// GenerateKicadToken mints a token for one KiCad workstation. Same strength and
// shape as a PAT, different prefix and different store.
func GenerateKicadToken() (raw, hash, suffix string, err error) {
	return generateToken(KicadTokenPrefix)
}

func generateToken(prefix string) (raw, hash, suffix string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", "", fmt.Errorf("reading random bytes: %w", err)
	}
	// URL-safe base64 without padding keeps the token to [A-Za-z0-9_-].
	body := base64.RawURLEncoding.EncodeToString(buf)
	raw = prefix + body
	hash = HashToken(raw)
	suffix = raw[len(raw)-4:]
	return raw, hash, suffix, nil
}

// HashToken returns the hex-encoded sha256 of a raw token. Deterministic, so
// the auth middleware can hash an incoming token and look it up by hash.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// IsPAT reports whether a bearer credential is a personal access token rather
// than a JWT.
func IsPAT(token string) bool {
	return strings.HasPrefix(token, PATPrefix)
}

// IsKicadToken reports whether a credential is a KiCad workstation token.
//
// Used to reject one presented as a Bearer on the ordinary API. Without this the
// rejection happens only by accident: IsPAT would be false, so it would fall
// through to JWT validation and fail there. Relying on that is fragile, and
// "read-only by construction" should be enforced rather than incidental.
func IsKicadToken(token string) bool {
	return strings.HasPrefix(token, KicadTokenPrefix)
}
