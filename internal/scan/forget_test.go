package scan

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/metadata"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

func libItems(t *testing.T, st storage.Store, lib storage.Library) []string {
	t.Helper()
	page, err := st.QueryItems(context.Background(), storage.ItemFilter{LibraryID: &lib.ID, Recursive: true, IncludeExtras: true, Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, it := range page.Items {
		n := string(it.Type) + ":" + filepath.Base(it.Path)
		if it.Path == "" {
			n = string(it.Type) + ":" + it.Name
		}
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func rescan(t *testing.T, s *Scanner, lib storage.Library) Stats {
	t.Helper()
	st, err := s.ScanLibrary(context.Background(), lib)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// An item whose file or folder is gone is forgotten by the next scan, with
// everything below it and what pointed at it; the user's play state stays.
func TestForgetVanishedMovies(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"A One (2001)/A One (2001).mp4", "B Two (2002)/B Two (2002).mp4", "Loose/L1.mp4", "Loose/L2.mp4"} {
		writeFile(t, filepath.Join(root, filepath.FromSlash(rel)), "x")
	}
	st := newTestStore(t)
	s := New(st, metadata.NoopProvider{}, Config{})
	lib := storage.Library{ID: "lib-1", Name: "Lib", CollectionType: storage.CollectionTypeMovies, Paths: []string{root}}
	if err := st.UpsertLibrary(context.Background(), lib); err != nil {
		t.Fatal(err)
	}
	rescan(t, s, lib)
	before := strings.Join(libItems(t, st, lib), ",")
	for _, want := range []string{"Folder:Loose", "Movie:L1.mp4", "Movie:A One (2001).mp4"} {
		if !strings.Contains(before, want) {
			t.Fatalf("first scan missing %s: %s", want, before)
		}
	}
	// Mark A One as played and favorite, then remove it, and one of Loose's files.
	page, _ := st.QueryItems(context.Background(), storage.ItemFilter{LibraryID: &lib.ID, Path: filepath.Join(root, "A One (2001)", "A One (2001).mp4"), Limit: 1})
	aID := page.Items[0].ID
	if err := st.SetPlaystate(context.Background(), "u1", aID, storage.Playstate{Played: true, IsFavorite: true}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "A One (2001)")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "Loose", "L1.mp4")); err != nil {
		t.Fatal(err)
	}
	stats := rescan(t, s, lib)
	got := strings.Join(libItems(t, st, lib), ",")
	if strings.Contains(got, "A One") || strings.Contains(got, "L1.mp4") || strings.Contains(got, "Folder:Loose") {
		t.Errorf("vanished items still cataloged: %s", got)
	}
	// Loose now holds one movie: it is that movie, and the old Folder is gone.
	if !strings.Contains(got, "Movie:L2.mp4") || !strings.Contains(got, "Movie:B Two (2002).mp4") {
		t.Errorf("surviving items missing: %s", got)
	}
	if stats.Removed < 3 {
		t.Errorf("Removed = %d, want at least 3", stats.Removed)
	}
	// The play state comes back with the item.
	writeFile(t, filepath.Join(root, "A One (2001)", "A One (2001).mp4"), "x")
	rescan(t, s, lib)
	ps, err := st.GetPlaystate(context.Background(), "u1", aID)
	if err != nil || !ps.Played || !ps.IsFavorite {
		t.Errorf("play state not kept across removal: %+v err=%v", ps, err)
	}
}

// A root that is gone, or empty, never empties the library.
func TestForgetKeepsInaccessibleRoot(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "A One (2001)", "A One (2001).mp4"), "x")
	st := newTestStore(t)
	s := New(st, metadata.NoopProvider{}, Config{})
	lib := storage.Library{ID: "lib-1", Name: "Lib", CollectionType: storage.CollectionTypeMovies, Paths: []string{root}}
	if err := st.UpsertLibrary(context.Background(), lib); err != nil {
		t.Fatal(err)
	}
	rescan(t, s, lib)
	want := strings.Join(libItems(t, st, lib), ",")
	moved := root + ".off"
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	rescan(t, s, lib) // inaccessible
	if got := strings.Join(libItems(t, st, lib), ","); got != want {
		t.Errorf("inaccessible root: %s, want %s", got, want)
	}
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	rescan(t, s, lib) // empty
	if got := strings.Join(libItems(t, st, lib), ","); got != want {
		t.Errorf("empty root: %s, want %s", got, want)
	}
	// Something in it, but nothing cataloged: everything goes.
	writeFile(t, filepath.Join(root, "notes.txt"), "x")
	rescan(t, s, lib)
	if got := libItems(t, st, lib); len(got) != 1 || !strings.HasPrefix(got[0], "Folder:") {
		t.Errorf("root with only a text file: %v", got)
	}
}

