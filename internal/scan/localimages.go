package scan

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// Local artwork: the picture files that sit beside an item. What is
// recognized, and in which order, was observed on the reference server
// (docs/contract/specs/images.md). In short:
//
//   - a picture is a file whose extension is one of imageExts, matched
//     case-insensitively; for one name the extensions are tried in that
//     order, so "poster.png" wins over "poster.jpg";
//   - every kind has an ordered list of base names and the first one that
//     exists wins (Primary: "<video>", "<video>-poster", ..., "poster",
//     "folder", "cover", "default", "movie");
//   - Backdrop is the exception: every match is kept, in list order, and the
//     numbered forms ("fanart-1", "backdrop2") are read in sequence until two
//     numbers in a row are missing;
//   - seasons read "seasonNN-<kind>" from the series folder and the plain
//     names from their own folder; episodes read "<episode>-thumb" and
//     "<episode>".

// imageExts are the picture extensions a local image may carry, in the
// order the reference prefers them for one base name.
var imageExts = []string{"png", "jpg", "jpeg", "webp", "tbn", "gif"}

// IsLocalImageFile reports whether name has an extension local artwork may
// carry.
func IsLocalImageFile(name string) bool {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
	for _, e := range imageExts {
		if e == ext {
			return true
		}
	}
	return false
}

// numbering says how a base name continues as a series: "fanart-1",
// "fanart-2" (dash) or "backdrop1", "backdrop2" (direct).
type numbering int

const (
	numNone numbering = iota
	numDash
	numDirect
)

// imageCand is one place a local image of a kind may be found.
type imageCand struct {
	dir  string // directory the file sits in
	base string // file name without the extension
	num  numbering
	// dup lists the match twice (the reference reports the "<name>-fanart"
	// backdrop of a movie that owns its folder, a series or a folder at two
	// indexes).
	dup bool
	// all takes every picture of the sub folder named base (extrafanart).
	all bool
}

// imageSlot is the list of candidates of one image kind. Single-image kinds
// take the first hit, multi kinds (Backdrop) all of them.
type imageSlot struct {
	kind  string
	multi bool
	cands []imageCand
}

// resolve returns the paths of the pictures the slot's candidates find, in
// order: at most one for a single-image kind.
func (s *Scanner) resolveSlot(ctx context.Context, slot imageSlot) []string {
	var out []string
	for _, c := range slot.cands {
		for _, p := range s.resolveCand(ctx, c) {
			out = append(out, p)
			if !slot.multi {
				return out
			}
		}
	}
	return out
}

func (s *Scanner) resolveCand(ctx context.Context, c imageCand) []string {
	idx := dirIndexOf(ctx, c.dir)
	if idx == nil {
		return nil
	}
	if c.all {
		sub, ok := idx.dirs[strings.ToLower(c.base)]
		if !ok {
			return nil
		}
		subDir := filepath.Join(c.dir, sub)
		si := dirIndexOf(ctx, subDir)
		if si == nil {
			return nil
		}
		var names []string
		for _, n := range si.files {
			if IsLocalImageFile(n) && !strings.HasPrefix(n, ".") {
				names = append(names, n)
			}
		}
		sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
		out := make([]string, 0, len(names))
		for _, n := range names {
			out = append(out, filepath.Join(subDir, n))
		}
		return out
	}
	var out []string
	if name, ok := idx.image(c.base); ok {
		out = append(out, filepath.Join(c.dir, name))
		if c.dup {
			out = append(out, filepath.Join(c.dir, name))
		}
	}
	if c.num == numNone {
		return out
	}
	misses := 0
	for n := 1; misses < 2 && n < 1000; n++ {
		base := c.base + strconv.Itoa(n)
		if c.num == numDash {
			base = c.base + "-" + strconv.Itoa(n)
		}
		if name, ok := idx.image(base); ok {
			out = append(out, filepath.Join(c.dir, name))
			misses = 0
		} else {
			misses++
		}
	}
	return out
}

