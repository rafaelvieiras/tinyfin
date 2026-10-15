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
	ItemTypeMovie       ItemType = "Movie"
	ItemTypeSeries      ItemType = "Series"
	ItemTypeSeason      ItemType = "Season"
	ItemTypeEpisode     ItemType = "Episode"
	ItemTypeTrailer     ItemType = "Trailer"
	ItemTypeMusicArtist ItemType = "MusicArtist"
	ItemTypeMusicAlbum  ItemType = "MusicAlbum"
	ItemTypeAudio       ItemType = "Audio"
	ItemTypeBoxSet      ItemType = "BoxSet" // collections, 04's "collections" group
	ItemTypePlaylist    ItemType = "Playlist"
	// ItemTypeManualPlaylistsFolder is the folder every playlist sits in.
	ItemTypeManualPlaylistsFolder ItemType = "ManualPlaylistsFolder"
	ItemTypePerson                ItemType = "Person" // cast/crew, see docs/05's note on Person
	ItemTypeGenre                 ItemType = "Genre"
	ItemTypeMusicGenre            ItemType = "MusicGenre"
	ItemTypeStudio                ItemType = "Studio"
	ItemTypeYear                  ItemType = "Year"
	ItemTypeVideo                 ItemType = "Video" // a video file of a home-videos library
	ItemTypeFolder                ItemType = "Folder"
	ItemTypePhoto                 ItemType = "Photo"            // a picture of a home-videos library
	ItemTypePhotoAlbum            ItemType = "PhotoAlbum"       // a folder that directly holds pictures
	ItemTypeCollectionFolder      ItemType = "CollectionFolder" // a library root
	ItemTypeUserView              ItemType = "UserView"         // a user's view of a library root
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
	SortByRuntime           SortBy = "Runtime"

	// SortBySeriesName sorts an Episode/Season by its owning series' SortName
	// (falling back to the item's own SortName for a type with no series,
	// e.g. Movie/Series themselves) — jellyfin-web's Favorites screen
	// (#/home?tab=1) sends "SortBy=SeriesSortName,SortName" on every
	// IncludeItemTypes call it makes, confirmed via Playwright network
	// capture against a real jellyfin-web build. Needed so favorited
	// episodes from different series group by series instead of
	// interleaving alphabetically by episode title (SortName alone) —
	// confirmed as an actual divergence from the reference server: favoriting two episodes of one series plus one of another
	// came back with the two same-series episodes split apart by SortName
	// alone, but grouped together by series when sorted this way.
	SortBySeriesName SortBy = "SeriesSortName"
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
	// StreamTypeData is a data track (timecodes, DVD navigation packets).
	StreamTypeData StreamType = "Data"
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

	// Profile is the codec profile ffprobe reports (e.g. "Main 10" for
	// HEVC video, "LC" for AAC audio) — the reference server's own
	// DisplayTitle algorithm (docs/12's "Episode/movie detail page —
	// MediaStream.DisplayTitle") prefers this over a friendly codec name
	// for an audio stream when it's set and isn't the generic "lc" value.
	Profile string

	// ChannelLayout is ffprobe's own channel-layout label (e.g. "5.1",
	// "stereo") — audio only, preferred over a bare Channels count in
	// DisplayTitle when the prober can supply it.
	ChannelLayout string

	// IsExternal marks a subtitle stream sourced from an external/sidecar
	// file rather than embedded in the media container — always false for
	// a video/audio stream mediaStreamsFromProbe (internal/scan) produces,
	// since ffprobe only ever sees the one file it's pointed at.
	IsExternal bool

	// Path is the file an external subtitle stream comes from (empty for
	// every embedded stream). IsHearingImpaired marks a subtitle stream for
	// the deaf and hard of hearing (an external file named with a "sdh",
	// "cc" or "hi" part).
	Path              string
	IsHearingImpaired bool

	// IsOriginal marks the stream flagged as the original-language track
	// — ffprobe's disposition.original, when the installed ffmpeg reports
	// it (an older ffmpeg build simply omits the key, read as false).
	IsOriginal bool

	// IsInterlaced is derived from ffprobe's field_order ("progressive" ->
	// false; "tt"/"bb"/"tb"/"bt" -> true; "unknown"/absent -> false, same
	// "don't guess" convention as the resolution-label breakpoints below).
	IsInterlaced bool

	// TimeBase/AspectRatio/RefFrames/PixelFormat/Level/SampleRate are
	// ffprobe passthrough fields with no other consumer today beyond
	// exposing them on the wire (mediaStreamDto, internal/api/dto.go) —
	// docs/12's DisplayTitle scope note lists these as fields "a prober
	// already extracts (or easily could)" that the reference server's own
	// MediaStream DTO also carries.
	TimeBase    string // e.g. "1/1000" (video r_frame_rate's inverse unit)
	AspectRatio string // e.g. "16:9" (display_aspect_ratio) — video only
	RefFrames   *int   // video only
	PixelFormat string // e.g. "yuv420p10le" — video only
	Level       *float64
	SampleRate  *int // audio only, Hz

	// ColorTransfer is ffprobe's raw color_transfer value (e.g.
	// "smpte2084", "arib-std-b67", "bt709") — video only. Not itself
	// exposed on the wire; DisplayTitle's dynamic-range label (HDR/SDR,
	// docs/12) is derived from it at response-build time rather than
	// classified and stored here, same "recompute, don't persist a
	// derived value" convention as DisplayTitle itself.
	ColorTransfer string
}

