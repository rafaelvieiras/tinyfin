package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// baseItemDto mirrors the OpenAPI schema's BaseItemDto, trimmed to fields
// storage.Item actually carries (same "minimal over guessed" approach as
// userDto in users.go). BaseItemDto has 150+ fields on the reference
// server, most describing per-user state (UserData), specialized-type
// aggregates (EpisodeCount, AlbumCount, ...), or images/people/chapters —
// all out of scope for this slice (see the package doc comments on
// library.go/playback.go for exactly what's deferred and why).
type baseItemDto struct {
	Name         string     `json:"Name"`
	ServerID     string     `json:"ServerId"`
	ID           string     `json:"Id"`
	DateCreated  time.Time  `json:"DateCreated"`
	Container    string     `json:"Container,omitempty"`
	SortName     string     `json:"SortName,omitempty"`
	PremiereDate *time.Time `json:"PremiereDate,omitempty"`
	EndDate      *time.Time `json:"EndDate,omitempty"`
	Path         string     `json:"Path,omitempty"`

	OfficialRating  string   `json:"OfficialRating,omitempty"`
	OriginalTitle   string   `json:"OriginalTitle,omitempty"`
	Overview        string   `json:"Overview,omitempty"`
	Taglines        []string `json:"Taglines,omitempty"`
	Genres          []string `json:"Genres,omitempty"`
	CommunityRating *float64 `json:"CommunityRating,omitempty"`
	CriticRating    *float64 `json:"CriticRating,omitempty"`
	RunTimeTicks    *int64   `json:"RunTimeTicks,omitempty"`
	ProductionYear  *int     `json:"ProductionYear,omitempty"`

	IndexNumber       *int `json:"IndexNumber,omitempty"`
	ParentIndexNumber *int `json:"ParentIndexNumber,omitempty"`

	ProviderIDs map[string]string `json:"ProviderIds,omitempty"`

	IsFolder bool   `json:"IsFolder"`
	ParentID string `json:"ParentId,omitempty"`
	Type     string `json:"Type"`
	SeriesID string `json:"SeriesId,omitempty"`
	SeasonID string `json:"SeasonId,omitempty"`

	// SeriesName/SeasonName: found missing, not just unpolished, by testing
	// an episode detail page against a real jellyfin-web build — its header
	// renders episode.SeriesName directly (as literal "undefined" text when
	// absent, not a graceful fallback), so an episode DTO carrying SeriesId
	// without the resolved name still breaks the page a real server's
	// wouldn't.
	SeriesName string `json:"SeriesName,omitempty"`
	SeasonName string `json:"SeasonName,omitempty"`

	Tags        []string `json:"Tags,omitempty"`
	Artists     []string `json:"Artists,omitempty"`
	Album       string   `json:"Album,omitempty"`
	AlbumArtist string   `json:"AlbumArtist,omitempty"`

	// CollectionType is set only on library-root items (Type ==
	// "CollectionFolder"/"UserView"), matching storage.Item's own
	// CollectionType field convention.
	CollectionType string `json:"CollectionType,omitempty"`

	// MediaType is the OpenAPI schema's coarse Video/Audio/Photo/Book
	// discriminator — distinct from Type (BaseItemKind: "Movie",
	// "Episode", ...) above. Confirmed load-bearing, not cosmetic, by
	// testing against a real jellyfin-web build: its playback-eligibility
	// check (playbackManager.canPlay -> getPlayer ->
	// htmlVideoPlayer.canPlayMediaType) matches player plugins against
	// this field, not Type, so an item missing it can never show a Play
	// button or be handed to a player at all, regardless of what
	// PlaybackInfo would have said.
	MediaType string `json:"MediaType,omitempty"`

	MediaStreams []mediaStreamDto `json:"MediaStreams,omitempty"`

	// ImageTags: an N+1 GetImageRefs call per item per list response (the
	// tradeoff this field's absence used to avoid — see git history)
	// turned out not to be optional. Confirmed by testing against a real
	// jellyfin-web build: without an ImageTags.Primary entry, the client
	// never even issues a GET /Items/{id}/Images/Primary request — it
	// treats the tag's absence as "no image exists", not merely "no
	// cache hint available" as this file previously assumed, and falls
	// back to a generic placeholder icon. Each page is still bounded
	// (defaultItemsLimit/ItemFilter.Limit, AGENTS.md principle 1) so this
	// is N+1 bounded queries, not an unbounded scan.
	ImageTags map[string]string `json:"ImageTags,omitempty"`

	// BackdropImageTags: unlike every other image kind, an item can have
	// more than one Backdrop (fanart.jpg plus any provider-returned
	// extras), so the reference server carries it as its own array field
	// rather than folding it into the ImageTags map above — array index N
	// corresponds to the tag for GET /Items/{itemId}/Images/Backdrop/N.
	// Found missing, not just unpolished, by comparing a movie detail
	// page's GET /Users/{userId}/Items/{itemId} response against a real
	// Jellyfin server for a title whose fanart.jpg the
	// scanner already indexed and Images/Backdrop/0 already served
	// correctly: without this field a client has no way to know the
	// backdrop exists at all, so it never requests it (same "tag absence
	// means no image" behavior ImageTags's own doc comment above
	// describes) — see docs/12's "Movie detail page — image tags for
	// Backdrop and Logo".
	BackdropImageTags []string `json:"BackdropImageTags,omitempty"`

	// PrimaryImageAspectRatio is the Primary image's width/height ratio.
	// Found missing, not just unpolished, by reading jellyfin-web's
	// cardBuilder image-loader module (the chunk exporting
	// getPrimaryImageAspectRatio) after visually confirming movie-grid
	// cards rendered as square album-art-style tiles instead of portrait
	// posters: an "auto"-shaped card list takes the *median*
	// PrimaryImageAspectRatio across all items to pick portraitCard
	// (~2:3) vs squareCard (~1:1) vs a banner shape, and falls back to
	// square when every item's ratio is null — which every item's was,
	// this field never having existed. nil when the item has no Primary
	// image, or its ImageRef predates Width/Height being recorded
	// (scan/scan.go's imageDimensions).
	PrimaryImageAspectRatio *float64 `json:"PrimaryImageAspectRatio,omitempty"`

	// UserData carries the requesting user's playstate for this item
	// (resume position, played/favorite status) — populated from
	// PlaystateStore, not Item itself, since it's per-user (storage/item.go's
	// doc comment on why Item doesn't carry it). nil for an unauthenticated
	// caller (there's no user to key the lookup off) or for the
	// per-user-blind call sites that build a baseItemDto without going
	// through baseItemDto() below (e.g. handleUserViews' library roots).
	//
	// Found missing, not just unpolished, by testing against a real
	// jellyfin-web build: Sessions/Playing/Progress and UserPlayedItems
	// (sessions_playback.go) were already writing resume position and
	// watched status correctly into PlaystateStore, but nothing ever read it
	// back onto an item response, so jellyfin-web's
	// `item.UserData.PlaybackPositionTicks > 0` resume check was always
	// false — "Continue Watching" never appeared no matter how much of a
	// file had actually been watched, even though the server had the exact
	// position on file.
	UserData *userItemDataDto `json:"UserData,omitempty"`

	// People is an item's cast/crew (BaseItemDto.People) — populated from
	// storage.Item.People, which today only a TMDB-enriched item carries
	// (internal/metadata's TMDBProvider.lookupMovie, docs/08's "consume
	// metadata, don't require it"). nil for a filename-derived item with
	// no provider enrichment, same convention as Genres/Overview above.
	People []baseItemPersonDto `json:"People,omitempty"`

	// GenreItems mirrors Genres above but with a stable Id per name — the
	// real server's own BaseItemDto carries both: Genres for the plain
	// string chip list, GenreItems for anywhere a client needs to link a
	// genre chip to a browsable Genre item (its Id). Derived from the same
	// item.Genres slice, not stored separately — same "derive, don't
	// store" convention as isFolderType/mediaTypeFor.
	GenreItems []nameIDPairDto `json:"GenreItems,omitempty"`

	// Studios mirrors the reference server's BaseItemDto.Studios — same
	// {Name, Id} NameIdPair shape GenreItems uses (confirmed against the
	// live OpenAPI schema before adding this field, not guessed), derived
	// from item.Studios (internal/metadata's TMDBProvider populates it
	// from TMDB's "networks" for a series today) the same "derive, don't
	// store" way GenreItems derives from item.Genres.
	Studios []nameIDPairDto `json:"Studios,omitempty"`

	// ExternalUrls links out to a provider's own page for this item (IMDb,
	// TheMovieDb) — derived from ProviderIDs (already on this DTO) rather
	// than stored, since it's a pure function of a provider id plus the
	// item's own Type. See externalURLsFor's doc comment for the exact
	// URL patterns.
	ExternalUrls []externalURLDto `json:"ExternalUrls,omitempty"`

	// ChildCount is only meaningful for a folder-shaped item that groups
	// other items (Series/Season/BoxSet) — how many direct children it has
	// (storage.ItemFilter.ParentID, non-recursive — same relationship
	// handleShowSeasons/handleShowEpisodes/itemTypeCount already use). nil
	// for every other Type, matching the reference server's own "only
	// present where it means something" convention for this field.
	ChildCount *int `json:"ChildCount,omitempty"`

	// MediaSources embeds the same per-source description
	// GET /Items/{itemId}/PlaybackInfo returns (mediaSourceInfoDto,
	// playback.go), minus that endpoint's device-profile transcode
	// negotiation — see mediaSourcesFor's doc comment for why there's
	// nothing to negotiate here. nil for a folder-shaped item with no
	// playable file behind it (item.Path empty), same as PlaybackInfo
	// itself would have nothing useful to say about one.
	MediaSources []mediaSourceInfoDto `json:"MediaSources,omitempty"`

	// Width/Height/IsHD/HasSubtitles/VideoType mirror the item's own
	// primary video track (docs/12's "Episode DTO — item-level
	// video/permission/identity fields") — distinct from mediaStreamDto's
	// own per-stream Width/Height. Populated only for a video-file item
	// (videoFileItemTypes below) that actually has a Path; nil/""/false
	// (omitempty) otherwise, matching that section's "absent, not a
	// guessed zero" convention.
	Width        *int   `json:"Width,omitempty"`
	Height       *int   `json:"Height,omitempty"`
	IsHD         bool   `json:"IsHD,omitempty"`
	HasSubtitles bool   `json:"HasSubtitles,omitempty"`
	VideoType    string `json:"VideoType,omitempty"`

	// PlayAccess/CanDelete/CanDownload/Etag/DisplayPreferencesId are all
	// per-request-caller fields (docs/12's same section) — populated only
	// alongside UserData below, when ctx carries an authenticated caller.
	PlayAccess           string `json:"PlayAccess,omitempty"`
	CanDelete            bool   `json:"CanDelete"`
	CanDownload          bool   `json:"CanDownload"`
	Etag                 string `json:"Etag,omitempty"`
	DisplayPreferencesID string `json:"DisplayPreferencesId,omitempty"`

	// LocationType: docs/12's same section — "FileSystem" for an item
	// with a real Path, "Virtual" otherwise (a placeholder or a
	// folder-shaped item with nothing of its own on disk). Unlike the
	// fields above, this doesn't depend on the caller, so it's always
	// populated, not gated behind auth.
	LocationType string `json:"LocationType,omitempty"`

	// SeriesStudio is the owning series' first Studios entry, when it has
	// one — docs/12's same section. Populated for both Season and Episode
	// (anything with a SeriesID), same fetch this file's SeriesName
	// lookup already does.
	SeriesStudio string `json:"SeriesStudio,omitempty"`

	// LocalTrailerCount/SpecialFeatureCount/RemoteTrailers/LockedFields:
	// this project doesn't implement trailers, extras, or metadata field
	// locking as concepts yet — always the zero/empty value, present so a
	// client expecting the keys doesn't have to special-case their
	// absence (docs/12's same section).
	LocalTrailerCount   int           `json:"LocalTrailerCount"`
	SpecialFeatureCount int           `json:"SpecialFeatureCount"`
	RemoteTrailers      []mediaURLDto `json:"RemoteTrailers,omitempty"`
	LockedFields        []string      `json:"LockedFields,omitempty"`

	// SeriesPrimaryImageTag/Parent{Logo,Backdrop,Thumb}* are the
	// image-inheritance fields docs/12's "Season/Episode DTO — inherited
	// Parent* image fields and SeriesPrimaryImageTag" section covers in
	// full — populated by parentImageFieldsFor below for a Season or
	// Episode item.
	SeriesPrimaryImageTag   string   `json:"SeriesPrimaryImageTag,omitempty"`
	ParentLogoItemID        string   `json:"ParentLogoItemId,omitempty"`
	ParentLogoImageTag      string   `json:"ParentLogoImageTag,omitempty"`
	ParentBackdropItemID    string   `json:"ParentBackdropItemId,omitempty"`
	ParentBackdropImageTags []string `json:"ParentBackdropImageTags,omitempty"`
	ParentThumbItemID       string   `json:"ParentThumbItemId,omitempty"`
	ParentThumbImageTag     string   `json:"ParentThumbImageTag,omitempty"`
}

