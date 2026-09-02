// nfo.go parses Kodi/Jellyfin .nfo sidecar files — a publicly documented,
// third-party XML metadata convention (kodi.wiki's "NFO files" pages), not
// something specific to or derived from the reference server's own source.
//
// A .nfo is a local, path-scoped input (it lives next to the file it
// describes), fundamentally different from a metadata.Provider — it does
// not implement that interface (D11: that contract is reserved for
// network/RPC-safe providers). Instead scan.go calls the apply* functions
// below directly, after enrich(), so a local .nfo's fields always win over
// a configured Provider's Result: docs/08's "letting manual edits always
// win over an external source". Each apply* function follows the exact
// same "if the source actually set this field, overwrite" pattern
// enrich() uses for Provider results.
//
// A missing or unparsable .nfo is never a scan failure — it's reported as
// a warning (jobPool.addWarning), the same tolerance enrich() gives a
// provider error, and the item stays cataloged from whatever
// filename/provider-derived data it already had.
package scan

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// commonNFOFields is the set of Kodi .nfo tags this project maps onto
// storage.Item, shared by the <movie> and <tvshow> root elements (they
// carry the same fields under Kodi's schema). Deliberately narrow: no
// <trailer>, <fileinfo>/<streamdetails> (ffprobe already covers technical
// media info, internal/scan/probe.go) — this project maps only the tags it
// actually has a storage.Item field for, per docs/08's minimal scope.
type commonNFOFields struct {
	Title         string       `xml:"title"`
	OriginalTitle string       `xml:"originaltitle"`
	Plot          string       `xml:"plot"`
	Year          int          `xml:"year"`
	Premiered     string       `xml:"premiered"`
	Genres        []string     `xml:"genre"`
	Tags          []string     `xml:"tag"`
	MPAA          string       `xml:"mpaa"`
	Rating        float64      `xml:"rating"`
	Ratings       *ratingsEl   `xml:"ratings"`
	UniqueIDs     []uniqueIDEl `xml:"uniqueid"`
}

// ratingsEl is Kodi's newer <ratings><rating><value>...</value></rating>
// shape (supports multiple rating sources); ratingsEl.Rating[0].Value is
// preferred over the older flat <rating> tag when both are present, since
// it's the schema Kodi/Jellyfin actually write today.
type ratingsEl struct {
	Rating []ratingEl `xml:"rating"`
}

type ratingEl struct {
	Value float64 `xml:"value"`
}

// uniqueIDEl is Kodi's <uniqueid type="tmdb">603</uniqueid> — the join key
// this project maps into storage.Item.ProviderIDs, the same map
// enrich()/metadata.Result.ExternalIDs already populates, keyed the same
// way ("Tmdb").
type uniqueIDEl struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

// movieNFO is a Kodi <movie> .nfo document.
type movieNFO struct {
	XMLName xml.Name `xml:"movie"`
	commonNFOFields
}

// tvshowNFO is a Kodi <tvshow> .nfo document (series root, tvshow.nfo).
type tvshowNFO struct {
	XMLName xml.Name `xml:"tvshow"`
	commonNFOFields
}

// episodeNFO is a Kodi <episodedetails> .nfo document. It embeds
// commonNFOFields so an episode .nfo gets the same Genres/ProviderIDs/
// CommunityRating/ProductionYear/Tags/MPAA/OriginalTitle handling as
// <movie>/<tvshow> via applyCommonNFOFields — Title/Plot/PremiereDate
// included, since commonNFOFields already reads <title>, <plot> and
// <premiered>.
//
// Aired is separate because Kodi's <episodedetails> schema historically
// uses <aired> (not <premiered>) for the air date; applyEpisodeNFO tries
// <premiered> first (via applyCommonNFOFields) and falls back to <aired>
// only if that left PremiereDate unset, so either convention works without
// duplicating the date-parsing logic.
//
// Season/Episode are parsed but deliberately not applied to the item:
// nameparse.go already derives season/episode numbers from the filename
// before any .nfo is consulted, and that's reliable enough on its own —
// see the package doc comment's "don't duplicate logic" note in the task
// this file implements.
type episodeNFO struct {
	XMLName xml.Name `xml:"episodedetails"`
	commonNFOFields
	Aired   string `xml:"aired"`
	Season  int    `xml:"season"`
	Episode int    `xml:"episode"`
}

// readFirstExisting returns the contents of the first path in candidates
// that exists and is readable, or (nil, false) if none do — used to try
// multiple valid .nfo filename conventions in priority order.
func readFirstExisting(candidates ...string) ([]byte, bool) {
	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err == nil {
			return data, true
		}
	}
	return nil, false
}

