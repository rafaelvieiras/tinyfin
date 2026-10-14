package scan

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// Extras: the trailers, special features, theme songs and theme videos that
// sit next to a movie, a series or a season, and the additional parts of a
// stacked movie. Which files are extras and what they are called was observed
// on the reference server (docs/contract/specs/images.md, "extras").
//
// An extra is cataloged as an item of its own (Item.OwnerID names the item it
// belongs to, Item.ExtraType what it is) that no listing, count or search
// returns; the owner reaches its extras through /Items/{id}/LocalTrailers and
// the other extras routes.

// extraFolders maps the lower-cased name of a folder next to the owner to the
// ExtraType of the videos in it. Only the plural forms count: a folder named
// "featurette" is an ordinary folder.
var extraFolders = []struct {
	name, extraType string
}{
	{"trailers", storage.ExtraTypeTrailer},
	{"extras", storage.ExtraTypeUnknown},
	{"extra", storage.ExtraTypeUnknown},
	{"featurettes", storage.ExtraTypeFeaturette},
	{"behind the scenes", storage.ExtraTypeBehindTheScenes},
	{"deleted scenes", storage.ExtraTypeDeletedScene},
	{"interviews", storage.ExtraTypeInterview},
	{"scenes", storage.ExtraTypeScene},
	{"samples", storage.ExtraTypeSample},
	{"shorts", storage.ExtraTypeShort},
	{"other", storage.ExtraTypeUnknown},
	{"clips", storage.ExtraTypeClip},
}

// extraSuffix is a file-name ending that marks a video as an extra of the
// type, and the name the extra gets when the file carries nothing else.
type extraSuffix struct {
	word      string
	extraType string
	name      string
	// anySep: "." and "_" may stand before the word as well as "-".
	anySep bool
}

var extraSuffixList = []extraSuffix{
	{"trailer", storage.ExtraTypeTrailer, "Trailer", true},
	{"featurette", storage.ExtraTypeFeaturette, "Featurette", false},
	{"behindthescenes", storage.ExtraTypeBehindTheScenes, "Behind The Scenes", false},
	{"deletedscene", storage.ExtraTypeDeletedScene, "Deleted Scene", false},
	{"deleted", storage.ExtraTypeDeletedScene, "Deleted Scene", false},
	{"interview", storage.ExtraTypeInterview, "Interview", false},
	{"scene", storage.ExtraTypeScene, "Scene", false},
	{"short", storage.ExtraTypeShort, "Short", false},
	{"sample", storage.ExtraTypeSample, "Sample", false},
	{"clip", storage.ExtraTypeClip, "Clip", false},
	{"extra", storage.ExtraTypeUnknown, "Extra", false},
	{"other", storage.ExtraTypeUnknown, "Extra", false},
}

// extraOfFile classifies a video file name (with extension) that sits beside
// its owner as an extra by its ending: "<anything>-trailer", "<anything>.trailer",
// "<anything>_trailer", "<anything>-featurette" ..., or the bare word
// "trailer". prefix is what stands before the ending ("" for the bare word).
func extraOfFile(name string) (prefix string, suffix extraSuffix, ok bool) {
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	lower := strings.ToLower(stem)
	if lower == "trailer" {
		return "", extraSuffixList[0], true
	}
	for _, sfx := range extraSuffixList {
		for _, sep := range []string{"-", ".", "_"} {
			if sep != "-" && !sfx.anySep {
				continue
			}
			if strings.HasSuffix(lower, sep+sfx.word) && len(lower) > len(sep+sfx.word) {
				return stem[:len(stem)-len(sep+sfx.word)], sfx, true
			}
		}
	}
	return "", extraSuffix{}, false
}

// IsExtraFile reports whether a video file name marks an extra of some other
// item (a trailer, a featurette, ...) rather than a video of its own, which
// keeps it out of the catalog proper whatever its owner.
func IsExtraFile(name string) bool {
	if _, _, ok := extraOfFile(name); ok {
		return true
	}
	// A sample is left out of the catalog by the bare word and by a "." or
	// "_" ending (only "-sample" is also a Sample extra).
	lower := strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
	return lower == "sample" || strings.HasSuffix(lower, ".sample") || strings.HasSuffix(lower, "_sample")
}

// audioExtensions are the file extensions of a theme song.
var audioExtensions = map[string]bool{
	".mp3": true, ".flac": true, ".m4a": true, ".ogg": true, ".wav": true, ".wma": true, ".aac": true, ".opus": true,
}

// IsAudioFile reports whether name has the extension of an audio file a theme
// song may be.
func IsAudioFile(name string) bool {
	return audioExtensions[strings.ToLower(filepath.Ext(name))]
}

