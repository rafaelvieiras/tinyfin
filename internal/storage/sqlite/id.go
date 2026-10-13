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
// so concurrent callers within the same nanosecond never collide. The
// result is 32 lowercase hex digits, the canonical identifier form
// (internal/guid, wire.id.R1).
func newID() string {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand.Read only fails if the OS entropy source is
		// unavailable, which is unrecoverable for a process that needs
		// unique identifiers at all.
		panic("sqlite: reading random bytes for ID generation: " + err.Error())
	}
	raw := fmt.Sprintf("%016x%s", uint64(time.Now().UTC().UnixNano()), hex.EncodeToString(b[:]))
	return raw[:32]
}

// newToken mints an opaque bearer token for IssueToken: 128 random bits
// rendered as 32 lower-case hex digits (users.auth.R1). It doubles as a
// secret credential, not just a lookup key, so it carries no time prefix
// (no reason to make any part of it predictable).
func newToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("sqlite: reading random bytes for token generation: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}