// mediaURLDto mirrors the reference server's MediaUrl — {Name, Url} —
// RemoteTrailers' element shape. Never populated today (baseItemDto.
// RemoteTrailers' own doc comment); kept as its own type rather than reusing
// externalURLDto so the two concepts (a provider's own page vs. a remote
// trailer's playback URL) don't get accidentally conflated later.
type mediaURLDto struct {
	Name string `json:"Name"`
	URL  string `json:"Url"`
}

// nameIDPairDto mirrors the reference server's NameIdPair — a {Name, Id}
// tuple, the shape GenreItems (and, on the real schema, TagItems/StudioItems
// this project doesn't populate yet) uses.
type nameIDPairDto struct {
	Name string `json:"Name"`
	ID   string `json:"Id"`
}

// genreID mints a stable, canonical-UUID-formatted Id for a genre name —
// same sha256+formatUUID technique as personID/themeMediaItemID above, each
// kept as its own small copy per those functions' doc comments on why: a
// genre appearing on more than one item needs to resolve to the same Id
// every time, not a fresh one per response.
func genreID(name string) string {
	sum := sha256.Sum256([]byte("genre-" + name))
	return formatUUID(hex.EncodeToString(sum[:16]))
}

// genreItemsFor builds BaseItemDto.GenreItems from the same genre names
// Genres already carries.
func genreItemsFor(genres []string) []nameIDPairDto {
	if len(genres) == 0 {
		return nil
	}
	items := make([]nameIDPairDto, 0, len(genres))
	for _, g := range genres {
		items = append(items, nameIDPairDto{Name: g, ID: genreID(g)})
	}
	return items
}

