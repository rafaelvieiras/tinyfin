package scan

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// musicExtensions are the file types a music library catalogs.
var musicExtensions = map[string]bool{
	".mp3": true, ".flac": true, ".ogg": true, ".oga": true, ".opus": true, ".m4a": true, ".aac": true,
	".wav": true, ".wma": true, ".ape": true, ".wv": true, ".aiff": true, ".aif": true, ".mka": true,
	".m4b": true, ".mpc": true,
}

// IsMusicFile reports whether name's extension is an audio file a music
// library catalogs.
func IsMusicFile(name string) bool {
	return musicExtensions[strings.ToLower(filepath.Ext(name))]
}

// albumCoverNames are the pictures of an album folder, in order of
// preference, that stand for the album's Primary image.
var albumCoverNames = []string{
	"cover.jpg", "cover.png", "folder.jpg", "folder.png", "poster.jpg", "poster.png",
}

// musicTags are the tags of one audio file the catalog keeps.
type musicTags struct {
	title, album, albumArtist, artist, composer, genre string
	track, disc                                        *int
	year                                               *int
	premiere                                           *time.Time
}

// tagValue returns the first non-empty tag among keys, whatever the case of
// the tag's name.
func tagValue(tags map[string]string, keys ...string) string {
	for _, k := range keys {
		for name, v := range tags {
			if strings.EqualFold(name, k) {
				if v = strings.TrimSpace(v); v != "" {
					return v
				}
			}
		}
	}
	return ""
}

// leadingNumber reads the number of a "3" or "3/12" tag.
func leadingNumber(v string) *int {
	if i := strings.IndexAny(v, "/ "); i >= 0 {
		v = v[:i]
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return nil
	}
	return &n
}

// parseTagDate reads a "2019", "2019-05" or "2019-05-06[T...]" date tag.
func parseTagDate(v string) (year *int, date *time.Time) {
	v = strings.TrimSpace(v)
	if len(v) < 4 {
		return nil, nil
	}
	y, err := strconv.Atoi(v[:4])
	if err != nil || y <= 0 {
		return nil, nil
	}
	year = &y
	month, day := 1, 1
	if len(v) >= 7 && v[4] == '-' {
		if m, err := strconv.Atoi(v[5:7]); err == nil && m >= 1 && m <= 12 {
			month = m
			if len(v) >= 10 && v[7] == '-' {
				if d, err := strconv.Atoi(v[8:10]); err == nil && d >= 1 && d <= 31 {
					day = d
				}
			}
		}
	}
	t := time.Date(y, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	return year, &t
}

func readMusicTags(tags map[string]string) musicTags {
	m := musicTags{
		title:       tagValue(tags, "title"),
		album:       tagValue(tags, "album"),
		albumArtist: tagValue(tags, "album_artist", "albumartist", "album artist"),
		artist:      tagValue(tags, "artist"),
		composer:    tagValue(tags, "composer"),
		genre:       tagValue(tags, "genre"),
		track:       leadingNumber(tagValue(tags, "track", "tracknumber")),
		disc:        leadingNumber(tagValue(tags, "disc", "discnumber")),
	}
	m.year, m.premiere = parseTagDate(tagValue(tags, "date", "year", "originaldate", "originalyear"))
	return m
}

// discFolderRe matches the name of a disc folder inside an album ("CD1",
// "Disc 2", "disk_03"): its tracks belong to the album that holds it.
var discFolderRe = regexp.MustCompile(`(?i)^(cd|disc|disk)[\s._-]*\d+\b`)

// isDiscFolderName reports whether a folder name is a disc folder.
func isDiscFolderName(name string) bool { return discFolderRe.MatchString(name) }

// musicPeople is the People member of a track or an album
// (music.people.R1): the composer tag, then the album artist, then the
// artists, each with an empty role. Conductor, lyricist and performer tags
// make no person.
func musicPeople(composer, albumArtist string, artists []string) []storage.Person {
	var out []storage.Person
	if composer = strings.TrimSpace(composer); composer != "" {
		out = append(out, storage.Person{Name: composer, Type: "Composer"})
	}
	if albumArtist != "" {
		out = append(out, storage.Person{Name: albumArtist, Type: "AlbumArtist"})
	}
	for _, a := range artists {
		out = append(out, storage.Person{Name: a, Type: "Artist"})
	}
	return out
}

// tagList is the list a tag's value stands for: the reference does not split
// a tag on ";" (it is one name), so a non-empty value is a list of one.
func tagList(v string) []string {
	if v = strings.TrimSpace(v); v == "" {
		return nil
	}
	return []string{v}
}

// musicTreeHasAudio reports whether dir holds an audio file, directly or in a
// folder below it (down to depth levels).
func (s *Scanner) musicTreeHasAudio(ctx context.Context, dir string, depth int) bool {
	entries, err := readScanDir(ctx, dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			if depth > 0 && !IgnoredFolder(e.Name()) && !ExtrasFolder(e.Name()) &&
				s.musicTreeHasAudio(ctx, filepath.Join(dir, e.Name()), depth-1) {
				return true
			}
			continue
		}
		if IsMusicFile(e.Name()) && !strings.HasPrefix(e.Name(), ".") {
			return true
		}
	}
	return false
}

