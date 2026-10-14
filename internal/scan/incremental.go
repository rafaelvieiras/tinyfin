package scan

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// signatureStore is the optional storage capability behind the incremental
// scan: a per-item (size, mtime) signature recorded the last time the item's
// file was successfully probed. Stores that do not implement it (test fakes)
// simply get a full probe on every scan, i.e. the previous behavior.
type signatureStore interface {
	GetScanSignature(ctx context.Context, id storage.ItemID) (size, mtimeNS int64, ok bool, err error)
	SetScanSignature(ctx context.Context, id storage.ItemID, size, mtimeNS int64) error
}

// probeResult is what the process* functions need from the probe step.
type probeResult struct {
	streams []storage.MediaStreamRef
	ticks   *int64
	err     error
	// record is true when the signature must be (re)written after the item
	// is upserted: the file was freshly and successfully probed.
	record  bool
	size    int64
	mtimeNS int64
	// added is the file's mtime in UTC (DateCreated), from the same stat
	// that produced mtimeNS.
	added time.Time
}

// probeOrReuse returns the media info for path. When the item already exists
// in the store with the same size and mtime as recorded at its last
// successful probe, and that probe left both streams and a duration, the
// stored values are reused and ffprobe is skipped. Anything else — a changed
// file, no recorded signature, missing stream data, or a store without
// signature support — runs ffprobe.
func (s *Scanner) probeOrReuse(ctx context.Context, pool *jobPool, itemID storage.ItemID, path string, size int64) probeResult {
	res := probeResult{size: size}
	fi, statErr := os.Stat(path)
	if statErr != nil {
		res.added = time.Now().UTC()
	} else {
		res.added = fi.ModTime().UTC()
		res.mtimeNS = fi.ModTime().UnixNano()
	}

	sigs, hasSigs := s.store.(signatureStore)
	if hasSigs && statErr == nil && !s.forceProbe {
		if sSize, sMtime, ok, err := sigs.GetScanSignature(ctx, itemID); err == nil && ok && sSize == size && sMtime == res.mtimeNS {
			if old, err := s.store.GetItem(ctx, itemID); err == nil && len(old.MediaStreams) > 0 && old.RunTimeTicks != nil {
				res.streams = old.MediaStreams
				res.ticks = old.RunTimeTicks
				pool.countSkipped()
				return res
			}
		}
	}

	info, err := s.probe(ctx, path)
	pool.countProbed()
	res.err = err
	res.streams = mediaStreamsFromProbe(info)
	res.ticks = runtimeTicksFromProbe(info)
	res.record = hasSigs && err == nil && statErr == nil
	return res
}

// recordSignature persists the signature after the item row exists (the
// signature table references items.id).
func (s *Scanner) recordSignature(ctx context.Context, itemID storage.ItemID, res probeResult) {
	if !res.record {
		return
	}
	if sigs, ok := s.store.(signatureStore); ok {
		_ = sigs.SetScanSignature(ctx, itemID, res.size, res.mtimeNS)
	}
}

// dirIndex is one directory listing, indexed for case-insensitive lookups of
// the sidecar files (pictures, metadata) that may sit beside the media.
type dirIndex struct {
	// files and dirs map the lower-cased entry name to its real name (a
	// real name that is already all lower case wins a case collision).
	files map[string]string
	dirs  map[string]string
	// dirAll lists every real name of the folders that share a lower-cased
	// name (two folders differing only in case), sorted.
	dirAll map[string][]string
	// names are the real file names, in directory order.
	names []string
}

func newDirIndex(entries []os.DirEntry) *dirIndex {
	d := &dirIndex{files: map[string]string{}, dirs: map[string]string{}, dirAll: map[string][]string{}}
	put := func(m map[string]string, name string) {
		l := strings.ToLower(name)
		if old, ok := m[l]; ok && old == l {
			return
		}
		m[l] = name
	}
	for _, e := range entries {
		if e.IsDir() {
			put(d.dirs, e.Name())
			l := strings.ToLower(e.Name())
			d.dirAll[l] = append(d.dirAll[l], e.Name())
			continue
		}
		put(d.files, e.Name())
		d.names = append(d.names, e.Name())
	}
	return d
}

// image returns the real name of the picture called base (any recognized
// extension, tried in imageExts order, any case).
func (d *dirIndex) image(base string) (string, bool) {
	lb := strings.ToLower(base)
	for _, ext := range imageExts {
		if name, ok := d.files[lb+"."+ext]; ok {
			return name, true
		}
	}
	return "", false
}

// dirCache memoizes one directory listing per folder for the duration of a
// ScanLibrary pass, so deciding which sidecar files exist costs one ReadDir
// per folder instead of one Stat per candidate filename.
type dirCache struct {
	m    map[string]*dirIndex
	lock chan struct{}
}

type dirCacheKey struct{}

func withDirCache(ctx context.Context) context.Context {
	c := &dirCache{m: map[string]*dirIndex{}, lock: make(chan struct{}, 1)}
	return context.WithValue(ctx, dirCacheKey{}, c)
}

// dirIndexOf returns the indexed listing of dir (nil when it cannot be read).
// Uncached outside a ScanLibrary pass (e.g. RefreshItem).
func dirIndexOf(ctx context.Context, dir string) *dirIndex {
	c, _ := ctx.Value(dirCacheKey{}).(*dirCache)
	if c != nil {
		c.lock <- struct{}{}
		idx, ok := c.m[dir]
		<-c.lock
		if ok {
			return idx
		}
	}
	var idx *dirIndex
	if entries, err := os.ReadDir(dir); err == nil {
		idx = newDirIndex(entries)
	}
	if c != nil {
		c.lock <- struct{}{}
		c.m[dir] = idx
		<-c.lock
	}
	return idx
}

// dirNames returns the set of non-directory entry names in dir (empty when it
// cannot be read).
func dirNames(ctx context.Context, dir string) map[string]struct{} {
	out := map[string]struct{}{}
	if idx := dirIndexOf(ctx, dir); idx != nil {
		for _, n := range idx.names {
			out[n] = struct{}{}
		}
	}
	return out
}
