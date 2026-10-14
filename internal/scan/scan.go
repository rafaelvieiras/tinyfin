// Package scan is Tinyfin's own library scanner — docs/08's "minimum
// needed to serve a browsable catalog": discover files under a Library's
// paths, extract technical media info via ffprobe, identify each item from
// its path via the Kodi/Jellyfin filename convention (nameparse.go), and
// hand off to a metadata.Provider for enrichment. It does not implement a
// second, competing cataloging system — see the package doc comment on
// docs/08's boundary — and everything it writes goes through
// storage.Store, the same interface the API layer uses.
//
// Filename-derived identification (title, year, season/episode numbers)
// is applied unconditionally and is enough on its own for a freshly
// scanned item to be fully browsable and playable; a configured Provider
// only ever overlays additional fields on top (Overview, Genres, ratings,
// images, ProviderIDs) — never required for an item to exist in the
// catalog at all, matching the "consume metadata, don't require it"
// framing in docs/00-decision-log.md's D3.
package scan

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/collection"
	"github.com/rafaelvieiras/tinyfin/internal/metadata"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// defaultConcurrency bounds simultaneous ffprobe subprocesses and provider
// HTTP calls when Config.Concurrency is left zero — docs/08's "Scan-time
// resource discipline" calls this out explicitly as a documented failure
// mode of the reference implementation this project deliberately avoids.
const defaultConcurrency = 8

// probeTimeout bounds one ffprobe run so a stalled remote read cannot hang a
// worker forever.
const probeTimeout = 3 * time.Minute

// Config configures a Scanner. Every field has a usable zero value.
type Config struct {
	// FFprobePath is the ffprobe binary to run. Empty means "ffprobe" on
	// $PATH, matching internal/transcode.Config's analogous fallback for
	// ffmpeg.
	FFprobePath string

	// ImageDir is where provider-returned image URLs get downloaded and
	// cached as local files (storage.ImageStore holds refs to on-disk
	// files, not URLs or blobs — see that interface's doc comment).
	// Empty disables image download entirely: items still get every other
	// enriched field, just no artwork beyond whatever the library folder
	// already had (a scanner enhancement, not required for Phase 3's own
	// goal).
	ImageDir string

	// Concurrency bounds simultaneous ffprobe subprocesses and provider
	// calls. <= 0 defaults to defaultConcurrency.
	Concurrency int

	// ProbeConcurrency bounds simultaneous ffprobe processes across every
	// scan running in this Scanner (library scans may run in parallel).
	// <= 0 defaults to the resolved Concurrency.
	ProbeConcurrency int

	// ForceProbe disables the incremental skip: every file is probed again.
	ForceProbe bool

	// DataDir is the server's data directory; the collections (BoxSet) the
	// scan builds from movie sets live under <DataDir>/collections. Empty
	// disables the automatic collections.
	DataDir string
}

// Scanner walks configured library paths and populates storage.Store.
type Scanner struct {
	store       storage.Store
	provider    metadata.Provider
	ffprobePath string
	imageDir    string
	concurrency int
	probeSem    chan struct{}
	forceProbe  bool
	dataDir     string
	httpClient  interface {
		fetchImage(ctx context.Context, url string) ([]byte, error)
	}
}

// New builds a Scanner. provider may be metadata.NoopProvider{} — Scan
// never treats a provider miss as an error (metadata.ErrNotFound), only
// unexpected provider failures (metadata.ProviderError) go into
// Stats.Errors, and even those don't stop the item from being cataloged
// with its filename-derived fields.
func New(store storage.Store, provider metadata.Provider, cfg Config) *Scanner {
	if provider == nil {
		provider = metadata.NoopProvider{}
	}
	ffprobePath := cfg.FFprobePath
	if ffprobePath == "" {
		ffprobePath = "ffprobe"
	}
	concurrency := cfg.Concurrency
	if concurrency <= 0 {
		concurrency = defaultConcurrency
	}
	probeConc := cfg.ProbeConcurrency
	if probeConc <= 0 {
		probeConc = concurrency
	}
	if cfg.ImageDir != "" {
		_ = os.MkdirAll(cfg.ImageDir, 0o755)
	}
	return &Scanner{
		store:       store,
		provider:    provider,
		ffprobePath: ffprobePath,
		imageDir:    cfg.ImageDir,
		concurrency: concurrency,
		probeSem:    make(chan struct{}, probeConc),
		forceProbe:  cfg.ForceProbe,
		dataDir:     cfg.DataDir,
		httpClient:  defaultImageFetcher{},
	}
}

// Stats summarizes one ScanLibrary pass. Errors holds a human-readable
// message per item that failed probing/enrichment/storage — the scan as a
// whole still completes and every other item still gets cataloged (see
// New's doc comment).
type Stats struct {
	ItemsScanned  int
	ItemsEnriched int
	// ItemsProbed counts files ffprobe was run on; ItemsSkipped counts files
	// whose stored media info was reused because size+mtime were unchanged.
	ItemsProbed  int
	ItemsSkipped int
	// Removed counts the items forgotten because their file or folder is gone.
	Removed  int
	Duration     time.Duration
	Errors       []string
}

// jobPool runs enqueued jobs with bounded concurrency, accumulating Stats
// safely across goroutines. Enqueue blocks once concurrency workers are
// already busy — a deliberate backpressure point, not just a limiter:
// it keeps ScanLibrary from ever building an unbounded in-memory job list
// for a very large library (docs/09's memory discipline), since the walk
// itself pauses rather than racing ahead of the workers.
type jobPool struct {
	sem   chan struct{}
	wg    sync.WaitGroup
	mu    sync.Mutex
	stats Stats
	// failed counts the jobs that returned an error (as opposed to a warning).
	failed int
}

func newJobPool(concurrency int) *jobPool {
	return &jobPool{sem: make(chan struct{}, concurrency)}
}

func (p *jobPool) enqueue(fn func() error) {
	p.wg.Add(1)
	p.sem <- struct{}{}
	go func() {
		defer p.wg.Done()
		defer func() { <-p.sem }()
		if err := fn(); err != nil {
			p.mu.Lock()
			p.stats.Errors = append(p.stats.Errors, err.Error())
			p.failed++
			p.mu.Unlock()
			return
		}
		p.mu.Lock()
		p.stats.ItemsScanned++
		p.mu.Unlock()
	}()
}

func (p *jobPool) countProbed() {
	p.mu.Lock()
	p.stats.ItemsProbed++
	p.mu.Unlock()
}

func (p *jobPool) countSkipped() {
	p.mu.Lock()
	p.stats.ItemsSkipped++
	p.mu.Unlock()
}

func (p *jobPool) markEnriched() {
	p.mu.Lock()
	p.stats.ItemsEnriched++
	p.mu.Unlock()
}

// addWarning records msg into Stats.Errors without counting the enclosing
// job as failed — for problems (a probe failure) that leave the item
// successfully cataloged, just missing some derived data, as opposed to
// enqueue's err path, which means the item did not make it into the store
// at all.
func (p *jobPool) addWarning(msg string) {
	p.mu.Lock()
	p.stats.Errors = append(p.stats.Errors, msg)
	p.mu.Unlock()
}

func (p *jobPool) wait() Stats {
	p.wg.Wait()
	return p.stats
}

// ScanLibrary walks every path in lib and upserts what it finds into the
// store, then stamps lib.LastScannedAt and persists it. Per-item failures
// (a bad probe, a provider error) are collected into the returned Stats
// rather than aborting the whole pass; only a directory-read failure at a
// library path's root, or the final LastScannedAt persist, is returned as
// err.
func (s *Scanner) ScanLibrary(ctx context.Context, lib storage.Library) (Stats, error) {
	started := time.Now()
	ctx = withPassState(withDirCache(ctx))
	pool := newJobPool(s.concurrency)

	// A root that cannot be read, or that has no entry at all (a mount that is
	// not there, an emptied share), is skipped: what the library holds under
	// it is kept, not forgotten.
	var live, skipped []string
	for _, root := range lib.Paths {
		switch lib.CollectionType {
		case storage.CollectionTypeMovies, storage.CollectionTypeTVShows, storage.CollectionTypeHomeVideos:
			if entries, err := os.ReadDir(root); err != nil || len(entries) == 0 {
				log.Printf("scan: library %q: folder %s is inaccessible or empty, skipping", lib.Name, root)
				skipped = append(skipped, root)
				continue
			}
			live = append(live, root)
		}
		switch lib.CollectionType {
		case storage.CollectionTypeMovies:
			s.recordRootFolder(ctx, lib, root)
			s.walkMovies(ctx, pool, lib, root)
		case storage.CollectionTypeTVShows:
			s.recordRootFolder(ctx, lib, root)
			s.walkSeries(ctx, pool, lib, root)
		case storage.CollectionTypeHomeVideos:
			s.recordRootFolder(ctx, lib, root)
			s.walkHomeVideos(ctx, pool, lib, root, nil)
		case storage.CollectionTypeBoxSets:
			// The collections library holds items made through the API and
			// by the automatic collections below; there is no directory to
			// walk.
		default:
			// Music/Mixed aren't in this scanner's scope yet — a
			// documented gap (docs/08 describes the video-first v1 scope
			// this project targets), not a silent no-op: logged once per
			// path so an operator adding an unsupported library type sees
			// why nothing gets cataloged.
			log.Printf("scan: library %q: collection type %q is not scanned yet, skipping %s", lib.Name, lib.CollectionType, root)
		}
	}

	stats := pool.wait()
	if len(live) > 0 && pool.failed == 0 && ctx.Err() == nil {
		stats.Removed = s.forgetVanished(ctx, lib, forgetScope{roots: live, skipped: skipped})
	}
	if lib.CollectionType == storage.CollectionTypeMovies && s.dataDir != "" && !skipCollections(ctx) {
		// Every movie of the pass is cataloged: group the ones that name a
		// set (only libraries with AutomaticallyAddToCollection on count).
		if _, err := collection.BuildAutomatic(ctx, s.store, s.dataDir); err != nil {
			stats.Errors = append(stats.Errors, fmt.Sprintf("building collections: %v", err))
		}
	}
	stats.Duration = time.Since(started)
	log.Printf("scan: library %q: %d items in %s (probed %d, skipped %d unchanged, %d enriched, %d warnings)",
		lib.Name, stats.ItemsScanned, stats.Duration.Round(time.Millisecond), stats.ItemsProbed, stats.ItemsSkipped, stats.ItemsEnriched, len(stats.Errors))

	now := time.Now().UTC()
	lib.LastScannedAt = &now
	if err := s.store.UpsertLibrary(ctx, lib); err != nil {
		return stats, fmt.Errorf("scan: persisting LastScannedAt for library %q: %w", lib.Name, err)
	}
	return stats, nil
}