// storeLocalImages records the pictures found for one kind of item as its
// image refs (index = position) and forgets refs of local pictures that are no
// longer there. A kind with no local picture is left alone: it may hold an
// image a provider downloaded.
func (s *Scanner) storeLocalImages(ctx context.Context, itemID storage.ItemID, kind string, paths []string) {
	existing, err := s.store.GetImageRefs(ctx, itemID, storage.ImageKind(kind))
	if err != nil {
		existing = nil
	}
	have := map[storage.ImageRefID]storage.ImageRef{}
	for _, r := range existing {
		have[r.ID] = r
	}
	keep := map[storage.ImageRefID]bool{}
	for i, path := range paths {
		id := storage.ImageRefID(deterministicID("image-" + string(itemID) + "-" + kind))
		if i > 0 {
			id = storage.ImageRefID(deterministicID("image-" + string(itemID) + "-" + kind + "-" + strconv.Itoa(i)))
		}
		keep[id] = true
		fi, err := os.Stat(path)
		if err != nil {
			continue
		}
		tag := imageTag(path, fi.Size(), fi.ModTime().UnixNano())
		if old, ok := have[id]; ok && old.Tag == tag && old.Path == path && old.Index == i {
			continue
		}
		width, height := imageFileDimensions(path)
		ref := storage.ImageRef{
			ID: id, ItemID: itemID, Kind: storage.ImageKind(kind), Index: i,
			Path: path, Tag: tag, BlurHash: imageFileBlurHash(path),
			Size: fi.Size(), Width: width, Height: height,
		}
		if err := s.store.UpsertImageRef(ctx, ref); err != nil {
			log.Printf("scan: storing %s image %s for %s: %v", kind, filepath.Base(path), itemID, err)
		}
	}
	if len(paths) == 0 {
		return
	}
	for _, r := range existing {
		if keep[r.ID] || s.isProviderImage(r.Path) {
			continue
		}
		if err := s.store.DeleteImageRef(ctx, r.ID); err != nil {
			log.Printf("scan: dropping stale %s image ref %s: %v", kind, r.ID, err)
		}
	}
}

// isProviderImage reports whether path is an image the scanner downloaded
// (or an administrator uploaded) into its own image directory, as opposed to
// a picture that sits beside the media.
func (s *Scanner) isProviderImage(path string) bool {
	return s.imageDir != "" && strings.HasPrefix(path, s.imageDir)
}

// imageTag is the cache-busting tag of a picture: it changes whenever the
// file at path changes (size or modification time).
func imageTag(path string, size, mtimeNS int64) string {
	sum := md5.Sum([]byte(fmt.Sprintf("%s|%d|%d", path, size, mtimeNS)))
	return hex.EncodeToString(sum[:])
}

// seedSlots resolves and stores every slot for itemID.
func (s *Scanner) seedSlots(ctx context.Context, itemID storage.ItemID, slots []imageSlot) {
	for _, slot := range slots {
		s.storeLocalImages(ctx, itemID, slot.kind, s.resolveSlot(ctx, slot))
	}
}

// ---- profiles: which names each kind of item reads -------------------------

// backdropCands is the Backdrop candidate list for prefix p ("" for none) in
// dir: "<p>-fanart" (twice), "<p>-fanart-N", "fanart", "fanart-N", the same for
// "background" and "art", the pictures of "extrafanart/", then "<p>-backdrop",
// "<p>-backdropN", "backdrop", "backdropN". withPlain is false for a movie
// that shares its folder with others: only the prefixed names then apply.
func backdropCands(dir, p string, withPlain bool) []imageCand {
	var c []imageCand
	if p != "" {
		c = append(c, imageCand{dir: dir, base: p + "-fanart", num: numDash, dup: withPlain})
	}
	if withPlain {
		c = append(c, imageCand{dir: dir, base: "fanart", num: numDash})
	}
	for _, name := range []string{"background", "art"} {
		if p != "" {
			c = append(c, imageCand{dir: dir, base: p + "-" + name, num: numDash})
		}
		if withPlain {
			c = append(c, imageCand{dir: dir, base: name, num: numDash})
		}
	}
	if withPlain {
		c = append(c, imageCand{dir: dir, base: "extrafanart", all: true})
	}
	if p != "" {
		c = append(c, imageCand{dir: dir, base: p + "-backdrop", num: numDirect})
	}
	if withPlain {
		c = append(c, imageCand{dir: dir, base: "backdrop", num: numDirect})
	}
	return c
}

