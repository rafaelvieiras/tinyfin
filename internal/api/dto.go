package api

import (
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

	MediaStreams []mediaStreamDto `json:"MediaStreams,omitempty"`

	// ImageTags deliberately omitted: building it honestly would require
	// an ImageStore.GetImageRefs call per item per kind on every list
	// response, which turns a bounded QueryItems page into N+1 extra
	// queries — against AGENTS.md principle 1's spirit even though each
	// individual query is itself bounded. A client can still fetch a
	// specific item's images directly via GET
	// /Items/{itemId}/Images/{imageType} without needing a tag first (the
	// tag is a caching hint, not required to fetch the image). Revisit if
	// a real client turns out to depend on ImageTags being present to
	// decide whether to even request an image.
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
type mediaStreamDto struct {
	Codec     string `json:"Codec,omitempty"`
	Language  string `json:"Language,omitempty"`
	Title     string `json:"Title,omitempty"`
	Type      string `json:"Type"`
	Index     int    `json:"Index"`
	IsDefault bool   `json:"IsDefault"`
	IsForced  bool   `json:"IsForced"`
	Channels  *int   `json:"Channels,omitempty"`
	Width     *int   `json:"Width,omitempty"`
	Height    *int   `json:"Height,omitempty"`
	BitRate   *int64 `json:"BitRate,omitempty"`
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

func (s *Server) baseItemDto(item storage.Item) baseItemDto {
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
		MediaStreams:      mediaStreamDtos(item.MediaStreams),
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
	return dto
}
