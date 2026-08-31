package storage

// SubtitleRef is one externally-added subtitle file reference — uploaded
// via the reference server's UploadSubtitleDto shape, or pulled from a
// provider and described by RemoteSubtitleInfo (04). ItemID is carried on
// the struct itself (not just a QuerySubtitles param) because
// UpsertSubtitle takes only a ref — see ImageRef's doc comment for the
// same reasoning. Same refs-not-blobs shape as ImageStore: Path points at
// the file on disk, the subtitle text itself is not stored in SQLite.
type SubtitleRef struct {
	ID     SubtitleID
	ItemID ItemID

	Path     string
	Language string // matches UploadSubtitleDto.Language / RemoteSubtitleInfo.ThreeLetterISOLanguageName
	Format   string // e.g. "srt", "ass", "vtt" — UploadSubtitleDto.Format / RemoteSubtitleInfo.Format

	IsForced          bool
	IsHearingImpaired bool

	// Source identifies where this subtitle came from: empty for a
	// direct client upload, or a provider name (RemoteSubtitleInfo.
	// ProviderName) when pulled from a metadata provider (08).
	Source string
}
