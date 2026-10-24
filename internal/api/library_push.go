package api

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// library_push.go: the "LibraryChanged" WebSocket message (spec:
// playstate.md is not the home of this one, see specs/websocket.md,
// ws.library.*). Library changes are batched: the first change after a quiet
// period starts a timer, every change that lands before it fires joins the
// same message, which is then sent once to every open socket (each user
// sees only what their libraries let them see).

// libraryChangeDelay is how long a batch collects changes before it is sent,
// counted from the first change in it.
var libraryChangeDelay = 30 * time.Second

// libraryChangeScopeAll marks a pending id every user may be told about.
const libraryChangeScopeAll = ""

// libDelta is one set of library changes: item id -> owning library id
// (libraryChangeScopeAll when no library owns it, like the user root).
type libDelta struct {
	added, removed, updated, addedTo, removedFrom map[string]storage.LibraryID
}

func newLibDelta() *libDelta {
	return &libDelta{
		added: map[string]storage.LibraryID{}, removed: map[string]storage.LibraryID{}, updated: map[string]storage.LibraryID{},
		addedTo: map[string]storage.LibraryID{}, removedFrom: map[string]storage.LibraryID{},
	}
}

func (d *libDelta) empty() bool {
	return len(d.added)+len(d.removed)+len(d.updated)+len(d.addedTo)+len(d.removedFrom) == 0
}

// libraryPush holds the batch being collected.
type libraryPush struct {
	mu      sync.Mutex
	pending *libDelta
}

// queueLibraryChange merges d into the pending batch and arms the timer.
func (s *Server) queueLibraryChange(d *libDelta) {
	if d == nil || d.empty() {
		return
	}
	p := &s.libPush
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pending == nil {
		p.pending = newLibDelta()
		time.AfterFunc(libraryChangeDelay, s.flushLibraryChanges)
	}
	cur := p.pending
	merge := func(dst, src map[string]storage.LibraryID) {
		for k, v := range src {
			dst[k] = v
		}
	}
	merge(cur.added, d.added)
	merge(cur.removed, d.removed)
	merge(cur.updated, d.updated)
	merge(cur.addedTo, d.addedTo)
	merge(cur.removedFrom, d.removedFrom)
	// An item added and removed within one batch was never seen; one added
	// and updated is just added.
	for id := range cur.removed {
		delete(cur.added, id)
		delete(cur.updated, id)
	}
	for id := range cur.added {
		delete(cur.updated, id)
	}
}

// libraryChangedDto is the Data of a "LibraryChanged" message.
type libraryChangedDto struct {
	FoldersAddedTo     []string `json:"FoldersAddedTo"`
	FoldersRemovedFrom []string `json:"FoldersRemovedFrom"`
	ItemsAdded         []string `json:"ItemsAdded"`
	ItemsRemoved       []string `json:"ItemsRemoved"`
	ItemsUpdated       []string `json:"ItemsUpdated"`
	CollectionFolders  []string `json:"CollectionFolders"`
	IsEmpty            bool     `json:"IsEmpty"`
}

// flushLibraryChanges sends the pending batch. Users that can see none of
// it are not told; removed ids are always passed on (they no longer exist to
// check against).
func (s *Server) flushLibraryChanges() {
	p := &s.libPush
	p.mu.Lock()
	d := p.pending
	p.pending = nil
	p.mu.Unlock()
	if d == nil {
		return
	}
	ctx := context.Background()
	conns := s.hub.snapshot()
	if len(conns) == 0 {
		return
	}
	libs, err := s.store.QueryLibraries(ctx)
	if err != nil {
		log.Printf("api: LibraryChanged: listing libraries: %v", err)
		return
	}
	type view struct {
		all  bool
		libs map[storage.LibraryID]bool
	}
	views := map[string]view{}
	for _, c := range conns {
		c.mu.Lock()
		uid, admin := c.userID, c.isAdmin
		c.mu.Unlock()
		if _, ok := views[uid]; ok {
			continue
		}
		if admin {
			views[uid] = view{all: true}
			continue
		}
		u, err := s.store.GetUser(ctx, storage.UserID(uid))
		if err != nil {
			continue
		}
		sc, err := s.itemsScopeFor(ctx, u)
		if err != nil {
			continue
		}
		views[uid] = view{libs: sc.libIDs}
	}
	payloads := map[string]*libraryChangedDto{}
	for uid, v := range views {
		visible := func(m map[string]storage.LibraryID) []string {
			out := []string{}
			for id, lib := range m {
				if v.all || lib == libraryChangeScopeAll || v.libs[lib] {
					out = append(out, id)
				}
			}
			return out
		}
		removed := []string{}
		for id := range d.removed {
			removed = append(removed, id)
		}
		dto := &libraryChangedDto{
			FoldersAddedTo: visible(d.addedTo), FoldersRemovedFrom: visible(d.removedFrom),
			ItemsAdded: visible(d.added), ItemsRemoved: removed, ItemsUpdated: visible(d.updated),
			CollectionFolders: []string{},
		}
		if len(dto.ItemsAdded)+len(dto.ItemsRemoved)+len(dto.ItemsUpdated)+len(dto.FoldersAddedTo)+len(dto.FoldersRemovedFrom) == 0 {
			continue
		}
		if len(d.added)+len(d.removed) > 0 {
			for _, lib := range libs {
				if lib.CollectionType != storage.CollectionTypePlaylists && (v.all || v.libs[lib.ID]) {
					dto.CollectionFolders = append(dto.CollectionFolders, string(lib.ID))
				}
			}
			dto.CollectionFolders = append(dto.CollectionFolders, playlistsFolderItemID)
		}
		payloads[uid] = dto
	}
	for _, c := range conns {
		c.mu.Lock()
		uid := c.userID
		c.mu.Unlock()
		if dto := payloads[uid]; dto != nil {
			c.send(ctx, "LibraryChanged", dto)
		}
	}
}

