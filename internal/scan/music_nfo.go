package scan

import (
	"encoding/xml"
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// artistNFO is the slice of an artist.nfo (Kodi <artist> document) a music
// library reads: the biography, the genres and the MusicBrainz artist id. The
// artist keeps its folder's name.
type artistNFO struct {
	XMLName     xml.Name `xml:"artist"`
	Biography   string   `xml:"biography"`
	Overview    string   `xml:"overview"`
	Genres      []string `xml:"genre"`
	MusicBrainz string   `xml:"musicbrainzartistid"`
}

// albumNFO is the slice of an album.nfo (<album>) a music library reads.
type albumNFO struct {
	XMLName      xml.Name `xml:"album"`
	Title        string   `xml:"title"`
	Review       string   `xml:"review"`
	Overview     string   `xml:"overview"`
	Year         string   `xml:"year"`
	Genres       []string `xml:"genre"`
	Album        string   `xml:"musicbrainzalbumid"`
	ReleaseGroup string   `xml:"musicbrainzreleasegroupid"`
	AlbumArtist  string   `xml:"musicbrainzalbumartistid"`
}

func setProviderID(item *storage.Item, key, value string) {
	if value = trimSpace(value); value == "" {
		return
	}
	if item.ProviderIDs == nil {
		item.ProviderIDs = map[string]string{}
	}
	item.ProviderIDs[key] = value
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v = trimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// applyArtistNFO reads artist.nfo from the artist folder into item. A file
// that is missing or cannot be parsed leaves the artist as it was.
func (s *Scanner) applyArtistNFO(pool *jobPool, item *storage.Item) {
	data, ok := readFirstExisting(filepath.Join(item.Path, "artist.nfo"))
	if !ok {
		return
	}
	var n artistNFO
	if err := xml.Unmarshal(data, &n); err != nil {
		pool.addWarning(fmt.Sprintf("parsing artist.nfo in %q: %v (ignored)", item.Path, err))
		return
	}
	if o := firstNonEmpty(n.Biography, n.Overview); o != "" {
		item.Overview = o
	}
	if g := dedupeNames(n.Genres); len(g) > 0 {
		item.Genres = g
	}
	setProviderID(item, "MusicBrainzArtist", n.MusicBrainz)
}

// applyAlbumNFO reads album.nfo from the album folder into item: the title
// replaces the name the tracks gave, and the review, year, genres and
// MusicBrainz ids are taken as they are.
func (s *Scanner) applyAlbumNFO(pool *jobPool, item *storage.Item) {
	data, ok := readFirstExisting(filepath.Join(item.Path, "album.nfo"))
	if !ok {
		return
	}
	var n albumNFO
	if err := xml.Unmarshal(data, &n); err != nil {
		pool.addWarning(fmt.Sprintf("parsing album.nfo in %q: %v (ignored)", item.Path, err))
		return
	}
	if t := trimSpace(n.Title); t != "" {
		item.Name, item.SortName = t, t
	}
	if o := firstNonEmpty(n.Review, n.Overview); o != "" {
		item.Overview = o
	}
	if y, err := strconv.Atoi(trimSpace(n.Year)); err == nil && y > 0 {
		item.ProductionYear = &y
	}
	if g := dedupeNames(n.Genres); len(g) > 0 {
		item.Genres = g
	}
	setProviderID(item, "MusicBrainzAlbum", n.Album)
	setProviderID(item, "MusicBrainzReleaseGroup", n.ReleaseGroup)
	setProviderID(item, "MusicBrainzAlbumArtist", n.AlbumArtist)
}