// names turns base names into candidates of one directory.
func names(dir string, bases ...string) []imageCand {
	out := make([]imageCand, 0, len(bases))
	for _, b := range bases {
		out = append(out, imageCand{dir: dir, base: b})
	}
	return out
}

// prefixed returns "<p>-suffix" for every suffix and, when withPlain, the bare
// names interleaved the way the reference orders them: each prefixed name
// first, then its plain twin.
func prefixedAndPlain(dir, p string, withPlain bool, pairs ...[2]string) []imageCand {
	var c []imageCand
	for _, pr := range pairs {
		if p != "" && pr[0] != "" {
			c = append(c, imageCand{dir: dir, base: p + "-" + pr[0]})
		}
		if withPlain && pr[1] != "" {
			c = append(c, imageCand{dir: dir, base: pr[1]})
		}
	}
	return c
}

// artSlots is the profile of an item that owns a directory of artwork: a movie
// that is alone in its folder, a series, a folder. p is the name the
// "<p>-poster" family is built from, last the extra Primary name ("movie",
// "show"), withDisc whether disc art is read (movies only).
func artSlots(dir, p, last string, withPlain, withDisc, prefixedDefault bool) []imageSlot {
	var primary []imageCand
	if p != "" {
		primary = append(primary, imageCand{dir: dir, base: p})
	}
	pairs := [][2]string{{"poster", ""}, {"folder", ""}, {"cover", ""}}
	if prefixedDefault {
		pairs = append(pairs, [2]string{"default", ""})
	}
	primary = append(primary, prefixedAndPlain(dir, p, false, pairs...)...)
	if withPlain {
		primary = append(primary, names(dir, "poster", "folder", "cover", "default")...)
		if last != "" {
			primary = append(primary, names(dir, last)...)
		}
	}
	slots := []imageSlot{
		{kind: "Primary", cands: primary},
		{kind: "Backdrop", multi: true, cands: backdropCands(dir, p, withPlain)},
		{kind: "Logo", cands: prefixedAndPlain(dir, p, withPlain, [2]string{"logo", "logo"}, [2]string{"clearlogo", "clearlogo"})},
		{kind: "Banner", cands: prefixedAndPlain(dir, p, withPlain, [2]string{"banner", "banner"})},
		{kind: "Art", cands: prefixedAndPlain(dir, p, withPlain, [2]string{"clearart", "clearart"})},
		{kind: "Thumb", cands: prefixedAndPlain(dir, p, withPlain, [2]string{"landscape", "landscape"}, [2]string{"thumb", "thumb"})},
	}
	if withDisc {
		slots = append(slots, imageSlot{kind: "Disc", cands: prefixedAndPlain(dir, p, withPlain,
			[2]string{"disc", "disc"}, [2]string{"cdart", "cdart"}, [2]string{"discart", "discart"})})
	}
	return slots
}

// movieSlots: a movie file p (its name without extension) in dir.
// alone is true when it is the only movie of its folder.
func movieSlots(dir, p string, alone bool) []imageSlot {
	if alone {
		return artSlots(dir, p, "movie", true, true, false)
	}
	return artSlots(dir, p, "", false, true, false)
}

// seriesSlots: a series folder.
func seriesSlots(dir string) []imageSlot {
	return artSlots(dir, filepath.Base(dir), "show", true, false, false)
}

// folderSlots: a plain folder (or a library root): "<folder>" and
// "<folder>-<kind>" first, then the plain names.
func folderSlots(dir string) []imageSlot {
	return artSlots(dir, filepath.Base(dir), "", true, false, true)
}

