package scan

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/collection"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// forgetScope bounds what a pass may forget.
type forgetScope struct {
	// roots are the library paths the pass walked; an item outside all of
	// them is not the pass's to judge.
	roots []string
	// skipped are the library paths that were not walked (inaccessible or
	// empty): what sits under them is kept, and so is every item without a
	// path (a season with no folder may belong to anything).
	skipped []string
	// under, when set, narrows the pass to one folder: only items whose Path
	// is that folder or below it are judged, plus the path-less items of
	// seriesID.
	under    string
	seriesID *storage.ItemID
}

func within(p, dir string) bool {
	return p == dir || strings.HasPrefix(p, strings.TrimRight(dir, string(os.PathSeparator))+string(os.PathSeparator))
}

// forgetVanished deletes the items of lib that the pass just finished did not
// catalog: their file or folder is gone (or no longer qualifies). Everything
// that hangs off an item (play state, image references, collection and
// playlist membership, extras, children) goes with it, except the users'
// play state, which is kept so that the item has it back when it returns.
// It returns how many items were forgotten.
func (s *Scanner) forgetVanished(ctx context.Context, lib storage.Library, scope forgetScope) int {
	p := passOf(ctx)
	if p == nil {
		return 0
	}
	p.mu.Lock()
	failed := p.failed
	seen := p.seen
	protected := append([]string(nil), p.protected...)
	p.mu.Unlock()
	if failed {
		return 0
	}
	protected = append(protected, scope.skipped...)

	var doomed []storage.ItemID
	const page = 500
	for off := 0; ; off += page {
		res, err := s.store.QueryItems(ctx, storage.ItemFilter{
			LibraryID: &lib.ID, Recursive: true, IncludeExtras: true, Limit: page, Offset: off,
		})
		if err != nil {
			log.Printf("scan: library %q: listing items to forget: %v", lib.Name, err)
			return 0
		}
		for _, it := range res.Items {
			if _, ok := seen[it.ID]; ok {
				continue
			}
			switch it.Type {
			case storage.ItemTypeCollectionFolder, storage.ItemTypeUserView, storage.ItemTypeBoxSet, storage.ItemTypePlaylist:
				continue
			}
			if it.Path == "" {
				if len(scope.skipped) > 0 {
					continue
				}
				if scope.under != "" && (scope.seriesID == nil || it.SeriesID == nil || *it.SeriesID != *scope.seriesID) {
					continue
				}
				doomed = append(doomed, it.ID)
				continue
			}
			if scope.under != "" && !within(it.Path, scope.under) {
				continue
			}
			inRoot := false
			for _, r := range scope.roots {
				if within(it.Path, r) {
					inRoot = true
					break
				}
			}
			if !inRoot {
				continue
			}
			kept := false
			for _, d := range protected {
				if within(it.Path, d) {
					kept = true
					break
				}
			}
			if kept {
				continue
			}
			doomed = append(doomed, it.ID)
		}
		if len(res.Items) < page {
			break
		}
	}

	removed := 0
	for _, id := range doomed {
		if ctx.Err() != nil {
			break
		}
		if err := s.store.DeleteItem(ctx, id); err != nil {
			log.Printf("scan: library %q: forgetting item %s: %v", lib.Name, id, err)
			continue
		}
		removed++
		if s.imageDir != "" {
			if files, err := filepath.Glob(filepath.Join(s.imageDir, string(id)+"-*")); err == nil {
				for _, f := range files {
					_ = os.Remove(f)
				}
			}
		}
	}
	if removed > 0 {
		log.Printf("scan: library %q: forgot %d item(s) whose file or folder is gone", lib.Name, removed)
	}
	return removed
}

