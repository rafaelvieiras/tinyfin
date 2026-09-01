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
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
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
			s.walkMovies(ctx, pool, lib, root)
		case storage.CollectionTypeTVShows:
			s.walkSeries(ctx, pool, lib, root)
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

	now := time.Now()
	lib.LastScannedAt = &now
	if err := s.store.UpsertLibrary(ctx, lib); err != nil {
		return stats, fmt.Errorf("scan: persisting LastScannedAt for library %q: %w", lib.Name, err)
	}
	return stats, nil
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

func (s *Scanner) processMovie(ctx context.Context, pool *jobPool, lib storage.Library, hostFolder, folder, videoFile string, size int64) error {
	title, year := ParseTitleYear(folder)
	itemPath := filepath.Join(hostFolder, videoFile)
	itemID := storage.ItemID(deterministicID("item-movie-" + itemPath))

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
		DateCreated:  time.Now(),
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

	s.seedFolderImages(ctx, itemID, hostFolder)
	s.enrich(ctx, pool, &item, metadata.Query{Kind: metadata.KindMovie, Title: title, Year: year})
	if err := s.store.UpsertItem(ctx, item); err != nil {
		return fmt.Errorf("upserting enriched movie %q: %w", folder, err)
	}

	if probeErr != nil {
		pool.addWarning(fmt.Sprintf("probing %q: %v (cataloged with no media stream info)", folder, probeErr))
	}
	return nil
}

// walkSeries handles a CollectionTypeTVShows path: Series/Season NN/
// episode-file, the Kodi/Jellyfin TV convention (nameparse.go).
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
		seriesTitle, seriesYear := ParseTitleYear(seriesFolder)
		seriesID := storage.ItemID(deterministicID("item-series-" + seriesPath))

		seriesItem := storage.Item{
			ID: seriesID, Type: storage.ItemTypeSeries, LibraryID: lib.ID,
			Name: seriesTitle, SortName: seriesTitle, DateCreated: time.Now(),
		}
		if seriesYear > 0 {
			seriesItem.ProductionYear = &seriesYear
		}
		if err := s.store.UpsertItem(ctx, seriesItem); err != nil {
			log.Printf("scan: upserting series %q: %v", seriesFolder, err)
			continue
		}
		s.seedFolderImages(ctx, seriesID, seriesPath)

		pool.enqueue(func() error {
			enriched := seriesItem
			s.enrich(ctx, pool, &enriched, metadata.Query{Kind: metadata.KindSeries, Title: seriesTitle, Year: seriesYear})
			if err := s.store.UpsertItem(ctx, enriched); err != nil {
				return fmt.Errorf("upserting enriched series %q: %w", seriesFolder, err)
			}
			return nil
		})

		s.walkSeasons(ctx, pool, lib, seriesPath, seriesID, seriesTitle)
	}
}

func (s *Scanner) walkSeasons(ctx context.Context, pool *jobPool, lib storage.Library, seriesPath string, seriesID storage.ItemID, seriesTitle string) {
	entries, err := os.ReadDir(seriesPath)
	if err != nil {
		log.Printf("scan: reading series folder %s: %v", seriesPath, err)
		return
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
		seasonID := storage.ItemID(deterministicID("item-season-" + seasonPath))
		idx := seasonNum

		seasonItem := storage.Item{
			ID: seasonID, Type: storage.ItemTypeSeason, LibraryID: lib.ID,
			ParentID: &seriesID, SeriesID: &seriesID,
			Name: fmt.Sprintf("Season %d", seasonNum), SortName: fmt.Sprintf("%04d", seasonNum),
			IndexNumber: &idx, DateCreated: time.Now(),
		}
		if err := s.store.UpsertItem(ctx, seasonItem); err != nil {
			log.Printf("scan: upserting season %q: %v", e.Name(), err)
			continue
		}

		s.walkEpisodes(ctx, pool, lib, seasonPath, seriesID, seasonID, seriesTitle, seasonNum)
	}
}

