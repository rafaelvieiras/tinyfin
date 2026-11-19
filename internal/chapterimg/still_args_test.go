package chapterimg

import (
	"strings"
	"testing"
)

// TestStillArgsPicksARepresentativeFrame: the picture of a chapter comes from a
// window of frames, not from the single frame at the chapter mark, so a chapter
// that opens on a black frame or a fade still gets a usable picture (the
// reference samples the same window).
func TestStillArgsPicksARepresentativeFrame(t *testing.T) {
	args := strings.Join(StillArgs("/media/film.mkv", 10_000_000, "/tmp/out.jpg"), " ")
	if !strings.Contains(args, "thumbnail=n=24") {
		t.Errorf("StillArgs() = %q, want a thumbnail=n=24 filter", args)
	}
	if !strings.Contains(args, "-frames:v 1") {
		t.Errorf("StillArgs() = %q, want a single frame", args)
	}
}

// TestStillArgsDoesNotPinThreads: pinning the decoder to one thread cost about
// twice the time per picture on a 1080p source, and the reference lets ffmpeg
// choose. Nothing here may pass -threads.
func TestStillArgsDoesNotPinThreads(t *testing.T) {
	for _, arg := range StillArgs("/media/film.mkv", 10_000_000, "/tmp/out.jpg") {
		if arg == "-threads" {
			t.Fatalf("StillArgs() pins the thread count: %v", StillArgs("/media/film.mkv", 10_000_000, "/tmp/out.jpg"))
		}
	}
}

// TestStillArgsTakesThePictureAtTheChapterMark: the still is taken at the
// chapter's start, or 15 s in for a chapter that starts at zero (a black
// opening is not worth a picture).
func TestStillArgsTakesThePictureAtTheChapterMark(t *testing.T) {
	for _, tc := range []struct {
		startTicks int64
		want       string
	}{
		{0, "15.000"},
		{10_000_000, "1.000"},
		{1_234_500_000, "123.450"},
	} {
		args := StillArgs("/media/film.mkv", tc.startTicks, "/tmp/out.jpg")
		at := -1
		for i, a := range args {
			if a == "-ss" && i+1 < len(args) {
				at = i + 1
				break
			}
		}
		if at < 0 {
			t.Fatalf("StillArgs(%d) = %v, want a -ss position", tc.startTicks, args)
		}
		if args[at] != tc.want {
			t.Errorf("StillArgs(%d) seeks to %s, want %s", tc.startTicks, args[at], tc.want)
		}
	}
}