// musicAlbumLayout decides whether dir is an album and lists the disc
// folders of it that hold audio. A folder is an album when it holds audio
// directly, or only through disc folders, and no other folder below it holds
// audio (that makes it an artist or a plain folder, whatever loose files it
// has itself).
func (s *Scanner) musicAlbumLayout(ctx context.Context, dir string) (album bool, discs []string) {
	entries, err := readScanDir(ctx, dir)
	if err != nil {
		return false, nil
	}
	direct := false
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() {
			if IsMusicFile(name) && !strings.HasPrefix(name, ".") {
				direct = true
			}
			continue
		}
		if IgnoredFolder(name) || ExtrasFolder(name) {
			continue
		}
		sub := filepath.Join(dir, name)
		if !s.musicTreeHasAudio(ctx, sub, 3) {
			continue
		}
		if !isDiscFolderName(name) || !dirHasAudio(ctx, sub) {
			return false, nil
		}
		discs = append(discs, sub)
	}
	sort.Strings(discs)
	return direct || len(discs) > 0, discs
}

// trackSortName orders the tracks of an album by disc then track number; a
// track with no number sorts by its name.
func trackSortName(title string, track, disc *int) string {
	switch {
	case track != nil && disc != nil:
		return fmt.Sprintf("%04d - %04d - %s", *disc, *track, title)
	case track != nil:
		return fmt.Sprintf("%04d - %s", *track, title)
	}
	return title
}

// walkMusic handles a CollectionTypeMusic path. A folder that directly holds
// audio files is an album; the folders above it, down from the library root,
// are artists (a folder further down with no audio of its own is a plain
// folder). Audio files that sit outside any album are tracks of their own.
func (s *Scanner) walkMusic(ctx context.Context, pool *jobPool, lib storage.Library, dir string, parent *storage.ItemID, depth int) {
	entries, err := readScanDir(ctx, dir)
	if err != nil {
		log.Printf("scan: reading music folder %s: %v", dir, err)
		return
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if e.IsDir() {
			if IgnoredFolder(e.Name()) || ExtrasFolder(e.Name()) {
				continue
			}
			s.walkMusicFolder(ctx, pool, lib, path, e.Name(), parent, depth)
			continue
		}
		if !IsMusicFile(e.Name()) || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		pth := path
		pool.enqueue(func() error {
			_, err := s.processTrack(ctx, pool, lib, pth, parent)
			return err
		})
	}
}

// dirHasAudio reports whether dir directly holds an audio file.
func dirHasAudio(ctx context.Context, dir string) bool {
	for name := range dirNames(ctx, dir) {
		if IsMusicFile(name) && !strings.HasPrefix(name, ".") {
			return true
		}
	}
	return false
}

// libraryGone reports whether the library was removed while a pass over it
// was running: what is left of the pass writes nothing.
func (s *Scanner) libraryGone(ctx context.Context, lib storage.Library) bool {
	_, err := s.store.GetLibrary(ctx, lib.ID)
	return err != nil
}