type skipCollectionsKey struct{}

// WithoutCollections marks a scan that must not build automatic collections:
// the refresh of a single library (as opposed to a full library refresh or
// the first scan of a new library) leaves the collections alone.
func WithoutCollections(ctx context.Context) context.Context {
	return context.WithValue(ctx, skipCollectionsKey{}, true)
}

func skipCollections(ctx context.Context) bool {
	v, _ := ctx.Value(skipCollectionsKey{}).(bool)
	return v
}

// recordRootFolder catalogs a library path as a plain Folder item named
// after its directory. It has no parent: a library's direct children are
// the parentless items that are not Folders, while a recursive listing of
// the library includes the folder (items.list.R7).
func (s *Scanner) recordRootFolder(ctx context.Context, lib storage.Library, root string) {
	name := filepath.Base(filepath.Clean(root))
	item := storage.Item{
		ID:          storage.ItemID(deterministicID("item-folder-" + string(lib.ID) + "-" + root)),
		Type:        storage.ItemTypeFolder,
		LibraryID:   lib.ID,
		Name:        name,
		SortName:    name,
		Path:        root,
		DateCreated: fileDateAdded(root),
	}
	if err := s.upsertItem(ctx, item); err != nil {
		log.Printf("scan: recording root folder %s: %v", root, err)
		return
	}
	s.seedFolderArtwork(ctx, item.ID, root)
}

// walkHomeVideos handles a CollectionTypeHomeVideos path: every folder under
// it becomes a Folder item and every video file a Video item, whatever the
// nesting and naming. A folder is cataloged before its content so the
// ancestor index sees the whole chain. parent is nil for the first level.
func (s *Scanner) walkHomeVideos(ctx context.Context, pool *jobPool, lib storage.Library, dir string, parent *storage.ItemID) {
	entries, err := readScanDir(ctx, dir)
	if err != nil {
		log.Printf("scan: reading home videos folder %s: %v", dir, err)
		return
	}
	videoBases := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() && IsVideoFile(e.Name()) {
			videoBases[strings.ToLower(strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())))] = true
		}
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if e.IsDir() {
			if IgnoredFolder(e.Name()) {
				continue
			}
			id := storage.ItemID(deterministicID("item-folder-" + string(lib.ID) + "-" + path))
			typ := storage.ItemTypeFolder
			firstPhoto := firstPhotoOf(ctx, path)
			if firstPhoto != "" {
				typ = storage.ItemTypePhotoAlbum
			}
			folder := storage.Item{
				ID: id, Type: typ, LibraryID: lib.ID, ParentID: parent,
				Name: e.Name(), SortName: e.Name(), Path: path, DateCreated: fileDateAdded(path),
			}
			if err := s.upsertItem(ctx, folder); err != nil {
				log.Printf("scan: upserting folder %q: %v", path, err)
				continue
			}
			s.seedFolderArtwork(ctx, id, path)
			if firstPhoto != "" {
				s.seedPhotoImage(ctx, id, filepath.Join(path, firstPhoto))
			}
			if !ExtrasFolder(e.Name()) {
				s.walkHomeVideos(ctx, pool, lib, path, &id)
			}
			continue
		}
		if IsPhotoFile(e.Name()) {
			if isPhotoEntry(e.Name(), videoBases) {
				s.processPhoto(ctx, lib, path, filepath.Base(dir), parent)
			}
			continue
		}
		if !IsVideoFile(e.Name()) || strings.HasPrefix(e.Name(), ".") || IsExtraVideo(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		size := info.Size()
		pool.enqueue(func() error { return s.processHomeVideo(ctx, pool, lib, path, parent, size) })
	}
}

// firstPhotoOf returns the name of the first picture (by name) that sits
// directly in dir, or "" when there is none.
func firstPhotoOf(ctx context.Context, dir string) string {
	entries, err := readScanDir(ctx, dir)
	if err != nil {
		return ""
	}
	videoBases := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() && IsVideoFile(e.Name()) {
			videoBases[strings.ToLower(strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())))] = true
		}
	}
	for _, e := range entries {
		if !e.IsDir() && IsPhotoFile(e.Name()) && isPhotoEntry(e.Name(), videoBases) {
			return e.Name()
		}
	}
	return ""
}

// processPhoto catalogs one picture of a home-videos library as a Photo named
// after its file; the picture is its own Primary image.
func (s *Scanner) processPhoto(ctx context.Context, lib storage.Library, path, album string, parent *storage.ItemID) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	id := storage.ItemID(deterministicID("item-photo-" + string(lib.ID) + "-" + path))
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	item := storage.Item{
		ID: id, Type: storage.ItemTypePhoto, LibraryID: lib.ID, ParentID: parent,
		Name: name, SortName: name, Path: path, Container: videoExt(path), Size: info.Size(),
		DateCreated: fileDateAdded(path),
	}
	if parent != nil {
		item.Album = album
	}
	if err := s.upsertItem(ctx, item); err != nil {
		log.Printf("scan: upserting photo %q: %v", path, err)
		return
	}
	s.seedPhotoImage(ctx, id, path)
}

// seedPhotoImage stores the picture at path as itemID's Primary image.
func (s *Scanner) seedPhotoImage(ctx context.Context, itemID storage.ItemID, path string) {
	fi, err := os.Stat(path)
	if err != nil {
		return
	}
	width, height := imageFileDimensions(path)
	ref := storage.ImageRef{
		ID:     storage.ImageRefID(deterministicID("image-" + string(itemID) + "-Primary")),
		ItemID: itemID, Kind: "Primary", Path: path, Size: fi.Size(), Width: width, Height: height,
	}
	if err := s.store.UpsertImageRef(ctx, ref); err != nil {
		log.Printf("scan: storing photo image %s: %v", path, err)
	}
}

// processHomeVideo catalogs one video file of a home-videos library: named
// after its file (no extension), with the technical data ffprobe gives and,
// when a "<name>.nfo" sits beside it, the fields of that file. Nothing is
// parsed out of the file name.
func (s *Scanner) processHomeVideo(ctx context.Context, pool *jobPool, lib storage.Library, path string, parent *storage.ItemID, size int64) error {
	itemID := s.reconcileItemID(ctx, lib.ID, path, storage.ItemID(deterministicID("item-video-"+string(lib.ID)+"-"+path)))
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	pr := s.probeOrReuse(ctx, pool, itemID, path, size)
	probeErr := pr.err
	item := storage.Item{
		ID: itemID, Type: storage.ItemTypeVideo, LibraryID: lib.ID, ParentID: parent,
		Name: name, SortName: name,
		Path: path, Container: videoExt(path), Size: size,
		MediaStreams: pr.streams,
		RunTimeTicks: pr.ticks,
		DateCreated:  pr.added,
	}
	if err := s.upsertItem(ctx, item); err != nil {
		return fmt.Errorf("upserting video %q: %w", path, err)
	}
	s.recordSignature(ctx, itemID, pr)
	s.seedVideoImages(ctx, itemID, path)
	s.applyVideoNFO(pool, &item, path)
	if err := s.upsertItem(ctx, item); err != nil {
		return fmt.Errorf("upserting enriched video %q: %w", path, err)
	}
	if probeErr != nil {
		pool.addWarning(fmt.Sprintf("probing %q: %v (cataloged with no media stream info)", path, probeErr))
	}
	return nil
}

// seedVideoImages stores a picture that sits beside a video and carries its
// name ("<name>.jpg", "<name>.png", "<name>-poster.jpg", "<name>-poster.png")
// as the video's Primary image.
func (s *Scanner) seedVideoImages(ctx context.Context, itemID storage.ItemID, videoPath string) {
	base := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	s.seedFolderImage(ctx, itemID, filepath.Dir(videoPath), "Primary",
		[]string{base + ".jpg", base + ".png", base + "-poster.jpg", base + "-poster.png"})
}

// movieGroup is one movie of a folder: a single file, the parts of a stacked
// movie ("Name - part1.mkv", "Name - part2.mkv") or the versions of the folder's
// movie ("Name (2014).mkv", "Name (2014) - 4K.mkv"). The primary file stands for it.
type movieGroup struct {
	primary string
	size    int64
	// parts are the other files of a stacked movie, in part order; versions
	// are the other versions ("Movie - 4K") of the movie.
	parts, versions []movieFile
	// sole is set for the only movie of a folder that is itself a Folder
	// item (it also holds sub folders): the folder's artwork and extras are
	// the movie's, but its name comes from its file.
	sole bool
}

// movieFile is a video file of a movie folder.
type movieFile struct {
	name string
	size int64
}

