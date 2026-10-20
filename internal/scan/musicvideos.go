package scan

import (
	"context"
	"encoding/xml"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// musicVideoNFO is a Kodi <musicvideo> .nfo document: the common fields plus
// the performers (<artist>, repeatable) and the <album> the clip is from.
type musicVideoNFO struct {
	XMLName xml.Name `xml:"musicvideo"`
	commonNFOFields
	Artists []string `xml:"artist"`
	Album   string   `xml:"album"`
}

// applyMusicVideoNFO reads "<video base>.nfo" over item.
func (s *Scanner) applyMusicVideoNFO(pool *jobPool, item *storage.Item, videoPath string) {
	base := strings.TrimSuffix(videoPath, filepath.Ext(videoPath))
	data, found := readFirstExisting(base + ".nfo")
	if !found {
		return
	}
	var n musicVideoNFO
	if err := xml.Unmarshal(data, &n); err != nil {
		pool.addWarning(fmt.Sprintf("parsing music video .nfo for %q: %v (ignored)", videoPath, err))
		return
	}
	applyCommonNFOFields(item, n.commonNFOFields)
	if a := dedupeNames(n.Artists); len(a) > 0 {
		item.Artists = a
	}
	if t := trimSpace(n.Album); t != "" {
		item.Album = t
	}
}

// mvClip is one clip of a music-video folder: a file, or the parts of a
// stacked clip ("Name - part1.mp4", "Name - part2.mp4"), which are the one
// clip of its first part.
type mvClip struct {
	name  string
	size  int64
	parts []movieFile // the other parts of a stacked clip, in part order
}

// musicVideoClips lists the clips directly in dir and its sub folders.
func musicVideoClips(ctx context.Context, dir string) (clips []mvClip, subdirs []string) {
	entries, err := readScanDir(ctx, dir)
	if err != nil {
		return nil, nil
	}
	type cand struct {
		clip  mvClip
		stack string
		part  int
	}
	var cands []cand
	for _, e := range entries {
		if e.IsDir() {
			if !IgnoredFolder(e.Name()) {
				subdirs = append(subdirs, filepath.Join(dir, e.Name()))
			}
			continue
		}
		if !IsVideoFile(e.Name()) || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		c := cand{clip: mvClip{name: e.Name(), size: info.Size()}}
		base := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		if m := stackPart.FindStringSubmatch(base); m != nil {
			c.stack = strings.ToLower(m[1])
			c.part, _ = strconv.Atoi(m[2])
		}
		cands = append(cands, c)
	}
	// A stack needs a second part: a lone "Name - part1" is a clip as it is.
	counts := map[string]int{}
	for _, c := range cands {
		if c.stack != "" {
			counts[c.stack]++
		}
	}
	first := map[string]int{} // stack -> index in cands of the lowest part
	for i, c := range cands {
		if c.stack == "" || counts[c.stack] < 2 {
			continue
		}
		if j, ok := first[c.stack]; !ok || c.part < cands[j].part {
			first[c.stack] = i
		}
	}
	var stacked []int
	for i, c := range cands {
		if c.stack != "" && counts[c.stack] >= 2 {
			stacked = append(stacked, i)
		}
	}
	sort.SliceStable(stacked, func(a, b int) bool { return cands[stacked[a]].part < cands[stacked[b]].part })
	owner := map[string]int{} // stack -> index in clips
	for i, c := range cands {
		if c.stack != "" && counts[c.stack] >= 2 && first[c.stack] != i {
			continue
		}
		if c.stack != "" && counts[c.stack] >= 2 {
			owner[c.stack] = len(clips)
		}
		clips = append(clips, c.clip)
	}
	for _, i := range stacked {
		c := cands[i]
		if first[c.stack] == i {
			continue
		}
		o := owner[c.stack]
		clips[o].parts = append(clips[o].parts, movieFile{name: c.clip.name, size: c.clip.size})
	}
	sort.Slice(clips, func(i, j int) bool { return clips[i].name < clips[j].name })
	return clips, subdirs
}

// walkMusicVideos handles a CollectionTypeMusicVideos path. A folder that
// holds exactly one clip and nothing else to walk is that clip (it is not a
// Folder item and its picture files apply to the clip); every other folder is
// a Folder item. A clip is named after its file. parent is nil at the root.
func (s *Scanner) walkMusicVideos(ctx context.Context, pool *jobPool, lib storage.Library, dir string, parent *storage.ItemID) {
	clips, subdirs := musicVideoClips(ctx, dir)
	for _, c := range clips {
		c := c
		pool.enqueue(func() error { return s.processMusicVideo(ctx, pool, lib, dir, c, parent, false) })
	}
	for _, sub := range subdirs {
		subClips, subSubdirs := musicVideoClips(ctx, sub)
		if len(subClips) == 1 && len(subSubdirs) == 0 {
			c := subClips[0]
			pool.enqueue(func() error { return s.processMusicVideo(ctx, pool, lib, sub, c, parent, true) })
			continue
		}
		id := storage.ItemID(deterministicID("item-folder-" + string(lib.ID) + "-" + sub))
		folder := storage.Item{
			ID: id, Type: storage.ItemTypeFolder, LibraryID: lib.ID, ParentID: parent,
			Name: filepath.Base(sub), SortName: filepath.Base(sub), Path: sub, DateCreated: fileDateAdded(sub),
		}
		if err := s.upsertItem(ctx, folder); err != nil {
			log.Printf("scan: upserting folder %q: %v", sub, err)
			continue
		}
		s.seedFolderArtwork(ctx, id, sub)
		s.walkMusicVideos(ctx, pool, lib, sub, &id)
	}
}

// processMusicVideo catalogs one clip. alone is true when the clip is the
// only content of its folder, so the folder's pictures apply to it.
func (s *Scanner) processMusicVideo(ctx context.Context, pool *jobPool, lib storage.Library, dir string, c mvClip, parent *storage.ItemID, alone bool) error {
	path := filepath.Join(dir, c.name)
	itemID := s.reconcileItemID(ctx, lib.ID, path, storage.ItemID(deterministicID("item-musicvideo-"+string(lib.ID)+"-"+path)))
	name := strings.TrimSuffix(c.name, filepath.Ext(c.name))
	_, year := CleanTitle(name, true)
	pr := s.probeOrReuse(ctx, pool, itemID, path, c.size)
	probeErr := pr.err
	item := storage.Item{
		ID: itemID, Type: storage.ItemTypeMusicVideo, LibraryID: lib.ID, ParentID: parent,
		Name: name, SortName: name, Path: path, Container: videoExt(path), Size: c.size,
		MediaStreams: pr.streams, Chapters: pr.chapters, RunTimeTicks: pr.ticks, Format: pr.format, TSTimestamp: pr.tsTimestamp, DateCreated: pr.added,
	}
	if pr.title != "" && optionsOf(ctx).embeddedTitles {
		item.Name, item.SortName = pr.title, pr.title
	}
	if year > 0 {
		item.ProductionYear = &year
	}
	if err := s.upsertItem(ctx, item); err != nil {
		return fmt.Errorf("upserting music video %q: %w", path, err)
	}
	s.recordSignature(ctx, itemID, pr)
	s.seedMovieImages(ctx, itemID, dir, c.name, alone)
	s.applyMusicVideoNFO(pool, &item, path)
	if err := s.upsertItem(ctx, item); err != nil {
		return fmt.Errorf("upserting enriched music video %q: %w", path, err)
	}
	if len(c.parts) > 0 {
		others := movieOthers(dir, name, movieGroup{primary: c.name, parts: c.parts})
		s.scanExtras(ctx, pool, lib, itemID, dir, []string{name}, item.ProductionYear, false, false, others)
	}
	if probeErr != nil {
		pool.addWarning(fmt.Sprintf("probing %q: %v (cataloged with no media stream info)", path, probeErr))
	}
	return nil
}

// syncMusicVideoArtists makes a MusicArtist item for every artist name the
// library's clips carry, so the clips' artists are listed and addressable like
// the artists of a music library.
func (s *Scanner) syncMusicVideoArtists(ctx context.Context, lib storage.Library) {
	if s.libraryGone(ctx, lib) {
		return
	}
	names := map[string]string{}
	genres := map[string]map[string]string{} // artist key -> genre key -> genre
	const page = 500
	for off := 0; ; off += page {
		res, err := s.store.QueryItems(ctx, storage.ItemFilter{
			LibraryID: &lib.ID, Recursive: true, Limit: page, Offset: off,
			ItemTypes: []storage.ItemType{storage.ItemTypeMusicVideo},
		})
		if err != nil {
			log.Printf("scan: library %q: listing artists: %v", lib.Name, err)
			return
		}
		for _, it := range res.Items {
			for _, n := range it.Artists {
				if n = strings.TrimSpace(n); n != "" {
					k := strings.ToLower(n)
					names[k] = n
					if genres[k] == nil {
						genres[k] = map[string]string{}
					}
					for _, g := range it.Genres {
						genres[k][strings.ToLower(g)] = g
					}
				}
			}
		}
		if len(res.Items) < page {
			break
		}
	}
	for key, name := range names {
		var gs []string
		for _, g := range genres[key] {
			gs = append(gs, g)
		}
		sort.Slice(gs, func(i, j int) bool { return strings.ToLower(gs[i]) < strings.ToLower(gs[j]) })
		item := storage.Item{
			Genres: gs,
			ID:     virtualArtistID(lib.ID, name), Type: storage.ItemTypeMusicArtist, LibraryID: lib.ID,
			Name: name, SortName: name, ExtraType: storage.ExtraTypeVirtualArtist, DateCreated: time.Now().UTC(),
		}
		if err := s.upsertItem(ctx, item); err != nil {
			log.Printf("scan: library %q: upserting artist %q: %v", lib.Name, name, err)
		}
	}
}