// studioID mints a stable, canonical-UUID-formatted Id for a studio name —
// same sha256+formatUUID technique as genreID above, with its own "studio-"
// prefix so a studio and a genre that happen to share a name don't collide
// on the same Id.
func studioID(name string) string {
	sum := sha256.Sum256([]byte("studio-" + name))
	return formatUUID(hex.EncodeToString(sum[:16]))
}

// studioItemsFor builds BaseItemDto.Studios from the same studio names
// item.Studios already carries — mirrors genreItemsFor above.
func studioItemsFor(studios []string) []nameIDPairDto {
	if len(studios) == 0 {
		return nil
	}
	items := make([]nameIDPairDto, 0, len(studios))
	for _, st := range studios {
		items = append(items, nameIDPairDto{Name: st, ID: studioID(st)})
	}
	return items
}

// externalURLDto mirrors the reference server's ExternalUrl — {Name, Url}.
type externalURLDto struct {
	Name string `json:"Name"`
	URL  string `json:"Url"`
}

// externalURLsFor builds BaseItemDto.ExternalUrls from item's ProviderIDs
// (already-resolved provider ids this project stores, e.g. from TMDB
// enrichment — internal/metadata) plus itemType, needed only to pick TMDB's
// movie-vs-tv URL path segment. URL patterns are each provider's own public,
// well-known page-URL convention (themoviedb.org/imdb.com), not anything
// sourced from the reference server's C# — same "public, well-known
// convention, not Jellyfin-specific" reasoning routing.go's
// normalizePathCase doc comment already applies to ASP.NET Core's
// case-insensitive routing.
func externalURLsFor(itemType storage.ItemType, providerIDs map[string]string) []externalURLDto {
	if len(providerIDs) == 0 {
		return nil
	}
	var urls []externalURLDto
	if id, ok := providerIDs["Tmdb"]; ok && id != "" {
		kind := "movie"
		if itemType == storage.ItemTypeSeries || itemType == storage.ItemTypeSeason || itemType == storage.ItemTypeEpisode {
			kind = "tv"
		}
		urls = append(urls, externalURLDto{Name: "TheMovieDb", URL: "https://www.themoviedb.org/" + kind + "/" + id})
	}
	if id, ok := providerIDs["Imdb"]; ok && id != "" {
		urls = append(urls, externalURLDto{Name: "IMDb", URL: "https://www.imdb.com/title/" + id})
	}
	return urls
}

