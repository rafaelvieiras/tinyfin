//go:build linux

package scan

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// A change below a library folder is reported once, as its first-level
// entry, after the quiet period; folders created later are watched too.
func TestMonitorReportsFirstLevelEntry(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "Movie A"), 0o755); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	got := map[string]int{}
	m, err := NewMonitor([]string{root}, 400*time.Millisecond, func(r, top string) {
		mu.Lock()
		got[top]++
		mu.Unlock()
	})
	if err != nil {
		t.Skipf("inotify unavailable: %v", err)
	}
	defer m.Close()

	writeFile(t, filepath.Join(root, "Movie A", "a.mp4"), "x")
	writeFile(t, filepath.Join(root, "Movie A", "b.mp4"), "x")
	if err := os.MkdirAll(filepath.Join(root, "Movie B", "Sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	writeFile(t, filepath.Join(root, "Movie B", "Sub", "deep.mp4"), "x")

	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		a, b := got["Movie A"], got["Movie B"]
		mu.Unlock()
		if a >= 1 && b >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("changes not reported: %v", got)
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(600 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if got["Movie A"] != 1 {
		t.Errorf("Movie A reported %d times, want once: %v", got["Movie A"], got)
	}
}