var themeSongStem = regexp.MustCompile(`(?i)^theme\d*$`)

// extraSource is one file that is an extra of the owner.
type extraSource struct {
	path      string
	extraType string
	name      string
	size      int64
}

// extraTitle is the name an extra found in an extras folder (or one whose
// ending names a different owner) takes from its file name: the text before a
// trailing " trailer"/" sample" or a "-"/"."/"_" ending word, and before the
// year and release tokens, like a loose movie file (items.names.R10).
func extraTitle(stem string) string {
	lower := strings.ToLower(stem)
	for _, w := range []string{"trailer", "sample"} {
		for _, sep := range []string{" ", "-", ".", "_"} {
			if strings.HasSuffix(lower, sep+w) && len(lower) > len(sep+w) {
				stem = stem[:len(stem)-len(sep+w)]
				lower = strings.ToLower(stem)
				break
			}
		}
	}
	if prefix, _, ok := extraOfFile(stem + ".x"); ok {
		stem = prefix
	}
	title, _ := CleanTitle(stem, true)
	return title
}

// findExtras lists the extras of an owner whose files live in dir. ownerNames
// are the names ("<movie file name>", "<folder name>") a file prefix may equal
// for the file to count as the owner's own ("Movie (2010)-trailer.mp4").
func findExtras(ctx context.Context, dir string, ownerNames []string, withThemes bool) []extraSource {
	idx := dirIndexOf(ctx, dir)
	if idx == nil {
		return nil
	}
	var out []extraSource
	sortedNames := func(m map[string]struct{}) []string {
		names := make([]string, 0, len(m))
		for n := range m {
			names = append(names, n)
		}
		sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
		return names
	}
	for _, ef := range extraFolders {
		// "Trailers" and "trailers" are two folders on a case-sensitive
		// file system; both count.
		for _, sub := range idx.dirAll[ef.name] {
			subDir := filepath.Join(dir, sub)
			si := dirIndexOf(ctx, subDir)
			if si == nil {
				continue
			}
			for _, n := range sortedNames(si.names) {
				if !IsVideoFile(n) || strings.HasPrefix(n, ".") {
					continue
				}
				out = append(out, extraSource{
					path: filepath.Join(subDir, n), extraType: ef.extraType,
					name: extraTitle(strings.TrimSuffix(n, filepath.Ext(n))),
				})
			}
		}
	}
	if withThemes {
		for _, sub := range idx.dirAll["theme-music"] {
			subDir := filepath.Join(dir, sub)
			if si := dirIndexOf(ctx, subDir); si != nil {
				for _, n := range sortedNames(si.names) {
					if !IsAudioFile(n) || strings.HasPrefix(n, ".") {
						continue
					}
					out = append(out, extraSource{path: filepath.Join(subDir, n), extraType: storage.ExtraTypeThemeSong,
						name: strings.TrimSuffix(n, filepath.Ext(n))})
				}
			}
		}
		for _, sub := range idx.dirAll["backdrops"] {
			subDir := filepath.Join(dir, sub)
			if si := dirIndexOf(ctx, subDir); si != nil {
				for _, n := range sortedNames(si.names) {
					if !IsVideoFile(n) || strings.HasPrefix(n, ".") {
						continue
					}
					out = append(out, extraSource{path: filepath.Join(subDir, n), extraType: storage.ExtraTypeThemeVideo,
						name: strings.TrimSuffix(n, filepath.Ext(n))})
				}
			}
		}
	}
	// The files beside the owner: typed by their ending, numbered when several
	// carry the same default name.
	taken := map[string]int{}
	for _, n := range sortedNames(idx.names) {
		if strings.HasPrefix(n, ".") {
			continue
		}
		stem := strings.TrimSuffix(n, filepath.Ext(n))
		switch {
		case withThemes && IsAudioFile(n) && themeSongStem.MatchString(stem):
			name := numberedName(taken, "Theme Song")
			out = append(out, extraSource{path: filepath.Join(dir, n), extraType: storage.ExtraTypeThemeSong, name: name})
		case IsVideoFile(n):
			prefix, sfx, ok := extraOfFile(n)
			if !ok {
				continue
			}
			name := ""
			own := strings.TrimSpace(prefix) == ""
			for _, o := range ownerNames {
				if strings.EqualFold(strings.TrimSpace(prefix), o) {
					own = true
				}
			}
			if own {
				name = numberedName(taken, sfx.name)
			} else {
				name = extraTitle(prefix)
			}
			out = append(out, extraSource{path: filepath.Join(dir, n), extraType: sfx.extraType, name: name})
		}
	}
	return out
}

// numberedName returns base for its first use and "base 2", "base 3" for the
// following ones.
func numberedName(taken map[string]int, base string) string {
	taken[base]++
	if taken[base] == 1 {
		return base
	}
	return fmt.Sprintf("%s %d", base, taken[base])
}

