// trickplay.go serves the seek-thumbnail sprite sheets
// (docs/contract/specs/trickplay.md): the tiles playlist, the JPEG sheets
// and the Trickplay member of BaseItemDto. Generation lives in
// internal/trickplay.
package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

type forceTrickplayKey struct{}

func withForceTrickplay(ctx context.Context) context.Context {
	return context.WithValue(ctx, forceTrickplayKey{}, true)
}

func forceTrickplay(ctx context.Context) bool {
	v, _ := ctx.Value(forceTrickplayKey{}).(bool)
	return v
}

// regenerateTrickplay rebuilds the sheets of an item (RegenerateTrickplay on
// an item refresh); a folder item (series, season) stands for its videos.
func (s *Server) regenerateTrickplay(id storage.ItemID) {
	ctx := context.Background()
	item, err := s.store.GetItem(ctx, id)
	if err != nil {
		return
	}
	lib, err := s.store.GetLibrary(ctx, item.LibraryID)
	if err != nil {
		return
	}
	switch item.Type {
	case storage.ItemTypeMovie, storage.ItemTypeEpisode, storage.ItemTypeVideo, storage.ItemTypeMusicVideo:
		s.trickplay.EnqueueItem(lib, item, true)
		return
	}
	for off := 0; ; off += 500 {
		page, err := s.store.QueryItems(ctx, storage.ItemFilter{
			ParentID: &id, Recursive: true, Limit: 500, Offset: off,
			ItemTypes: []storage.ItemType{storage.ItemTypeMovie, storage.ItemTypeEpisode, storage.ItemTypeVideo, storage.ItemTypeMusicVideo},
		})
		if err != nil {
			return
		}
		for _, it := range page.Items {
			s.trickplay.EnqueueItem(lib, it, true)
		}
		if len(page.Items) < 500 {
			return
		}
	}
}

// trickplayDto is one resolution of BaseItemDto.Trickplay.
type trickplayDto struct {
	Width          int `json:"Width"`
	Height         int `json:"Height"`
	TileWidth      int `json:"TileWidth"`
	TileHeight     int `json:"TileHeight"`
	ThumbnailCount int `json:"ThumbnailCount"`
	Interval       int `json:"Interval"`
	Bandwidth      int `json:"Bandwidth"`
}

// applyTrickplay fills dto.Trickplay: {mediaSourceId: {width: info}} for a
// video item, an empty object when it has no sheets; folders carry none.
func (s *Server) applyTrickplay(ctx context.Context, dto *baseItemDto, item storage.Item) {
	switch item.Type {
	case storage.ItemTypeMovie, storage.ItemTypeEpisode, storage.ItemTypeVideo, storage.ItemTypeMusicVideo, storage.ItemTypeTrailer:
	default:
		return
	}
	empty := map[string]map[string]trickplayDto{}
	dto.Trickplay = &empty
	infos, err := s.store.QueryTrickplay(ctx, item.ID)
	if err != nil || len(infos) == 0 {
		return
	}
	byWidth := make(map[string]trickplayDto, len(infos))
	for _, in := range infos {
		byWidth[strconv.Itoa(in.Width)] = trickplayDto{in.Width, in.Height, in.TileWidth, in.TileHeight, in.ThumbnailCount, in.Interval, in.Bandwidth}
	}
	m := map[string]map[string]trickplayDto{string(item.ID): byWidth}
	dto.Trickplay = &m
}

