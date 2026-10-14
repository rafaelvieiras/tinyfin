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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/metadata"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// defaultConcurrency bounds simultaneous ffprobe subprocesses and provider
// HTTP calls when Config.Concurrency is left zero — docs/08's "Scan-time
// resource discipline" calls this out explicitly as a documented failure
// mode of the reference implementation this project deliberately avoids.
const defaultConcurrency = 4

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
}

// Scanner walks configured library paths and populates storage.Store.
type Scanner struct {
	store       storage.Store
	provider    metadata.Provider
	ffprobePath string
	imageDir    string
	concurrency int
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
	if cfg.ImageDir != "" {
		_ = os.MkdirAll(cfg.ImageDir, 0o755)
	}
	return &Scanner{
		store:       store,
		provider:    provider,
		ffprobePath: ffprobePath,
		imageDir:    cfg.ImageDir,
		concurrency: concurrency,
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
	Errors        []string
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
			p.mu.Unlock()
			return
		}
		p.mu.Lock()
		p.stats.ItemsScanned++
		p.mu.Unlock()
	}()
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
	pool := newJobPool(s.concurrency)

	for _, root := range lib.Paths {
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
		default:
			// Music/BoxSets/Mixed aren't in this scanner's scope yet — a
			// documented gap (docs/08 describes the video-first v1 scope
			// this project targets), not a silent no-op: logged once per
			// path so an operator adding an unsupported library type sees
			// why nothing gets cataloged.
			log.Printf("scan: library %q: collection type %q is not scanned yet, skipping %s", lib.Name, lib.CollectionType, root)
		}
	}

	stats := pool.wait()

	now := time.Now().UTC()
	lib.LastScannedAt = &now
	if err := s.store.UpsertLibrary(ctx, lib); err != nil {
		return stats, fmt.Errorf("scan: persisting LastScannedAt for library %q: %w", lib.Name, err)
	}
	return stats, nil
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
	if err := s.store.UpsertItem(ctx, item); err != nil {
		log.Printf("scan: recording root folder %s: %v", root, err)
	}
}

