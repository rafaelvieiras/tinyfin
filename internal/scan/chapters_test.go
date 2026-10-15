package scan

import "testing"

func TestChaptersFromProbe(t *testing.T) {
	got := chaptersFromProbe(ffprobeOutput{Chapters: []ffprobeChapter{
		{StartTime: "4.000000", Tags: map[string]string{"TITLE": " Finale "}},
		{StartTime: "0.000000", Tags: map[string]string{"title": "Opening"}},
		{StartTime: "2.500000"},
		{StartTime: "bogus"},
	}})
	if len(got) != 3 {
		t.Fatalf("got %d chapters, want 3: %+v", len(got), got)
	}
	want := []struct {
		ticks int64
		name  string
	}{{0, "Opening"}, {25000000, ""}, {40000000, "Finale"}}
	for i, w := range want {
		if got[i].StartTicks != w.ticks || got[i].Name != w.name {
			t.Errorf("chapter %d = %+v, want %+v", i, got[i], w)
		}
	}
}