// childCountItemTypes are the ItemTypes ChildCount is ever populated for —
// see baseItemDto.ChildCount's doc comment.
var childCountItemTypes = map[storage.ItemType]bool{
	storage.ItemTypeSeries: true,
	storage.ItemTypeSeason: true,
	storage.ItemTypeBoxSet: true,
}

// childCountFor computes baseItemDto.ChildCount for id — how many items
// have it as their direct (non-recursive) ParentID, the same relationship
// itemTypeCount (latest_grouping.go) already reads, just without that
// helper's own ItemTypes narrowing (a Series' direct children are Seasons,
// but a BoxSet's direct children can be any collected type).
func (s *Server) childCountFor(ctx context.Context, id storage.ItemID) (int, error) {
	page, err := s.store.QueryItems(ctx, storage.ItemFilter{
		ParentID: &id,
		Limit:    1,
	})
	if err != nil {
		return 0, err
	}
	return page.TotalCount, nil
}

// unplayedItemCountFor computes UserData.UnplayedItemCount for (userID,
// item) — see docs/12's "Season/Series/BoxSet UserData — UnplayedItemCount"
// for the full behavior spec. Gated by childCountItemTypes, same as
// ChildCount above: unlike ChildCount, this counts unplayed *leaf*
// descendants (Episodes), not direct children (a Series' direct children
// are Seasons, which aren't the thing a client wants an unplayed count
// over) — but it's the same folder-shaped-item set the reference server
// aggregates user data for, so there's no need for a separate item-type
// gate.
//
// A Season's direct ParentID children already are its Episodes (no deeper
// level to recurse into, per item.go's ParentID doc comment), so that case
// reuses the same non-recursive ParentID query childCountFor does. A
// Series' direct children are Seasons, not Episodes, so that case queries
// Episodes by SeriesID instead — the same one-hop back-reference
// SeriesName's own lookup elsewhere in this file relies on — rather than
// walking through Seasons. BoxSet child membership isn't wired up beyond
// ParentID in this codebase yet (childCountFor's own doc comment), so it
// falls back to the same ParentID query Season uses, with nothing further
// to recurse into today either.
//
// One bounded QueryItems call per item, same N+1-but-bounded-by-the-page
// trade-off childCountFor's own doc comment already accepts.
func (s *Server) unplayedItemCountFor(ctx context.Context, userID storage.UserID, item storage.Item) (*int, error) {
	played := false
	filter := storage.ItemFilter{
		UserID: &userID,
		Played: &played,
		Limit:  1,
	}
	if item.Type == storage.ItemTypeSeries {
		filter.SeriesID = &item.ID
		filter.ItemTypes = []storage.ItemType{storage.ItemTypeEpisode}
	} else {
		filter.ParentID = &item.ID
	}
	page, err := s.store.QueryItems(ctx, filter)
	if err != nil {
		return nil, err
	}
	n := page.TotalCount
	return &n, nil
}

// mediaSourcesFor builds BaseItemDto.MediaSources for item — see that
// field's doc comment on baseItemDto. Confirmed against the reference
// server (reference server): GET /Items/{itemId} embeds this same array
// directly on the item response, not just on PlaybackInfo. nil for an item
// with no playable file (item.Path empty — a folder-shaped item like a
// Series/Season/BoxSet), matching PlaybackInfo's own 404 for such an item
// (nothing to describe).
func mediaSourcesFor(item storage.Item) []mediaSourceInfoDto {
	if item.Path == "" {
		return nil
	}
	source := baseMediaSourceInfo(item, item.Container)
	// No device profile to negotiate against here (this function's doc
	// comment) — SupportsDirectPlay/TranscodingSubProtocol mirror
	// handlePlaybackInfo's own ModeDirectPlay branch (playback.go) since a
	// bare item description has nothing to decide against, same as that
	// branch's own reasoning.
	source.SupportsDirectPlay = true
	source.TranscodingSubProtocol = "http"
	return []mediaSourceInfoDto{source}
}

// baseItemPersonDto mirrors the OpenAPI schema's BaseItemPerson, trimmed
// to the fields storage.Person carries (same "minimal over guessed"
// approach as baseItemDto itself). Id is a deterministic UUID derived from
// the person's name (personID below) — the same identity convention
// internal/scan's deterministicID uses for items/images, chosen for the
// same reason: a person appearing on more than one item (or surviving a
// re-scan) needs to resolve to the same Id every time, not a fresh one per
// response.
type baseItemPersonDto struct {
	Name string `json:"Name"`
	ID   string `json:"Id"`
	Role string `json:"Role,omitempty"`
	Type string `json:"Type"`
}

// personID mints a stable, canonical-UUID-formatted Id for a cast/crew
// credit from its name — deterministic (same sha256+formatUUID technique
// as internal/scan/id.go's deterministicID and this package's own
// themeMediaItemID, each package keeping its own small copy per those
// files' doc comments on why) so the same actor/director appearing across
// different items, or across a re-scan, always gets the same Id instead of
// a fresh one per response.
func personID(name string) string {
	sum := sha256.Sum256([]byte("person-" + name))
	return formatUUID(hex.EncodeToString(sum[:16]))
}

func peopleDtos(people []storage.Person) []baseItemPersonDto {
	if len(people) == 0 {
		return nil
	}
	dtos := make([]baseItemPersonDto, 0, len(people))
	for _, p := range people {
		dtos = append(dtos, baseItemPersonDto{Name: p.Name, ID: personID(p.Name), Role: p.Role, Type: p.Type})
	}
	return dtos
}

// baseItemDtoQueryResult mirrors the OpenAPI schema's
// BaseItemDtoQueryResult — the response shape for both GET /UserViews and
// GET /Items.
type baseItemDtoQueryResult struct {
	Items            []baseItemDto `json:"Items"`
	TotalRecordCount int           `json:"TotalRecordCount"`
	StartIndex       int           `json:"StartIndex"`
}

