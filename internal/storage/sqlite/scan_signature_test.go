package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

func TestScanSignatureRoundTrip(t *testing.T) {
	st, err := New(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if _, _, ok, err := st.GetScanSignature(ctx, "nope"); err != nil || ok {
		t.Fatalf("missing: ok=%v err=%v", ok, err)
	}
	if err := st.UpsertLibrary(ctx, storage.Library{ID: "l", Name: "L", CollectionType: storage.CollectionTypeMovies}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertItem(ctx, storage.Item{ID: "i1", Type: storage.ItemTypeMovie, LibraryID: "l", Name: "A"}); err != nil {
		t.Fatal(err)
	}
	for _, sz := range []int64{10, 20} {
		if err := st.SetScanSignature(ctx, "i1", sz, 99); err != nil {
			t.Fatal(err)
		}
	}
	sz, m, ok, err := st.GetScanSignature(ctx, "i1")
	if err != nil || !ok || sz != 20 || m != 99 {
		t.Fatalf("got %d %d %v %v", sz, m, ok, err)
	}
}