// trickplayTarget resolves the item, the version and the stored info of a
// request's {itemId}/{width}; it answers the failure itself.
func (s *Server) trickplayTarget(w http.ResponseWriter, r *http.Request) (storage.Item, storage.TrickplayInfo, bool) {
	notFound := func() (storage.Item, storage.TrickplayInfo, bool) {
		writeProblemDoc(w, http.StatusNotFound, problemTypeNotFound, "Not Found", nil)
		return storage.Item{}, storage.TrickplayInfo{}, false
	}
	width, err := strconv.Atoi(r.PathValue("width"))
	if err != nil {
		writeInvalidID(w, "width", r.PathValue("width"))
		return storage.Item{}, storage.TrickplayInfo{}, false
	}
	if raw := caseInsensitiveQuery(r).Get("mediasourceid"); raw != "" {
		if _, ok := normalizeGUID(raw); !ok {
			writeInvalidID(w, "mediaSourceId", raw)
			return storage.Item{}, storage.TrickplayInfo{}, false
		}
	}
	norm, ok := normalizeGUID(r.PathValue("itemId"))
	if !ok {
		return notFound()
	}
	item, err := s.store.GetItem(r.Context(), storage.ItemID(norm))
	if err != nil || !s.itemReadableByCaller(r, item) {
		return notFound()
	}
	if v, ok := s.versionFor(r.Context(), r, item); ok {
		item = v
	}
	infos, err := s.store.QueryTrickplay(r.Context(), item.ID)
	if err != nil {
		return notFound()
	}
	for _, in := range infos {
		if in.Width == width {
			return item, in, true
		}
	}
	return notFound()
}

// handleTrickplayTiles is GET /Videos/{itemId}/Trickplay/{width}/tiles.m3u8.
func (s *Server) handleTrickplayTiles(w http.ResponseWriter, r *http.Request) {
	item, info, ok := s.trickplayTarget(w, r)
	if !ok {
		return
	}
	per := info.TileWidth * info.TileHeight
	if per <= 0 || info.Interval <= 0 {
		writeProblemDoc(w, http.StatusNotFound, problemTypeNotFound, "Not Found", nil)
		return
	}
	images := (info.ThumbnailCount + per - 1) / per
	token := ""
	if ac, ok := AuthFromContext(r.Context()); ok {
		token = ac.Token
	}
	q := "MediaSourceId=" + string(item.ID)
	if token != "" {
		q += "&ApiKey=" + url.QueryEscape(token)
	}
	intervalSec := info.Interval / 1000
	var b strings.Builder
	// TARGETDURATION is the number of sheets (what the reference writes,
	// trickplay.playlist.R2), not a segment length.
	fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-TARGETDURATION:%d\n#EXT-X-VERSION:7\n#EXT-X-MEDIA-SEQUENCE:1\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-IMAGES-ONLY\n", images)
	for i := 0; i < images; i++ {
		n := per
		if rest := info.ThumbnailCount - i*per; rest < per {
			n = rest
		}
		fmt.Fprintf(&b, "#EXTINF:%d,\n#EXT-X-TILES:RESOLUTION=%dx%d,LAYOUT=%dx%d,DURATION=%d\n%d.jpg?%s\n",
			n*intervalSec, info.Width, info.Height, info.TileWidth, info.TileHeight, intervalSec, i, q)
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}

// handleTrickplaySheet is GET /Videos/{itemId}/Trickplay/{width}/{index}.jpg
// (and the playlist, which shares the route shape).
func (s *Server) handleTrickplaySheet(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("file")
	if file == "tiles.m3u8" {
		s.handleTrickplayTiles(w, r)
		return
	}
	idxText := strings.TrimSuffix(file, ".jpg")
	idx, err := strconv.Atoi(idxText)
	if err != nil || idxText == file {
		writeInvalidID(w, "index", idxText)
		return
	}
	item, info, ok := s.trickplayTarget(w, r)
	if !ok {
		return
	}
	dir := s.trickplay.DirFor(item, info)
	if dir == "" || idx < 0 {
		writeProblemDoc(w, http.StatusNotFound, problemTypeNotFound, "Not Found", nil)
		return
	}
	f, err := os.Open(filepath.Join(dir, strconv.Itoa(idx)+".jpg"))
	if err != nil {
		writeProblemDoc(w, http.StatusNotFound, problemTypeNotFound, "Not Found", nil)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Disposition", "attachment")
	http.ServeContent(w, r, "", fi.ModTime(), f)
}
