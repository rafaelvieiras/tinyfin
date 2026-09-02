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
	// Jellyfin server (staging, :8097) for a title whose fanart.jpg the
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
// IsInterlaced through SupportsExternalStream are NOT omitempty and carry
// no corresponding storage.MediaStreamRef field — found required by
// reading jellyfin-sdk-kotlin's MediaStream.kt directly: this DTO is
// nested inside both MediaSourceInfo (PlaybackInfo response) and
// BaseItemDto (GetItem response), so it hits kotlinx.serialization's
// strict MissingFieldException on every item that has any audio/video
// track at all, i.e. effectively every playable item, the moment the
// native player parses either response. false is an accurate value for
// every one of these given what this project tracks today (no interlace
// detection, no forced/external-subtitle distinction beyond IsForced
// itself, no "original language" flag).
type mediaStreamDto struct {
	Codec                  string `json:"Codec,omitempty"`
	Language               string `json:"Language,omitempty"`
	Title                  string `json:"Title,omitempty"`
	Type                   string `json:"Type"`
	Index                  int    `json:"Index"`
	IsDefault              bool   `json:"IsDefault"`
	IsForced               bool   `json:"IsForced"`
	IsInterlaced           bool   `json:"IsInterlaced"`
	IsHearingImpaired      bool   `json:"IsHearingImpaired"`
	IsOriginal             bool   `json:"IsOriginal"`
	IsExternal             bool   `json:"IsExternal"`
	IsTextSubtitleStream   bool   `json:"IsTextSubtitleStream"`
	SupportsExternalStream bool   `json:"SupportsExternalStream"`
	Channels               *int   `json:"Channels,omitempty"`
	Width                  *int   `json:"Width,omitempty"`
	Height                 *int   `json:"Height,omitempty"`
	BitRate                *int64 `json:"BitRate,omitempty"`
}

func mediaStreamDtos(streams []storage.MediaStreamRef) []mediaStreamDto {
	if len(streams) == 0 {
		return nil
	}
	dtos := make([]mediaStreamDto, 0, len(streams))
	for _, s := range streams {
		dtos = append(dtos, mediaStreamDto{
			Codec:     s.Codec,
			Language:  s.Language,
			Title:     s.Title,
			Type:      string(s.Type),
			Index:     s.Index,
			IsDefault: s.IsDefault,
			IsForced:  s.IsForced,
			Channels:  s.Channels,
			Width:     s.Width,
			Height:    s.Height,
			BitRate:   s.BitRate,
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

// libraryBaseItemDto builds the BaseItemDto for a library root itself — a
// storage.Library, not a storage.Item (they're separate stores,
// storage/library.go's doc comment), so it can't go through baseItemDto()
// below. Shared by handleUserViews (library.go), handleItemGet's
// library-root fallback, and handleItemAncestors' final entry — previously
// duplicated ad hoc in the first of those.
//
// Type is always "CollectionFolder", never storage.ItemTypeUserView —
// confirmed against a real Jellyfin server (staging, :8097): GET /UserViews
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
		dto.UserData = s.userDataFor(ctx, ac.User.ID, storage.ItemID(lib.ID))
	}
	return dto
}

// userDataFor builds BaseItemDto.UserData for (userID, itemID) from
// PlaystateStore — see baseItemDto's UserData field doc comment. A lookup
// error (no row yet — the common case for an item the user has never
// touched) is treated as the storage.Playstate zero value, same convention
// sessions_playback.go's reportPlaybackPosition/handleUserPlayedItemsMark
// already use.
func (s *Server) userDataFor(ctx context.Context, userID storage.UserID, itemID storage.ItemID) *userItemDataDto {
	state, err := s.store.GetPlaystate(ctx, userID, itemID)
	if err != nil {
		state = storage.Playstate{}
	}
	return &userItemDataDto{
		PlaybackPositionTicks: state.PlaybackPositionTicks,
		PlayCount:             state.PlayCount,
		IsFavorite:            state.IsFavorite,
		Played:                state.Played,
		Key:                   string(itemID),
		ItemID:                string(itemID),
	}
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
	}
	if item.ParentID != nil {
		dto.ParentID = string(*item.ParentID)
	}
	if item.SeriesID != nil {
		dto.SeriesID = string(*item.SeriesID)
		if series, err := s.store.GetItem(ctx, *item.SeriesID); err == nil {
			dto.SeriesName = series.Name
		}
	}
	if item.SeasonID != nil {
		dto.SeasonID = string(*item.SeasonID)
		if season, err := s.store.GetItem(ctx, *item.SeasonID); err == nil {
			dto.SeasonName = season.Name
		}
	}
	if ac, ok := AuthFromContext(ctx); ok && ac.Authenticated {
		dto.UserData = s.userDataFor(ctx, ac.User.ID, item.ID)
	}
	return dto
}