// walkHomeVideos handles a CollectionTypeHomeVideos path: every folder under
// it becomes a Folder item and every video file a Video item, whatever the
// nesting and naming. A folder is cataloged before its content so the
// ancestor index sees the whole chain. parent is nil for the first level.
func (s *Scanner) walkHomeVideos(ctx context.Context, pool *jobPool, lib storage.Library, dir string, parent *storage.ItemID) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Printf("scan: reading home videos folder %s: %v", dir, err)
		return
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if e.IsDir() {
			if IgnoredFolder(e.Name()) {
				continue
			}
			id := storage.ItemID(deterministicID("item-folder-" + string(lib.ID) + "-" + path))
			folder := storage.Item{
				ID: id, Type: storage.ItemTypeFolder, LibraryID: lib.ID, ParentID: parent,
				Name: e.Name(), SortName: e.Name(), Path: path, DateCreated: fileDateAdded(path),
			}
			if err := s.store.UpsertItem(ctx, folder); err != nil {
				log.Printf("scan: upserting folder %q: %v", path, err)
				continue
			}
			s.seedFolderImages(ctx, id, path, "")
			if !ExtrasFolder(e.Name()) {
				s.walkHomeVideos(ctx, pool, lib, path, &id)
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

// processHomeVideo catalogs one video file of a home-videos library: named
// after its file (no extension), with the technical data ffprobe gives and,
// when a "<name>.nfo" sits beside it, the fields of that file. Nothing is
// parsed out of the file name.
func (s *Scanner) processHomeVideo(ctx context.Context, pool *jobPool, lib storage.Library, path string, parent *storage.ItemID, size int64) error {
	itemID := s.reconcileItemID(ctx, lib.ID, path, storage.ItemID(deterministicID("item-video-"+string(lib.ID)+"-"+path)))
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	info, probeErr := s.probe(ctx, path)
	item := storage.Item{
		ID: itemID, Type: storage.ItemTypeVideo, LibraryID: lib.ID, ParentID: parent,
		Name: name, SortName: name,
		Path: path, Container: videoExt(path), Size: size,
		MediaStreams: mediaStreamsFromProbe(info),
		RunTimeTicks: runtimeTicksFromProbe(info),
		DateCreated:  fileDateAdded(path),
	}
	if err := s.store.UpsertItem(ctx, item); err != nil {
		return fmt.Errorf("upserting video %q: %w", path, err)
	}
	s.seedVideoImages(ctx, itemID, path)
	s.applyVideoNFO(pool, &item, path)
	if err := s.store.UpsertItem(ctx, item); err != nil {
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

// walkMovies handles a CollectionTypeMovies path: one top-level directory
// per movie, "Title (Year)" named, holding one video file (the largest one
// present, same convention devseed used) plus optional poster.jpg/
// fanart.jpg.
func (s *Scanner) walkMovies(ctx context.Context, pool *jobPool, lib storage.Library, root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		log.Printf("scan: reading movies root %s: %v", root, err)
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		folder := e.Name()
		hostFolder := filepath.Join(root, folder)
		videoFile, size := largestVideoFile(hostFolder)
		if videoFile == "" {
			continue
		}

		pool.enqueue(func() error {
			return s.processMovie(ctx, pool, lib, hostFolder, folder, videoFile, size)
		})
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

func (s *Scanner) processMovie(ctx context.Context, pool *jobPool, lib storage.Library, hostFolder, folder, videoFile string, size int64) error {
	title, year := ParseTitleYear(folder)
	itemPath := filepath.Join(hostFolder, videoFile)
	itemID := s.reconcileItemID(ctx, lib.ID, itemPath, storage.ItemID(deterministicID("item-movie-"+string(lib.ID)+"-"+itemPath)))

	info, probeErr := s.probe(ctx, itemPath)
	item := storage.Item{
		ID:           itemID,
		Type:         storage.ItemTypeMovie,
		LibraryID:    lib.ID,
		Name:         title,
		SortName:     title,
		Path:         itemPath,
		Container:    videoExt(videoFile),
		Size:         size,
		MediaStreams: mediaStreamsFromProbe(info),
		RunTimeTicks: runtimeTicksFromProbe(info),
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
		DateCreated: fileDateAdded(itemPath),
	}
	if year > 0 {
		item.ProductionYear = &year
	}

	// The item row must exist before seedFolderImages/enrich can store an
	// ImageRef against it (ImageRef.ItemID is a foreign key) — upsert the
	// filename/probe-derived item first, enrich in place, then upsert
	// again to persist the enrichment.
	if err := s.store.UpsertItem(ctx, item); err != nil {
		return fmt.Errorf("upserting movie %q: %w", folder, err)
	}

	s.seedFolderImages(ctx, itemID, hostFolder, videoFile)
	s.enrich(ctx, pool, &item, metadata.Query{Kind: metadata.KindMovie, Title: title, Year: year}, false)
	s.applyMovieNFO(pool, &item, hostFolder, videoFile)
	if err := s.store.UpsertItem(ctx, item); err != nil {
		return fmt.Errorf("upserting enriched movie %q: %w", folder, err)
	}

	if probeErr != nil {
		pool.addWarning(fmt.Sprintf("probing %q: %v (cataloged with no media stream info)", folder, probeErr))
	}
	return nil
}

// walkSeries handles a CollectionTypeTVShows path: Series/Season NN/
// episode-file, the Kodi/Jellyfin TV convention (nameparse.go), plus episode
// files that sit directly in the series folder.
func (s *Scanner) walkSeries(ctx context.Context, pool *jobPool, lib storage.Library, root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		log.Printf("scan: reading tvshows root %s: %v", root, err)
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		seriesFolder := e.Name()
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
		if err := s.store.UpsertItem(ctx, seriesItem); err != nil {
			log.Printf("scan: upserting series %q: %v", seriesFolder, err)
			continue
		}
		s.seedFolderImages(ctx, seriesID, seriesPath, "")

		pool.enqueue(func() error {
			enriched := seriesItem
			s.enrich(ctx, pool, &enriched, metadata.Query{Kind: metadata.KindSeries, Title: seriesTitle, Year: seriesYear}, false)
			s.applySeriesNFO(pool, &enriched, seriesPath)
			if err := s.store.UpsertItem(ctx, enriched); err != nil {
				return fmt.Errorf("upserting enriched series %q: %w", seriesFolder, err)
			}
			return nil
		})

		seasons := s.walkSeasons(ctx, pool, lib, seriesPath, seriesID, seriesTitle)
		s.walkSeriesRootEpisodes(ctx, pool, lib, seriesPath, seriesID, seriesTitle, seasons)
	}
}

// seasonSet resolves the season an episode that is not inside a season folder
// belongs to: the season folder of the same number when there is one,
// otherwise a season without a folder (created once, on first use).
type seasonSet struct {
	s        *Scanner
	lib      storage.Library
	seriesID storage.ItemID
	dir      string
	real     map[int]storage.ItemID // season number -> season folder item
	mu       sync.Mutex
	virtual  map[string]storage.ItemID
}

// resolve returns the season item for number (nil: the season is unknown).
func (ss *seasonSet) resolve(ctx context.Context, number *int) (storage.ItemID, error) {
	if number != nil {
		if id, ok := ss.real[*number]; ok {
			return id, nil
		}
	}
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
	if err := ss.s.store.UpsertItem(ctx, item); err != nil {
		return "", fmt.Errorf("upserting season %q: %w", item.Name, err)
	}
	ss.virtual[key] = id
	return id, nil
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
	entries, err := os.ReadDir(seriesPath)
	if err != nil {
		log.Printf("scan: reading series folder %s: %v", seriesPath, err)
		return seasons
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		seasonNum, ok := ParseSeasonFolder(e.Name())
		if !ok {
			continue
		}
		seasonPath := filepath.Join(seriesPath, e.Name())
		seasonID := storage.ItemID(deterministicID("item-season-" + string(lib.ID) + "-" + seasonPath))
		idx := seasonNum

		seasonItem := storage.Item{
			ID: seasonID, Type: storage.ItemTypeSeason, LibraryID: lib.ID,
			ParentID: &seriesID, SeriesID: &seriesID,
			Name: seasonName(&idx), SortName: fmt.Sprintf("%04d", seasonNum),
			IndexNumber: &idx, DateCreated: fileDateAdded(seasonPath),
			Path: seasonPath,
		}
		if err := s.store.UpsertItem(ctx, seasonItem); err != nil {
			log.Printf("scan: upserting season %q: %v", e.Name(), err)
			continue
		}
		if _, dup := seasons.real[seasonNum]; !dup {
			seasons.real[seasonNum] = seasonID
		}
		s.seedSeasonImage(ctx, seasonID, seriesPath, seasonNum)

		pool.enqueue(func() error {
			enriched := seasonItem
			s.enrich(ctx, pool, &enriched, metadata.Query{Kind: metadata.KindSeason, SeriesTitle: seriesTitle, SeasonNumber: seasonNum}, false)
			s.applySeasonNFO(pool, &enriched, seasonPath, seriesPath, seasonNum)
			if err := s.store.UpsertItem(ctx, enriched); err != nil {
				return fmt.Errorf("upserting enriched season %q: %w", e.Name(), err)
			}
			return nil
		})

		// walkEpisodes only needs seasonID (already set above), not the
		// season's own enrichment, so it doesn't wait on the enqueue above
		// — same "children don't block on their parent's enrich" ordering
		// walkSeries already establishes for the series/season relationship.
		s.walkEpisodes(ctx, pool, lib, seasonPath, seriesID, seasonID, seriesTitle, seasonNum, seasons)
	}
	return seasons
}

// episodeJob is one episode file to catalog.
type episodeJob struct {
	path, name   string
	size         int64
	seriesID     storage.ItemID
	seriesTitle  string
	folderSeason *int            // the season folder's number; nil for a file in the series folder
	folderID     *storage.ItemID // the season folder's item; nil for a file in the series folder
	seasons      *seasonSet
}

func (s *Scanner) walkEpisodes(ctx context.Context, pool *jobPool, lib storage.Library, seasonPath string, seriesID, seasonID storage.ItemID, seriesTitle string, seasonNum int, seasons *seasonSet) {
	s.walkEpisodeFiles(ctx, pool, lib, seasonPath, episodeJob{
		seriesID: seriesID, seriesTitle: seriesTitle, folderSeason: &seasonNum, folderID: &seasonID, seasons: seasons,
	})
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
	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Printf("scan: reading season folder %s: %v", dir, err)
		return
	}
	for _, e := range entries {
		if e.IsDir() || !IsVideoFile(e.Name()) {
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
		pool.enqueue(func() error { return s.processEpisode(ctx, pool, lib, job) })
	}
}

func (s *Scanner) processEpisode(ctx context.Context, pool *jobPool, lib storage.Library, job episodeJob) error {
	episodePath := job.path
	itemID := s.reconcileItemID(ctx, lib.ID, episodePath, storage.ItemID(deterministicID("item-episode-"+string(lib.ID)+"-"+episodePath)))

	// Numbers: the file name first, then the .nfo over it. An episode-only
	// pattern in a season folder belongs to that folder's season.
	season, episode := ParseEpisodeNumbers(filepath.Base(episodePath))
	if season == nil && episode != nil && job.folderSeason != nil {
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
	}

	// The season an episode hangs under: its folder's, else the season of
	// the same number, else the one without a number.
	var seasonID storage.ItemID
	if job.folderID != nil {
		seasonID = *job.folderID
	} else {
		id, err := job.seasons.resolve(ctx, season)
		if err != nil {
			return err
		}
		seasonID = id
	}
	seriesID := job.seriesID

	info, probeErr := s.probe(ctx, episodePath)
	item := storage.Item{
		ID: itemID, Type: storage.ItemTypeEpisode, LibraryID: lib.ID,
		ParentID: &seasonID, SeriesID: &seriesID, SeasonID: &seasonID,
		Name: job.name, SortName: job.name,
		IndexNumber: episode, ParentIndexNumber: season,
		Path: episodePath, Container: videoExt(episodePath), Size: job.size,
		MediaStreams: mediaStreamsFromProbe(info),
		RunTimeTicks: runtimeTicksFromProbe(info),
		DateCreated:  fileDateAdded(episodePath),
	}

	// Same ordering requirement as processMovie: upsert first so
	// enrich's image storage has a row to point its foreign key at.
	if err := s.store.UpsertItem(ctx, item); err != nil {
		return fmt.Errorf("upserting episode %q: %w", episodePath, err)
	}
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
	if err := s.store.UpsertItem(ctx, item); err != nil {
		return fmt.Errorf("upserting enriched episode %q: %w", episodePath, err)
	}
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
		return s.store.UpsertItem(ctx, item)
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
	return s.store.UpsertItem(ctx, item)
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
		s.seedFolderImages(ctx, item.ID, filepath.Dir(item.Path), filepath.Base(item.Path))
	case storage.ItemTypeSeries:
		if seriesPath, ok := s.seriesFolderOf(ctx, *item); ok {
			s.seedFolderImages(ctx, item.ID, seriesPath, "")
		}
	case storage.ItemTypeSeason:
		seasonPath, ok := s.seasonFolderOf(ctx, *item)
		if !ok {
			return
		}
		seasonNum := 0
		if item.IndexNumber != nil {
			seasonNum = *item.IndexNumber
		}
		s.seedSeasonImage(ctx, item.ID, filepath.Dir(seasonPath), seasonNum)
	}
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

// seedFolderImages picks up a movie/series folder's own local artwork, if
// present — the Kodi/Jellyfin filename conventions documented at
// kodi.wiki's "Artwork" guide (Primary/Backdrop) and Jellyfin's own
// published movie-library naming guide, jellyfin.org (Logo/Banner —
// docs/12's "Movie detail page — local Logo/clearlogo artwork
// recognition"): poster.jpg, poster.png, folder.jpg, folder.png, and
// "<video-basename>-poster.jpg" for the Primary image; fanart.jpg,
// fanart.png, backdrop.jpg, and "<video-basename>-fanart.jpg" for
// Backdrop; logo.png, logo.jpg, clearlogo.png, clearlogo.jpg, and their
// "<video-basename>-logo.*" suffix forms for Logo; banner.png, banner.jpg,
// and their "<video-basename>-banner.*" suffix forms for Banner. Each list
// is tried in that priority order and the scan stops at the first match,
// per file's own kind. This is a fallback that runs before provider
// enrichment so a locally-provided image always exists even with no
// Provider configured; storeProviderImage overwrites it (same
// deterministic ImageRef ID) if the provider later returns a match.
// videoFile is the item's video filename (with extension) so the
// "<video-basename>-*" candidates can be built; pass "" when there's no
// single associated video file (e.g. a series root folder) to skip those
// candidates.
func (s *Scanner) seedFolderImages(ctx context.Context, itemID storage.ItemID, folder, videoFile string) {
	primary := []string{"poster.jpg", "poster.png", "folder.jpg", "folder.png"}
	backdrop := []string{"fanart.jpg", "fanart.png", "backdrop.jpg"}
	logo := []string{"logo.png", "logo.jpg", "clearlogo.png", "clearlogo.jpg"}
	banner := []string{"banner.png", "banner.jpg"}
	if videoFile != "" {
		base := strings.TrimSuffix(videoFile, filepath.Ext(videoFile))
		primary = append(primary, base+"-poster.jpg")
		backdrop = append(backdrop, base+"-fanart.jpg")
		logo = append(logo, base+"-logo.png", base+"-logo.jpg")
		banner = append(banner, base+"-banner.png", base+"-banner.jpg")
	}
	s.seedFolderImage(ctx, itemID, folder, "Primary", primary)
	s.seedFolderImage(ctx, itemID, folder, "Backdrop", backdrop)
	s.seedFolderImage(ctx, itemID, folder, "Logo", logo)
	s.seedFolderImage(ctx, itemID, folder, "Banner", banner)
}

// seedFolderImage stores the first of candidates (filenames relative to
// folder) that actually exists as itemID's image of the given kind.
func (s *Scanner) seedFolderImage(ctx context.Context, itemID storage.ItemID, folder, kind string, candidates []string) {
	for _, filename := range candidates {
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

// seedSeasonImage picks up a season's local Primary artwork — a distinct
// Kodi filename convention from seedFolderImages above (kodi.wiki's
// "Artwork" guide, "Season" section): unlike a movie/series poster, a
// season poster is not stored inside the season's own subfolder — it lives
// directly in the *series root* folder (the same folder that already holds
// poster.jpg/fanart.jpg/tvshow.nfo for the series itself), named
// season{NN}-poster.jpg where NN is the season number zero-padded to two
// digits (season01-poster.jpg, season02-poster.jpg, ...). Kodi also accepts
// season{NN}.jpg/seasonNN.tbn variants; this only covers the -poster.jpg/
// .jpg forms (plus their .png equivalents), matching what's actually seen
// on real libraries. Same "first match wins, local runs before provider
// enrichment" fallback as seedFolderImage/seedFolderImages: a locally
// provided season poster always exists even with no Provider configured,
// and storeProviderImage overwrites it later (same deterministic ImageRef
// ID) if the provider returns a Season image.
func (s *Scanner) seedSeasonImage(ctx context.Context, seasonID storage.ItemID, seriesPath string, seasonNum int) {
	candidates := []string{
		fmt.Sprintf("season%02d-poster.jpg", seasonNum),
		fmt.Sprintf("season%02d.jpg", seasonNum),
		fmt.Sprintf("season%02d-poster.png", seasonNum),
		fmt.Sprintf("season%02d.png", seasonNum),
	}
	s.seedFolderImage(ctx, seasonID, seriesPath, "Primary", candidates)
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