// Empty and pattern .ignore files, hidden and fixed-name folders.
func TestIgnoreFiles(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		"Plain (2001)/Plain (2001).mp4",
		"Gone (2002)/Gone (2002).mp4",
		"NoMedia (2003)/NoMedia (2003).mp4",
		"Pat/Keep One (2004).mp4", "Pat/Skip Sample (2005).mp4", "Pat/KEEP Neg (2007).mp4", "Pat/skip two (2006).mp4",
		"Pat/sub1/Sub One (2008).mp4", "Pat/sub2/Sub Two (2009).mp4",
		"Anch/a/deep-skip.mp4", "Anch/a/deep-keep.mp4", "Anch/top-skip.mp4", "Anch/top-keep.mp4", "Anch/a/b/under-skip.mp4", "Anch/a/b/under-keep.mp4",
		"Own/x/one-skip.mp4", "Own/x/one-e2.mp4", "Own/x/one-e3.mp4", "Own/one-e2.mp4", "Own/one-keep.mp4",
		"@eaDir/F1 (2011).mp4", "#recycle/F2 (2011).mp4", "Subs/F3 (2011).mp4", "System Volume Information/F4 (2011).mp4",
		"$RECYCLE.BIN/F5 (2011).mp4", ".hidden/F6 (2011).mp4", "lost+found/F7 (2011).mp4", "METADATA/F8 (2011).mp4",
		"Shown/Sub/F9 (2011).mp4", "Blank/Blank (2012).mp4", "Comment/Comment (2013).mp4",
	} {
		writeFile(t, filepath.Join(root, filepath.FromSlash(rel)), "x")
	}
	writeFile(t, filepath.Join(root, "Gone (2002)", ".ignore"), "")
	writeFile(t, filepath.Join(root, "NoMedia (2003)", ".nomedia"), "")
	writeFile(t, filepath.Join(root, "Blank", ".ignore"), "  \n\r\n")
	writeFile(t, filepath.Join(root, "Comment", ".ignore"), "\n# only a comment\n")
	writeFile(t, filepath.Join(root, "Pat", ".ignore"), "# note\r\n*sample*\r\nSKIP TWO*\r\nsub1/\r\n")
	writeFile(t, filepath.Join(root, "Anch", ".ignore"), "*-skip*\nx/*.mp4\n/top-keep.mp4\n")
	writeFile(t, filepath.Join(root, "Own", ".ignore"), "*-e2*\n")
	writeFile(t, filepath.Join(root, "Own", "x", ".ignore"), "*-e3*\n")

	got, _, _ := scanAll(t, root, storage.CollectionTypeMovies)
	have := func(file string) bool {
		for k := range got {
			if k == file {
				return true
			}
		}
		return false
	}
	for _, f := range []string{
		"Plain (2001).mp4", "NoMedia (2003).mp4", "Keep One (2004).mp4", "KEEP Neg (2007).mp4", "Sub Two (2009).mp4",
		"top-keep.mp4", "deep-keep.mp4", "under-keep.mp4", "one-keep.mp4", "one-skip.mp4", "F9 (2011).mp4", "Blank (2012).mp4", "Comment (2013).mp4",
	} {
		// "Blank" has an all-whitespace .ignore: excluded, see below.
		if f == "Blank (2012).mp4" {
			continue
		}
		if !have(f) {
			t.Errorf("%s should be cataloged", f)
		}
	}
	for _, f := range []string{
		"Gone (2002).mp4", "Skip Sample (2005).mp4", "skip two (2006).mp4", "Sub One (2008).mp4", "Blank (2012).mp4",
		"deep-skip.mp4", "top-skip.mp4", "under-skip.mp4", "one-e3.mp4",
		"F1 (2011).mp4", "F2 (2011).mp4", "F3 (2011).mp4", "F4 (2011).mp4", "F5 (2011).mp4", "F6 (2011).mp4", "F7 (2011).mp4", "F8 (2011).mp4",
	} {
		if have(f) {
			t.Errorf("%s should be ignored", f)
		}
	}
	// Own/one-e2.mp4 is ignored by Own/.ignore; Own/x has its own file, so
	// only *-e3* applies there: one-e2 stays, one-skip stays, one-e3 goes.
	// (the file names collide on base name above; check by full path instead)
	checkPath := func(rel string, want bool) {
		t.Helper()
		found := false
		for _, it := range got {
			if strings.HasSuffix(it.Path, filepath.FromSlash(rel)) {
				found = true
			}
		}
		if found != want {
			t.Errorf("%s cataloged = %v, want %v", rel, found, want)
		}
	}
	checkPath("Own/one-e2.mp4", false)
	checkPath("Own/x/one-e2.mp4", true)
	checkPath("Own/x/one-e3.mp4", false)
}

