package storage

// LyricRef is one lyric file reference for an item — same refs-not-blobs
// shape as SubtitleRef (Path points at the file on disk; the timed lines
// themselves, the reference server's LyricDto.Lyrics, are parsed from
// that file at request time, not stored in SQLite). ItemID is carried on
// the struct itself for the same reason as SubtitleRef.ItemID: UpsertLyric
// takes only a ref. Format/Author/IsSynced mirror the reference server's
// LyricMetadata — the subset useful for picking/describing a lyric file
// without parsing it (IsSynced distinguishes timed LRC-style lyrics from
// plain, unsynced text).
type LyricRef struct {
	ID     LyricID
	ItemID ItemID

	Path     string
	Format   string // e.g. "lrc", "txt"
	Author   string
	IsSynced bool

	// Source identifies where this lyric file came from: empty for a
	// locally-saved file, or a provider name when pulled via the metadata
	// provider interface (08) — same convention as SubtitleRef.Source.
	Source string
}
