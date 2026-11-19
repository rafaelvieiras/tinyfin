package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// personImageFixture stores one movie whose credit carries a photo URL, so the
// scan-side registration that fills person_images has run.
func personImageFixture(t *testing.T) (*Store, storage.ItemID) {
	t.Helper()
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.UpsertLibrary(ctx, storage.Library{ID: "lib", Name: "Movies", CollectionType: storage.CollectionTypeMovies}); err != nil {
		t.Fatal(err)
	}
	item := storage.Item{
		ID: "m1", Type: storage.ItemTypeMovie, LibraryID: "lib", Name: "Shot", SortName: "shot",
		People: []storage.Person{
			{Name: "Ann Actor", Type: "Actor", Role: "Lead", ImageURL: "https://images.example.com/ann.jpg"},
			{Name: "Bob Actor", Type: "Actor", Role: "Friend"},
		},
	}
	if err := st.UpsertItem(ctx, item); err != nil {
		t.Fatal(err)
	}
	return st, storage.NamedID(storage.NamedPerson, "Ann Actor")
}

func TestUpsertItemRegistersAPersonImageSource(t *testing.T) {
	st, id := personImageFixture(t)
	ctx := context.Background()

	img, ok, err := st.GetPersonImage(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("a credit with an image URL left no person_images row")
	}
	if img.Source != "https://images.example.com/ann.jpg" {
		t.Fatalf("source: got %q", img.Source)
	}
	if img.Path != "" || img.Tag != "" {
		t.Fatalf("a fresh source should carry no cached picture, got path=%q tag=%q", img.Path, img.Tag)
	}

	// The credit with no URL gets no row at all: absence is the answer the
	// DTO needs, and a row of empty strings would be one more thing to
	// distinguish.
	other := storage.NamedID(storage.NamedPerson, "Bob Actor")
	if _, ok, err := st.GetPersonImage(ctx, other); err != nil || ok {
		t.Fatalf("a credit with no URL: ok=%v err=%v, want no row", ok, err)
	}
}

func TestRescanningTheSameSourceKeepsTheCachedPicture(t *testing.T) {
	st, id := personImageFixture(t)
	ctx := context.Background()

	failed := time.Unix(1_700_000_000, 0).UTC()
	cached := storage.PersonImage{
		PersonID: id, Source: "https://images.example.com/ann.jpg",
		Path: "/data/x.jpg", Tag: "t1", BlurHash: "LKO2", Width: 100, Height: 150, Size: 4096,
		FailedAt: failed,
	}
	if err := st.UpsertPersonImage(ctx, cached); err != nil {
		t.Fatal(err)
	}
	// A second scan of the same item re-registers the same URL.
	if err := st.UpsertPersonImageSource(ctx, id, "https://images.example.com/ann.jpg"); err != nil {
		t.Fatal(err)
	}
	got, _, err := st.GetPersonImage(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != cached.Path || got.Tag != cached.Tag || got.BlurHash != cached.BlurHash ||
		got.Width != cached.Width || got.Height != cached.Height || got.Size != cached.Size {
		t.Fatalf("an unchanged source dropped the cached picture: %+v", got)
	}
	if !got.FailedAt.Equal(failed) {
		t.Fatalf("an unchanged source dropped the failure record: got %v", got.FailedAt)
	}
}

func TestChangingTheSourceDropsTheCachedPicture(t *testing.T) {
	st, id := personImageFixture(t)
	ctx := context.Background()

	if err := st.UpsertPersonImage(ctx, storage.PersonImage{
		PersonID: id, Source: "https://images.example.com/ann-old.jpg",
		Path: "/data/old.jpg", Tag: "old", BlurHash: "OLD", Width: 10, Height: 20, Size: 30,
		FailedAt: time.Unix(1_700_000_000, 0).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertPersonImageSource(ctx, id, "https://images.example.com/ann.jpg"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.GetPersonImage(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("the row disappeared")
	}
	if got.Source != "https://images.example.com/ann.jpg" {
		t.Fatalf("source: got %q", got.Source)
	}
	if got.Path != "" || got.Tag != "" || got.BlurHash != "" || got.Width != 0 || got.Height != 0 || got.Size != 0 {
		t.Fatalf("a changed source kept the old picture: %+v", got)
	}
	if !got.FailedAt.IsZero() {
		t.Fatalf("a changed source kept the old failure record: %v", got.FailedAt)
	}
}

func TestQueryPersonImagesReturnsOnlyTheAskedFor(t *testing.T) {
	st, id := personImageFixture(t)
	ctx := context.Background()
	if err := st.UpsertPersonImage(ctx, storage.PersonImage{
		PersonID: id, Source: "https://images.example.com/ann.jpg", Path: "/data/x.jpg", Tag: "t1",
	}); err != nil {
		t.Fatal(err)
	}

	got, err := st.QueryPersonImages(ctx, []storage.ItemID{id, "nobody"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("rows: got %d (%v), want 1", len(got), got)
	}
	if got[id].Tag != "t1" {
		t.Fatalf("row: got %+v", got[id])
	}
	// An empty list is an empty map, not an error and not a full scan.
	empty, err := st.QueryPersonImages(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("empty id list: got %d rows", len(empty))
	}
}

func TestPersonImageFailureRoundTripsThroughStorage(t *testing.T) {
	st, id := personImageFixture(t)
	ctx := context.Background()
	at := time.Unix(1_712_345_678, 0).UTC()
	if err := st.UpsertPersonImage(ctx, storage.PersonImage{
		PersonID: id, Source: "https://images.example.com/ann.jpg", FailedAt: at,
	}); err != nil {
		t.Fatal(err)
	}
	got, _, err := st.GetPersonImage(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	// Stored as whole seconds, so compare at that resolution.
	if !got.FailedAt.Equal(at.Truncate(time.Second)) {
		t.Fatalf("failed_at: got %v, want %v", got.FailedAt, at)
	}
	// A zero failure time reads back as zero, not as 1970.
	if err := st.UpsertPersonImage(ctx, storage.PersonImage{PersonID: id, Source: "s"}); err != nil {
		t.Fatal(err)
	}
	got, _, err = st.GetPersonImage(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !got.FailedAt.IsZero() {
		t.Fatalf("failed_at: got %v, want the zero time", got.FailedAt)
	}
}