func (s *Scanner) walkMusicFolder(ctx context.Context, pool *jobPool, lib storage.Library, path, name string, parent *storage.ItemID, depth int) {
	if s.libraryGone(ctx, lib) {
		return
	}
	hasAudio, _ := s.musicAlbumLayout(ctx, path)
	typ := storage.ItemTypeFolder
	idPrefix := "item-folder-"
	switch {
	case hasAudio:
		typ, idPrefix = storage.ItemTypeMusicAlbum, "item-album-"
	case depth == 0:
		typ, idPrefix = storage.ItemTypeMusicArtist, "item-artist-"
	}
	id := storage.ItemID(deterministicID(idPrefix + string(lib.ID) + "-" + path))
	folder := storage.Item{
		ID: id, Type: typ, LibraryID: lib.ID, ParentID: parent,
		Name: name, SortName: name, Path: path, DateCreated: fileDateAdded(path),
	}
	if typ == storage.ItemTypeMusicArtist {
		s.applyArtistNFO(pool, &folder)
	}
	if err := s.upsertItem(ctx, folder); err != nil {
		log.Printf("scan: upserting music folder %q: %v", path, err)
		return
	}
	if !hasAudio {
		if typ == storage.ItemTypeMusicArtist {
			s.seedFolderArtwork(ctx, id, path)
		}
		s.walkMusic(ctx, pool, lib, path, &id, depth+1)
		return
	}
	// An album: its tracks are cataloged one after the other by one job, so
	// the album can be completed from what they carry once the last one is in.
	pool.enqueue(func() error { return s.processAlbum(ctx, pool, lib, folder) })
}

// albumAcc collects what an album takes from its tracks.
type albumAcc struct {
	name, albumArtist string
	year              *int
	premiere          *time.Time
	genres            []string
	ticks             int64
	haveTicks         bool
	artFrom           string // the picture of the first track that has one
	artists           []string
}

func (s *Scanner) processAlbum(ctx context.Context, pool *jobPool, lib storage.Library, album storage.Item) error {
	entries, err := readScanDir(ctx, album.Path)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && IsMusicFile(e.Name()) && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	// The tracks of the disc folders follow, folder by folder: they belong to
	// the album like the ones beside the folders.
	_, discs := s.musicAlbumLayout(ctx, album.Path)
	for _, d := range discs {
		sub, err := readScanDir(ctx, d)
		if err != nil {
			continue
		}
		var in []string
		for _, e := range sub {
			if !e.IsDir() && IsMusicFile(e.Name()) && !strings.HasPrefix(e.Name(), ".") {
				in = append(in, e.Name())
			}
		}
		sort.Strings(in)
		for _, n := range in {
			names = append(names, filepath.Join(filepath.Base(d), n))
		}
	}
	acc := albumAcc{}
	var firstErr error
	albumID := album.ID
	for _, n := range names {
		if s.libraryGone(ctx, lib) {
			return nil
		}
		tr, err := s.processTrack(ctx, pool, lib, filepath.Join(album.Path, n), &albumID)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if tr.ID == "" {
			continue
		}
		if acc.name == "" && tr.Album != "" {
			acc.name = tr.Album
		}
		if acc.albumArtist == "" {
			acc.albumArtist = tr.AlbumArtist
		}
		if acc.year == nil && tr.ProductionYear != nil {
			acc.year, acc.premiere = tr.ProductionYear, tr.PremiereDate
		}
		for _, a := range tr.Artists {
			dup := false
			for _, have := range acc.artists {
				if strings.EqualFold(have, a) {
					dup = true
				}
			}
			if !dup {
				acc.artists = append(acc.artists, a)
			}
		}
		for _, g := range tr.Genres {
			dup := false
			for _, have := range acc.genres {
				if strings.EqualFold(have, g) {
					dup = true
				}
			}
			if !dup {
				acc.genres = append(acc.genres, g)
			}
		}
		if tr.RunTimeTicks != nil {
			acc.ticks += *tr.RunTimeTicks
			acc.haveTicks = true
		}
		if acc.artFrom == "" {
			if refs, err := s.store.GetImageRefs(ctx, tr.ID, storage.ImageKind("Primary")); err == nil && len(refs) > 0 {
				acc.artFrom = refs[0].Path
			}
		}
	}
	if s.libraryGone(ctx, lib) {
		return nil
	}
	if acc.name != "" {
		album.Name, album.SortName = acc.name, acc.name
	}
	album.AlbumArtist = acc.albumArtist
	album.Artists = acc.artists
	album.People = musicPeople("", album.AlbumArtist, album.Artists)
	album.ProductionYear, album.PremiereDate = acc.year, acc.premiere
	album.Genres = acc.genres
	if acc.haveTicks {
		t := acc.ticks
		album.RunTimeTicks = &t
	}
	s.applyAlbumNFO(pool, &album)
	if err := s.upsertItem(ctx, album); err != nil {
		return fmt.Errorf("upserting album %q: %w", album.Path, err)
	}
	s.seedFolderImage(ctx, album.ID, album.Path, "Primary", albumCoverNames)
	if refs, err := s.store.GetImageRefs(ctx, album.ID, storage.ImageKind("Primary")); err != nil || len(refs) == 0 {
		if acc.artFrom != "" {
			s.seedPhotoImage(ctx, album.ID, acc.artFrom)
		}
	}
	return firstErr
}

