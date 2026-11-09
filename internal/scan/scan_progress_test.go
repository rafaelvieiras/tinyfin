package scan

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// progressRecorder collects the percentages a scan reports, in order.
type progressRecorder struct {
	mu   sync.Mutex
	seen []float64
}

func (r *progressRecorder) report(pct float64) {
	r.mu.Lock()
	r.seen = append(r.seen, pct)
	r.mu.Unlock()
}

func (r *progressRecorder) values() []float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]float64{}, r.seen...)
}

// TestScanLibraryWithProgressReportsRisingShare covers the number GET
// /ScheduledTasks publishes as CurrentProgressPercentage while a scan runs:
// the pass reports how much of the work it queued it has finished, never
// going backwards (a walk that discovers more work must not make the bar
// retreat) and never claiming 100 while it is still running.
func TestScanLibraryWithProgressReportsRisingShare(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 12; i++ {
		writeFile(t, filepath.Join(root, fmt.Sprintf("Movie %02d (1999)", i), "movie.mkv"), "not a real video, just enough to exist")
	}

	st := newTestStore(t)
	s := New(st, nil, Config{})
	lib := storage.Library{
		ID: "lib-progress", Name: "Movies", CollectionType: storage.CollectionTypeMovies, Paths: []string{root},
	}
	if err := st.UpsertLibrary(context.Background(), lib); err != nil {
		t.Fatalf("UpsertLibrary: %v", err)
	}

	rec := &progressRecorder{}
	stats, err := s.ScanLibraryWithProgress(context.Background(), lib, rec.report)
	if err != nil {
		t.Fatalf("ScanLibraryWithProgress: %v", err)
	}
	if stats.ItemsScanned != 12 {
		t.Fatalf("ItemsScanned = %d, want 12", stats.ItemsScanned)
	}

	seen := rec.values()
	if len(seen) == 0 {
		t.Fatal("the scan reported no progress at all")
	}
	for i, pct := range seen {
		if pct < 0 || pct > 99 {
			t.Errorf("report %d = %v, want a value in [0, 99] while the pass runs", i, pct)
		}
		if i > 0 && pct < seen[i-1] {
			t.Errorf("report %d = %v, went backwards from %v", i, pct, seen[i-1])
		}
	}
	if last := seen[len(seen)-1]; last <= 0 {
		t.Errorf("last report = %v, want a positive share once work finished", last)
	}
}

// A nil report is exactly ScanLibrary: the progress callback is optional so
// existing callers (tests, the item-level paths) keep their behavior.
func TestScanLibraryWithoutProgressCallback(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Alone (2001)", "movie.mkv"), "not a real video, just enough to exist")

	st := newTestStore(t)
	s := New(st, nil, Config{})
	lib := storage.Library{
		ID: "lib-no-progress", Name: "Movies", CollectionType: storage.CollectionTypeMovies, Paths: []string{root},
	}
	if err := st.UpsertLibrary(context.Background(), lib); err != nil {
		t.Fatalf("UpsertLibrary: %v", err)
	}
	stats, err := s.ScanLibrary(context.Background(), lib)
	if err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}
	if stats.ItemsScanned != 1 {
		t.Fatalf("ItemsScanned = %d, want 1", stats.ItemsScanned)
	}
}