var stackPart = regexp.MustCompile(`(?i)^(.*?)[ ._-]*(?:cd|dvd|part|pt|disc|disk)[ ._-]?(\d{1,2})$`)

// movieOthers lists the files of a movie group beyond its primary file as
// the owned items they are stored as.
func movieOthers(dir, source string, g movieGroup) []otherFile {
	var out []otherFile
	for _, p := range g.parts {
		name, _ := CleanTitle(strings.TrimSuffix(p.name, filepath.Ext(p.name)), true)
		out = append(out, otherFile{path: filepath.Join(dir, p.name), name: name, extraType: storage.ExtraTypePart})
	}
	base := strings.TrimSuffix(g.primary, filepath.Ext(g.primary))
	for _, v := range g.versions {
		out = append(out, otherFile{
			path: filepath.Join(dir, v.name), extraType: storage.ExtraTypeVersion,
			name: versionName(strings.TrimSuffix(v.name, filepath.Ext(v.name)), base),
		})
	}
	return out
}

// isVersionOf reports whether a file base name is another version of the
// folder's movie: the movie's name followed by " - ", "-", "." or "_" and
// whatever names the version ("Movie (2014) - 4K", "Movie (2014).3D",
// "Movie (2014)-extras"). A space and a word is another movie.
func isVersionOf(base, movie string) bool {
	if len(base) <= len(movie) || !strings.EqualFold(base[:len(movie)], movie) {
		return false
	}
	switch base[len(movie)] {
	case '-', '.', '_':
		return true
	case ' ':
		return strings.HasPrefix(base[len(movie):], " - ")
	}
	return false
}

// movieDirContent lists what a movie library folder holds: its movies and
// the sub folders worth walking. dirIsFolderMovie names the folder whose own
// name stands for versions of its movie (not the library root).
func movieDirContent(ctx context.Context, dir string, versionsOfFolder bool) (groups []movieGroup, subdirs []string) {
	entries, err := readScanDir(ctx, dir)
	if err != nil {
		return nil, nil
	}
	dirName := filepath.Base(dir)
	type file struct {
		base string
		name string
		size int64
		part int
		key  string
	}
	var files []file
	for _, e := range entries {
		if e.IsDir() {
			if !SkippedFolder(e.Name()) {
				subdirs = append(subdirs, filepath.Join(dir, e.Name()))
			}
			continue
		}
		if !IsVideoFile(e.Name()) || strings.HasPrefix(e.Name(), ".") || IsExtraFile(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		f := file{base: strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())), name: e.Name(), size: info.Size()}
		if m := stackPart.FindStringSubmatch(f.base); m != nil {
			f.key = strings.ToLower(strings.TrimRight(m[1], " ._-"))
			f.part, _ = strconv.Atoi(m[2])
		}
		files = append(files, f)
	}
	used := make([]bool, len(files))
	// Stacked parts: two or more files whose names differ by the part number.
	stacks := map[string][]int{}
	var order []string
	for i, f := range files {
		if used[i] || f.key == "" {
			continue
		}
		if _, ok := stacks[f.key]; !ok {
			order = append(order, f.key)
		}
		stacks[f.key] = append(stacks[f.key], i)
	}
	for _, k := range order {
		idx := stacks[k]
		if len(idx) < 2 {
			continue
		}
		sort.SliceStable(idx, func(a, b int) bool { return files[idx[a]].part < files[idx[b]].part })
		best := idx[0]
		g := movieGroup{primary: files[best].name, size: files[best].size}
		for _, i := range idx {
			used[i] = true
			if i != best {
				g.parts = append(g.parts, movieFile{name: files[i].name, size: files[i].size})
			}
		}
		groups = append(groups, g)
	}
	// The versions of the folder's movie.
	if versionsOfFolder {
		best := -1
		var members []int
		for i, f := range files {
			if used[i] {
				continue
			}
			if f.base == dirName || isVersionOf(f.base, dirName) {
				used[i] = true
				members = append(members, i)
				if best < 0 || (f.base == dirName && files[best].base != dirName) {
					best = i
				}
			}
		}
		if best >= 0 {
			g := movieGroup{primary: files[best].name, size: files[best].size}
			for _, i := range members {
				if i != best {
					g.versions = append(g.versions, movieFile{name: files[i].name, size: files[i].size})
				}
			}
			groups = append(groups, g)
		}
	}
	for i, f := range files {
		if !used[i] {
			groups = append(groups, movieGroup{primary: f.name, size: f.size})
		}
	}
	return groups, subdirs
}

// walkMovies handles a CollectionTypeMovies path. A folder that holds one
// movie (a file, its parts or its versions) is that movie, named after the
// folder; every other folder is a Folder item holding what is in it, and its
// video files are movies named after the file (items.names.R9).
func (s *Scanner) walkMovies(ctx context.Context, pool *jobPool, lib storage.Library, root string) {
	if _, err := readScanDir(ctx, root); err != nil {
		log.Printf("scan: reading movies root %s: %v", root, err)
		return
	}
	s.walkMovieDir(ctx, pool, lib, root, nil, true)
}