// libSnap is what a diff compares per item: a fingerprint of the fields a
// client shows, and where the item hangs (its parent, or its library root
// folder for a top-level item).
type libSnap struct {
	fp     string
	parent string
}

// snapshotLibrary reads every item of a library.
func (s *Server) snapshotLibrary(ctx context.Context, libID storage.LibraryID) map[string]libSnap {
	out := map[string]libSnap{}
	lib, err := s.store.GetLibrary(ctx, libID)
	if err != nil {
		return out
	}
	var items []storage.Item
	for off := 0; ; off += 500 {
		page, err := s.store.QueryItems(ctx, storage.ItemFilter{LibraryID: &libID, Recursive: true, Limit: 500, Offset: off})
		if err != nil {
			return out
		}
		items = append(items, page.Items...)
		if len(page.Items) < 500 {
			break
		}
	}
	roots := map[string]string{} // library path -> root folder item id
	for _, it := range items {
		if it.Type == storage.ItemTypeFolder && it.ParentID == nil {
			for _, p := range lib.Paths {
				if p == it.Path {
					roots[p] = string(it.ID)
				}
			}
		}
	}
	for _, it := range items {
		snap := libSnap{fp: fmt.Sprintf("%s|%s|%d|%v|%s|%v|%d", it.Name, it.Overview, it.Size, it.RunTimeTicks, it.OfficialRating, it.ProductionYear, len(it.MediaStreams))}
		switch {
		case it.ParentID != nil:
			snap.parent = string(*it.ParentID)
		default:
			best := ""
			for p, id := range roots {
				if it.Path != p && len(it.Path) > len(p) && it.Path[:len(p)] == p && it.Path[len(p)] == '/' && len(p) > len(best) {
					best, snap.parent = p, id
				}
			}
		}
		out[string(it.ID)] = snap
	}
	return out
}

// diffLibrary turns two snapshots of one library into a delta.
func diffLibrary(libID storage.LibraryID, before, after map[string]libSnap) *libDelta {
	d := newLibDelta()
	for id, a := range after {
		b, existed := before[id]
		switch {
		case !existed:
			d.added[id] = libID
			if a.parent != "" {
				d.addedTo[a.parent] = libID
			}
		case b.fp != a.fp:
			d.updated[id] = libID
		}
	}
	for id, b := range before {
		if _, still := after[id]; !still {
			d.removed[id] = libID
			if b.parent != "" {
				d.removedFrom[b.parent] = libID
			}
		}
	}
	return d
}

// trackLibraryChanges runs fn and queues what it changed in the library.
func (s *Server) trackLibraryChanges(ctx context.Context, libID storage.LibraryID, fn func()) {
	ctx = context.WithoutCancel(ctx)
	before := s.snapshotLibrary(ctx, libID)
	fn()
	s.queueLibraryChange(diffLibrary(libID, before, s.snapshotLibrary(ctx, libID)))
}

// queueLibraryAdded records the change a new library makes outside its items:
// its collection folder appears under the user root.
func (s *Server) queueLibraryAdded(lib storage.Library) {
	d := newLibDelta()
	d.added[string(lib.ID)] = lib.ID
	d.addedTo[userRootItemID] = libraryChangeScopeAll
	d.updated[userRootItemID] = libraryChangeScopeAll
	s.queueLibraryChange(d)
}
