package transcode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// memKeyFrameStore is a KeyFrameStore in memory, with the same contract the
// catalog implementation has: a record is keyed by item and only reused while
// the file's size and modification time still match.
type memKeyFrameStore struct {
	mu    sync.Mutex
	recs  map[string]KeyFrameRecord
	loads int
	saves int
}

func (m *memKeyFrameStore) LoadKeyFrames(_ context.Context, itemID string) (KeyFrameRecord, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loads++
	rec, ok := m.recs[itemID]
	return rec, ok, nil
}

func (m *memKeyFrameStore) SaveKeyFrames(_ context.Context, itemID string, rec KeyFrameRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saves++
	if m.recs == nil {
		m.recs = map[string]KeyFrameRecord{}
	}
	m.recs[itemID] = rec
	return nil
}

// clutter overwrites a file's bytes without changing its size or modification
// time: the only way to tell "answered from the catalog" from "answered from
// the file" when both are available.
func clutter(t *testing.T, path string) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, st.Size()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}
}

// The answer is recorded once and then comes back from the catalog: a new
// process (a new manager, no memory) with no usable ffprobe and a file whose
// bytes no longer parse still answers, which is the whole point of persisting
// it outside the container's writable layer.
func TestKeyFramesAreRecordedAndReused(t *testing.T) {
	spec := videoOnly(0, 9, 17)
	path, _ := writeFixture(t, spec)
	store := &memKeyFrameStore{}

	m := New(Config{FFprobePath: "/nonexistent", KeyFrames: store, WorkDir: t.TempDir()})
	times, err := m.KeyFrames(context.Background(), "item-1", path)
	if err != nil {
		t.Fatal(err)
	}
	assertFrames(t, times, 0, 9, 17)
	if store.saves != 1 || len(store.recs["item-1"].Ticks) != 3 {
		t.Fatalf("the read was not recorded: %d saves, %+v", store.saves, store.recs)
	}

	clutter(t, path)
	again := New(Config{FFprobePath: "/nonexistent", KeyFrames: store, WorkDir: t.TempDir()})
	times, err = again.KeyFrames(context.Background(), "item-1", path)
	if err != nil {
		t.Fatalf("the record must answer when the file cannot be read: %v", err)
	}
	assertFrames(t, times, 0, 9, 17)
	if store.loads != 2 {
		t.Fatalf("the catalog was asked %d times, want 2 (one per manager)", store.loads)
	}
}

// A record about another version of the file is stale: it must not be used, so
// the file is read again.
func TestKeyFramesIgnoreARecordOfAnotherVersionOfTheFile(t *testing.T) {
	path, _ := writeFixture(t, videoOnly(0, 9, 17))
	store := &memKeyFrameStore{}

	if _, err := New(Config{KeyFrames: store, WorkDir: t.TempDir()}).KeyFrames(context.Background(), "item-1", path); err != nil {
		t.Fatal(err)
	}
	// the item id stays, the file changes size: every read is stale from now on
	if err := os.WriteFile(path, []byte("no longer a Matroska file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Config{FFprobePath: "/nonexistent", KeyFrames: store, WorkDir: t.TempDir()}).
		KeyFrames(context.Background(), "item-1", path); err == nil {
		t.Fatal("a record of another version of the file must not answer")
	}
}

// The recorded ticks are the key frame times in the catalog's unit (100 ns),
// so a read that comes back from the catalog cuts in the same places.
func TestKeyFramesRecordedTicksAreTheSameCuttingPlan(t *testing.T) {
	path, _ := writeFixture(t, videoOnly(0, 6, 13, 25))
	store := &memKeyFrameStore{}

	direct, err := New(Config{KeyFrames: store, WorkDir: t.TempDir()}).KeyFrames(context.Background(), "item-1", path)
	if err != nil {
		t.Fatal(err)
	}
	fromStore, err := New(Config{FFprobePath: "/nonexistent", KeyFrames: store, WorkDir: t.TempDir()}).
		KeyFrames(context.Background(), "item-1", path)
	if err != nil {
		t.Fatal(err)
	}
	a, b := SegmentCuts(direct, 6), SegmentCuts(fromStore, 6)
	if fmt.Sprint(a) != fmt.Sprint(b) {
		t.Fatalf("the plan changed: %v (%v) against %v (%v)", a, direct, b, fromStore)
	}
}

// Without a store (the package's standalone use, and every test that does not
// care) the answer still comes from the container's index.
func TestKeyFramesWithoutAStoreStillReadTheIndex(t *testing.T) {
	path, _ := writeFixture(t, videoOnly(0, 5))
	times, err := New(Config{FFprobePath: "/nonexistent", WorkDir: t.TempDir()}).KeyFrames(context.Background(), "", path)
	if err != nil {
		t.Fatal(err)
	}
	assertFrames(t, times, 0, 5)
}

// A path that is not a local file is not read: the caller answers without the
// key frame plan instead of waiting.
func TestKeyFramesNeedALocalFile(t *testing.T) {
	m := New(Config{WorkDir: t.TempDir()})
	if _, err := m.KeyFrames(context.Background(), "item-1", filepath.Join(t.TempDir(), "missing.mkv")); err == nil {
		t.Fatal("a missing file must be an error")
	}
	if _, err := m.KeyFrames(context.Background(), "item-1", t.TempDir()); err == nil {
		t.Fatal("a directory must be an error")
	}
}