func (s *Scanner) walkMovieDir(ctx context.Context, pool *jobPool, lib storage.Library, dir string, parent *storage.ItemID, isRoot bool) {
	groups, subdirs := movieDirContent(ctx, dir, !isRoot)
	if !isRoot && len(groups) == 1 && len(subdirs) == 0 {
		g := groups[0]
		pool.enqueue(func() error {
			return s.processMovie(ctx, pool, lib, dir, g, parent, true)
		})
	} else {
		if !isRoot && len(groups) == 1 {
			groups[0].sole = true
		}
		for _, g := range groups {
			g := g
			pool.enqueue(func() error {
				return s.processMovie(ctx, pool, lib, dir, g, parent, false)
			})
		}
	}
	for _, sub := range subdirs {
		subGroups, subSubdirs := movieDirContent(ctx, sub, true)
		if len(subGroups) == 1 && len(subSubdirs) == 0 {
			s.walkMovieDir(ctx, pool, lib, sub, parent, false)
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
		s.walkMovieDir(ctx, pool, lib, sub, &id, false)
	}
}

// reconcileItemID returns the ID a movie/episode at path within lib should
// be upserted under: an existing item already cataloged at that path, if
// one is found, otherwise freshID (the freshly computed deterministic ID
// for path).
//
// path+libraryID is normally enough to make freshID stable across rescans
// (deterministicID is a pure hash of it), so in the common case this
// lookup finds nothing and just confirms freshID is unclaimed. It exists
// to reconcile the case where a row already exists at path under some
// *other* ID — the ID-minting formula changed, or the row predates the
// scanner entirely (cmd/devseed, a throwaway manual-seeding tool, used
// its own "item-"+slug(folder) scheme before this project settled on
// "item-movie-"+libraryID+"-"+path/"item-episode-"+libraryID+"-"+path;
// running the real scanner over devseed-seeded data reproduced this exact
// scenario, confirmed live in a long-lived test deployment). Without this lookup, UpsertItem's
// ON CONFLICT(id) can only de-duplicate against a row sharing the same ID —
// never against another row that is, by path, obviously the same file — so
// every such mismatch silently doubles the catalog instead of updating it
// in place.
//
// libraryID is folded into freshID's hash (by every caller, not by this
// function) precisely so that this lookup's own LibraryID scoping can't be
// fooled: two libraries whose Paths happen to overlap on disk used to mint
// the *same* freshID for the same file (a pure hash of path alone), and
// since this lookup only ever checks the *current* library, it never saw
// the other library's row — two concurrent "Scan All Libraries" goroutines
// (Server.refreshAllLibraries) racing on the same file's UpsertItem would
// then silently steal that item's LibraryID back and forth, splitting one
// library's catalog into two nondeterministically. Reproduced live in
// a long-lived test deployment: two movie libraries pointing at the same host folder, one ending
// up with a random subset of the other's movies after a concurrent scan.
// Folding libraryID into the hash means each library now mints its own
// distinct ID for the same physical file, so there is nothing left to race
// over.
func (s *Scanner) reconcileItemID(ctx context.Context, libraryID storage.LibraryID, path string, freshID storage.ItemID) storage.ItemID {
	page, err := s.store.QueryItems(ctx, storage.ItemFilter{LibraryID: &libraryID, Path: path, Limit: 1})
	if err != nil || len(page.Items) == 0 {
		return freshID
	}
	return page.Items[0].ID
}

// processMovie catalogs one movie. folderMovie is true when the file is the
// only movie of its folder: the movie is then named after the folder, as
// written, and the folder's movie.nfo and artwork apply; otherwise it is
// named after the file (items.names.R9, R10).
func (s *Scanner) processMovie(ctx context.Context, pool *jobPool, lib storage.Library, dir string, group movieGroup, parent *storage.ItemID, folderMovie bool) error {
	videoFile, size := group.primary, group.size
	itemPath := filepath.Join(dir, videoFile)
	itemID := s.reconcileItemID(ctx, lib.ID, itemPath, storage.ItemID(deterministicID("item-movie-"+string(lib.ID)+"-"+itemPath)))

	source := strings.TrimSuffix(videoFile, filepath.Ext(videoFile))
	if folderMovie {
		source = filepath.Base(dir)
	}
	queryTitle, year := CleanTitle(source, true)
	name := queryTitle
	if folderMovie || (group.sole && source == filepath.Base(dir)) {
		name = source
	}
	alone := folderMovie || group.sole

	pr := s.probeOrReuse(ctx, pool, itemID, itemPath, size)
	probeErr := pr.err
	item := storage.Item{
		ID:           itemID,
		Type:         storage.ItemTypeMovie,
		LibraryID:    lib.ID,
		ParentID:     parent,
		Name:         name,
		SortName:     name,
		Path:         itemPath,
		Container:    videoExt(videoFile),
		Size:         size,
		MediaStreams: pr.streams,
		RunTimeTicks: pr.ticks,
		// .UTC(), not bare time.Now(): the sqlite driver stores a time.Time
		// as its own t.String() text (modernc.org/sqlite's formatTime), so
		// a non-UTC-zoned value serializes with its local offset instead of
		// "+0000 UTC" — different rows then sort inconsistently under a
		// plain SQL "ORDER BY date_created" (lexicographic on that raw
		// text), a real bug found live: two shows scanned seconds apart
		// under a non-UTC host TZ ranked out of order because one process
		// invocation happened to straddle local midnight while UTC hadn't.
		// UpsertItem's own DateCreated-defaulting already follows this
		// convention (item.go); every DateCreated this scanner sets
		// explicitly needs to match it. fileDateAdded already applies
		// .UTC() itself.
		DateCreated: pr.added,
	}
	if year > 0 {
		item.ProductionYear = &year
	}

	// The item row must exist before seedFolderImages/enrich can store an
	// ImageRef against it (ImageRef.ItemID is a foreign key) — upsert the
	// filename/probe-derived item first, enrich in place, then upsert
	// again to persist the enrichment.
	if err := s.upsertItem(ctx, item); err != nil {
		return fmt.Errorf("upserting movie %q: %w", itemPath, err)
	}
	s.recordSignature(ctx, itemID, pr)

	s.seedMovieImages(ctx, itemID, dir, videoFile, alone)
	s.enrich(ctx, pool, &item, metadata.Query{Kind: metadata.KindMovie, Title: queryTitle, Year: year}, false)
	if folderMovie {
		s.applyMovieNFO(pool, &item, dir, videoFile)
	} else {
		s.applyVideoNFO(pool, &item, itemPath)
	}
	if err := s.upsertItem(ctx, item); err != nil {
		return fmt.Errorf("upserting enriched movie %q: %w", itemPath, err)
	}
	// A movie whose folder is a Folder item (it holds sub folders) keeps its
	// extras only while no trailer or feature file sits next to it.
	attach := alone && !(group.sole && hasRootExtraFiles(ctx, dir))
	s.scanExtras(ctx, pool, lib, itemID, dir, []string{strings.TrimSuffix(videoFile, filepath.Ext(videoFile)), filepath.Base(dir)},
		item.ProductionYear, attach, false, movieOthers(dir, source, group))

	if probeErr != nil {
		pool.addWarning(fmt.Sprintf("probing %q: %v (cataloged with no media stream info)", itemPath, probeErr))
	}
	return nil
}

// walkSeries handles a CollectionTypeTVShows path: every folder is a series.
// Inside it, a folder named like a season is that season, any other folder
// is a season named after it, and the episode files of the series folder
// fall into the season of their number (nameparse.go, episodeparse.go).
func (s *Scanner) walkSeries(ctx context.Context, pool *jobPool, lib storage.Library, root string) {
	entries, err := readScanDir(ctx, root)
	if err != nil {
		log.Printf("scan: reading tvshows root %s: %v", root, err)
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if SkippedFolder(e.Name()) {
			continue
		}
		s.walkSeriesFolder(ctx, pool, lib, root, e.Name())
	}
}

// walkSeriesFolder catalogs one series folder of a tvshows root: the series,
// its seasons, its episodes and its extras.
func (s *Scanner) walkSeriesFolder(ctx context.Context, pool *jobPool, lib storage.Library, root, seriesFolder string) {
	seriesPath := filepath.Join(root, seriesFolder)
	seriesTitle, seriesYear := ParseSeriesTitleYear(seriesFolder)
	seriesID := storage.ItemID(deterministicID("item-series-" + string(lib.ID) + "-" + seriesPath))

	seriesItem := storage.Item{
		ID: seriesID, Type: storage.ItemTypeSeries, LibraryID: lib.ID,
		Name: seriesTitle, SortName: seriesTitle, DateCreated: fileDateAdded(seriesPath),
		Path: seriesPath, // the series directory, as the reference server reports it
	}
	if seriesYear > 0 {
		seriesItem.ProductionYear = &seriesYear
	}
	if err := s.upsertItem(ctx, seriesItem); err != nil {
		log.Printf("scan: upserting series %q: %v", seriesFolder, err)
		return
	}
	s.seedSeriesImages(ctx, seriesID, seriesPath)
	var seriesYearPtr *int
	if seriesYear > 0 {
		seriesYearPtr = &seriesYear
	}
	s.scanExtras(ctx, pool, lib, seriesID, seriesPath, []string{seriesFolder, seriesTitle}, seriesYearPtr, true, true, nil)

	pool.enqueue(func() error {
		enriched := seriesItem
		s.enrich(ctx, pool, &enriched, metadata.Query{Kind: metadata.KindSeries, Title: seriesTitle, Year: seriesYear}, false)
		s.applySeriesNFO(pool, &enriched, seriesPath)
		if err := s.upsertItem(ctx, enriched); err != nil {
			return fmt.Errorf("upserting enriched series %q: %w", seriesFolder, err)
		}
		return nil
	})

	seasons := s.walkSeasons(ctx, pool, lib, seriesPath, seriesID, seriesTitle)
	s.walkSeriesRootEpisodes(ctx, pool, lib, seriesPath, seriesID, seriesTitle, seasons)
	s.flushEpisodes(ctx, pool, lib, seasons)
}

// seasonSet resolves the season an episode that is not inside a season folder
// belongs to: the season folder of the same number when there is one,
// otherwise a season without a folder (created once, on first use).
type seasonSet struct {
	s          *Scanner
	lib        storage.Library
	seriesID   storage.ItemID
	dir        string
	real       map[int]storage.ItemID // season number -> season folder item
	unnumbered *storage.ItemID        // the first season folder whose name carries no number
	mu         sync.Mutex
	virtual    map[string]storage.ItemID
	// jobs are the episode files of the series, held until the whole series
	// is walked so that files of one episode can be merged into one item.
	jobs []episodeJob
}

// resolve returns the season item for number (nil: the season is unknown).
func (ss *seasonSet) resolve(ctx context.Context, number *int) (storage.ItemID, error) {
	if number != nil {
		if id, ok := ss.real[*number]; ok {
			return id, nil
		}
	} else if ss.unnumbered != nil {
		return *ss.unnumbered, nil
	}
	return ss.virtualSeason(ctx, number)
}

// virtualSeason returns the season without a folder for number, creating it
// on first use.
func (ss *seasonSet) virtualSeason(ctx context.Context, number *int) (storage.ItemID, error) {
	key := "unknown"
	if number != nil {
		key = strconv.Itoa(*number)
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if id, ok := ss.virtual[key]; ok {
		return id, nil
	}
	id := storage.ItemID(deterministicID("item-season-virtual-" + string(ss.lib.ID) + "-" + ss.dir + "-" + key))
	item := storage.Item{
		ID: id, Type: storage.ItemTypeSeason, LibraryID: ss.lib.ID,
		ParentID: &ss.seriesID, SeriesID: &ss.seriesID,
		Name: seasonName(number), DateCreated: fileDateAdded(ss.dir),
	}
	if number != nil {
		n := *number
		item.IndexNumber = &n
	}
	item.SortName = item.Name
	if err := ss.s.upsertItem(ctx, item); err != nil {
		return "", fmt.Errorf("upserting season %q: %w", item.Name, err)
	}
	ss.s.seedSeasonImages(ctx, id, ss.dir, "", item.IndexNumber)
	ss.virtual[key] = id
	return id, nil
}

// ensure makes sure a season of number exists (a folder season or a created
// one) for an episode that says it belongs to it from inside another folder.
func (ss *seasonSet) ensure(ctx context.Context, number int) error {
	if _, ok := ss.real[number]; ok {
		return nil
	}
	_, err := ss.virtualSeason(ctx, &number)
	return err
}

// seasonName is the display name of a season: "Specials" for number 0,
// "Season N", and "Season Unknown" for a season with no number.
func seasonName(number *int) string {
	switch {
	case number == nil:
		return "Season Unknown"
	case *number == 0:
		return "Specials"
	}
	return fmt.Sprintf("Season %d", *number)
}

func (s *Scanner) walkSeasons(ctx context.Context, pool *jobPool, lib storage.Library, seriesPath string, seriesID storage.ItemID, seriesTitle string) *seasonSet {
	seasons := &seasonSet{
		s: s, lib: lib, seriesID: seriesID, dir: seriesPath,
		real: map[int]storage.ItemID{}, virtual: map[string]storage.ItemID{},
	}
	entries, err := readScanDir(ctx, seriesPath)
	if err != nil {
		log.Printf("scan: reading series folder %s: %v", seriesPath, err)
		return seasons
	}
	type seasonDir struct {
		name   string
		path   string
		number *int
		id     storage.ItemID
	}
	var dirs []seasonDir
	var episodeFolders, plainFolders []seasonDir
	for _, e := range entries {
		if !e.IsDir() || SkippedFolder(e.Name()) {
			continue
		}
		d := seasonDir{name: e.Name(), path: filepath.Join(seriesPath, e.Name())}
		if n, ok := ParseSeasonFolder(e.Name()); ok {
			v := n
			d.number = &v
		} else if !containsVideo(ctx, d.path, 4) {
			// A folder with no video in it, at any depth, is not a season.
			plainFolders = append(plainFolders, d)
			continue
		} else if ef := ParseEpisodeFile(e.Name()); ef.Season != nil && ef.Episode != nil {
			// A folder named like an episode ("S26E01", "Show S02E03") is a
			// plain folder: the files in it are episodes of the season their
			// numbers name.
			episodeFolders = append(episodeFolders, d)
			continue
		}
		dirs = append(dirs, d)
	}
	// The season items first. Several folders may claim one number
	// ("Season 1", "Season 1 Bonus"; "Season 0", "Specials"): one season
	// item stands for them, taken from the last folder whose whole name is
	// the season (no text after the number), else the first folder.
	winner := map[int]int{}
	for i, d := range dirs {
		if d.number == nil {
			continue
		}
		pure := pureSeasonName(d.name)
		if _, ok := winner[*d.number]; !ok || pure {
			winner[*d.number] = i
		}
	}
	for i := range dirs {
		d := &dirs[i]
		if d.number != nil && winner[*d.number] != i {
			// Another folder stands for this number; keep this one only so
			// that the series still counts it among its children.
			owner := seriesID
			merged := storage.Item{
				ID:   storage.ItemID(deterministicID("item-merged-season-" + string(lib.ID) + "-" + d.path)),
				Type: storage.ItemTypeSeason, LibraryID: lib.ID, Name: d.name, SortName: d.name, Path: d.path,
				ExtraType: storage.ExtraTypeMergedSeason, OwnerID: &owner, DateCreated: fileDateAdded(d.path),
			}
			if err := s.upsertItem(ctx, merged); err != nil {
				log.Printf("scan: upserting merged season folder %q: %v", d.path, err)
			}
			continue
		}
		d.id = storage.ItemID(deterministicID("item-season-" + string(lib.ID) + "-" + d.path))
		seasonItem := storage.Item{
			ID: d.id, Type: storage.ItemTypeSeason, LibraryID: lib.ID,
			ParentID: &seriesID, SeriesID: &seriesID,
			DateCreated: fileDateAdded(d.path), Path: d.path,
		}
		if d.number != nil {
			idx := *d.number
			seasonItem.Name = seasonName(&idx)
			seasonItem.SortName = fmt.Sprintf("%04d", idx)
			seasonItem.IndexNumber = &idx
			seasons.real[idx] = d.id
		} else {
			seasonItem.Name = d.name
			seasonItem.SortName = d.name
			if seasons.unnumbered == nil {
				id := d.id
				seasons.unnumbered = &id
			}
		}
		if err := s.upsertItem(ctx, seasonItem); err != nil {
			log.Printf("scan: upserting season %q: %v", d.name, err)
			continue
		}
		s.seedSeasonImages(ctx, d.id, seriesPath, d.path, seasonItem.IndexNumber)
		s.scanExtras(ctx, pool, lib, d.id, d.path, []string{d.name}, nil, true, true, nil)
		pool.enqueue(func() error {
			enriched := seasonItem
			num := 0
			if enriched.IndexNumber != nil {
				num = *enriched.IndexNumber
				s.enrich(ctx, pool, &enriched, metadata.Query{Kind: metadata.KindSeason, SeriesTitle: seriesTitle, SeasonNumber: num}, false)
				s.applySeasonNFO(pool, &enriched, enriched.Path, seriesPath, num)
			}
			if err := s.upsertItem(ctx, enriched); err != nil {
				return fmt.Errorf("upserting enriched season %q: %w", enriched.Name, err)
			}
			return nil
		})
	}
	for _, d := range plainFolders {
		id := storage.ItemID(deterministicID("item-folder-" + string(lib.ID) + "-" + d.path))
		folder := storage.Item{
			ID: id, Type: storage.ItemTypeFolder, LibraryID: lib.ID, ParentID: &seriesID,
			Name: d.name, SortName: d.name, Path: d.path, DateCreated: fileDateAdded(d.path),
		}
		if err := s.upsertItem(ctx, folder); err != nil {
			log.Printf("scan: upserting folder %q: %v", d.path, err)
			continue
		}
		s.seedFolderArtwork(ctx, id, d.path)
	}
	for _, d := range episodeFolders {
		id := storage.ItemID(deterministicID("item-folder-" + string(lib.ID) + "-" + d.path))
		folder := storage.Item{
			ID: id, Type: storage.ItemTypeFolder, LibraryID: lib.ID, ParentID: &seriesID,
			Name: d.name, SortName: d.name, Path: d.path, DateCreated: fileDateAdded(d.path),
		}
		if err := s.upsertItem(ctx, folder); err != nil {
			log.Printf("scan: upserting folder %q: %v", d.path, err)
			continue
		}
		ef := ParseEpisodeFile(d.name)
		s.walkEpisodeFiles(ctx, pool, lib, d.path, episodeJob{
			seriesID: seriesID, seriesTitle: seriesTitle, seasons: seasons,
			folderNumbers: &ef,
		})
	}
	// The folders that lost map onto the season of their number.
	for i := range dirs {
		if d := &dirs[i]; d.number != nil && d.id == "" {
			d.id = seasons.real[*d.number]
		}
	}
	for _, d := range dirs {
		if d.id == "" {
			continue
		}
		s.walkSeasonDir(ctx, pool, lib, d.path, episodeJob{
			seriesID: seriesID, seriesTitle: seriesTitle, folderSeason: d.number,
			seasonID: &d.id, parentID: d.id, seasons: seasons,
		})
	}
	return seasons
}

// containsVideo reports whether dir holds a video file directly or in a folder
// below it (up to depth levels, skipped folders left out).
func containsVideo(ctx context.Context, dir string, depth int) bool {
	idx := dirIndexOf(ctx, dir)
	if idx == nil {
		return false
	}
	for _, n := range idx.names {
		if IsVideoFile(n) && !strings.HasPrefix(n, ".") && !IsExtraFile(n) && !ignoredEntry(ctx, dir, n, false) {
			return true
		}
	}
	if depth <= 1 {
		return false
	}
	for _, names := range idx.dirAll {
		for _, name := range names {
			if SkippedFolder(name) || ignoredEntry(ctx, dir, name, true) {
				continue
			}
			if containsVideo(ctx, filepath.Join(dir, name), depth-1) {
				return true
			}
		}
	}
	return false
}

// pureSeasonName reports whether a folder name is the season and nothing
// else: "Season 3", "S04", "Specials" (not "Season 1 Bonus").
func pureSeasonName(name string) bool {
	if specialsFolder.MatchString(name) {
		return true
	}
	return pureSeason.MatchString(name)
}

// episodeJob is one episode file to catalog.
type episodeJob struct {
	path, name   string
	size         int64
	seriesID     storage.ItemID
	seriesTitle  string
	folderSeason *int            // the season folder's number; nil when it has none or for a file in the series folder
	seasonID     *storage.ItemID // the season folder's item; nil for a file in the series folder
	parentID     storage.ItemID  // the folder the file sits in (the season or a folder inside it)
	seasons      *seasonSet
	// versions are the other files of the same episode.
	versions []string
	// folderNumbers is the numbering of the episode-like folder the file sits
	// in; its season and episode stand in for those a file name lacks.
	folderNumbers *EpisodeFile
}

// walkSeasonDir catalogs the video files of a season folder and, below it,
// the folders it holds (each a Folder item holding what is in it).
func (s *Scanner) walkSeasonDir(ctx context.Context, pool *jobPool, lib storage.Library, dir string, base episodeJob) {
	s.walkEpisodeFiles(ctx, pool, lib, dir, base)
	entries, err := readScanDir(ctx, dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || SkippedFolder(e.Name()) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		id := storage.ItemID(deterministicID("item-folder-" + string(lib.ID) + "-" + path))
		parent := base.parentID
		folder := storage.Item{
			ID: id, Type: storage.ItemTypeFolder, LibraryID: lib.ID, ParentID: &parent,
			Name: e.Name(), SortName: e.Name(), Path: path, DateCreated: fileDateAdded(path),
		}
		if err := s.upsertItem(ctx, folder); err != nil {
			log.Printf("scan: upserting folder %q: %v", path, err)
			continue
		}
		child := base
		child.parentID = id
		s.walkSeasonDir(ctx, pool, lib, path, child)
	}
}

// episodeKey identifies the episode a job's file stands for by its numbering
// (season and episode taken from the file name, the season of its folder when
// the name has none) and its name once the quality and release tokens are cut
// off ("Show S01E01", "Show S01E01 - 4K"); "" when the name does not give both
// numbers.
func episodeKey(job episodeJob) string {
	ef := ParseEpisodeFile(filepath.Base(job.path))
	if fn := job.folderNumbers; fn != nil && ef.Season == nil && ef.Episode == nil {
		ef.Season, ef.Episode, ef.EpisodeEnd = fn.Season, fn.Episode, fn.EpisodeEnd
	}
	season := ef.Season
	if season == nil && job.folderSeason != nil {
		season = job.folderSeason
	}
	if season == nil || ef.Episode == nil || ef.Date != nil {
		return ""
	}
	end := 0
	if ef.EpisodeEnd != nil {
		end = *ef.EpisodeEnd
	}
	return fmt.Sprintf("%d:%d:%d:%s", *season, *ef.Episode, end, strings.ToLower(strings.TrimRight(ef.Name, " -._")))
}

// flushEpisodes enqueues the episode files collected for a series. Files that
// carry the same numbers and name are versions of one episode: the
// one with the shortest name (then the first by path) is the episode, the
// others are cataloged as its versions.
func (s *Scanner) flushEpisodes(ctx context.Context, pool *jobPool, lib storage.Library, ss *seasonSet) {
	jobs := ss.jobs
	ss.jobs = nil
	byKey := map[string][]int{}
	for i, j := range jobs {
		if k := episodeKey(j); k != "" {
			byKey[k] = append(byKey[k], i)
		}
	}
	skip := map[int]bool{}
	for _, idx := range byKey {
		if len(idx) < 2 {
			continue
		}
		primary := idx[0]
		for _, i := range idx[1:] {
			a, b := filepath.Base(jobs[i].path), filepath.Base(jobs[primary].path)
			if len(a) < len(b) || (len(a) == len(b) && jobs[i].path < jobs[primary].path) {
				primary = i
			}
		}
		var others []string
		for _, i := range idx {
			if i != primary {
				skip[i] = true
				others = append(others, jobs[i].path)
			}
		}
		sort.Strings(others)
		jobs[primary].versions = others
	}
	for i, j := range jobs {
		if skip[i] {
			continue
		}
		job := j
		pool.enqueue(func() error { return s.processEpisode(ctx, pool, lib, job) })
	}
}

// walkSeriesRootEpisodes catalogs the episode files that sit directly in the
// series folder.
func (s *Scanner) walkSeriesRootEpisodes(ctx context.Context, pool *jobPool, lib storage.Library, seriesPath string, seriesID storage.ItemID, seriesTitle string, seasons *seasonSet) {
	s.walkEpisodeFiles(ctx, pool, lib, seriesPath, episodeJob{seriesID: seriesID, seriesTitle: seriesTitle, seasons: seasons})
}

// walkEpisodeFiles enqueues every video file directly inside dir. A file needs
// no season or episode number to be an episode: its numbers come from the
// file name and its .nfo when they carry any, and its name is the file name
// without the extension unless the .nfo gives a title.
func (s *Scanner) walkEpisodeFiles(ctx context.Context, pool *jobPool, lib storage.Library, dir string, base episodeJob) {
	entries, err := readScanDir(ctx, dir)
	if err != nil {
		log.Printf("scan: reading season folder %s: %v", dir, err)
		return
	}
	for _, e := range entries {
		if e.IsDir() || !IsVideoFile(e.Name()) || strings.HasPrefix(e.Name(), ".") || IsExtraFile(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		job := base
		job.path = filepath.Join(dir, e.Name())
		job.name = strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		job.size = info.Size()
		base.seasons.jobs = append(base.seasons.jobs, job)
	}
}

func (s *Scanner) processEpisode(ctx context.Context, pool *jobPool, lib storage.Library, job episodeJob) error {
	episodePath := job.path
	itemID := s.reconcileItemID(ctx, lib.ID, episodePath, storage.ItemID(deterministicID("item-episode-"+string(lib.ID)+"-"+episodePath)))

	// Numbers: the file name first, then the .nfo over it. A name that gives
	// an episode number (or an air date) but no season belongs to the season
	// of the folder it sits in.
	ef := ParseEpisodeFile(filepath.Base(episodePath))
	season, episode, episodeEnd := ef.Season, ef.Episode, ef.EpisodeEnd
	if fn := job.folderNumbers; fn != nil && season == nil && episode == nil {
		season, episode, episodeEnd = fn.Season, fn.Episode, fn.EpisodeEnd
	}
	if season == nil && (episode != nil || ef.Date != nil) && job.folderSeason != nil {
		n := *job.folderSeason
		season = &n
	}
	nfo, nfoFound := s.episodeNFOFor(pool, episodePath)
	if nfoFound {
		if v := nfoNumber(nfo.Season); v != nil {
			season = v
		}
		if v := nfoNumber(nfo.Episode); v != nil {
			episode = v
		}
		if nfo.EpisodeEnd != nil {
			episodeEnd = nfo.EpisodeEnd
		}
	}

	// The season an episode hangs under: its folder's, else the season of
	// the same number, else the one without a number. An explicit season
	// number that no season has is created, empty, from inside a folder.
	var seasonID storage.ItemID
	if job.seasonID != nil {
		seasonID = *job.seasonID
		if season != nil && (job.folderSeason == nil || *season != *job.folderSeason) {
			if err := job.seasons.ensure(ctx, *season); err != nil {
				return err
			}
		}
	} else {
		id, err := job.seasons.resolve(ctx, season)
		if err != nil {
			return err
		}
		seasonID = id
	}
	parentID := seasonID
	if job.parentID != "" {
		parentID = job.parentID
	}
	seriesID := job.seriesID

	pr := s.probeOrReuse(ctx, pool, itemID, episodePath, job.size)
	probeErr := pr.err
	item := storage.Item{
		ID: itemID, Type: storage.ItemTypeEpisode, LibraryID: lib.ID,
		ParentID: &parentID, SeriesID: &seriesID, SeasonID: &seasonID,
		Name: ef.Name, SortName: ef.Name,
		IndexNumber: episode, ParentIndexNumber: season, IndexNumberEnd: episodeEnd,
		PremiereDate: ef.Date,
		Path:         episodePath, Container: videoExt(episodePath), Size: job.size,
		MediaStreams: pr.streams,
		RunTimeTicks: pr.ticks,
		DateCreated:  pr.added,
	}
	if ef.Year > 0 {
		y := ef.Year
		item.ProductionYear = &y
	}

	// Same ordering requirement as processMovie: upsert first so
	// enrich's image storage has a row to point its foreign key at.
	if err := s.upsertItem(ctx, item); err != nil {
		return fmt.Errorf("upserting episode %q: %w", episodePath, err)
	}
	s.recordSignature(ctx, itemID, pr)
	s.seedEpisodeImages(ctx, itemID, episodePath)
	q := metadata.Query{Kind: metadata.KindEpisode, SeriesTitle: job.seriesTitle}
	if season != nil {
		q.SeasonNumber = *season
	}
	if episode != nil {
		q.EpisodeNumber = *episode
	}
	s.enrich(ctx, pool, &item, q, false)
	if nfoFound {
		applyParsedEpisodeNFO(&item, nfo)
	}
	if err := s.upsertItem(ctx, item); err != nil {
		return fmt.Errorf("upserting enriched episode %q: %w", episodePath, err)
	}
	var versions []otherFile
	base := strings.TrimSuffix(filepath.Base(episodePath), filepath.Ext(episodePath))
	for _, p := range job.versions {
		stem := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
		versions = append(versions, otherFile{path: p, extraType: storage.ExtraTypeVersion, name: versionName(stem, base)})
	}
	s.scanExtras(ctx, pool, lib, itemID, filepath.Dir(episodePath), nil, nil, false, false, versions)
	if probeErr != nil {
		pool.addWarning(fmt.Sprintf("probing %q: %v (cataloged with no media stream info)", episodePath, probeErr))
	}
	return nil
}

// enrich calls the configured Provider and overlays every non-zero Result
// field onto item, downloading and caching any returned images. A
// metadata.ErrNotFound miss is silent (item keeps its filename-derived
// fields, per New's doc comment); any other error is logged to pool's
// Stats but does not fail the caller — enrichment is always best-effort,
// never required for an item to be cataloged.
//
// replaceImages gates storeProviderImage's own decision to overwrite an
// image kind item already has — see that function's doc comment; every
// ScanLibrary call site passes false (a routine/re-scan never replaces an
// existing image, validated against the reference server's own default
// behavior, docs/08's "Local-vs-remote precedence" section), only
// RefreshItem ever passes the caller's own ReplaceAllImages request
// through.
func (s *Scanner) enrich(ctx context.Context, pool *jobPool, item *storage.Item, q metadata.Query, replaceImages bool) {
	res, err := s.provider.Lookup(ctx, q)
	if err != nil {
		if err != metadata.ErrNotFound {
			pool.mu.Lock()
			pool.stats.Errors = append(pool.stats.Errors, fmt.Sprintf("provider lookup for %q: %v", item.Name, err))
			pool.mu.Unlock()
		}
		return
	}
	pool.markEnriched()

	if res.Title != "" {
		item.Name = res.Title
		item.SortName = res.Title
	}
	if res.OriginalTitle != "" {
		item.OriginalTitle = res.OriginalTitle
	}
	if res.Overview != "" {
		item.Overview = res.Overview
	}
	if len(res.Genres) > 0 {
		item.Genres = res.Genres
	}
	if len(res.Tags) > 0 {
		item.Tags = res.Tags
	}
	if len(res.Studios) > 0 {
		item.Studios = res.Studios
	}
	if res.ProductionYear > 0 {
		y := res.ProductionYear
		item.ProductionYear = &y
	}
	if res.PremiereDateUnix > 0 {
		t := time.Unix(res.PremiereDateUnix, 0).UTC()
		item.PremiereDate = &t
	}
	if res.EndDate > 0 {
		t := time.Unix(res.EndDate, 0).UTC()
		item.EndDate = &t
	}
	if res.OfficialRating != "" {
		item.OfficialRating = res.OfficialRating
	}
	if res.CommunityRating > 0 {
		r := res.CommunityRating
		item.CommunityRating = &r
	}
	if len(res.ExternalIDs) > 0 {
		item.ProviderIDs = res.ExternalIDs
	}
	if len(res.People) > 0 {
		item.People = peopleFromResult(res.People)
	}

	for _, img := range res.Images {
		s.storeProviderImage(ctx, item.ID, img, replaceImages)
	}
}

// RefreshItem re-enriches one already-cataloged item — both the local
// filesystem sources ScanLibrary itself reads (a sidecar .nfo, folder-
// convention artwork) and the configured remote Provider — without walking
// the whole library again. The per-item counterpart to ScanLibrary's own
// per-item seed-images/enrich/apply-NFO sequence, for jellyfin-web's
// per-item "Refresh metadata" dashboard action (POST /Items/{itemId}/
// Refresh, api/item_refresh.go), which used to have no effect at all
// (docs/11's documented gap). Found live: a series' incomplete metadata
// stayed incomplete after a refresh even though its media folder already
// had a tvshow.nfo and artwork this scanner already knows how to read —
// this isn't just a "no TMDB key configured" gap, since none of that needs
// a remote provider at all.
//
// Same ordering ScanLibrary's own walk functions use, and for the same
// reason (nfo.go's doc comments): local artwork is seeded first, then the
// remote Provider runs, then the local .nfo is applied last so it overrides
// whatever the Provider just set — "NFO overrides provider" is this
// scanner's documented convention (TestScanLibraryNFOOverridesProvider),
// not specific to a fresh scan. replaceImages is the caller's
// ReplaceAllImages request (api/item_refresh.go's query param): when false
// (jellyfin-web's dashboard action's own default), a Provider-returned
// image never overwrites an image kind the item already has — including
// the one refreshLocalImages just seeded above, matching the reference
// server's own default behavior (validated, docs/08's "Local-vs-remote
// precedence" section) — Tinyfin used to always let the Provider win here
// regardless of this flag, which this parameter fixes.
//
// Only Movie/Series/Season/Episode are refreshed — the same set ScanLibrary
// itself enriches (docs/08's video-first v1 scope); any other ItemType
// (Trailer, Audio, a library/folder row, ...) is a silent no-op, since this
// scanner has never had an enrichment path for those kinds. A
// metadata.ErrNotFound miss from the remote Provider is likewise not an
// error: item keeps its existing fields, matching enrich's own "a provider
// miss is a normal outcome" convention (New's doc comment) — the local
// steps still run regardless.
func (s *Scanner) RefreshItem(ctx context.Context, itemID storage.ItemID, replaceImages bool) error {
	item, err := s.store.GetItem(ctx, itemID)
	if err != nil {
		return fmt.Errorf("refreshing item %s: %w", itemID, err)
	}

	if item.Type == storage.ItemTypeVideo {
		// A home video has no provider lookup: its picture and .nfo only.
		pool := newJobPool(1)
		item.SortName = item.Name
		s.seedVideoImages(ctx, item.ID, item.Path)
		s.applyVideoNFO(pool, &item, item.Path)
		if len(pool.stats.Errors) > 0 {
			return fmt.Errorf("refreshing item %q: %s", item.Name, strings.Join(pool.stats.Errors, "; "))
		}
		return s.upsertItem(ctx, item)
	}

	q, ok := s.refreshQuery(ctx, item)
	if !ok {
		return nil
	}

	pool := newJobPool(1)
	// The stored sort name is the derived key; start again from the name so
	// the key is not derived from itself.
	item.SortName = item.Name
	s.refreshLocalImages(ctx, &item)
	s.enrich(ctx, pool, &item, q, replaceImages)
	s.refreshLocalNFO(ctx, pool, &item)
	if len(pool.stats.Errors) > 0 {
		return fmt.Errorf("refreshing item %q: %s", item.Name, strings.Join(pool.stats.Errors, "; "))
	}
	return s.upsertItem(ctx, item)
}

// refreshLocalImages re-seeds item's folder-convention local artwork
// (poster.jpg, seasonNN-poster.jpg, ...) the same way ScanLibrary's own
// seedFolderImages/seedSeasonImage do during a fresh scan. Movie/Episode
// store their own video file's Path directly (storage.Item.Path); Episode
// has no local-artwork convention of its own in this scanner (processEpisode
// never calls seedFolderImages either, so neither does this) and Series/
// Season store their folder as Path since the scanner started recording
// it, but rows cataloged by an older version have none until rescanned — so
// seriesFolder/seasonFolder keep deriving the folder from one child
// episode's Path instead. A series/season with no episode rows yet
// has nothing to derive a folder from, so this step is silently skipped —
// RefreshItem's remote-provider step below still runs regardless.
func (s *Scanner) refreshLocalImages(ctx context.Context, item *storage.Item) {
	switch item.Type {
	case storage.ItemTypeMovie:
		if item.Path == "" {
			return
		}
		s.seedMovieImages(ctx, item.ID, filepath.Dir(item.Path), filepath.Base(item.Path), s.movieIsAlone(ctx, *item))
	case storage.ItemTypeEpisode:
		if item.Path != "" {
			s.seedEpisodeImages(ctx, item.ID, item.Path)
		}
	case storage.ItemTypeFolder:
		if item.Path != "" {
			s.seedFolderArtwork(ctx, item.ID, item.Path)
		}
	case storage.ItemTypeSeries:
		if seriesPath, ok := s.seriesFolderOf(ctx, *item); ok {
			s.seedSeriesImages(ctx, item.ID, seriesPath)
		}
	case storage.ItemTypeSeason:
		seasonPath, ok := s.seasonFolderOf(ctx, *item)
		if !ok {
			return
		}
		seriesPath := filepath.Dir(seasonPath)
		if item.Path == "" {
			// A season cataloged without a folder of its own has only the
			// series-level pictures.
			seasonPath = ""
		}
		s.seedSeasonImages(ctx, item.ID, seriesPath, seasonPath, item.IndexNumber)
	}
}

// movieIsAlone reports whether a movie is the only movie of its folder, the
// case in which the folder's own pictures (poster.jpg, ...) are its artwork.
func (s *Scanner) movieIsAlone(ctx context.Context, item storage.Item) bool {
	dir := filepath.Dir(item.Path)
	if item.ParentID == nil {
		return item.Name == filepath.Base(dir)
	}
	parent, err := s.store.GetItem(ctx, *item.ParentID)
	if err != nil || parent.Path != dir {
		return true
	}
	// The movie's folder is a Folder item: the movie is alone when it is
	// the only movie in it.
	page, err := s.store.QueryItems(ctx, storage.ItemFilter{
		ParentID: &parent.ID, ItemTypes: []storage.ItemType{storage.ItemTypeMovie}, Limit: 2,
	})
	return err == nil && page.TotalCount == 1
}

// refreshLocalNFO re-applies item's sidecar .nfo file, if any is present,
// overlaying it onto item's already remote-enriched fields — deliberately
// called after enrich in RefreshItem, not before, so "NFO overrides
// provider" holds here the same as it does during a fresh scan (see
// RefreshItem's doc comment). Season's own convention (applySeasonNFO) only
// ever overlays item.Name from a <namedseason> match — see its doc comment
// for why every other field in that file is series-level, not season-level.
func (s *Scanner) refreshLocalNFO(ctx context.Context, pool *jobPool, item *storage.Item) {
	switch item.Type {
	case storage.ItemTypeMovie:
		if item.Path == "" {
			return
		}
		s.applyMovieNFO(pool, item, filepath.Dir(item.Path), filepath.Base(item.Path))
	case storage.ItemTypeSeries:
		if seriesPath, ok := s.seriesFolderOf(ctx, *item); ok {
			s.applySeriesNFO(pool, item, seriesPath)
		}
	case storage.ItemTypeSeason:
		seasonPath, ok := s.seasonFolderOf(ctx, *item)
		if !ok {
			return
		}
		seriesPath := filepath.Dir(seasonPath)
		seasonNum := 0
		if item.IndexNumber != nil {
			seasonNum = *item.IndexNumber
		}
		s.applySeasonNFO(pool, item, seasonPath, seriesPath, seasonNum)
	case storage.ItemTypeEpisode:
		if item.Path != "" {
			s.applyEpisodeNFO(pool, item, item.Path)
		}
	}
}

// seriesFolder derives a series item's own root folder from one of its
// episodes' stored Path — older Series rows have no Path of their own until
// rescanned (see refreshLocalImages' doc comment) — by walking up two levels from an episode
// file (.../SeriesFolder/Season NN/episode.ext), the same Kodi/Jellyfin
// layout ScanLibrary's own walkSeries/walkSeasons/walkEpisodes already
// assume throughout. ok is false if the series has no episode rows yet to
// derive a path from.
func (s *Scanner) seriesFolder(ctx context.Context, seriesID storage.ItemID) (string, bool) {
	page, err := s.store.QueryItems(ctx, storage.ItemFilter{
		SeriesID: &seriesID, ItemTypes: []storage.ItemType{storage.ItemTypeEpisode}, Limit: 1,
	})
	if err != nil || len(page.Items) == 0 || page.Items[0].Path == "" {
		return "", false
	}
	return filepath.Dir(filepath.Dir(page.Items[0].Path)), true
}

// seriesFolderOf is the series' folder: its own Path, or for a row cataloged
// before series stored one, derived from an episode.
func (s *Scanner) seriesFolderOf(ctx context.Context, item storage.Item) (string, bool) {
	if item.Path != "" {
		return item.Path, true
	}
	return s.seriesFolder(ctx, item.ID)
}

// seasonFolderOf is the season's folder: its own Path, or for a row
// cataloged before seasons stored one, the folder of an episode when that
// folder is a season folder. A season without a folder has none.
func (s *Scanner) seasonFolderOf(ctx context.Context, item storage.Item) (string, bool) {
	if item.Path != "" {
		return item.Path, true
	}
	dir, ok := s.seasonFolder(ctx, item.ID)
	if !ok {
		return "", false
	}
	if _, isSeason := ParseSeasonFolder(filepath.Base(dir)); !isSeason {
		return "", false
	}
	return dir, true
}

// seasonFolder is seriesFolder's Season-level counterpart: one level up
// from a child episode's Path instead of two.
func (s *Scanner) seasonFolder(ctx context.Context, seasonID storage.ItemID) (string, bool) {
	page, err := s.store.QueryItems(ctx, storage.ItemFilter{
		ParentID: &seasonID, ItemTypes: []storage.ItemType{storage.ItemTypeEpisode}, Limit: 1,
	})
	if err != nil || len(page.Items) == 0 || page.Items[0].Path == "" {
		return "", false
	}
	return filepath.Dir(page.Items[0].Path), true
}

// refreshQuery builds the metadata.Query RefreshItem should look item up
// with, from item's own stored fields — no filename parsing, unlike a fresh
// scan, since item is already cataloged. ok is false for an ItemType this
// scanner never enriches (see RefreshItem's doc comment), telling the
// caller to skip the lookup entirely rather than ask a Provider a question
// it has no Kind for.
//
// ExternalIDs carries the provider ID a prior lookup already resolved, so a
// re-fetch re-identifies the same title/series instead of re-searching by
// name (Query.ExternalIDs' own doc comment) — for a Season/Episode this is
// the *parent series'* ProviderIDs, matching what TMDBProvider.
// resolveSeriesID expects there (tmdb.go), not the season/episode's own
// (which, for episodes, TMDB has no separate ID scheme for in this
// project's Result shape anyway).
func (s *Scanner) refreshQuery(ctx context.Context, item storage.Item) (metadata.Query, bool) {
	year := 0
	if item.ProductionYear != nil {
		year = *item.ProductionYear
	}

	switch item.Type {
	case storage.ItemTypeMovie:
		return metadata.Query{Kind: metadata.KindMovie, Title: item.Name, Year: year, ExternalIDs: item.ProviderIDs}, true
	case storage.ItemTypeSeries:
		return metadata.Query{Kind: metadata.KindSeries, Title: item.Name, Year: year, ExternalIDs: item.ProviderIDs}, true
	case storage.ItemTypeSeason:
		series, ok := s.parentSeries(ctx, item)
		if !ok {
			return metadata.Query{}, false
		}
		seasonNum := 0
		if item.IndexNumber != nil {
			seasonNum = *item.IndexNumber
		}
		return metadata.Query{Kind: metadata.KindSeason, SeriesTitle: series.Name, SeasonNumber: seasonNum, ExternalIDs: series.ProviderIDs}, true
	case storage.ItemTypeEpisode:
		series, ok := s.parentSeries(ctx, item)
		if !ok {
			return metadata.Query{}, false
		}
		seasonNum, episodeNum := 0, 0
		if item.ParentIndexNumber != nil {
			seasonNum = *item.ParentIndexNumber
		}
		if item.IndexNumber != nil {
			episodeNum = *item.IndexNumber
		}
		return metadata.Query{
			Kind: metadata.KindEpisode, SeriesTitle: series.Name,
			SeasonNumber: seasonNum, EpisodeNumber: episodeNum, ExternalIDs: series.ProviderIDs,
		}, true
	default:
		return metadata.Query{}, false
	}
}

// parentSeries fetches item.SeriesID for a Season/Episode item, reporting ok
// false if item has no SeriesID set (shouldn't happen for a scanner-produced
// row, but a manually-seeded one — cmd/devseed — might predate that
// convention) or the referenced series row is gone.
func (s *Scanner) parentSeries(ctx context.Context, item storage.Item) (storage.Item, bool) {
	if item.SeriesID == nil {
		return storage.Item{}, false
	}
	series, err := s.store.GetItem(ctx, *item.SeriesID)
	if err != nil {
		return storage.Item{}, false
	}
	return series, true
}

// peopleFromResult converts a metadata.Result's provider-decoupled People
// (metadata.Person) into storage.Person — the same kind of Result ->
// storage.Item field conversion enrich already does inline for every other
// field above, pulled into its own function only because it's a slice
// element-by-element mapping rather than a plain assignment.
func peopleFromResult(people []metadata.Person) []storage.Person {
	out := make([]storage.Person, 0, len(people))
	for _, p := range people {
		out = append(out, storage.Person{Name: p.Name, Role: p.Role, Type: p.Type, TMDBID: p.TMDBID})
	}
	return out
}

// storeProviderImage downloads img and stores a local ImageRef for it,
// unless itemID already has an image of img.Kind and replaceImages is
// false — validated against the reference server's own
// ItemImageProvider.RefreshImages: with ReplaceAllImages=false (what
// jellyfin-web's dashboard "Refresh metadata" action actually sends,
// docs/08's "Local-vs-remote precedence" section), an existing image is
// left alone and the remote provider isn't even asked for that type. This
// also means local, folder-convention artwork (seedFolderImage/
// seedFolderImages/seedSeasonImage, always run before enrich) already wins
// by the time this runs during a normal scan pass, without needing its own
// separate check — it's already on the item by then. Failures are logged,
// not propagated — missing artwork is not a reason to fail the whole item
// (New's doc comment).
func (s *Scanner) storeProviderImage(ctx context.Context, itemID storage.ItemID, img metadata.Image, replaceImages bool) {
	if s.imageDir == "" {
		return
	}
	if !replaceImages {
		existing, err := s.store.GetImageRefs(ctx, itemID, storage.ImageKind(img.Kind))
		if err == nil && len(existing) > 0 {
			return
		}
	}
	data, err := s.httpClient.fetchImage(ctx, img.URL)
	if err != nil {
		log.Printf("scan: downloading %s image for %s: %v", img.Kind, itemID, err)
		return
	}
	path := filepath.Join(s.imageDir, fmt.Sprintf("%s-%s.jpg", itemID, img.Kind))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		log.Printf("scan: writing %s image for %s: %v", img.Kind, itemID, err)
		return
	}
	width, height := imageDimensions(bytes.NewReader(data))
	ref := storage.ImageRef{
		ID:     storage.ImageRefID(deterministicID("image-" + string(itemID) + "-" + img.Kind)),
		ItemID: itemID, Kind: storage.ImageKind(img.Kind),
		Path: path, Size: int64(len(data)), Width: width, Height: height,
	}
	if err := s.store.UpsertImageRef(ctx, ref); err != nil {
		log.Printf("scan: storing %s image ref for %s: %v", img.Kind, itemID, err)
	}
}

// seedFolderImage stores the first of candidates (filenames relative to
// folder) that actually exists as itemID's image of the given kind.
func (s *Scanner) seedFolderImage(ctx context.Context, itemID storage.ItemID, folder, kind string, candidates []string) {
	names := dirNames(ctx, folder)
	for _, filename := range candidates {
		if _, ok := names[filename]; !ok {
			continue
		}
		path := filepath.Join(folder, filename)
		fi, err := os.Stat(path)
		if err != nil {
			continue
		}
		width, height := imageFileDimensions(path)
		ref := storage.ImageRef{
			ID:     storage.ImageRefID(deterministicID("image-" + string(itemID) + "-" + kind)),
			ItemID: itemID, Kind: storage.ImageKind(kind),
			Path: path, Size: fi.Size(), Width: width, Height: height,
		}
		if err := s.store.UpsertImageRef(ctx, ref); err != nil {
			log.Printf("scan: storing folder image %s for %s: %v", filename, itemID, err)
		}
		return
	}
}

// imageDimensions decodes just r's header to get an image's pixel
// dimensions — image.DecodeConfig reads only as much as each registered
// format needs to parse width/height, not the full image, so this is
// cheap even for a large poster. Used to populate ImageRef.Width/Height,
// which BaseItemDto.PrimaryImageAspectRatio (api/dto.go) is derived from:
// confirmed load-bearing, not cosmetic, by reading jellyfin-web's
// cardBuilder image-loader module — its "auto" card shape takes the
// median PrimaryImageAspectRatio across a list to choose portrait (~2:3)
// vs square (~1:1) vs banner, and falls back to square when every item's
// ratio is absent. 0,0 (silently treated as "unknown", not an error) on
// any decode failure — same "missing artwork isn't fatal" convention
// storeProviderImage/seedFolderImage already use for the image itself.
func imageDimensions(r io.Reader) (width, height int) {
	cfg, _, err := image.DecodeConfig(r)
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

// imageFileDimensions is imageDimensions for a file already on disk
// (seedFolderImage's case, where the bytes were never read into memory).
func imageFileDimensions(path string) (width, height int) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	return imageDimensions(f)
}

func videoExt(filename string) string {
	ext := filepath.Ext(filename)
	if len(ext) > 0 {
		return ext[1:]
	}
	return ""
}

// largestVideoFile returns the largest recognized video file directly
// inside dir (not recursive) — the same "one file per movie folder"
// convention devseed's throwaway helper used.
// fileDateAdded returns path's on-disk modification time in UTC, for use as
// a newly-discovered item's DateCreated — so a library scanned in bulk (e.g.
// an initial import of an existing collection) sorts "recently added" by
// when each file actually landed on disk, not by scan/traversal order.
// Mirrors the reference server's own default of seeding a new item's "date
// added" from the file's own filesystem timestamp rather than the scan's
// wall-clock time (confirmed by reading jellyfin/jellyfin's resolver
// behavior: it seeds DateCreated from the file's creation time unless that
// value is unavailable, in which case it falls back to the scan's own
// wall-clock time — the same fallback used here).
//
// Go's os.Stat cannot portably read a file's *creation* time on Linux (birth
// time isn't exposed by the standard stat syscall on common filesystems like
// ext4, the reference server has this same platform gap), so this uses
// ModTime instead: for a freshly copied/downloaded file, mtime is at least
// as good a proxy for "date added" as creation time, and is reliably
// available on every platform Tinyfin runs on.
func fileDateAdded(path string) time.Time {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Now().UTC()
	}
	return fi.ModTime().UTC()
}

func largestVideoFile(dir string) (string, int64) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", 0
	}
	var best string
	var bestSize int64
	for _, e := range entries {
		if e.IsDir() || !IsVideoFile(e.Name()) {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		if fi.Size() > bestSize {
			best, bestSize = e.Name(), fi.Size()
		}
	}
	return best, bestSize
}