// extraItemType is the item type an extra of the given ExtraType is.
func extraItemType(extraType string) storage.ItemType {
	switch extraType {
	case storage.ExtraTypeTrailer:
		return storage.ItemTypeTrailer
	case storage.ExtraTypeThemeSong:
		return storage.ItemTypeAudio
	}
	return storage.ItemTypeVideo
}

// otherFile is a file that belongs to its owner without being an extra of one
// of the named kinds: an additional part, a version, a merged season folder.
type otherFile struct {
	path      string
	name      string
	extraType string
}

// versionName is the name a version of a movie or episode takes from its file
// name: the text that follows the name the shortest file stem of the group
// shares ("Movie (2014) - 4K" gives "4K"); the file that is that stem keeps
// its whole name.
func versionName(stem, base string) string {
	if len(stem) > len(base) && strings.EqualFold(stem[:len(base)], base) {
		if rest := strings.Trim(stem[len(base):], " -._"); rest != "" {
			return rest
		}
	}
	return stem
}

// scanExtras catalogs the extras that sit beside an owner (a movie that owns
// its folder, a series, a season) and forgets those of its extras whose files
// are gone. async is true when the caller is not itself a pool job. attach is false for a movie that shares its folder with others
// (its folder's trailers and features belong to nobody); parts are the
// additional files of a stacked movie.
func (s *Scanner) scanExtras(ctx context.Context, pool *jobPool, lib storage.Library, ownerID storage.ItemID, dir string, ownerNames []string, year *int, attach, async bool, others []otherFile) {
	// From inside a pool job the extras are processed in line: enqueueing
	// from a job could wait for a worker slot that only the job itself holds.
	run := func(fn func() error) {
		if async {
			pool.enqueue(fn)
		} else if err := fn(); err != nil {
			pool.addWarning(err.Error())
		}
	}
	var srcs []extraSource
	if attach {
		srcs = findExtras(ctx, dir, ownerNames, true)
	}
	seen := map[storage.ItemID]bool{}
	for _, src := range srcs {
		src := src
		id := storage.ItemID(deterministicID("item-extra-" + string(lib.ID) + "-" + src.path))
		seen[id] = true
		run(func() error {
			return s.processExtra(ctx, pool, lib, ownerID, id, src.path, src.extraType, src.name, year)
		})
	}
	for _, o := range others {
		o := o
		id := storage.ItemID(deterministicID("item-" + strings.ToLower(o.extraType) + "-" + string(lib.ID) + "-" + o.path))
		seen[id] = true
		if o.extraType == storage.ExtraTypeMergedSeason {
			continue // stored by the season walker
		}
		run(func() error {
			return s.processExtra(ctx, pool, lib, ownerID, id, o.path, o.extraType, o.name, year)
		})
	}
	s.forgetExtras(ctx, ownerID, seen)
}

// forgetExtras deletes the extras of ownerID that are not in keep.
func (s *Scanner) forgetExtras(ctx context.Context, ownerID storage.ItemID, keep map[storage.ItemID]bool) {
	page, err := s.store.QueryItems(ctx, storage.ItemFilter{OwnerID: &ownerID, Limit: 1000})
	if err != nil {
		return
	}
	for _, old := range page.Items {
		if keep[old.ID] {
			continue
		}
		if err := s.store.DeleteItem(ctx, old.ID); err != nil {
			log.Printf("scan: forgetting extra %q: %v", old.Path, err)
		}
	}
}

// processExtra catalogs one extra file.
func (s *Scanner) processExtra(ctx context.Context, pool *jobPool, lib storage.Library, ownerID, id storage.ItemID, path, extraType, name string, year *int) error {
	size := int64(0)
	if fi, err := os.Stat(path); err == nil {
		size = fi.Size()
	}
	pr := s.probeOrReuse(ctx, pool, id, path, size)
	owner := ownerID
	item := storage.Item{
		ID: id, Type: extraItemType(extraType), LibraryID: lib.ID,
		Name: name, SortName: name, Path: path, Container: videoExt(path), Size: size,
		MediaStreams: pr.streams, RunTimeTicks: pr.ticks, DateCreated: pr.added,
		ExtraType: extraType, OwnerID: &owner, ProductionYear: year,
	}
	if err := s.store.UpsertItem(ctx, item); err != nil {
		return fmt.Errorf("upserting extra %q: %w", path, err)
	}
	s.recordSignature(ctx, id, pr)
	if pr.err != nil {
		pool.addWarning(fmt.Sprintf("probing %q: %v (cataloged with no media stream info)", path, pr.err))
	}
	return nil
}
