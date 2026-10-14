package scan

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// TestScanExternalSubtitles covers the cataloging of subtitle files beside a
// video: which names belong to it, the order, the stream fields, and that a
// new, removed or renamed file shows up on the next scan although the video
// itself is unchanged (its probe is skipped).
func TestScanExternalSubtitles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Movie (2001)")
	video := filepath.Join(dir, "Movie (2001).mkv")
	writeFile(t, video, "0123456789")
	for _, n := range []string{
		"Movie (2001).srt", "Movie (2001).en.srt", "Movie (2001).pt-BR.forced.srt", "Movie (2001).fr.default.vtt",
		"Movie (2001).de.sdh.ass", "Movie (2001).Commentary.srt", "Movie (2001) 2.srt", "Other.srt",
		"Movie (2001).txt", "Movie (2001).en.sub",
	} {
		writeFile(t, filepath.Join(dir, n), "x")
	}
	script, counter := fakeFFprobe(t)
	st := newTestStore(t)
	s := New(st, nil, Config{FFprobePath: script})
	lib := storage.Library{ID: "lib", Name: "M", CollectionType: storage.CollectionTypeMovies, Paths: []string{root}}
	ctx := context.Background()
	if err := st.UpsertLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	subs := func() []storage.MediaStreamRef {
		t.Helper()
		page, err := st.QueryItems(ctx, storage.ItemFilter{LibraryID: &lib.ID, ItemTypes: []storage.ItemType{storage.ItemTypeMovie}, Limit: 5})
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("items: %v %+v", err, page.Items)
		}
		var out []storage.MediaStreamRef
		for _, m := range page.Items[0].MediaStreams {
			if m.Type == storage.StreamTypeSubtitle {
				out = append(out, m)
			}
		}
		return out
	}
	if _, err := s.ScanLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	got := subs()
	var names []string
	for _, m := range got {
		names = append(names, filepath.Base(m.Path))
	}
	want := []string{
		"Movie (2001).Commentary.srt", "Movie (2001).de.sdh.ass", "Movie (2001).en.srt", "Movie (2001).fr.default.vtt",
		"Movie (2001).pt-BR.forced.srt", "Movie (2001).srt",
	}
	if len(names) != len(want) {
		t.Fatalf("subtitles = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("subtitles = %v, want %v", names, want)
		}
		if got[i].Index != i || !got[i].IsExternal {
			t.Errorf("stream %d: %+v", i, got[i])
		}
	}
	if got[0].Title != "Commentary" || got[0].Language != "" {
		t.Errorf("title-only file: %+v", got[0])
	}
	if got[1].Language != "deu" || !got[1].IsHearingImpaired || got[1].Codec != "ass" {
		t.Errorf("de.sdh.ass: %+v", got[1])
	}
	if got[3].Language != "fra" || !got[3].IsDefault || got[3].Codec != "webvtt" {
		t.Errorf("fr.default.vtt: %+v", got[3])
	}
	if got[4].Language != "pt-br" || !got[4].IsForced {
		t.Errorf("pt-BR.forced.srt: %+v", got[4])
	}

	// The embedded video stream moves behind the external subtitles.
	page, _ := st.QueryItems(ctx, storage.ItemFilter{LibraryID: &lib.ID, ItemTypes: []storage.ItemType{storage.ItemTypeMovie}, Limit: 5})
	last := page.Items[0].MediaStreams[len(page.Items[0].MediaStreams)-1]
	if last.Type != storage.StreamTypeVideo || last.Index != len(want) {
		t.Errorf("video stream: %+v", last)
	}

	// New and removed files are seen without a new probe.
	writeFile(t, filepath.Join(dir, "Movie (2001).es.srt"), "x")
	if err := os.Remove(filepath.Join(dir, "Movie (2001).en.srt")); err != nil {
		t.Fatal(err)
	}
	stats, err := s.ScanLibrary(ctx, lib)
	if err != nil || stats.ItemsProbed != 0 || stats.ItemsSkipped != 1 {
		t.Fatalf("second scan: %+v %v", stats, err)
	}
	if n := probeCalls(t, counter); n != 1 {
		t.Fatalf("ffprobe calls = %d, want 1", n)
	}
	got = subs()
	if len(got) != len(want) || filepath.Base(got[2].Path) != "Movie (2001).es.srt" {
		t.Fatalf("after change: %+v", got)
	}
	page, _ = st.QueryItems(ctx, storage.ItemFilter{LibraryID: &lib.ID, ItemTypes: []storage.ItemType{storage.ItemTypeMovie}, Limit: 5})
	if v := page.Items[0].MediaStreams; len(v) == 0 || v[len(v)-1].Type != storage.StreamTypeVideo || v[len(v)-1].Index != len(want) {
		t.Errorf("video stream index after change: %+v", v)
	}

	// RefreshExternalSubtitles (upload/delete path) re-reads the folder.
	writeFile(t, filepath.Join(dir, "Movie (2001).it.srt"), "x")
	if err := s.RefreshExternalSubtitles(ctx, page.Items[0].ID); err != nil {
		t.Fatal(err)
	}
	if got = subs(); len(got) != len(want)+1 {
		t.Fatalf("after refresh: %d streams", len(got))
	}
}
