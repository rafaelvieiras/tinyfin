package scan

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// fakeFFprobe writes a shell script that appends a line to a counter file per
// call and prints a minimal valid ffprobe JSON document.
func fakeFFprobe(t *testing.T) (script, counter string) {
	t.Helper()
	dir := t.TempDir()
	counter = filepath.Join(dir, "calls")
	script = filepath.Join(dir, "ffprobe")
	body := "#!/bin/sh\necho x >> " + counter + "\n" +
		`echo '{"streams":[{"codec_type":"video","codec_name":"h264","width":640,"height":360}],"format":{"duration":"12.5"}}'` + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script, counter
}

func probeCalls(t *testing.T, counter string) int {
	b, err := os.ReadFile(counter)
	if err != nil {
		return 0
	}
	return len(strings.Fields(string(b)))
}

func TestScanLibraryIncrementalSkipsUnchangedProbe(t *testing.T) {
	root := t.TempDir()
	video := filepath.Join(root, "Movie (2001)", "movie.mkv")
	writeFile(t, video, "0123456789")
	script, counter := fakeFFprobe(t)

	st := newTestStore(t)
	s := New(st, nil, Config{FFprobePath: script})
	lib := storage.Library{ID: "lib", Name: "M", CollectionType: storage.CollectionTypeMovies, Paths: []string{root}}
	ctx := context.Background()
	if err := st.UpsertLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}

	stats, err := s.ScanLibrary(ctx, lib)
	if err != nil || stats.ItemsProbed != 1 || stats.ItemsSkipped != 0 {
		t.Fatalf("first scan: stats=%+v err=%v", stats, err)
	}
	stats, err = s.ScanLibrary(ctx, lib)
	if err != nil || stats.ItemsProbed != 0 || stats.ItemsSkipped != 1 {
		t.Fatalf("second scan: stats=%+v err=%v", stats, err)
	}
	if n := probeCalls(t, counter); n != 1 {
		t.Fatalf("ffprobe calls = %d, want 1", n)
	}
	page, _ := st.QueryItems(ctx, storage.ItemFilter{LibraryID: &lib.ID, ItemTypes: []storage.ItemType{storage.ItemTypeMovie}, Limit: 5})
	if len(page.Items) != 1 || len(page.Items[0].MediaStreams) != 1 || page.Items[0].RunTimeTicks == nil {
		t.Fatalf("reused item lost media info: %+v", page.Items)
	}

	// Changed mtime (same size) forces a re-probe.
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(video, future, future); err != nil {
		t.Fatal(err)
	}
	stats, _ = s.ScanLibrary(ctx, lib)
	if stats.ItemsProbed != 1 || stats.ItemsSkipped != 0 {
		t.Fatalf("after mtime change: stats=%+v", stats)
	}

	// ForceProbe re-probes even when unchanged.
	f := New(st, nil, Config{FFprobePath: script, ForceProbe: true})
	stats, _ = f.ScanLibrary(ctx, lib)
	if stats.ItemsProbed != 1 {
		t.Fatalf("force probe: stats=%+v", stats)
	}
}

func TestScanLibraryFailedProbeIsRetried(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Movie (2001)", "movie.mkv"), "0123456789")
	st := newTestStore(t)
	s := New(st, nil, Config{FFprobePath: "/nonexistent/ffprobe"})
	lib := storage.Library{ID: "lib", Name: "M", CollectionType: storage.CollectionTypeMovies, Paths: []string{root}}
	ctx := context.Background()
	_ = st.UpsertLibrary(ctx, lib)
	for i := 0; i < 2; i++ {
		stats, _ := s.ScanLibrary(ctx, lib)
		if stats.ItemsProbed != 1 || stats.ItemsSkipped != 0 {
			t.Fatalf("scan %d: stats=%+v", i, stats)
		}
	}
}
