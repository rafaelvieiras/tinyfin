package api

import (
	"context"
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

// imageTags builds BaseItemDto.ImageTags for id: currently just the
// Primary kind, which is all the card/detail views this slice renders
// actually need (see baseItemDto's ImageTags doc comment). ref.Tag is
// used as the cache-busting value when the store has one; handleItemImage
// doesn't validate the tag against anything, so any stable non-empty
// value works when a scanner hasn't populated a real one yet.
func (s *Server) imageTags(ctx context.Context, id storage.ItemID) map[string]string {
	refs, err := s.store.GetImageRefs(ctx, id, storage.ImageKind("Primary"))
	if err != nil || len(refs) == 0 {
		return nil
	}
	tag := refs[0].Tag
	if tag == "" {
		tag = "1"
	}
	return map[string]string{"Primary": tag}
}

// libraryBaseItemDto builds the BaseItemDto for a library root itself
// (Type=UserView/CollectionFolder) — a storage.Library, not a storage.Item
// (they're separate stores, storage/library.go's doc comment), so it can't
// go through baseItemDto() below. Shared by handleUserViews (library.go),
// handleItemGet's library-root fallback, and handleItemAncestors' final
// entry — previously duplicated ad hoc in the first of those.
func (s *Server) libraryBaseItemDto(lib storage.Library) baseItemDto {
	return baseItemDto{
		Name:           lib.Name,
		ServerID:       s.cfg.ServerID,
		ID:             string(lib.ID),
		IsFolder:       true,
		Type:           string(storage.ItemTypeUserView),
		CollectionType: string(lib.CollectionType),
	}
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
	dto := baseItemDto{
		Name:              item.Name,
		ServerID:          s.cfg.ServerID,
		ID:                string(item.ID),
		DateCreated:       item.DateCreated,
		Container:         item.Container,
		SortName:          item.SortName,
		PremiereDate:      item.PremiereDate,
		EndDate:           item.EndDate,
		Path:              item.Path,
		OfficialRating:    item.OfficialRating,
		Overview:          item.Overview,
		Taglines:          item.Taglines,
		Genres:            item.Genres,
		CommunityRating:   item.CommunityRating,
		CriticRating:      item.CriticRating,
		RunTimeTicks:      item.RunTimeTicks,
		ProductionYear:    item.ProductionYear,
		IndexNumber:       item.IndexNumber,
		ParentIndexNumber: item.ParentIndexNumber,
		ProviderIDs:       item.ProviderIDs,
		IsFolder:          isFolderType(item.Type),
		Type:              string(item.Type),
		Tags:              item.Tags,
		Artists:           item.Artists,
		Album:             item.Album,
		AlbumArtist:       item.AlbumArtist,
		CollectionType:    string(item.CollectionType),
		MediaType:         mediaTypeFor(item.Type),
		MediaStreams:      mediaStreamDtos(item.MediaStreams),
		ImageTags:         s.imageTags(ctx, item.ID),
	}
	if item.ParentID != nil {
		dto.ParentID = string(*item.ParentID)
	}
	if item.SeriesID != nil {
		dto.SeriesID = string(*item.SeriesID)
	}
	if item.SeasonID != nil {
		dto.SeasonID = string(*item.SeasonID)
	}
	if ac, ok := AuthFromContext(ctx); ok && ac.Authenticated {
		dto.UserData = s.userDataFor(ctx, ac.User.ID, item.ID)
	}
	return dto
}