func (s *Scanner) walkEpisodes(ctx context.Context, pool *jobPool, lib storage.Library, seasonPath string, seriesID, seasonID storage.ItemID, seriesTitle string, seasonNum int) {
	entries, err := os.ReadDir(seasonPath)
	if err != nil {
		log.Printf("scan: reading season folder %s: %v", seasonPath, err)
		return
	}
	for _, e := range entries {
		if e.IsDir() || !IsVideoFile(e.Name()) {
			continue
		}
		filename := e.Name()
		_, episodeNum, epTitle, ok := ParseEpisode(filename)
		if !ok {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		episodePath := filepath.Join(seasonPath, filename)
		size := info.Size()

		pool.enqueue(func() error {
			return s.processEpisode(ctx, pool, lib, episodePath, seriesID, seasonID, seriesTitle, seasonNum, episodeNum, epTitle, size)
		})
	}
}

func (s *Scanner) processEpisode(ctx context.Context, pool *jobPool, lib storage.Library, episodePath string, seriesID, seasonID storage.ItemID, seriesTitle string, seasonNum, episodeNum int, epTitle string, size int64) error {
	itemID := storage.ItemID(deterministicID("item-episode-" + episodePath))
	name := epTitle
	if name == "" {
		name = fmt.Sprintf("Episode %d", episodeNum)
	}
	season, episode := seasonNum, episodeNum

	info, probeErr := s.probe(ctx, episodePath)
	item := storage.Item{
		ID: itemID, Type: storage.ItemTypeEpisode, LibraryID: lib.ID,
		ParentID: &seasonID, SeriesID: &seriesID, SeasonID: &seasonID,
		Name: name, SortName: name,
		IndexNumber: &episode, ParentIndexNumber: &season,
		Path: episodePath, Container: videoExt(episodePath), Size: size,
		MediaStreams: mediaStreamsFromProbe(info),
		RunTimeTicks: runtimeTicksFromProbe(info),
		DateCreated:  time.Now(),
	}

	// Same ordering requirement as processMovie: upsert first so
	// enrich's image storage has a row to point its foreign key at.
	if err := s.store.UpsertItem(ctx, item); err != nil {
		return fmt.Errorf("upserting episode %q: %w", episodePath, err)
	}
	s.enrich(ctx, pool, &item, metadata.Query{
		Kind: metadata.KindEpisode, SeriesTitle: seriesTitle,
		SeasonNumber: seasonNum, EpisodeNumber: episodeNum,
	})
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
func (s *Scanner) enrich(ctx context.Context, pool *jobPool, item *storage.Item, q metadata.Query) {
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
	if res.ProductionYear > 0 {
		y := res.ProductionYear
		item.ProductionYear = &y
	}
	if res.PremiereDateUnix > 0 {
		t := time.Unix(res.PremiereDateUnix, 0).UTC()
		item.PremiereDate = &t
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

	for _, img := range res.Images {
		s.storeProviderImage(ctx, item.ID, img)
	}
}

// storeProviderImage downloads img and stores a local ImageRef for it.
// Failures are logged, not propagated — missing artwork is not a reason
// to fail the whole item (New's doc comment).
func (s *Scanner) storeProviderImage(ctx context.Context, itemID storage.ItemID, img metadata.Image) {
	if s.imageDir == "" {
		return
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
	ref := storage.ImageRef{
		ID:     storage.ImageRefID(deterministicID("image-" + string(itemID) + "-" + img.Kind)),
		ItemID: itemID, Kind: storage.ImageKind(img.Kind),
		Path: path, Size: int64(len(data)),
	}
	if err := s.store.UpsertImageRef(ctx, ref); err != nil {
		log.Printf("scan: storing %s image ref for %s: %v", img.Kind, itemID, err)
	}
}

// seedFolderImages picks up a movie/series folder's own poster.jpg/
// fanart.jpg, if present — the same convention devseed's throwaway
// seedImage used, kept as a fallback that runs before provider enrichment
// so a locally-provided image always exists even with no Provider
// configured; storeProviderImage overwrites it (same deterministic
// ImageRef ID) if the provider later returns a match.
func (s *Scanner) seedFolderImages(ctx context.Context, itemID storage.ItemID, folder string) {
	s.seedFolderImage(ctx, itemID, folder, "poster.jpg", "Primary")
	s.seedFolderImage(ctx, itemID, folder, "fanart.jpg", "Backdrop")
}

func (s *Scanner) seedFolderImage(ctx context.Context, itemID storage.ItemID, folder, filename, kind string) {
	path := filepath.Join(folder, filename)
	fi, err := os.Stat(path)
	if err != nil {
		return
	}
	ref := storage.ImageRef{
		ID:     storage.ImageRefID(deterministicID("image-" + string(itemID) + "-" + kind)),
		ItemID: itemID, Kind: storage.ImageKind(kind),
		Path: path, Size: fi.Size(),
	}
	if err := s.store.UpsertImageRef(ctx, ref); err != nil {
		log.Printf("scan: storing folder image %s for %s: %v", filename, itemID, err)
	}
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
