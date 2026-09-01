package sqlite

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// newID mints a new opaque identifier for any Upsert/Create call that needs
// to generate one (see the per-method minting convention in the package
// doc comment in store.go). Stdlib-only (crypto/rand + encoding/hex) rather
// than a uuid/ulid dependency, per AGENTS.md principle 2.
//
// The value is time-prefixed so IDs sort roughly chronologically (useful
// for DateCreated-adjacent debugging/inspection), followed by random bytes
// so concurrent callers within the same nanosecond never collide, then
// formatted as a canonical UUID (formatUUID) — required, not cosmetic: see
// that function's doc comment.
func newID() string {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand.Read only fails if the OS entropy source is
		// unavailable, which is unrecoverable for a process that needs
		// unique identifiers at all.
		panic("sqlite: reading random bytes for ID generation: " + err.Error())
	}
	raw := fmt.Sprintf("%016x%s", uint64(time.Now().UTC().UnixNano()), hex.EncodeToString(b[:]))
	return formatUUID(raw[:32])
}

// formatUUID inserts hyphens into a 32-character hex string at the
// canonical UUID positions (8-4-4-4-12). This is required for wire
// compatibility, not cosmetic: real Jellyfin clients validate ID fields by
// parsing them as UUIDs. Confirmed by reading jellyfin-android's
// JellyfinWebViewClient.kt — its post-login WebView credential check does
// `UserId.toUUIDOrNull()` on the User.Id this project emits, and silently
// treats a non-UUID value as "no user", showing the app's native
// connection-error screen even with a perfectly valid AccessToken read
// alongside it. Found by testing against a real jellyfin-android build:
// server-side logs showed a fully successful login on every attempt, yet
// the app kept reopening its "can't connect" screen — until the actual
// stored WebView credentials were inspected and showed a valid token next
// to a UserId in this package's old plain-hex format (no hyphens).
func formatUUID(hex32 string) string {
	return hex32[0:8] + "-" + hex32[8:12] + "-" + hex32[12:16] + "-" + hex32[16:20] + "-" + hex32[20:32]
}

// newToken mints an opaque bearer token for IssueToken — a longer random
// value than newID since it doubles as a secret credential, not just a
// lookup key, so it carries no time prefix (no reason to make any part of
// it predictable).
func newToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("sqlite: reading random bytes for token generation: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}