// Person is one cast/crew credit on an Item — the storage-side counterpart
// of the reference server's BaseItemPerson (confirmed via its published
// OpenAPI schema, not the C# source). Type is the enum value that schema
// documents: "Actor", "Director", "Writer", "Producer", ... — kept as a
// plain string, same convention as ItemType/SortBy above, rather than a
// closed Go type, so a value this project doesn't have a constant for yet
// doesn't need an interface change to store. Role is the character name
// for an Actor credit, empty for every other Type (mirrors
// BaseItemPerson.Role, which the reference server itself only populates
// for actors).
type Person struct {
	Name string
	Role string
	Type string

	// TMDBID is the credit's TMDB person ID, when the enriching Provider
	// supplied one (internal/metadata's TMDBProvider) — kept distinct from
	// Item.ProviderIDs (which maps a provider name to *this item's* ID,
	// not a person's) so a later pass can resolve a person's own image
	// without re-searching TMDB by name. 0 when unknown (e.g. a filename-
	// derived item with no provider enrichment yet).
	TMDBID int
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
	// ForcedSortName is an explicit sort title from the item's metadata
	// (a `<sorttitle>`). It is not persisted: UpsertItem folds it into the
	// stored sort key, which then reads back through SortName.
	ForcedSortName string
	Overview       string
	Taglines       []string
	Genres         []string
	Tags           []string

	// Studios is derived, not guessed: populated only by a Provider that
	// returns them (internal/metadata's TMDBProvider maps TMDB's "networks"
	// field for a series today), same "consume metadata, don't require it"
	// convention as Genres above. nil for a filename-derived item with no
	// provider enrichment.
	Studios []string

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
	// IndexNumberEnd is the last episode number of a file that holds
	// several episodes (BaseItemDto.IndexNumberEnd).
	IndexNumberEnd *int

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

	// SetName is the collection ("set") a movie declares in its metadata
	// file (<set><name>X</name></set> or <set>X</set>), kept as written. It
	// is what the scanner groups by when a library has
	// AutomaticallyAddToCollection on; empty when the movie names none.
	SetName string

	// CollectionType is set on library-root items (Type ==
	// ItemTypeCollectionFolder) — mirrors VirtualFolderInfo.CollectionType
	// — and empty otherwise.
	CollectionType CollectionType

	MediaStreams []MediaStreamRef

	// Chapters are the chapter marks of the file, in start order, as the
	// last probe found them. Name is the title the file gives the chapter
	// (empty when it has none: the API layer names it by position).
	Chapters []ChapterRef

	// People is an item's cast/crew — BaseItemDto.People on the reference
	// server. Populated only by a Provider that returns credits
	// (internal/metadata's TMDBProvider today); nil for a filename-derived
	// item with no provider enrichment, same "consume metadata, don't
	// require it" convention as Genres/Overview/ratings above.
	People []Person

	DateCreated time.Time

	// ExtraType and OwnerID mark an item that is not part of a library's
	// own catalog but belongs to another item: a trailer, a featurette, a
	// theme song or an additional part of a stacked movie. An extra has no
	// ParentID (it never shows up in a folder listing or a count), OwnerID is
	// the movie, series or season it hangs off, and ExtraType is one of the
	// ExtraType* values. Empty for every ordinary item.
	ExtraType string
	OwnerID   *ItemID

	// VideoType is the kind of video the item's Path is, when it is not a plain
	// file: VideoTypeDvd or VideoTypeBluRay (Path is the disc's folder),
	// VideoTypeIso (Path is a disc image file). Empty for a plain file.
	// IsoType, for an image, is the kind of disc inside (VideoTypeDvd or
	// VideoTypeBluRay), empty when it is not known.
	VideoType string
	IsoType   string

	// RemoteURL is the address a .strm file points at, when its content is a
	// URL. The item's Path is still the .strm file.
	RemoteURL string
}

