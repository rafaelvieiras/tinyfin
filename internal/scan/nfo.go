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
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

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
	SortTitle     string       `xml:"sorttitle"`
	Plot          string       `xml:"plot"`
	Year          int          `xml:"year"`
	Premiered     string       `xml:"premiered"`
	Genres        []string     `xml:"genre"`
	Tags          []string     `xml:"tag"`
	MPAA          string       `xml:"mpaa"`
	Rating        float64      `xml:"rating"`
	Ratings       *ratingsEl   `xml:"ratings"`
	UniqueIDs     []uniqueIDEl `xml:"uniqueid"`

	// Studios, Actors, Directors, Credits and Writers are the credits of
	// the item: <studio>, <actor>, <director>, <credits> (a writer) and
	// <writer>, each repeatable.
	Studios   []string  `xml:"studio"`
	Actors    []actorEl `xml:"actor"`
	Directors []string  `xml:"director"`
	Credits   []string  `xml:"credits"`
	Writers   []string  `xml:"writer"`
}

// actorEl is one <actor><name/><role/><type/><order/></actor> credit. Type
// is the credit kind and defaults to Actor; Order ranks the actors of one
// document (credits without one come last, in document order).
type actorEl struct {
	Name  string `xml:"name"`
	Role  string `xml:"role"`
	Type  string `xml:"type"`
	Order string `xml:"order"`
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

// setEl is a movie's <set>: either <set><name>X</name>...</set> or the
// older <set>X</set>.
type setEl struct {
	Name *string `xml:"name"`
	Text string  `xml:",chardata"`
}

// value is the collection name as written: the <name> child when there is
// one, else the element's own text. It is not trimmed (movies are grouped by
// the raw value); an all-white-space value names no collection.
func (e *setEl) value() string {
	if e == nil {
		return ""
	}
	v := e.Text
	if e.Name != nil {
		v = *e.Name
	}
	if strings.TrimSpace(v) == "" {
		return ""
	}
	return v
}

// movieNFO is a Kodi <movie> .nfo document.
type movieNFO struct {
	XMLName xml.Name `xml:"movie"`
	commonNFOFields
	Set *setEl `xml:"set"`
}

// tvshowNFO is a Kodi <tvshow> .nfo document (series root, tvshow.nfo).
type tvshowNFO struct {
	XMLName xml.Name `xml:"tvshow"`
	commonNFOFields
	// Runtime is the raw <runtime> text (minutes); see nfoRuntimeTicks.
	Runtime string `xml:"runtime"`
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
// Season/Episode, when present, replace the numbers the file name gave
// (applyParsedEpisodeNFO).
type episodeNFO struct {
	XMLName xml.Name `xml:"episodedetails"`
	commonNFOFields
	Aired string `xml:"aired"`
	// Season and Episode stay text so that a value that is not a number
	// leaves the file's other tags usable; nfoNumber converts them.
	Season  string `xml:"season"`
	Episode string `xml:"episode"`

	// EpisodeEnd is the episode number of the last document of a file that
	// holds several <episodedetails> (multi-episode files); nil for one.
	EpisodeEnd *int `xml:"-"`
}

// nfoNumber converts a <season>/<episode> value; nil when the tag is absent
// or not an integer.
func nfoNumber(s string) *int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return nil
	}
	return &n
}

// trimSpace drops every leading and trailing Unicode space character.
func trimSpace(s string) string {
	return strings.TrimFunc(s, unicode.IsSpace)
}