// mediaStreamDto mirrors the OpenAPI schema's MediaStream, trimmed to the
// fields storage.MediaStreamRef carries (docs/05's item.go doc comment:
// "not every MediaStream field, most of which ... is transcode-decision
// detail out of this slice's scope").
//
// IsHearingImpaired/IsTextSubtitleStream/SupportsExternalStream are NOT
// omitempty and carry no corresponding storage.MediaStreamRef field —
// found required by reading jellyfin-sdk-kotlin's MediaStream.kt directly:
// this DTO is nested inside both MediaSourceInfo (PlaybackInfo response)
// and BaseItemDto (GetItem response), so it hits kotlinx.serialization's
// strict MissingFieldException on every item that has any audio/video
// track at all, i.e. effectively every playable item, the moment the
// native player parses either response. false is an accurate value for
// every one of these given what this project tracks today (no hearing-
// impaired flag, no forced/external-subtitle distinction beyond IsForced
// itself). IsInterlaced/IsOriginal/IsExternal, unlike those three, DO now
// have a real storage.MediaStreamRef field behind them (docs/12's
// "Episode/movie detail page — MediaStream.DisplayTitle" — needed to
// compute DisplayTitle below, and exposed on the wire since the reference
// server does too).
type mediaStreamDto struct {
	Codec                  string   `json:"Codec,omitempty"`
	Language               string   `json:"Language,omitempty"`
	Title                  string   `json:"Title,omitempty"`
	DisplayTitle           string   `json:"DisplayTitle,omitempty"`
	Type                   string   `json:"Type"`
	Index                  int      `json:"Index"`
	IsDefault              bool     `json:"IsDefault"`
	IsForced               bool     `json:"IsForced"`
	IsInterlaced           bool     `json:"IsInterlaced"`
	IsHearingImpaired      bool     `json:"IsHearingImpaired"`
	IsOriginal             bool     `json:"IsOriginal"`
	IsExternal             bool     `json:"IsExternal"`
	IsTextSubtitleStream   bool     `json:"IsTextSubtitleStream"`
	SupportsExternalStream bool     `json:"SupportsExternalStream"`
	Channels               *int     `json:"Channels,omitempty"`
	Width                  *int     `json:"Width,omitempty"`
	Height                 *int     `json:"Height,omitempty"`
	BitRate                *int64   `json:"BitRate,omitempty"`
	Profile                string   `json:"Profile,omitempty"`
	ChannelLayout          string   `json:"ChannelLayout,omitempty"`
	TimeBase               string   `json:"TimeBase,omitempty"`
	AspectRatio            string   `json:"AspectRatio,omitempty"`
	RefFrames              *int     `json:"RefFrames,omitempty"`
	PixelFormat            string   `json:"PixelFormat,omitempty"`
	Level                  *float64 `json:"Level,omitempty"`
	SampleRate             *int     `json:"SampleRate,omitempty"`
}

func mediaStreamDtos(streams []storage.MediaStreamRef) []mediaStreamDto {
	if len(streams) == 0 {
		return nil
	}
	dtos := make([]mediaStreamDto, 0, len(streams))
	for _, s := range streams {
		dtos = append(dtos, mediaStreamDto{
			Codec:         s.Codec,
			Language:      s.Language,
			Title:         s.Title,
			DisplayTitle:  displayTitleFor(s),
			Type:          string(s.Type),
			Index:         s.Index,
			IsDefault:     s.IsDefault,
			IsForced:      s.IsForced,
			IsInterlaced:  s.IsInterlaced,
			IsOriginal:    s.IsOriginal,
			IsExternal:    s.IsExternal,
			Channels:      s.Channels,
			Width:         s.Width,
			Height:        s.Height,
			BitRate:       s.BitRate,
			Profile:       s.Profile,
			ChannelLayout: s.ChannelLayout,
			TimeBase:      s.TimeBase,
			AspectRatio:   s.AspectRatio,
			RefFrames:     s.RefFrames,
			PixelFormat:   s.PixelFormat,
			Level:         s.Level,
			SampleRate:    s.SampleRate,
		})
	}
	return dtos
}

// isFolderType reports BaseItemDto.IsFolder for t — a query-time aggregate
// the reference server derives from ItemType, per storage/item.go's doc
// comment on why it isn't stored on Item itself.
func isFolderType(t storage.ItemType) bool {
	switch t {
	case storage.ItemTypeSeries, storage.ItemTypeSeason,
		storage.ItemTypeMusicArtist, storage.ItemTypeMusicAlbum,
		storage.ItemTypeBoxSet, storage.ItemTypePlaylist,
		storage.ItemTypeFolder, storage.ItemTypeCollectionFolder,
		storage.ItemTypeUserView:
		return true
	default:
		return false
	}
}

// mediaTypeFor reports BaseItemDto.MediaType for t, another query-time
// aggregate derived from ItemType rather than stored (same reasoning as
// isFolderType above) — "" for folder-like/non-playable types (BoxSet,
// Playlist, Genre, ...), which the OpenAPI schema's own "Unknown" default
// covers via omitempty.
func mediaTypeFor(t storage.ItemType) string {
	switch t {
	case storage.ItemTypeMovie, storage.ItemTypeSeries, storage.ItemTypeSeason,
		storage.ItemTypeEpisode, storage.ItemTypeTrailer:
		return "Video"
	case storage.ItemTypeMusicArtist, storage.ItemTypeMusicAlbum, storage.ItemTypeAudio:
		return "Audio"
	default:
		return ""
	}
}