// VideoType values of Item.VideoType and Item.IsoType (the reference
// server's VideoType names).
const (
	VideoTypeDvd    = "Dvd"
	VideoTypeBluRay = "BluRay"
	VideoTypeIso    = "Iso"
)

// ExtraType values of Item.ExtraType (the reference server's ExtraType names;
// ExtraTypePart, ExtraTypeVersion and ExtraTypeMergedSeason are internal: the
// wire shows an additional part of a stacked movie as a plain Video with no
// ExtraType).
const (
	ExtraTypeTrailer         = "Trailer"
	ExtraTypeFeaturette      = "Featurette"
	ExtraTypeBehindTheScenes = "BehindTheScenes"
	ExtraTypeDeletedScene    = "DeletedScene"
	ExtraTypeInterview       = "Interview"
	ExtraTypeScene           = "Scene"
	ExtraTypeShort           = "Short"
	ExtraTypeSample          = "Sample"
	ExtraTypeClip            = "Clip"
	ExtraTypeUnknown         = "Unknown"
	ExtraTypeThemeSong       = "ThemeSong"
	ExtraTypeThemeVideo      = "ThemeVideo"
	ExtraTypePart            = "Part"
	// ExtraTypeVersion is another file of the same movie or episode (a
	// quality or cut), listed among its media sources.
	ExtraTypeVersion = "Version"
	// ExtraTypeMergedSeason is a season folder that shares its number with
	// another folder: one season item stands for both, this item only
	// keeps the folder counted among the series' children.
	ExtraTypeMergedSeason = "Merged"
)

// HasMediaFile reports whether Path names a playable file of the item's own.
// A Series, Season or Folder stores its directory as Path (matching the
// reference server, which reports the directory there) but has no file to
// play, probe or stream.
func (i Item) HasMediaFile() bool {
	if i.Path == "" {
		return false
	}
	switch i.Type {
	case ItemTypeSeries, ItemTypeSeason, ItemTypeFolder, ItemTypeCollectionFolder, ItemTypePhotoAlbum,
		ItemTypePlaylist, ItemTypeManualPlaylistsFolder:
		return false
	}
	return true
}

// SeriesActivity is one entry of QuerySeriesLatestActivity's result: a
// series' own most recent Episode/Season DateCreated within that query's
// scope. Backs GET /Items/Latest's per-series ranking (internal/api's
// latest_grouping.go, docs/12's "Items/Latest — series grouping") without
// first materializing a raw-item-count-bounded pool across the whole
// scope: a pool bounded by item count biases toward whichever handful of
// series happen to contribute the most rows, silently excluding a
// genuinely more-recently-touched series with fewer episodes — confirmed
// live against a real library (four series scanned together, the one with
// the earliest per-episode timestamps had none of its rows survive a
// 200-item pool once the other three's combined episode counts exceeded
// it, so it never appeared in the grouped result at all, at any limit).
type SeriesActivity struct {
	SeriesID ItemID
	Latest   time.Time
}

// ChapterRef is one chapter mark of a media file.
type ChapterRef struct {
	// StartTicks is the chapter's start in 100 ns ticks.
	StartTicks int64
	// Name is the chapter's title tag; empty when the file has none.
	Name string
}