// nfoRuntimeTicks converts a <runtime> value (minutes) to ticks. Only the
// text before the first space is read, as a base-10 integer that may carry a
// sign (so "20 min" is 20, " 20 " and "12.5" are not numbers); a value
// outside 32 bits is not a number either. ok is false then.
func nfoRuntimeTicks(s string) (ticks int64, ok bool) {
	tok, _, _ := strings.Cut(s, " ")
	tok = strings.Trim(tok, "\t\n\v\f\r")
	n, err := strconv.ParseInt(tok, 10, 32)
	if err != nil {
		return 0, false
	}
	return n * 60 * 10_000_000, true
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
	if t := trimSpace(n.Title); t != "" {
		item.Name = t
		item.SortName = t
		item.ForcedSortName = ""
	}
	if t := trimSpace(n.SortTitle); t != "" {
		item.SortName = t
		item.ForcedSortName = t
	}
	if t := trimSpace(n.OriginalTitle); t != "" {
		item.OriginalTitle = t
	}
	if t := trimSpace(n.Plot); t != "" {
		item.Overview = t
	}
	if n.Year > 0 {
		y := n.Year
		item.ProductionYear = &y
	}
	if t, ok := parseNFODate(n.Premiered); ok {
		item.PremiereDate = &t
	}
	if g := dedupeNames(n.Genres); len(g) > 0 {
		item.Genres = g
	}
	if st := dedupeNames(n.Studios); len(st) > 0 {
		item.Studios = st
	}
	if people := nfoPeople(n); len(people) > 0 {
		item.People = people
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

// dedupeNames trims the names, drops empty ones and repeats that differ only
// in case (the first spelling wins).
func dedupeNames(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, n := range in {
		n = trimSpace(n)
		k := strings.ToLower(n)
		if n == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, n)
	}
	return out
}

// nfoPeople builds the credits of a document: the actors in their <order>
// (those without one after, in document order), then the directors, then
// the writers (<credits> before <writer>). A person repeated with the same
// credit type is kept once.
func nfoPeople(n commonNFOFields) []storage.Person {
	type ranked struct {
		p     storage.Person
		order int
		seq   int
	}
	var actors []ranked
	for i, a := range n.Actors {
		name := trimSpace(a.Name)
		if name == "" {
			continue
		}
		typ := trimSpace(a.Type)
		if typ == "" {
			typ = "Actor"
		}
		order := 1 << 30
		if v := nfoNumber(a.Order); v != nil && *v >= 0 {
			order = *v
		}
		actors = append(actors, ranked{p: storage.Person{Name: name, Role: trimSpace(a.Role), Type: typ}, order: order, seq: i})
	}
	sort.SliceStable(actors, func(i, j int) bool { return actors[i].order < actors[j].order })
	var out []storage.Person
	seen := map[string]bool{}
	add := func(p storage.Person) {
		k := strings.ToLower(p.Name) + "\x00" + p.Type
		if seen[k] {
			return
		}
		seen[k] = true
		out = append(out, p)
	}
	for _, a := range actors {
		add(a.p)
	}
	for _, d := range dedupeNames(n.Directors) {
		add(storage.Person{Name: d, Type: "Director"})
	}
	for _, w := range dedupeNames(append(append([]string{}, n.Credits...), n.Writers...)) {
		add(storage.Person{Name: w, Type: "Writer"})
	}
	return out
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
	item.SetName = n.Set.value()
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
	if t, ok := nfoRuntimeTicks(n.Runtime); ok {
		item.RunTimeTicks = &t
	}
}

// namedSeasonEl is Kodi's <namedseason number="1">Custom Name</namedseason>
// — a <tvshow>-root-level override for one specific season's display name.
type namedSeasonEl struct {
	Number int    `xml:"number,attr"`
	Name   string `xml:",chardata"`
}

// seasonNFO is the narrow slice of a Kodi <tvshow> .nfo document this
// scanner reads for Season items: <namedseason> only. Matches the reference
// server's SeriesNfoSeasonProvider/SeriesNfoSeasonParser (validated,
// docs/08's "Local-vs-remote precedence" section), which ignores every
// other <tvshow> field for a Season entity — title/plot/cast/etc. in that
// same file are series-level, read by applySeriesNFO instead.
type seasonNFO struct {
	XMLName      xml.Name        `xml:"tvshow"`
	NamedSeasons []namedSeasonEl `xml:"namedseason"`
}

// applySeasonNFO looks for "tvshow.nfo" in seasonPath (the season's own
// folder) first, falling back to seriesPath (the series root) if absent
// there — the same lookup order as the reference server's
// SeriesNfoSeasonProvider. Unlike applySeriesNFO, this deliberately extracts
// only the <namedseason> entry whose number attribute matches seasonNum,
// overlaying its text onto item.Name — every other field in the file
// (title, plot, cast, ...) is series-level, not season-level, and is
// ignored here exactly as the reference server's SeriesNfoSeasonParser
// ignores it. A file with no matching <namedseason> (e.g. a series-root
// tvshow.nfo with no <namedseason> tags at all) is a normal miss, not a
// warning.
func (s *Scanner) applySeasonNFO(pool *jobPool, item *storage.Item, seasonPath, seriesPath string, seasonNum int) {
	data, found := readFirstExisting(
		filepath.Join(seasonPath, "tvshow.nfo"),
		filepath.Join(seriesPath, "tvshow.nfo"),
	)
	if !found {
		return
	}
	var n seasonNFO
	if err := xml.Unmarshal(data, &n); err != nil {
		pool.addWarning(fmt.Sprintf("parsing season tvshow.nfo for %q: %v (ignored)", seasonPath, err))
		return
	}
	for _, ns := range n.NamedSeasons {
		if ns.Number != seasonNum {
			continue
		}
		// The name is used as written (surrounding white space included);
		// a value that is only white space is not a name.
		if trimSpace(ns.Name) != "" {
			item.Name = ns.Name
		}
		return
	}
}

// episodeNFOFor reads and parses "<episode-video-basename>.nfo" next to
// episodePath. found is false when there is no readable file; a file that does
// not parse is reported as a warning and treated as missing.
//
// A file may hold several <episodedetails> documents (a multi-episode file).
// They are ordered by episode number (documents without one last); the first
// one supplies every field, except that the titles of all documents are
// joined with " / " (so are the plots), and the episode number of the last
// document is the file's last episode (EpisodeEnd).
func (s *Scanner) episodeNFOFor(pool *jobPool, episodePath string) (n episodeNFO, found bool) {
	base := strings.TrimSuffix(episodePath, filepath.Ext(episodePath))
	data, ok := readFirstExisting(base + ".nfo")
	if !ok {
		return n, false
	}
	docs, err := decodeEpisodeDocs(data)
	if err != nil || len(docs) == 0 {
		if err == nil {
			err = fmt.Errorf("no <episodedetails> document")
		}
		pool.addWarning(fmt.Sprintf("parsing episode .nfo for %q: %v (ignored)", episodePath, err))
		return n, false
	}
	return mergeEpisodeDocs(docs), true
}

// decodeEpisodeDocs reads every <episodedetails> element of data, in order.
// Text and processing instructions between the documents are skipped; a
// document that does not parse ends the list (the ones before it are kept),
// and a file whose first document does not parse is an error.
func decodeEpisodeDocs(data []byte) ([]episodeNFO, error) {
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	dec.Strict = false
	var docs []episodeNFO
	for {
		tok, err := dec.Token()
		if err != nil {
			if len(docs) == 0 && err.Error() != "EOF" {
				return nil, err
			}
			return docs, nil
		}
		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Local != "episodedetails" {
			continue
		}
		var d episodeNFO
		if err := dec.DecodeElement(&d, &start); err != nil {
			if len(docs) == 0 {
				return nil, err
			}
			return docs, nil
		}
		docs = append(docs, d)
	}
}

// mergeEpisodeDocs folds the documents of one file into one (see
// episodeNFOFor).
func mergeEpisodeDocs(docs []episodeNFO) episodeNFO {
	if len(docs) == 1 {
		return docs[0]
	}
	sort.SliceStable(docs, func(i, j int) bool {
		a, b := nfoNumber(docs[i].Episode), nfoNumber(docs[j].Episode)
		switch {
		case a == nil:
			return false
		case b == nil:
			return true
		}
		return *a < *b
	})
	out := docs[0]
	var titles, plots []string
	for _, d := range docs {
		if t := trimSpace(d.Title); t != "" {
			titles = append(titles, t)
		}
		if p := trimSpace(d.Plot); p != "" {
			plots = append(plots, p)
		}
	}
	out.Title = strings.Join(titles, " / ")
	out.Plot = strings.Join(plots, " / ")
	if end := nfoNumber(docs[len(docs)-1].Episode); end != nil {
		out.EpisodeEnd = end
	}
	return out
}

// applyParsedEpisodeNFO overlays n onto item: the common fields, the air date
// as the fallback premiere date, and the season and episode numbers when the
// file carries them (they win over what the file name gave).
func applyParsedEpisodeNFO(item *storage.Item, n episodeNFO) {
	applyCommonNFOFields(item, n.commonNFOFields)
	if item.PremiereDate == nil && n.Aired != "" {
		if t, ok := parseNFODate(n.Aired); ok {
			item.PremiereDate = &t
		}
	}
	if v := nfoNumber(n.Season); v != nil {
		item.ParentIndexNumber = v
	}
	if v := nfoNumber(n.Episode); v != nil {
		item.IndexNumber = v
	}
	if n.EpisodeEnd != nil {
		item.IndexNumberEnd = n.EpisodeEnd
	}
}

// applyEpisodeNFO looks for "<episode-video-basename>.nfo" next to
// episodePath and overlays any fields it defines onto item. It reuses
// applyCommonNFOFields (same handling <movie>/<tvshow> get), then falls
// back to <aired> for PremiereDate if <premiered> wasn't present — see the
// episodeNFO doc comment.
func (s *Scanner) applyEpisodeNFO(pool *jobPool, item *storage.Item, episodePath string) {
	if n, ok := s.episodeNFOFor(pool, episodePath); ok {
		applyParsedEpisodeNFO(item, n)
	}
}

// applyVideoNFO overlays "<video-basename>.nfo" onto a home video. The file
// is a <movie> document; a "movie.nfo" in the folder is not read, because the
// folder holds many unrelated videos.
func (s *Scanner) applyVideoNFO(pool *jobPool, item *storage.Item, videoPath string) {
	base := strings.TrimSuffix(videoPath, filepath.Ext(videoPath))
	data, found := readFirstExisting(base + ".nfo")
	if !found {
		return
	}
	var n movieNFO
	if err := xml.Unmarshal(data, &n); err != nil {
		pool.addWarning(fmt.Sprintf("parsing video .nfo for %q: %v (ignored)", videoPath, err))
		return
	}
	applyCommonNFOFields(item, n.commonNFOFields)
	item.SetName = n.Set.value()
}