// RescanFolder re-walks the folder that itemID stands for (a folder of a
// movies or home-videos library, a series, or a season, which stands for its
// series): files that appeared are cataloged and items whose file or folder
// is gone are forgotten, as a library scan would, without touching the rest
// of the library. It reports whether the item is still in the catalog; an
// item that is not a folder is left alone (and reported as present).
func (s *Scanner) RescanFolder(ctx context.Context, itemID storage.ItemID) (bool, error) {
	item, err := s.store.GetItem(ctx, itemID)
	if err != nil {
		return false, err
	}
	seriesID := (*storage.ItemID)(nil)
	switch item.Type {
	case storage.ItemTypeSeries:
		seriesID = &item.ID
	case storage.ItemTypeSeason:
		if item.SeriesID == nil {
			return true, nil
		}
		series, err := s.store.GetItem(ctx, *item.SeriesID)
		if err != nil {
			return true, nil
		}
		item = series
		seriesID = &item.ID
	case storage.ItemTypeFolder:
	default:
		return true, nil
	}
	if item.Path == "" {
		return true, nil
	}
	lib, err := s.store.GetLibrary(ctx, item.LibraryID)
	if err != nil {
		return true, err
	}
	switch lib.CollectionType {
	case storage.CollectionTypeMovies, storage.CollectionTypeTVShows, storage.CollectionTypeHomeVideos, storage.CollectionTypeMusicVideos:
	default:
		return true, nil
	}
	if seriesID != nil && lib.CollectionType != storage.CollectionTypeTVShows {
		return true, nil
	}
	// A library root (its own Folder item) is a library scan.
	for _, r := range lib.Paths {
		if filepath.Clean(r) == filepath.Clean(item.Path) {
			if _, err := s.ScanLibrary(ctx, lib); err != nil {
				return true, err
			}
			return true, nil
		}
	}
	// The library must be reachable at all: an unmounted library is not an
	// emptied one.
	var roots []string
	for _, r := range lib.Paths {
		if within(item.Path, r) {
			if entries, err := os.ReadDir(r); err != nil || len(entries) == 0 {
				return true, nil
			}
			roots = append(roots, r)
		}
	}
	if len(roots) == 0 {
		return true, nil
	}

	ctx = withLibOptions(withPassState(withDirCache(ctx)), lib)
	pool := newJobPool(s.concurrency)
	dir := item.Path
	parentDir := filepath.Dir(dir)
	exists := false
	if fi, err := os.Stat(dir); err == nil && fi.IsDir() && !ignoredEntry(ctx, parentDir, filepath.Base(dir), true) {
		exists = true
	}
	if exists {
		switch {
		case seriesID != nil:
			s.walkSeriesFolder(ctx, pool, lib, parentDir, filepath.Base(dir))
		case lib.CollectionType == storage.CollectionTypeHomeVideos:
			noteSeen(ctx, item.ID)
			s.walkHomeVideos(ctx, pool, lib, dir, &item.ID)
		case lib.CollectionType == storage.CollectionTypeMusicVideos:
			noteSeen(ctx, item.ID)
			s.walkMusicVideos(ctx, pool, lib, dir, &item.ID)
		default:
			parent := &item.ID
			groups, subdirs := movieDirContent(ctx, dir, true)
			if len(groups) == 1 && len(subdirs) == 0 {
				// The folder now holds one movie: it is that movie (its folder
				// is no Folder item any more).
				parent = item.ParentID
			} else {
				noteSeen(ctx, item.ID)
			}
			s.walkMovieDir(ctx, pool, lib, dir, parent, false)
		}
	}
	pool.wait()
	if pool.failed > 0 || ctx.Err() != nil {
		return true, nil
	}
	n := s.forgetVanished(ctx, lib, forgetScope{roots: roots, under: dir, seriesID: seriesID})
	if n == 0 {
		return true, nil
	}
	_, err = s.store.GetItem(ctx, itemID)
	return err == nil, nil
}

// RescanPath re-walks the part of lib that top stands for: the entry called
// top directly below the library folder root (a movie's folder, a series, a
// first-level folder). New files are cataloged and what is gone from there is
// forgotten, as in a library scan, without looking at the rest of the
// library. An empty top, or a file, is a change among the library folder's
// own files: the whole library is scanned.
func (s *Scanner) RescanPath(ctx context.Context, lib storage.Library, root, top string) error {
	switch lib.CollectionType {
	case storage.CollectionTypeMovies, storage.CollectionTypeTVShows, storage.CollectionTypeHomeVideos, storage.CollectionTypeMusicVideos:
	default:
		return nil
	}
	if top == "" {
		_, err := s.ScanLibrary(WithoutCollections(ctx), lib)
		return err
	}
	if fi, err := os.Stat(filepath.Join(root, top)); err == nil && !fi.IsDir() {
		_, err := s.ScanLibrary(WithoutCollections(ctx), lib)
		return err
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) == 0 {
		return nil // the library folder is gone or empty: not a reason to forget
	}
	ctx = withOnly(withLibOptions(withPassState(withDirCache(ctx)), lib), root, top)
	pool := newJobPool(s.concurrency)
	switch lib.CollectionType {
	case storage.CollectionTypeMovies:
		s.walkMovies(ctx, pool, lib, root)
	case storage.CollectionTypeTVShows:
		s.walkSeries(ctx, pool, lib, root)
	case storage.CollectionTypeHomeVideos:
		s.walkHomeVideos(ctx, pool, lib, root, nil)
	case storage.CollectionTypeMusicVideos:
		s.walkMusicVideos(ctx, pool, lib, root, nil)
	}
	pool.wait()
	if pool.failed > 0 || ctx.Err() != nil {
		return nil
	}
	s.forgetVanished(ctx, lib, forgetScope{roots: []string{root}, under: filepath.Join(root, top)})
	if lib.CollectionType == storage.CollectionTypeMovies && s.dataDir != "" {
		if _, err := collection.BuildAutomatic(ctx, s.store, s.dataDir); err != nil {
			return err
		}
	}
	return nil
}
