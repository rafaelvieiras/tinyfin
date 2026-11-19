package api

import (
	"context"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

func TestDiffLibrary(t *testing.T) {
	before := map[string]libSnap{
		"keep": {fp: "a", parent: "root"}, "gone": {fp: "b", parent: "root"}, "edit": {fp: "c", parent: "root"},
	}
	after := map[string]libSnap{
		"keep": {fp: "a", parent: "root"}, "edit": {fp: "c2", parent: "root"}, "new": {fp: "d", parent: "root"},
	}
	d := diffLibrary("lib", before, after)
	if _, ok := d.added["new"]; !ok || len(d.added) != 1 {
		t.Errorf("added = %v, want only new", d.added)
	}
	if _, ok := d.removed["gone"]; !ok || len(d.removed) != 1 {
		t.Errorf("removed = %v, want only gone", d.removed)
	}
	if _, ok := d.updated["edit"]; !ok || len(d.updated) != 1 {
		t.Errorf("updated = %v, want only edit", d.updated)
	}
	if _, ok := d.addedTo["root"]; !ok {
		t.Errorf("addedTo = %v, want root", d.addedTo)
	}
	if _, ok := d.removedFrom["root"]; !ok {
		t.Errorf("removedFrom = %v, want root", d.removedFrom)
	}
	if !diffLibrary("lib", before, before).empty() {
		t.Error("identical snapshots produced a delta")
	}
}

// TestMonitorRescanQueuesTheChange covers the monitor's path: what a
// filesystem rescan finds has to end up in the pending LibraryChanged batch.
// It did not: rescanChanged walked the library directly, so the items it
// cataloged were never pushed, and the explicit scan a client runs afterwards
// found nothing new to report — the change was lost for good.
func TestMonitorRescanQueuesTheChange(t *testing.T) {
	_, st := newTestServer(t)
	srv := startTestServer(t, st, Config{ServerName: "TestServer"})
	lib := createTestLibrary(t, st, "Mon", storage.CollectionTypeMovies)

	// The walk stands in for the scanner: it catalogs one item, which is what
	// the monitor's rescan does when it finds a new file.
	var added storage.ItemID = "monitor-item-1"
	err := srv.scanTracked(context.Background(), storage.Library{ID: lib, Name: "Mon"}, func() error {
		return st.UpsertItem(context.Background(), storage.Item{ID: added, LibraryID: lib, Name: "Found", Path: "/media/Mon/Found/movie.mkv", Type: storage.ItemTypeMovie})
	})
	if err != nil {
		t.Fatalf("scanTracked: %v", err)
	}

	p := &srv.libPush
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pending == nil {
		t.Fatal("the rescan queued nothing: no LibraryChanged would be pushed")
	}
	if _, ok := p.pending.added[string(added)]; !ok {
		t.Fatalf("pending.added = %v, want the item the walk cataloged", p.pending.added)
	}
}
