// Package collection implements the collections (BoxSet) model on top of the
// storage interface: the virtual "Collections" library the boxes live in,
// creating and filling a box, the metadata a box derives from its members and
// the automatic boxes a scan builds from the movies' <set> names.
//
// A box is a catalog item of type BoxSet in its own library. Its members keep
// their own library and parent; membership is a link (storage.RelationCollection)
// and a box is a set, so a member is in at most once.
package collection

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/guid"
	"github.com/rafaelvieiras/tinyfin/internal/rating"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// LibraryName is the display name of the library that holds every box.
const LibraryName = "Collections"

// folderSuffix is appended to a box's name to form the last segment of its
// path, the way the box's folder is named on disk by the reference server.
const folderSuffix = " [boxset]"

// LibraryID is the fixed id of the collections library.
func LibraryID() storage.LibraryID {
	return storage.LibraryID(guid.Derive("tinyfin-collections-library"))
}

// Dir is the directory the boxes live under: <dataDir>/collections.
func Dir(dataDir string) string { return filepath.Join(dataDir, "collections") }

// fileName is name as the last path segment: path separators and NUL become
// a space, so two names that differ only there are the same box.
func fileName(name string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == 0 {
			return ' '
		}
		return r
	}, name)
}

// BoxPath is the path recorded for the box called name.
func BoxPath(dataDir, name string) string {
	return filepath.Join(Dir(dataDir), fileName(name)+folderSuffix)
}

// BoxID is the item id of the box called name. It follows the sanitized file
// name, so creating a box twice under one name addresses the same item.
func BoxID(name string) storage.ItemID {
	return storage.ItemID(guid.Derive("tinyfin-boxset:" + fileName(name)))
}

// FolderID is the id of the Folder item that stands for the collections
// directory: the parent a collection reports.
func FolderID() storage.ItemID { return storage.ItemID(guid.Derive("tinyfin-collections-folder")) }

// EnsureLibrary makes the collections library (and its folder item) exist.
func EnsureLibrary(ctx context.Context, store storage.Store, dataDir string) (storage.Library, error) {
	if lib, err := store.GetLibrary(ctx, LibraryID()); err == nil {
		return lib, nil
	}
	dir := Dir(dataDir)
	opts, _ := json.Marshal(map[string]any{
		"Enabled": true, "SaveLocalMetadata": true, "AutomaticallyAddToCollection": false,
		"PathInfos": []map[string]string{{"Path": dir}},
	})
	lib := storage.Library{
		ID:             LibraryID(),
		Name:           LibraryName,
		CollectionType: storage.CollectionTypeBoxSets,
		Paths:          []string{dir},
		LibraryOptions: opts,
	}
	if err := store.UpsertLibrary(ctx, lib); err != nil {
		return lib, err
	}
	return lib, nil
}

// ensureFolder records the physical collections folder as a Folder item, so a
// recursive, unfiltered listing shows it beside the boxes like it shows the
// root folder of every other library.
func ensureFolder(ctx context.Context, store storage.Store, dataDir string) error {
	return store.UpsertItem(ctx, storage.Item{
		ID:          FolderID(),
		Type:        storage.ItemTypeFolder,
		LibraryID:   LibraryID(),
		Name:        "collections",
		SortName:    "collections",
		Path:        Dir(dataDir),
		DateCreated: time.Now().UTC(),
	})
}

// Create makes the box called name with exactly the given members (an
// existing box of that name has its members replaced) and returns it. Its
// year, genres and rating come from the members as in Derive. memberIDs must
// name existing items; the caller validates them.
func Create(ctx context.Context, store storage.Store, dataDir, name string, memberIDs []storage.ItemID) (storage.Item, error) {
	if _, err := EnsureLibrary(ctx, store, dataDir); err != nil {
		return storage.Item{}, err
	}
	if err := ensureFolder(ctx, store, dataDir); err != nil {
		return storage.Item{}, err
	}
	box := storage.Item{
		ID:          BoxID(name),
		Type:        storage.ItemTypeBoxSet,
		LibraryID:   LibraryID(),
		Name:        name,
		SortName:    name,
		Path:        BoxPath(dataDir, name),
		DateCreated: time.Now().UTC(),
	}
	if err := store.UpsertItem(ctx, box); err != nil {
		return box, err
	}
	if err := store.SetCollectionMembers(ctx, box.ID, memberIDs); err != nil {
		return box, err
	}
	return Refresh(ctx, store, box.ID)
}

// Refresh recomputes the box's metadata from its members: the earliest
// production year, the genres of the members in order and the lowest official
// rating. It returns the stored box.
func Refresh(ctx context.Context, store storage.Store, boxID storage.ItemID) (storage.Item, error) {
	box, err := store.GetItem(ctx, boxID)
	if err != nil {
		return box, err
	}
	members, err := loadMembers(ctx, store, boxID)
	if err != nil {
		return box, err
	}
	box.ProductionYear = earliestYear(members)
	box.Genres = unionGenres(nil, members)
	box.OfficialRating = lowestRating("", members)
	return box, store.UpsertItem(ctx, box)
}