// singleInstanceImageKinds are the ImageKind values an item can only ever
// have one of — these fold into the ImageTags map (key = kind name), the
// same way Primary always has. Backdrop is deliberately not in this list:
// an item can have more than one, so it gets its own BackdropImageTags
// array instead (see that field's doc comment on baseItemDto and
// docs/12's "Movie detail page — image tags for Backdrop and Logo").
// Trickplay/Chapter images aren't image_refs kinds at all (separate
// stores) and so aren't candidates here regardless.
var singleInstanceImageKinds = []storage.ImageKind{"Primary", "Logo", "Banner", "Thumb", "Disc"}

// imageTagsFor builds BaseItemDto.ImageTags, BackdropImageTags, and
// PrimaryImageAspectRatio for id — one GetImageRefs call per candidate
// kind (bounded by singleInstanceImageKinds plus Backdrop, and by the
// page size the caller itself is bounded to, per ImageTags's own doc
// comment on the N+1 tradeoff). ref.Tag is used as the cache-busting
// value when the store has one; handleItemImage doesn't validate the tag
// against anything, so any stable non-empty value works when a scanner
// hasn't populated a real one yet. The aspect ratio is nil, not 0, when
// Width/Height weren't recorded (an ImageRef predating scan/scan.go's
// imageDimensions) — 0 would read to jellyfin-web as a real "square"
// ratio (see PrimaryImageAspectRatio's doc comment) instead of "unknown".
//
// BackdropImageTags is built in the order GetImageRefs already returns
// (ORDER BY idx ASC, sqlite/image.go) — the same index
// GET /Items/{itemId}/Images/Backdrop/{index} matches against
// (handleItemImage), so array position N always corresponds to that
// route's index N for any item whose Backdrop refs were written with
// contiguous indices starting at 0 (true of every writer in this
// codebase today: storeProviderImage/seedFolderImage never set Index
// explicitly, so it defaults to 0).
func (s *Server) imageTagsFor(ctx context.Context, id storage.ItemID) (tags map[string]string, aspectRatio *float64, backdropTags []string) {
	for _, kind := range singleInstanceImageKinds {
		refs, err := s.store.GetImageRefs(ctx, id, kind)
		if err != nil || len(refs) == 0 {
			continue
		}
		tag := refs[0].Tag
		if tag == "" {
			tag = "1"
		}
		if tags == nil {
			tags = make(map[string]string, len(singleInstanceImageKinds))
		}
		tags[string(kind)] = tag
		if kind == storage.ImageKind("Primary") && refs[0].Width > 0 && refs[0].Height > 0 {
			ratio := float64(refs[0].Width) / float64(refs[0].Height)
			aspectRatio = &ratio
		}
	}

	if refs, err := s.store.GetImageRefs(ctx, id, storage.ImageKind("Backdrop")); err == nil && len(refs) > 0 {
		backdropTags = make([]string, 0, len(refs))
		for _, ref := range refs {
			tag := ref.Tag
			if tag == "" {
				tag = "1"
			}
			backdropTags = append(backdropTags, tag)
		}
	}

	return tags, aspectRatio, backdropTags
}

// parentImageInheritanceItemTypes are the ItemTypes SeriesPrimaryImageTag
// and the Parent{Logo,Backdrop,Thumb}* fields are ever populated for —
// docs/12's "Season/Episode DTO — inherited Parent* image fields and
// SeriesPrimaryImageTag" section.
var parentImageInheritanceItemTypes = map[storage.ItemType]bool{
	storage.ItemTypeSeason:  true,
	storage.ItemTypeEpisode: true,
}

// parentImageInheritance holds baseItemDto's SeriesPrimaryImageTag and
// Parent{Logo,Backdrop,Thumb}* fields — see parentImageFieldsFor below.
type parentImageInheritance struct {
	SeriesPrimaryImageTag   string
	ParentLogoItemID        string
	ParentLogoImageTag      string
	ParentBackdropItemID    string
	ParentBackdropImageTags []string
	ParentThumbItemID       string
	ParentThumbImageTag     string
}

// parentImageFieldsFor computes parentImageInheritance for an item whose
// own already-computed image tags are ownTags/ownBackdropTags (baseItemDto
// already has these from imageTagsFor above, so this never re-derives
// them) — see docs/12's "Season/Episode DTO — inherited Parent* image
// fields and SeriesPrimaryImageTag" for the full behavior spec.
//
// series is the owning series' own item (nil if unresolved) — always the
// SeriesPrimaryImageTag source when non-nil, regardless of the item's own
// art. ancestors is the image-inheritance chain closest-first (an
// Episode's is [season, series]; a Season's is just [series]) — for each
// of Logo/Backdrop/Thumb, the first ancestor in that order carrying an
// image of the missing kind wins; a kind the item already has its own
// image for is left alone entirely. The reference server's own
// Thumb-specific quirk (the series' Thumb overriding a closer ancestor's)
// is deliberately not replicated — see docs/12's "Scope note for the
// implementer" in that same section for why it's unobservable given what
// this project's scanner indexes today.
func (s *Server) parentImageFieldsFor(ctx context.Context, ownTags map[string]string, ownBackdropTags []string, series *storage.Item, ancestors []storage.Item) parentImageInheritance {
	var out parentImageInheritance

	if series != nil {
		if tags, _, _ := s.imageTagsFor(ctx, series.ID); tags != nil {
			if tag, ok := tags["Primary"]; ok {
				out.SeriesPrimaryImageTag = tag
			}
		}
	}

	if _, ok := ownTags["Logo"]; !ok {
		for _, anc := range ancestors {
			tags, _, _ := s.imageTagsFor(ctx, anc.ID)
			if tag, ok := tags["Logo"]; ok {
				out.ParentLogoItemID, out.ParentLogoImageTag = string(anc.ID), tag
				break
			}
		}
	}

	if len(ownBackdropTags) == 0 {
		for _, anc := range ancestors {
			_, _, backdrops := s.imageTagsFor(ctx, anc.ID)
			if len(backdrops) > 0 {
				out.ParentBackdropItemID, out.ParentBackdropImageTags = string(anc.ID), backdrops
				break
			}
		}
	}

	if _, ok := ownTags["Thumb"]; !ok {
		for _, anc := range ancestors {
			tags, _, _ := s.imageTagsFor(ctx, anc.ID)
			if tag, ok := tags["Thumb"]; ok {
				out.ParentThumbItemID, out.ParentThumbImageTag = string(anc.ID), tag
				break
			}
		}
	}

	return out
}

