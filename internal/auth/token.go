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

// GeneratePAT mints a new personal access token. It returns the raw token
// (shown to the user exactly once), its sha256 hash (stored for comparison),
// and the last four characters (stored so the UI can disambiguate tokens in a
// list without exposing them).
func GeneratePAT() (raw, hash, suffix string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", "", fmt.Errorf("reading random bytes: %w", err)
	}
	// URL-safe base64 without padding keeps the token to [A-Za-z0-9_-].
	body := base64.RawURLEncoding.EncodeToString(buf)
	raw = PATPrefix + body
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
