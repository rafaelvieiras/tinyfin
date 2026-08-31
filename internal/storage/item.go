package storage

import "time"

// ItemType is the reference server's BaseItemKind (OpenAPI schema
// BaseItemKind), narrowed to the kinds this project's scope actually
// needs (docs/01: video-first library — movies/series — plus music,
// collections, playlists, and the browsing scaffolding, e.g. Genre,
// Studio; docs/04's "library and item navigation" and "specialized media
// types" groups). Constant values equal the wire strings BaseItemDto.Type
// uses, so an implementation can pass ItemType straight through to JSON
// without a translation table. Live TV/DVR kinds (LiveTvChannel,
// LiveTvProgram, Recording, ...) and other kinds outside v1 scope (Book,
// Photo, Channel, ...) are deliberately omitted — see docs/01 "Explicitly
// out of scope for v1"; add a kind here only once a doc actually motivates
// it, per this project's "minimal over guessed" preference.
type ItemType string

const (
	ItemTypeMovie            ItemType = "Movie"
	ItemTypeSeries           ItemType = "Series"
	ItemTypeSeason           ItemType = "Season"
	ItemTypeEpisode          ItemType = "Episode"
	ItemTypeTrailer          ItemType = "Trailer"
	ItemTypeMusicArtist      ItemType = "MusicArtist"
	ItemTypeMusicAlbum       ItemType = "MusicAlbum"
	ItemTypeAudio            ItemType = "Audio"
	ItemTypeBoxSet           ItemType = "BoxSet" // collections, 04's "collections" group
	ItemTypePlaylist         ItemType = "Playlist"
	ItemTypePerson           ItemType = "Person" // cast/crew, see docs/05's note on Person
	ItemTypeGenre            ItemType = "Genre"
	ItemTypeMusicGenre       ItemType = "MusicGenre"
	ItemTypeStudio           ItemType = "Studio"
	ItemTypeYear             ItemType = "Year"
	ItemTypeFolder           ItemType = "Folder"
	ItemTypeCollectionFolder ItemType = "CollectionFolder" // a library root
	ItemTypeUserView         ItemType = "UserView"         // a user's view of a library root
)

// SortBy is a QueryItems sort key. Values equal the reference server's
// ItemSortBy values (04) that this project's sort UI actually exposes;
// like ItemType, kept as a plain string type rather than a closed set
// enforced elsewhere so a new value doesn't require an interface change,
// but a constant is provided for every value QueryItems callers use today.
type SortBy string

const (
	SortByName              SortBy = "SortName"
	SortByDateCreated       SortBy = "DateCreated"
	SortByPremiereDate      SortBy = "PremiereDate"
	SortByProductionYear    SortBy = "ProductionYear"
	SortByCommunityRating   SortBy = "CommunityRating"
	SortByRandom            SortBy = "Random"
	SortByIndexNumber       SortBy = "IndexNumber"
	SortByParentIndexNumber SortBy = "ParentIndexNumber"
)

// SortField is one entry of ItemFilter.SortBy: a sort key plus direction.
// QueryItems callers pass more than one to break ties (e.g. sort by
// ParentIndexNumber then IndexNumber for an episode listing).
type SortField struct {
	By         SortBy
	Descending bool
}

// StreamType distinguishes the embedded tracks reported on Item.MediaStreams
// — mirrors the reference server's MediaStream.Type discriminator.
type StreamType string

const (
	StreamTypeVideo    StreamType = "Video"
	StreamTypeAudio    StreamType = "Audio"
	StreamTypeSubtitle StreamType = "Subtitle"
)

// MediaStreamRef is one scan-derived embedded track on an item's media
// file — video, audio, or subtitle. Per docs/05's SubtitleStore comment,
// embedded subtitle tracks live here, on Item, not in SubtitleStore
// (which is for externally-added files only); the same reasoning applies
// to embedded audio/video tracks. Shape is a minimal subset of the
// reference server's MediaStream needed to answer "what tracks does this
// item have" for playback negotiation (07) — not every MediaStream field,
// most of which (color space, HDR metadata, ...) is transcode-decision
// detail out of this slice's scope.
type MediaStreamRef struct {
	Index     int
	Type      StreamType
	Codec     string
	Language  string
	Title     string
	IsDefault bool
	IsForced  bool
	Channels  *int // audio only
	Width     *int // video only
	Height    *int // video only
	BitRate   *int64
}

// Item is the generic item model backing ItemStore — the storage-side
// counterpart of the reference server's BaseItemDto, narrowed to the
// fields docs/04's highest-priority groups (item navigation, playback,
// specialized media types) actually need. Fields that are purely
// query-time aggregates in the reference server (e.g. UnplayedItemCount,
// PlayedPercentage, IsFolder — derivable from ItemType) are intentionally
// not stored here; per-user playstate (Played, IsFavorite, resume
// position) lives in Playstate, not Item, since it varies by user and
// this struct does not.
type Item struct {
	ID   ItemID
	Type ItemType

	// LibraryID is the owning library (docs/05's LibraryStore) — the root
	// ancestor every item under a library root resolves to. Distinct from
	// ParentID: ParentID walks the immediate tree (season -> episode),
	// LibraryID answers ItemFilter.LibraryID directly without a recursive
	// walk.
	LibraryID LibraryID
	ParentID  *ItemID

	Name          string
	OriginalTitle string
	SortName      string
	Overview      string
	Taglines      []string
	Genres        []string
	Tags          []string

	ProductionYear  *int
	PremiereDate    *time.Time
	EndDate         *time.Time
	OfficialRating  string
	CommunityRating *float64
	CriticRating    *float64
	RunTimeTicks    *int64

	// IndexNumber/ParentIndexNumber are the reference server's episode
	// and season numbers respectively (BaseItemDto.IndexNumber /
	// ParentIndexNumber) — also used generically for track/disc numbers
	// on music items.
	IndexNumber       *int
	ParentIndexNumber *int

	// SeriesID/SeasonID duplicate what ParentID's chain already encodes
	// (episode -> season -> series) but let an Episode row answer "which
	// series is this" in one lookup instead of a two-hop walk, matching
	// BaseItemDto's own SeriesId/SeasonId convenience fields.
	SeriesID *ItemID
	SeasonID *ItemID

	// Music-specific fields (BaseItemDto.Album/AlbumArtist/Artists),
	// populated on Audio items.
	Album       string
	AlbumArtist string
	Artists     []string

	// ProviderIDs maps a provider name (e.g. "Tmdb", "Imdb") to that
	// provider's ID for this item — BaseItemDto.ProviderIds — the join
	// key the metadata provider interface (08) uses to refresh an item
	// against its source.
	ProviderIDs map[string]string

	// Path/Container/Size back Direct Play/media-info responses (04's
	// playback group) without needing a full MediaSourceInfo — those are
	// assembled at the API layer from this plus MediaStreams.
	Path      string
	Container string
	Size      int64

	// CollectionType is set on library-root items (Type ==
	// ItemTypeCollectionFolder) — mirrors VirtualFolderInfo.CollectionType
	// — and empty otherwise.
	CollectionType CollectionType

	MediaStreams []MediaStreamRef

	DateCreated time.Time
}