// processTrack catalogs one audio file: named after its title tag (the file
// name without extension when it has none), with the album, artists, genre,
// year, track and disc numbers its tags give.
func (s *Scanner) processTrack(ctx context.Context, pool *jobPool, lib storage.Library, path string, parent *storage.ItemID) (storage.Item, error) {
	info, err := os.Stat(path)
	if err != nil {
		return storage.Item{}, err
	}
	itemID := s.reconcileItemID(ctx, lib.ID, path, storage.ItemID(deterministicID("item-audio-"+string(lib.ID)+"-"+path)))
	pr := s.probeOrReuse(ctx, pool, itemID, path, info.Size())
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	item := storage.Item{
		ID: itemID, Type: storage.ItemTypeAudio, LibraryID: lib.ID, ParentID: parent,
		Path: path, Container: videoExt(path), Size: info.Size(),
		MediaStreams: pr.streams, RunTimeTicks: pr.ticks, Format: pr.format, TSTimestamp: pr.tsTimestamp, DateCreated: pr.added,
	}
	if pr.tags == nil {
		// Reused from an earlier probe: the tags are what the item holds.
		if old, err := s.store.GetItem(ctx, itemID); err == nil && old.Type == storage.ItemTypeAudio {
			item.Name = old.Name
			item.SortName = trackSortName(old.Name, old.IndexNumber, old.ParentIndexNumber)
			item.Album, item.AlbumArtist, item.Artists = old.Album, old.AlbumArtist, old.Artists
			item.Genres, item.IndexNumber, item.ParentIndexNumber = old.Genres, old.IndexNumber, old.ParentIndexNumber
			item.People = old.People
			item.ProductionYear, item.PremiereDate = old.ProductionYear, old.PremiereDate
		} else {
			item.Name, item.SortName = base, base
		}
	} else {
		mt := readMusicTags(pr.tags)
		item.Name = mt.title
		if item.Name == "" {
			item.Name = base
		}
		item.SortName = trackSortName(item.Name, mt.track, mt.disc)
		item.Album, item.AlbumArtist = mt.album, mt.albumArtist
		item.Artists = tagList(mt.artist)
		item.People = musicPeople(mt.composer, item.AlbumArtist, item.Artists)
		item.Genres = tagList(mt.genre)
		item.IndexNumber, item.ParentIndexNumber = mt.track, mt.disc
		item.ProductionYear, item.PremiereDate = mt.year, mt.premiere
	}
	if err := s.upsertItem(ctx, item); err != nil {
		return storage.Item{}, fmt.Errorf("upserting track %q: %w", path, err)
	}
	s.recordSignature(ctx, itemID, pr)
	if pr.err != nil {
		pool.addWarning(fmt.Sprintf("probing %q: %v (cataloged with no media stream info)", path, pr.err))
	}
	if pr.embeddedArt != nil {
		s.seedEmbeddedArt(ctx, itemID, path, *pr.embeddedArt)
	} else {
		// A reused probe: the picture was extracted by the pass that made it.
		s.reseedEmbeddedArt(ctx, itemID)
	}
	return item, nil
}

// embeddedArtExt names the file type an attached picture is written as.
func embeddedArtExt(codec string) string {
	if codec == "png" {
		return "png"
	}
	return "jpg"
}

func (s *Scanner) embeddedArtPath(itemID storage.ItemID, ext string) string {
	return filepath.Join(s.imageDir, "embedded", string(itemID)+"."+ext)
}