// videoFileItemTypes are the ItemTypes Width/Height/IsHD/VideoType are
// ever populated for — docs/12's "Episode DTO — item-level
// video/permission/identity fields" section. Series/Season are folder-
// shaped and never have a video file of their own, so they're excluded
// even though mediaTypeFor labels them "Video" for a different purpose
// (playback-eligibility, not "this item has a resolution").
var videoFileItemTypes = map[storage.ItemType]bool{
	storage.ItemTypeMovie:   true,
	storage.ItemTypeEpisode: true,
	storage.ItemTypeTrailer: true,
}

// primaryVideoStreamFor returns item's own primary video track — the first
// Video-type entry in its MediaStreams, in ffprobe's own stream order — or
// nil if it has none. Backs Width/Height/IsHD (docs/12's same section).
func primaryVideoStreamFor(streams []storage.MediaStreamRef) *storage.MediaStreamRef {
	for i := range streams {
		if streams[i].Type == storage.StreamTypeVideo {
			return &streams[i]
		}
	}
	return nil
}

// hasSubtitleStream reports whether item has at least one Subtitle-type
// MediaStream — backs HasSubtitles (docs/12's same section).
func hasSubtitleStream(streams []storage.MediaStreamRef) bool {
	for _, s := range streams {
		if s.Type == storage.StreamTypeSubtitle {
			return true
		}
	}
	return false
}

// locationTypeFor computes BaseItemDto.LocationType for item — docs/12's
// "Episode DTO — item-level video/permission/identity fields" section.
// "FileSystem" for an item with a real Path, "Virtual" otherwise (a
// placeholder or a folder-shaped item with nothing of its own on disk) —
// Tinyfin has no remote/channel source concept, so the reference server's
// third possible value ("Remote") never applies here.
func locationTypeFor(item storage.Item) string {
	if item.Path != "" {
		return "FileSystem"
	}
	return "Virtual"
}

// etagFor and displayPreferencesIDFor mint the opaque, deterministic
// identifier strings docs/12's "Episode DTO — item-level
// video/permission/identity fields" section describes — a client never
// parses or compares these across servers, so any deterministic hash
// producing the reference server's own fixed-length-hex-string shape is
// equivalent. etagFor is keyed on the item's own identity plus its
// DateCreated (this project's closest equivalent to a "last modified"
// signal — storage.Item doesn't separately track one). displayPreferencesIDFor
// is deliberately keyed on itemType alone, not the item's own id — mirroring
// the reference server's own (surprising but confirmed) behavior of sharing
// one DisplayPreferencesId across every item of the same type.
func etagFor(item storage.Item) string {
	sum := sha256.Sum256([]byte("etag-" + string(item.ID) + "-" + item.DateCreated.UTC().Format(time.RFC3339Nano)))
	return hex.EncodeToString(sum[:16])
}

func displayPreferencesIDFor(itemType storage.ItemType) string {
	sum := sha256.Sum256([]byte("display-preferences-" + string(itemType)))
	return hex.EncodeToString(sum[:16])
}

// libraryBaseItemDto builds the BaseItemDto for a library root itself — a
// storage.Library, not a storage.Item (they're separate stores,
// storage/library.go's doc comment), so it can't go through baseItemDto()
// below. Shared by handleUserViews (library.go), handleItemGet's
// library-root fallback, and handleItemAncestors' final entry — previously
// duplicated ad hoc in the first of those.
//
// Type is always "CollectionFolder", never storage.ItemTypeUserView —
// confirmed against a real Jellyfin server (reference server): GET /UserViews
// for an ordinary library (not a synthetic aggregate view like Favorites)
// comes back with Type "CollectionFolder". storage.ItemTypeUserView exists
// for a genuinely different kind of view this project doesn't emit here.
//
// UserData is populated the same way baseItemDto's is (userDataFor) rather
// than left nil — confirmed via the same real-server comparison: a library
// root's UserData carries the caller's playstate for it (favorite/played on
// the library itself) same as any other item, not omitted. ctx must carry
// an authenticated caller (every registered call site requires auth, see
// server.go) for this to populate; callers with no reliable caller in
// context would need to pass one in some other way, but none of today's do.
func (s *Server) libraryBaseItemDto(ctx context.Context, lib storage.Library) baseItemDto {
	dto := baseItemDto{
		Name:           lib.Name,
		ServerID:       s.cfg.ServerID,
		ID:             string(lib.ID),
		IsFolder:       true,
		Type:           string(storage.ItemTypeCollectionFolder),
		CollectionType: string(lib.CollectionType),
	}
	if ac, ok := AuthFromContext(ctx); ok && ac != nil {
		dto.UserData = s.userDataFor(ctx, ac.User.ID, storage.Item{ID: storage.ItemID(lib.ID), Type: storage.ItemTypeCollectionFolder})
	}
	return dto
}

