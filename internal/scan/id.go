package scan

import (
	"crypto/sha256"
	"encoding/hex"
)

// deterministicID derives a canonical-UUID-formatted ID from seed (a
// stable string built from the item's path — see scan.go's callers) via a
// hash rather than crypto/rand: re-scanning the same file must upsert the
// same Item row, not mint a duplicate every pass. Same technique and same
// UUID-formatting requirement as the throwaway cmd/devseed's
// deterministicID (real Jellyfin clients parse ID fields as UUIDs — see
// internal/storage/sqlite/id.go's formatUUID doc comment for the full
// story) — duplicated here rather than shared, matching this project's
// existing precedent of a small formatUUID copy per package that needs
// one (cmd/tinyfin/main.go, internal/storage/sqlite/id.go) over adding a
// cross-package dependency for a few lines.
func deterministicID(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return formatUUID(hex.EncodeToString(sum[:16]))
}

func formatUUID(hex32 string) string {
	return hex32[0:8] + "-" + hex32[8:12] + "-" + hex32[12:16] + "-" + hex32[16:20] + "-" + hex32[20:32]
}
