// The byte cache behind GET /Items/{itemId}/Images/{imageType}: image refs
// point at files in the media library (storage's ImageStore doc comment), and
// on a remote library — an rclone/WebDAV/NFS mount — every request that misses
// would otherwise pull the same poster across the network again. A client
// scrolling a library asks for dozens of posters per screen and asks again on
// every scroll back, so the same few kilobytes traverse the mount over and
// over. This file keeps one copy of each served image next to the database
// (`<data>.images/cache`), keyed by everything that identifies the bytes:
// item, kind, index, the ref's tag, and the source's size and mtime.
//
// What it is not: a resizer. Tinyfin serves the library's own file bytes
// (images.api.R1); this only decides *where* those bytes are read from.
//
// Freshness is decided by the key, never by the cached file's own mtime — a
// poster replaced on disk changes size and/or mtime, which produces a
// different key, so a stale entry is simply never looked up (and is evicted by
// the size cap instead of by an invalidation pass).
//
// The cache is bounded: once its bytes exceed Config.ImageCacheBytes, the
// least recently used files are deleted until it is back under 80% of that
// budget. "Used" is the cached file's mtime, bumped on every hit — the write
// costs microseconds on the local filesystem and saves the remote read.
package api

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// imageCacheDirName is the subdirectory of the scan image directory that holds
// served-image copies, next to the "frames" subdirectory internal/scan fills.
const imageCacheDirName = "cache"

// imageCachePruneTarget is the fraction of the budget a prune aims for, so
// pruning happens in batches instead of on every write once the cap is hit.
const imageCachePruneTarget = 0.8

type imageCache struct {
	dir     string
	maxSize int64

	mu sync.Mutex
	// size is the bytes currently accounted for in dir. Recomputed from disk
	// on the first write after start (see ensureSizeLocked) so a restart
	// inherits what the previous process cached.
	size int64
	// known is set once size has been read from disk.
	known bool
}

// newImageCache prepares the cache directory. A dir that cannot be created
// disables the cache (nil) rather than failing the server: the routes keep
// working straight from the library, only slower — the same degradation
// scan.Config.ImageDir documents.
func newImageCache(dir string, maxSize int64) *imageCache {
	if dir == "" || maxSize <= 0 {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("tinyfin: image cache disabled (%s): %v", dir, err)
		return nil
	}
	return &imageCache{dir: dir, maxSize: maxSize}
}

// imageCacheKey derives the cache key of one served image: what identifies the
// picture (item, kind, index, ref tag) plus what identifies its current bytes
// on disk (size, mtime). Any change to either produces a different key, so a
// replaced or re-tagged image is never served from a stale entry.
func imageCacheKey(itemID storage.ItemID, kind storage.ImageKind, index int, ref storage.ImageRef, info os.FileInfo) string {
	var mtime int64
	if info != nil {
		mtime = info.ModTime().UnixNano()
	}
	var size int64
	if info != nil {
		size = info.Size()
	}
	h := sha256.New()
	// Length-prefixed fields keep the concatenation unambiguous: without
	// them an item id ending in "1" and a kind starting with "2" could
	// collide with a different pair.
	for _, field := range []string{
		string(itemID),
		string(kind),
		strconv.Itoa(index),
		ref.Tag,
		strconv.FormatInt(size, 10),
		strconv.FormatInt(mtime, 10),
	} {
		fmt.Fprintf(h, "%d:%s", len(field), field)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// pathOf is where a key's bytes live: one level of sharding keeps any single
// directory from holding the whole cache. The source's extension rides along so
// the file keeps a name http.ServeContent can derive a Content-Type from.
func (c *imageCache) pathOf(key, ext string) string {
	return filepath.Join(c.dir, shardOf(key), key+ext)
}

// shardOf is the directory shard of a key. Keys minted by imageCacheKey are
// 64 hex characters, but nothing here depends on that: a caller passing
// something shorter gets its own key back rather than a slicing panic.
func shardOf(key string) string {
	if len(key) < 2 {
		return "_"
	}
	return key[:2]
}

// open returns the cached copy of key, bumping its mtime so the LRU order
// reflects the read. ok=false means "not cached (or evicted)": the caller
// falls back to the fill path.
func (c *imageCache) open(key, ext string) (*os.File, os.FileInfo, bool) {
	path := c.pathOf(key, ext)
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, false
	}
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		f.Close()
		return nil, nil, false
	}
	// Best-effort LRU bump: a read-only or otherwise odd cache directory must
	// never turn a cache hit into an error.
	now := time.Now()
	_ = os.Chtimes(path, now, now)
	return f, info, true
}

// fill copies the source file into the cache under key and returns the open
// cached copy. Callers serve from the returned file, so even a miss reads the
// source exactly once. A fill failure returns nil and the caller serves the
// source path directly — the cache is an optimization, never a requirement.
func (c *imageCache) fill(key, ext, source string) (*os.File, os.FileInfo, bool) {
	in, err := os.Open(source)
	if err != nil {
		return nil, nil, false
	}
	defer in.Close()

	path := c.pathOf(key, ext)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, nil, false
	}
	prefix := key
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), prefix+"-*.tmp")
	if err != nil {
		return nil, nil, false
	}
	tmpName := tmp.Name()
	written, err := io.Copy(tmp, in)
	closeErr := tmp.Close()
	if err != nil || closeErr != nil {
		os.Remove(tmpName)
		return nil, nil, false
	}
	// Rename is atomic within the directory: a concurrent reader sees either
	// no entry or the complete file, never a partial one.
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return nil, nil, false
	}

	c.account(written)
	c.prune()

	f, err := os.Open(path)
	if err != nil {
		return nil, nil, false
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, false
	}
	return f, info, true
}

// account adds n bytes to the running total and wakes the pruner when the
// budget is crossed.
func (c *imageCache) account(n int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.known {
		c.size = c.measureLocked()
		c.known = true
	}
	c.size += n
}

// measureLocked sums the bytes of every file in the cache directory. Called
// once per process (the first write), and by the tests.
func (c *imageCache) measureLocked() int64 {
	var total int64
	_ = filepath.WalkDir(c.dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// prune enforces the size budget, deleting least-recently-used entries until
// the cache is back under imageCachePruneTarget of it. Runs after a write, so
// the walk cost is amortized across the batch that crossed the line.
func (c *imageCache) prune() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.size <= c.maxSize {
		return
	}
	target := int64(float64(c.maxSize) * imageCachePruneTarget)
	type entry struct {
		path string
		size int64
		mod  time.Time
	}
	var entries []entry
	_ = filepath.WalkDir(c.dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(path, ".tmp") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		entries = append(entries, entry{path: path, size: info.Size(), mod: info.ModTime()})
		return nil
	})
	sort.Slice(entries, func(i, j int) bool { return entries[i].mod.Before(entries[j].mod) })
	for _, e := range entries {
		if c.size <= target {
			break
		}
		if err := os.Remove(e.path); err == nil {
			c.size -= e.size
		}
	}
	if c.size > target {
		// Entries we could not remove (permissions, concurrent writers) leave
		// the accounting honest rather than optimistic: the next write
		// re-measures.
		c.known = false
	}
}