// AddMembers links the items into the box and returns how many were new. The
// box's rating becomes the lowest of its own and the new members'; its genres
// are taken from the new members only while it has none. The production year
// is not touched (it is set when the box is created or refreshed).
func AddMembers(ctx context.Context, store storage.Store, boxID storage.ItemID, memberIDs []storage.ItemID) (int, error) {
	added, err := store.AddToCollection(ctx, boxID, memberIDs)
	if err != nil || len(added) == 0 {
		return 0, err
	}
	box, err := store.GetItem(ctx, boxID)
	if err != nil {
		return len(added), err
	}
	var members []storage.Item
	for _, id := range added {
		if it, err := store.GetItem(ctx, id); err == nil {
			members = append(members, it)
		}
	}
	changed := false
	if r := lowestRating(box.OfficialRating, members); r != box.OfficialRating {
		box.OfficialRating, changed = r, true
	}
	if len(box.Genres) == 0 {
		if g := unionGenres(nil, members); len(g) > 0 {
			box.Genres, changed = g, true
		}
	}
	if box.ProductionYear == nil {
		if y := earliestYear(members); y != nil {
			box.ProductionYear, changed = y, true
		}
	}
	if changed {
		err = store.UpsertItem(ctx, box)
	}
	return len(added), err
}

func loadMembers(ctx context.Context, store storage.Store, boxID storage.ItemID) ([]storage.Item, error) {
	ids, err := store.CollectionMembers(ctx, boxID)
	if err != nil {
		return nil, err
	}
	out := make([]storage.Item, 0, len(ids))
	for _, id := range ids {
		if it, err := store.GetItem(ctx, id); err == nil {
			out = append(out, it)
		}
	}
	return out, nil
}

func earliestYear(members []storage.Item) *int {
	var best *int
	for _, m := range members {
		if m.ProductionYear != nil && (best == nil || *m.ProductionYear < *best) {
			y := *m.ProductionYear
			best = &y
		}
	}
	return best
}

func unionGenres(have []string, members []storage.Item) []string {
	seen := map[string]bool{}
	out := append([]string(nil), have...)
	for _, g := range have {
		seen[g] = true
	}
	for _, m := range members {
		for _, g := range m.Genres {
			if !seen[g] {
				seen[g] = true
				out = append(out, g)
			}
		}
	}
	return out
}

// lowestRating is the lowest official rating among current and the members'
// (members without a recognizable rating do not take part). current is
// returned when nothing is lower.
func lowestRating(current string, members []storage.Item) string {
	best := current
	bestScore, bestOK := rating.Parse(current)
	for _, m := range members {
		sc, ok := rating.Parse(m.OfficialRating)
		if !ok {
			continue
		}
		if !bestOK || sc.Value < bestScore.Value || (sc.Value == bestScore.Value && sc.Sub < bestScore.Sub) {
			best, bestScore, bestOK = m.OfficialRating, sc, true
		}
	}
	return best
}

// OptionAutomaticallyAdd reports whether the library options blob turns the
// automatic collections on.
func OptionAutomaticallyAdd(libraryOptions []byte) bool {
	if len(libraryOptions) == 0 {
		return false
	}
	var o struct {
		AutomaticallyAddToCollection bool `json:"AutomaticallyAddToCollection"`
	}
	if json.Unmarshal(libraryOptions, &o) != nil {
		return false
	}
	return o.AutomaticallyAddToCollection
}

// BuildAutomatic creates and fills the boxes of the movie libraries that have
// AutomaticallyAddToCollection on: every set name carried by at least two
// movies of those libraries becomes (or joins) the box of that name, and a
// name with a single movie creates nothing. Existing boxes only gain members;
// nothing is ever removed. It reports how many members were added.
func BuildAutomatic(ctx context.Context, store storage.Store, dataDir string) (int, error) {
	libs, err := store.QueryLibraries(ctx)
	if err != nil {
		return 0, err
	}
	var ids []storage.LibraryID
	for _, l := range libs {
		if l.CollectionType == storage.CollectionTypeMovies && OptionAutomaticallyAdd(l.LibraryOptions) {
			ids = append(ids, l.ID)
		}
	}
	if len(ids) == 0 {
		return 0, nil
	}
	groups, err := store.MoviesWithSet(ctx, ids)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, g := range groups {
		name := strings.TrimSpace(g.Name)
		if name == "" {
			continue
		}
		boxID := BoxID(name)
		if _, err := store.GetItem(ctx, boxID); err != nil {
			if len(g.Members) < 2 {
				continue
			}
			if _, err := Create(ctx, store, dataDir, name, g.Members); err != nil {
				return total, err
			}
			total += len(g.Members)
			continue
		}
		n, err := AddMembers(ctx, store, boxID, g.Members)
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

// PruneEmptyFolder removes the collections folder item once no box is left.
func PruneEmptyFolder(ctx context.Context, store storage.Store) {
	lib := LibraryID()
	page, err := store.QueryItems(ctx, storage.ItemFilter{LibraryID: &lib, ItemTypes: []storage.ItemType{storage.ItemTypeBoxSet}, Limit: 1})
	if err != nil || page.TotalCount > 0 {
		return
	}
	_ = store.DeleteItem(ctx, FolderID())
}