// seasonSlots: a season. seasonDir is its folder ("" for a season with no
// folder), seriesDir the series folder the "seasonNN-*" pictures sit in.
// number is the season number; nil means none.
func seasonSlots(seriesDir, seasonDir string, number *int) []imageSlot {
	var rootNames func(kind string) []string
	if number == nil {
		rootNames = func(string) []string { return nil }
	} else if *number == 0 {
		rootNames = func(kind string) []string {
			return []string{"season-specials-" + kind, "specials-" + kind}
		}
	} else {
		n := *number
		rootNames = func(kind string) []string {
			return []string{fmt.Sprintf("season%d-%s", n, kind), fmt.Sprintf("season%02d-%s", n, kind)}
		}
	}
	root := func(kind string) []imageCand {
		var c []imageCand
		seen := map[string]bool{}
		for _, b := range rootNames(kind) {
			if !seen[b] {
				seen[b] = true
				c = append(c, imageCand{dir: seriesDir, base: b})
			}
		}
		return c
	}
	var dir func(bases ...string) []imageCand
	dir = func(bases ...string) []imageCand {
		if seasonDir == "" {
			return nil
		}
		return names(seasonDir, bases...)
	}
	var backdrop []imageCand
	backdrop = append(backdrop, root("fanart")...)
	if seasonDir != "" {
		backdrop = append(backdrop, backdropCands(seasonDir, "", true)...)
	}
	primary := root("poster")
	if seasonDir != "" {
		primary = append(primary, names(seasonDir, filepath.Base(seasonDir))...)
		primary = append(primary, dir("poster", "folder", "cover", "default")...)
	}
	return []imageSlot{
		{kind: "Primary", cands: primary},
		{kind: "Backdrop", multi: true, cands: backdrop},
		{kind: "Logo", cands: dir("logo", "clearlogo")},
		{kind: "Banner", cands: append(root("banner"), dir("banner")...)},
		{kind: "Art", cands: dir("clearart")},
		{kind: "Thumb", cands: append(root("landscape"), dir("landscape", "thumb")...)},
	}
}

// episodeSlots: an episode file with the given base name in dir; a
// "metadata" folder beside it is read as well.
func episodeSlots(dir, base string) []imageSlot {
	c := names(dir, base+"-thumb", base)
	c = append(c, names(filepath.Join(dir, "metadata"), base+"-thumb", base)...)
	return []imageSlot{{kind: "Primary", cands: c}}
}

// ---- seeding entry points ---------------------------------------------------

// seedMovieImages reads the artwork of a movie file in dir.
func (s *Scanner) seedMovieImages(ctx context.Context, itemID storage.ItemID, dir, videoFile string, alone bool) {
	base := strings.TrimSuffix(videoFile, filepath.Ext(videoFile))
	s.seedSlots(ctx, itemID, movieSlots(dir, base, alone))
}

// seedSeriesImages reads the artwork of a series folder.
func (s *Scanner) seedSeriesImages(ctx context.Context, itemID storage.ItemID, seriesDir string) {
	s.seedSlots(ctx, itemID, seriesSlots(seriesDir))
}

// seedFolderArtwork reads the artwork of a plain folder item.
func (s *Scanner) seedFolderArtwork(ctx context.Context, itemID storage.ItemID, dir string) {
	s.seedSlots(ctx, itemID, folderSlots(dir))
}

// seedSeasonImages reads the artwork of a season.
func (s *Scanner) seedSeasonImages(ctx context.Context, seasonID storage.ItemID, seriesDir, seasonDir string, number *int) {
	s.seedSlots(ctx, seasonID, seasonSlots(seriesDir, seasonDir, number))
}

// seedEpisodeImages reads the picture of an episode file.
func (s *Scanner) seedEpisodeImages(ctx context.Context, itemID storage.ItemID, episodePath string) {
	base := strings.TrimSuffix(filepath.Base(episodePath), filepath.Ext(episodePath))
	s.seedSlots(ctx, itemID, episodeSlots(filepath.Dir(episodePath), base))
}