// parseNFODate parses a Kodi .nfo date tag (<premiered>, <aired>), always
// "YYYY-MM-DD". Returns ok=false for an empty or malformed value — that's
// not a parse failure worth a warning on its own, just "this tag wasn't
// usable," matching how the rest of this file treats absent tags.
func parseNFODate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// nfoRating picks the community rating value out of either .nfo shape:
// the newer <ratings><rating><value> (preferred, first entry) or the
// older flat <rating>. Returns ok=false if neither is present/positive.
func nfoRating(n commonNFOFields) (float64, bool) {
	if n.Ratings != nil {
		for _, r := range n.Ratings.Rating {
			if r.Value > 0 {
				return r.Value, true
			}
		}
	}
	if n.Rating > 0 {
		return n.Rating, true
	}
	return 0, false
}

// nfoProviderID returns the value of the first <uniqueid> of the given
// type (case-insensitive, e.g. "tmdb"), if any.
func nfoProviderID(ids []uniqueIDEl, idType string) (string, bool) {
	for _, id := range ids {
		if strings.EqualFold(id.Type, idType) {
			if v := strings.TrimSpace(id.Value); v != "" {
				return v, true
			}
		}
	}
	return "", false
}

// applyCommonNFOFields overlays n's non-empty fields onto item, using the
// same "if the source actually set this, overwrite" pattern as enrich().
func applyCommonNFOFields(item *storage.Item, n commonNFOFields) {
	if n.Title != "" {
		item.Name = n.Title
		item.SortName = n.Title
	}
	if n.OriginalTitle != "" {
		item.OriginalTitle = n.OriginalTitle
	}
	if n.Plot != "" {
		item.Overview = n.Plot
	}
	if n.Year > 0 {
		y := n.Year
		item.ProductionYear = &y
	}
	if t, ok := parseNFODate(n.Premiered); ok {
		item.PremiereDate = &t
	}
	if len(n.Genres) > 0 {
		item.Genres = n.Genres
	}
	if len(n.Tags) > 0 {
		item.Tags = n.Tags
	}
	if n.MPAA != "" {
		item.OfficialRating = n.MPAA
	}
	if r, ok := nfoRating(n); ok {
		item.CommunityRating = &r
	}
	if id, ok := nfoProviderID(n.UniqueIDs, "tmdb"); ok {
		if item.ProviderIDs == nil {
			item.ProviderIDs = map[string]string{}
		}
		item.ProviderIDs["Tmdb"] = id
	}
}

// applyMovieNFO looks for a movie .nfo next to videoFile inside folder —
// "movie.nfo" first, then "<video-basename>.nfo" (both are conventions
// Kodi documents as valid, tried in that priority order) — and overlays
// any fields it defines onto item. A missing or malformed .nfo is not a
// scan failure: see the package doc comment.
func (s *Scanner) applyMovieNFO(pool *jobPool, item *storage.Item, folder, videoFile string) {
	base := strings.TrimSuffix(videoFile, filepath.Ext(videoFile))
	data, found := readFirstExisting(
		filepath.Join(folder, "movie.nfo"),
		filepath.Join(folder, base+".nfo"),
	)
	if !found {
		return
	}
	var n movieNFO
	if err := xml.Unmarshal(data, &n); err != nil {
		pool.addWarning(fmt.Sprintf("parsing movie .nfo in %q: %v (ignored)", folder, err))
		return
	}
	applyCommonNFOFields(item, n.commonNFOFields)
}

// applySeriesNFO looks for "tvshow.nfo" in seriesPath (the series root
// folder) and overlays any fields it defines onto item.
func (s *Scanner) applySeriesNFO(pool *jobPool, item *storage.Item, seriesPath string) {
	data, found := readFirstExisting(filepath.Join(seriesPath, "tvshow.nfo"))
	if !found {
		return
	}
	var n tvshowNFO
	if err := xml.Unmarshal(data, &n); err != nil {
		pool.addWarning(fmt.Sprintf("parsing tvshow.nfo in %q: %v (ignored)", seriesPath, err))
		return
	}
	applyCommonNFOFields(item, n.commonNFOFields)
}

// applyEpisodeNFO looks for "<episode-video-basename>.nfo" next to
// episodePath and overlays any fields it defines onto item. It reuses
// applyCommonNFOFields (same handling <movie>/<tvshow> get), then falls
// back to <aired> for PremiereDate if <premiered> wasn't present — see the
// episodeNFO doc comment.
func (s *Scanner) applyEpisodeNFO(pool *jobPool, item *storage.Item, episodePath string) {
	base := strings.TrimSuffix(episodePath, filepath.Ext(episodePath))
	data, found := readFirstExisting(base + ".nfo")
	if !found {
		return
	}
	var n episodeNFO
	if err := xml.Unmarshal(data, &n); err != nil {
		pool.addWarning(fmt.Sprintf("parsing episode .nfo for %q: %v (ignored)", episodePath, err))
		return
	}
	applyCommonNFOFields(item, n.commonNFOFields)
	if item.PremiereDate == nil && n.Aired != "" {
		if t, ok := parseNFODate(n.Aired); ok {
			item.PremiereDate = &t
		}
	}
}
