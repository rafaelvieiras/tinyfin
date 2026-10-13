// Package guid is the single home of Tinyfin's identifier wire/storage
// format: 32 lowercase hexadecimal digits with no dashes (wire.id.R1).
// Identifiers are minted, stored and emitted in that canonical form;
// input in the dashed 8-4-4-4-12 form or in any letter case is accepted
// everywhere and folded to it with Normalize (wire.id.R2).
package guid

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Zero is the empty GUID in canonical form.
const Zero = "00000000000000000000000000000000"

// Normalize returns s in canonical form (32 lowercase hex digits), or false
// when s is not a GUID in either the dashed or the undashed spelling.
// Surrounding whitespace is ignored. Zero is a valid GUID here; callers that
// reject it compare against Zero.
func Normalize(s string) (string, bool) {
	s = strings.TrimSpace(s)
	switch len(s) {
	case 32:
	case 36:
		if s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
			return "", false
		}
		s = s[0:8] + s[9:13] + s[14:18] + s[19:23] + s[24:]
	default:
		return "", false
	}
	b := make([]byte, 32)
	for i := 0; i < 32; i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
			b[i] = c
		case c >= 'A' && c <= 'F':
			b[i] = c + ('a' - 'A')
		default:
			return "", false
		}
	}
	return string(b), true
}

// Dashed renders a canonical (or any valid) GUID in the 8-4-4-4-12 form.
// It exists for tests and diagnostics; no response body uses it.
func Dashed(s string) string {
	n, ok := Normalize(s)
	if !ok {
		return s
	}
	return n[0:8] + "-" + n[8:12] + "-" + n[12:16] + "-" + n[16:20] + "-" + n[20:]
}

// New mints a random identifier.
func New() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand only fails when the OS entropy source is gone, which
		// is unrecoverable for a process that needs unique identifiers.
		panic("guid: reading random bytes: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

// FromBytes renders the first 16 bytes of b in canonical form. b must hold
// at least 16 bytes.
func FromBytes(b []byte) string { return hex.EncodeToString(b[:16]) }

// Derive returns a stable identifier computed from seed: the same seed
// always names the same object.
func Derive(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return FromBytes(sum[:])
}