// seedEmbeddedArt extracts the picture attached to an audio file (stream
// streamIndex of the file, of the given codec) and stores it as the track's
// Primary image.
func (s *Scanner) seedEmbeddedArt(ctx context.Context, itemID storage.ItemID, path string, art embeddedArt) {
	if s.imageDir == "" {
		return
	}
	out := s.embeddedArtPath(itemID, embeddedArtExt(art.codec))
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, s.ffmpegPath(), "-v", "error", "-y", "-i", path,
		"-map", "0:"+strconv.Itoa(art.index), "-c", "copy", "-frames:v", "1", "-f", "image2", out)
	if err := cmd.Run(); err != nil {
		log.Printf("scan: extracting the picture of %q: %v", path, err)
		_ = os.Remove(out)
		return
	}
	s.seedPhotoImage(ctx, itemID, out)
}

// reseedEmbeddedArt keeps the image ref of a track whose picture was
// extracted earlier (the file is still there); nothing to do when it is.
func (s *Scanner) reseedEmbeddedArt(ctx context.Context, itemID storage.ItemID) {
	for _, ext := range []string{"jpg", "png"} {
		if p := s.embeddedArtPath(itemID, ext); s.imageDir != "" {
			if _, err := os.Stat(p); err == nil {
				s.seedPhotoImage(ctx, itemID, p)
				return
			}
		}
	}
}

// ffmpegPath is the ffmpeg that sits beside the configured ffprobe (or the
// one on PATH).
func (s *Scanner) ffmpegPath() string {
	base := filepath.Base(s.ffprobePath)
	if base == "ffprobe" || base == "ffprobe.exe" {
		return filepath.Join(filepath.Dir(s.ffprobePath), strings.Replace(base, "ffprobe", "ffmpeg", 1))
	}
	return "ffmpeg"
}

// virtualArtistID is the identifier of an artist that has no folder of its
// own in the library: it follows the name, so it is the same on every scan.
func virtualArtistID(libraryID storage.LibraryID, name string) storage.ItemID {
	return storage.ItemID(deterministicID("item-virtual-artist-" + string(libraryID) + "-" + strings.ToLower(name)))
}

// VirtualArtistID is virtualArtistID for callers outside the scan.
func VirtualArtistID(libraryID storage.LibraryID, name string) storage.ItemID {
	return virtualArtistID(libraryID, name)
}

// syncVirtualArtists makes a MusicArtist item for every artist or album
// artist name the library's tracks and albums carry that no artist folder of
// the library has. Such an artist is not a child of anything and is not
// listed among a folder's children: it is stored as an internal extra and
// reached by identifier and by the artist listings.
func (s *Scanner) syncVirtualArtists(ctx context.Context, lib storage.Library) {
	if s.libraryGone(ctx, lib) {
		return
	}
	have := map[string]bool{}
	names := map[string]string{}
	const page = 500
	for off := 0; ; off += page {
		res, err := s.store.QueryItems(ctx, storage.ItemFilter{
			LibraryID: &lib.ID, Recursive: true, IncludeExtras: true, Limit: page, Offset: off,
			ItemTypes: []storage.ItemType{storage.ItemTypeMusicArtist, storage.ItemTypeMusicAlbum, storage.ItemTypeAudio},
		})
		if err != nil {
			log.Printf("scan: library %q: listing artists: %v", lib.Name, err)
			return
		}
		for _, it := range res.Items {
			switch it.Type {
			case storage.ItemTypeMusicArtist:
				if it.ExtraType == "" {
					have[strings.ToLower(it.Name)] = true
				}
			default:
				for _, n := range append([]string{it.AlbumArtist}, it.Artists...) {
					if n = strings.TrimSpace(n); n != "" {
						names[strings.ToLower(n)] = n
					}
				}
			}
		}
		if len(res.Items) < page {
			break
		}
	}
	for key, name := range names {
		if have[key] {
			continue
		}
		item := storage.Item{
			ID: virtualArtistID(lib.ID, name), Type: storage.ItemTypeMusicArtist, LibraryID: lib.ID,
			Name: name, SortName: name, ExtraType: storage.ExtraTypeVirtualArtist, DateCreated: time.Now().UTC(),
		}
		if err := s.upsertItem(ctx, item); err != nil {
			log.Printf("scan: library %q: upserting artist %q: %v", lib.Name, name, err)
		}
	}
}