// Refreshing a series (or a folder) re-walks only it: new files appear, gone
// ones and emptied seasons are forgotten, the rest of the library is not
// looked at.
func TestRescanFolder(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		"One (2010)/Season 01/One S01E01.mp4", "One (2010)/Season 01/One S01E02.mp4", "One (2010)/Season 02/One S02E01.mp4",
		"Two (2011)/Season 01/Two S01E01.mp4",
	} {
		writeFile(t, filepath.Join(root, filepath.FromSlash(rel)), "x")
	}
	st := newTestStore(t)
	s := New(st, metadata.NoopProvider{}, Config{})
	lib := storage.Library{ID: "lib-1", Name: "Lib", CollectionType: storage.CollectionTypeTVShows, Paths: []string{root}}
	if err := st.UpsertLibrary(context.Background(), lib); err != nil {
		t.Fatal(err)
	}
	rescan(t, s, lib)
	page, _ := st.QueryItems(context.Background(), storage.ItemFilter{LibraryID: &lib.ID, ItemTypes: []storage.ItemType{storage.ItemTypeSeries}, Limit: 10})
	var one storage.ItemID
	for _, it := range page.Items {
		if it.Name == "One" {
			one = it.ID
		}
	}
	os.Remove(filepath.Join(root, "One (2010)", "Season 01", "One S01E01.mp4"))
	os.RemoveAll(filepath.Join(root, "One (2010)", "Season 02"))
	os.Remove(filepath.Join(root, "Two (2011)", "Season 01", "Two S01E01.mp4"))
	writeFile(t, filepath.Join(root, "One (2010)", "Season 01", "One S01E05.mp4"), "x")
	present, err := s.RescanFolder(context.Background(), one)
	if err != nil || !present {
		t.Fatalf("RescanFolder: present=%v err=%v", present, err)
	}
	got := strings.Join(libItems(t, st, lib), ",")
	for _, gone := range []string{"One S01E01.mp4", "One S02E01.mp4", "Season 02"} {
		if strings.Contains(got, gone) {
			t.Errorf("%s still cataloged: %s", gone, got)
		}
	}
	if !strings.Contains(got, "One S01E05.mp4") || !strings.Contains(got, "One S01E02.mp4") {
		t.Errorf("series items missing: %s", got)
	}
	// Two was not refreshed: its vanished episode is still cataloged.
	if !strings.Contains(got, "Two S01E01.mp4") {
		t.Errorf("another series was looked at: %s", got)
	}
	// The series folder itself gone: the series goes.
	os.RemoveAll(filepath.Join(root, "One (2010)"))
	present, err = s.RescanFolder(context.Background(), one)
	if err != nil || present {
		t.Errorf("series of a vanished folder: present=%v err=%v", present, err)
	}
}
