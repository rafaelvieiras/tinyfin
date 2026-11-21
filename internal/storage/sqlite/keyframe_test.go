package sqlite

import (
	"context"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// The key frame record is the answer a copied picture's playlist is laid out
// from, kept between processes: what goes in must come out, including the size
// and modification time that decide whether it still describes the file.
func TestKeyFrameDataRoundTrip(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	if _, ok, err := st.QueryKeyFrames(ctx, "missing"); err != nil || ok {
		t.Fatalf("an item without a record: ok=%v err=%v", ok, err)
	}

	want := storage.KeyFrameData{
		Size:  3414032775,
		Mtime: 1763758800000000000,
		Ticks: []int64{0, 40000000, 110060000, 190000000, 250000000, 33000000 * 100},
	}
	if err := st.SaveKeyFrames(ctx, "m1", want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.QueryKeyFrames(ctx, "m1")
	if err != nil || !ok {
		t.Fatalf("record: ok=%v err=%v", ok, err)
	}
	if got.Size != want.Size || got.Mtime != want.Mtime {
		t.Fatalf("guard: got %d/%d, want %d/%d", got.Size, got.Mtime, want.Size, want.Mtime)
	}
	if len(got.Ticks) != len(want.Ticks) {
		t.Fatalf("ticks: got %v, want %v", got.Ticks, want.Ticks)
	}
	for i := range want.Ticks {
		if got.Ticks[i] != want.Ticks[i] {
			t.Fatalf("ticks: got %v, want %v", got.Ticks, want.Ticks)
		}
	}

	// saving again replaces the record instead of failing on the key
	want.Size = 1
	want.Ticks = []int64{0, 20000000}
	if err := st.SaveKeyFrames(ctx, "m1", want); err != nil {
		t.Fatal(err)
	}
	got, _, err = st.QueryKeyFrames(ctx, "m1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Size != 1 || len(got.Ticks) != 2 || got.Ticks[1] != 20000000 {
		t.Fatalf("the second save must replace the record: %+v", got)
	}
}

// A two hour film has a few thousand key frames; the stored form is deltas, so
// the row stays small.
func TestKeyFrameDataIsCompact(t *testing.T) {
	ticks := make([]int64, 0, 1200)
	for i := int64(0); i < 1200; i++ {
		ticks = append(ticks, i*100000000)
	}
	blob := encodeTicks(ticks)
	// deltas of 10 s in 100 ns ticks take five bytes each, against the eight a
	// fixed 64-bit list would take for every entry
	if len(blob) > 6<<10 {
		t.Fatalf("1200 key frames took %d bytes", len(blob))
	}
	back, err := decodeTicks(blob)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != len(ticks) || back[len(back)-1] != ticks[len(ticks)-1] {
		t.Fatalf("round trip: %d entries, last %d", len(back), back[len(back)-1])
	}
}