// userDataFor builds BaseItemDto.UserData for (userID, item) from
// PlaystateStore — see baseItemDto's UserData field doc comment. A lookup
// error (no row yet — the common case for an item the user has never
// touched) is treated as the storage.Playstate zero value, same convention
// sessions_playback.go's reportPlaybackPosition/handleUserPlayedItemsMark
// already use.
//
// UnplayedItemCount is populated alongside the rest, gated by
// childCountItemTypes (same folder-shaped-item set ChildCount uses) — see
// unplayedItemCountFor's doc comment and docs/12's "Season/Series/BoxSet
// UserData — UnplayedItemCount". A query error there is treated as "leave
// it unset" rather than failing the whole UserData, same "best effort,
// don't break the item response over a secondary field" posture ChildCount
// itself already takes in baseItemDto below.
func (s *Server) userDataFor(ctx context.Context, userID storage.UserID, item storage.Item) *userItemDataDto {
	state, err := s.store.GetPlaystate(ctx, userID, item.ID)
	if err != nil {
		state = storage.Playstate{}
	}
	dto := &userItemDataDto{
		PlaybackPositionTicks: state.PlaybackPositionTicks,
		PlayCount:             state.PlayCount,
		IsFavorite:            state.IsFavorite,
		Played:                state.Played,
		Key:                   string(item.ID),
		ItemID:                string(item.ID),
	}
	if childCountItemTypes[item.Type] {
		if n, err := s.unplayedItemCountFor(ctx, userID, item); err == nil {
			dto.UnplayedItemCount = n
		}
	}
	return dto
}

func (s *Server) baseItemDto(ctx context.Context, item storage.Item) baseItemDto {
	imageTags, primaryImageAspectRatio, backdropImageTags := s.imageTagsFor(ctx, item.ID)
	dto := baseItemDto{
		Name:                    item.Name,
		ServerID:                s.cfg.ServerID,
		ID:                      string(item.ID),
		DateCreated:             item.DateCreated,
		Container:               item.Container,
		SortName:                item.SortName,
		PremiereDate:            item.PremiereDate,
		EndDate:                 item.EndDate,
		Path:                    item.Path,
		OfficialRating:          item.OfficialRating,
		OriginalTitle:           item.OriginalTitle,
		Overview:                item.Overview,
		Taglines:                item.Taglines,
		Genres:                  item.Genres,
		CommunityRating:         item.CommunityRating,
		CriticRating:            item.CriticRating,
		RunTimeTicks:            item.RunTimeTicks,
		ProductionYear:          item.ProductionYear,
		IndexNumber:             item.IndexNumber,
		ParentIndexNumber:       item.ParentIndexNumber,
		ProviderIDs:             item.ProviderIDs,
		IsFolder:                isFolderType(item.Type),
		Type:                    string(item.Type),
		Tags:                    item.Tags,
		Artists:                 item.Artists,
		Album:                   item.Album,
		AlbumArtist:             item.AlbumArtist,
		CollectionType:          string(item.CollectionType),
		MediaType:               mediaTypeFor(item.Type),
		MediaStreams:            mediaStreamDtos(item.MediaStreams),
		ImageTags:               imageTags,
		BackdropImageTags:       backdropImageTags,
		PrimaryImageAspectRatio: primaryImageAspectRatio,
		People:                  peopleDtos(item.People),
		GenreItems:              genreItemsFor(item.Genres),
		Studios:                 studioItemsFor(item.Studios),
		ExternalUrls:            externalURLsFor(item.Type, item.ProviderIDs),
		MediaSources:            mediaSourcesFor(item),
		LocationType:            locationTypeFor(item),
	}
	if childCountItemTypes[item.Type] {
		if n, err := s.childCountFor(ctx, item.ID); err == nil {
			dto.ChildCount = &n
		}
	}
	if item.ParentID != nil {
		dto.ParentID = string(*item.ParentID)
	}

	var seriesItem, seasonItem *storage.Item
	if item.SeriesID != nil {
		dto.SeriesID = string(*item.SeriesID)
		if series, err := s.store.GetItem(ctx, *item.SeriesID); err == nil {
			dto.SeriesName = series.Name
			seriesItem = &series
			if len(series.Studios) > 0 {
				dto.SeriesStudio = series.Studios[0]
			}
		}
	}
	if item.SeasonID != nil {
		dto.SeasonID = string(*item.SeasonID)
		if season, err := s.store.GetItem(ctx, *item.SeasonID); err == nil {
			dto.SeasonName = season.Name
			seasonItem = &season
		}
	}

	if parentImageInheritanceItemTypes[item.Type] {
		var ancestors []storage.Item
		if seasonItem != nil {
			ancestors = append(ancestors, *seasonItem)
		}
		if seriesItem != nil {
			ancestors = append(ancestors, *seriesItem)
		}
		fields := s.parentImageFieldsFor(ctx, imageTags, backdropImageTags, seriesItem, ancestors)
		dto.SeriesPrimaryImageTag = fields.SeriesPrimaryImageTag
		dto.ParentLogoItemID = fields.ParentLogoItemID
		dto.ParentLogoImageTag = fields.ParentLogoImageTag
		dto.ParentBackdropItemID = fields.ParentBackdropItemID
		dto.ParentBackdropImageTags = fields.ParentBackdropImageTags
		dto.ParentThumbItemID = fields.ParentThumbItemID
		dto.ParentThumbImageTag = fields.ParentThumbImageTag
	}

	if videoFileItemTypes[item.Type] && item.Path != "" {
		dto.VideoType = "VideoFile"
		if primary := primaryVideoStreamFor(item.MediaStreams); primary != nil {
			dto.Width = primary.Width
			dto.Height = primary.Height
			if primary.Height != nil && *primary.Height >= 720 {
				dto.IsHD = true
			}
		}
	}
	dto.HasSubtitles = hasSubtitleStream(item.MediaStreams)

	if ac, ok := AuthFromContext(ctx); ok && ac.Authenticated {
		dto.UserData = s.userDataFor(ctx, ac.User.ID, item)
		dto.PlayAccess = "Full"
		dto.CanDelete = ac.User.IsAdministrator
		dto.CanDownload = ac.User.IsAdministrator && !isFolderType(item.Type)
		dto.Etag = etagFor(item)
		dto.DisplayPreferencesID = displayPreferencesIDFor(item.Type)
	}
	return dto
}
