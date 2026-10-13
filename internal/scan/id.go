package scan

import "github.com/rafaelvieiras/tinyfin/internal/guid"

// deterministicID derives a canonical identifier from seed (a stable string
// built from the item's path — see scan.go's callers): re-scanning the same
// file must upsert the same Item row, not mint a duplicate every pass.
func deterministicID(seed string) string { return guid.Derive(seed) }
