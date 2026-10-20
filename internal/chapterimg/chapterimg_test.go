package chapterimg

import (
	"context"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

func TestParseLibraryOptions(t *testing.T) {
	if got := ParseLibraryOptions([]byte(`{"EnableChapterImageExtraction":true}`)); !got.Enable || got.DuringScan {
		t.Errorf("enable only: %+v", got)
	}
	if got := ParseLibraryOptions([]byte(`{"EnableChapterImageExtraction":true,"ExtractChapterImagesDuringLibraryScan":true}`)); !got.Enable || !got.DuringScan {
		t.Errorf("both: %+v", got)
	}
	if got := ParseLibraryOptions(nil); got.Enable || got.DuringScan {
		t.Errorf("none: %+v", got)
	}
	if got := ParseLibraryOptions([]byte(`{`)); got.Enable {
		t.Errorf("malformed: %+v", got)
	}
}

func TestExtractTicks(t *testing.T) {
	if got := ExtractTicks(storage.ChapterRef{StartTicks: 0}); got != 150_000_000 {
		t.Errorf("first chapter: %d", got)
	}
	if got := ExtractTicks(storage.ChapterRef{StartTicks: 60_000_000}); got != 60_000_000 {
		t.Errorf("later chapter: %d", got)
	}
}

// ramp makes a 640x360 video whose luma grows with time (8 per second), with
// chapters at the given starts (seconds).
func ramp(t *testing.T, dir string, secs int, starts ...int) string {
	t.Helper()
	meta := ";FFMETADATA1\n"
	for i, s := range starts {
		end := secs
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		meta += "[CHAPTER]\nTIMEBASE=1/1000\nSTART=" + strconv.Itoa(s*1000) + "\nEND=" + strconv.Itoa(end*1000) + "\n"
	}
	m := filepath.Join(dir, "m.txt")
	if err := os.WriteFile(m, []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "v.mkv")
	cmd := exec.Command("ffmpeg", "-v", "error", "-y", "-f", "lavfi",
		"-i", "color=c=black:s=640x360:r=5:d="+strconv.Itoa(secs)+",geq=lum='min(255,T*8)':cb=128:cr=128",
		"-i", m, "-map", "0", "-map_metadata", "1", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot generate the video: %v\n%s", err, b)
	}
	return out
}

// lumaSeconds reads the moment a still was taken from off its brightness.
func lumaSeconds(t *testing.T, jpg string) float64 {
	t.Helper()
	b, err := exec.Command("ffmpeg", "-v", "error", "-i", jpg, "-vf", "crop=20:20:300:170,format=gray", "-f", "rawvideo", "-").Output()
	if err != nil || len(b) == 0 {
		t.Fatalf("decode %s: %v", jpg, err)
	}
	sum := 0
	for _, v := range b {
		sum += int(v)
	}
	// limited-range luma of the ramp: about 9.3 per second, 18.6 below zero
	return (float64(sum)/float64(len(b)) + 18.6) / 9.32
}

func TestProcessTakesPicturesAtChapterStarts(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	vid := ramp(t, dir, 30, 0, 10, 20)
	g := New(nil, "ffmpeg", filepath.Join(dir, "data"))
	item := storage.Item{ID: "abc", Path: vid, Type: storage.ItemTypeMovie, Chapters: []storage.ChapterRef{
		{StartTicks: 0}, {StartTicks: 100_000_000}, {StartTicks: 200_000_000}}}
	g.process(context.Background(), item)
	want := []float64{15, 10, 20}
	for i, c := range item.Chapters {
		path, _, ok := g.Picture(item.ID, c)
		if !ok {
			t.Fatalf("chapter %d has no picture", i)
		}
		if got := lumaSeconds(t, path); got < want[i]-0.7 || got > want[i]+0.7 {
			t.Errorf("chapter %d picture taken at %.1f s, want %.0f", i, got, want[i])
		}
	}
	// unchanged: not made again
	path, mod, _ := g.Picture(item.ID, item.Chapters[1])
	g.process(context.Background(), item)
	if _, mod2, _ := g.Picture(item.ID, item.Chapters[1]); !mod2.Equal(mod) {
		t.Error("an unchanged item was processed again")
	}
	if Tag(path, mod) == "" || len(Tag(path, mod)) != 32 {
		t.Error("tag is not 32 hex digits")
	}
}

func TestProcessKeepsNothingWhenAPositionIsPastTheEnd(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	vid := ramp(t, dir, 10, 0, 5)
	g := New(nil, "ffmpeg", filepath.Join(dir, "data"))
	item := storage.Item{ID: "short", Path: vid, Type: storage.ItemTypeMovie, Chapters: []storage.ChapterRef{{StartTicks: 0}, {StartTicks: 50_000_000}}}
	g.process(context.Background(), item)
	for i, c := range item.Chapters {
		if _, _, ok := g.Picture(item.ID, c); ok {
			t.Errorf("chapter %d has a picture", i)
		}
	}
}

func intp(v int) *int { return &v }

func TestPlanFor(t *testing.T) {
	cases := []struct {
		name         string
		p            Params
		sw, sh       int
		pass         bool
		w, h         int
		png          bool
		quality      int
		qualityCheck bool
	}{
		{name: "nothing", sw: 640, sh: 360, pass: true},
		{name: "maxWidth", p: Params{MaxWidth: intp(100)}, sw: 640, sh: 360, w: 100, h: 56},
		{name: "maxHeight", p: Params{MaxHeight: intp(40)}, sw: 640, sh: 360, w: 71, h: 40},
		{name: "tie goes down", p: Params{MaxWidth: intp(40)}, sw: 640, sh: 360, w: 40, h: 22},
		{name: "width", p: Params{Width: intp(100)}, sw: 640, sh: 360, w: 100, h: 56},
		{name: "width and height", p: Params{Width: intp(100), Height: intp(50)}, sw: 640, sh: 360, w: 100, h: 50},
		{name: "tighter maximum", p: Params{MaxWidth: intp(100), MaxHeight: intp(30)}, sw: 640, sh: 360, w: 53, h: 30},
		{name: "odd source", p: Params{MaxWidth: intp(200), MaxHeight: intp(64)}, sw: 650, sh: 366, w: 114, h: 64},
		{name: "width then maximum", p: Params{Width: intp(111), MaxHeight: intp(40)}, sw: 650, sh: 366, w: 70, h: 40},
		{name: "maximum above the size", p: Params{MaxWidth: intp(2000)}, sw: 120, sh: 68, pass: true},
		{name: "maximum equal to the size", p: Params{MaxWidth: intp(120), MaxHeight: intp(68)}, sw: 120, sh: 68, pass: true},
		{name: "width above the size", p: Params{Width: intp(2000)}, sw: 120, sh: 68, w: 120, h: 68},
		{name: "pair above the size", p: Params{Width: intp(200), Height: intp(200)}, sw: 120, sh: 68, w: 68, h: 68},
		{name: "height then maximum", p: Params{Height: intp(111), MaxWidth: intp(40)}, sw: 120, sh: 68, w: 40, h: 23},
		{name: "zero maximum", p: Params{MaxWidth: intp(0)}, sw: 640, sh: 360, w: 640, h: 360},
		{name: "fill width", p: Params{FillWidth: intp(100), Quality: intp(50)}, sw: 640, sh: 360, w: 100, h: 57},
		{name: "fill alone", p: Params{FillWidth: intp(100)}, sw: 640, sh: 360, pass: true},
		{name: "fill covers", p: Params{FillWidth: intp(300), FillHeight: intp(300), Quality: intp(50)}, sw: 640, sh: 360, w: 534, h: 300},
		{name: "fill above the size", p: Params{FillWidth: intp(900), FillHeight: intp(900), Quality: intp(50)}, sw: 640, sh: 360, w: 640, h: 360},
		{name: "maximum wins over fill", p: Params{FillWidth: intp(300), FillHeight: intp(300), MaxWidth: intp(100)}, sw: 640, sh: 360, w: 100, h: 56},
		{name: "caps in sequence", p: Params{MaxWidth: intp(72), MaxHeight: intp(71)}, sw: 640, sh: 360, w: 72, h: 41},
		{name: "caps in sequence, odd", p: Params{MaxWidth: intp(33), MaxHeight: intp(20)}, sw: 640, sh: 360, w: 33, h: 18},
		{name: "tie to the even, up", p: Params{Width: intp(24)}, sw: 640, sh: 360, w: 24, h: 14},
		{name: "tie to the even, down", p: Params{Width: intp(56)}, sw: 640, sh: 360, w: 56, h: 31},
		{name: "fill quotient not cleaned", p: Params{FillWidth: intp(245), Quality: intp(50)}, sw: 640, sh: 360, w: 246, h: 138},
		{name: "fill, shorter side is one", p: Params{FillWidth: intp(1), Quality: intp(50)}, sw: 640, sh: 360, w: 2, h: 1},
		{name: "fill, shorter side is one, portrait", p: Params{FillHeight: intp(1), Quality: intp(50)}, sw: 360, sh: 640, w: 1, h: 2},
		{name: "negative fill cancels", p: Params{Height: intp(60), FillWidth: intp(-2), FillHeight: intp(30)}, sw: 640, sh: 360, w: 107, h: 60},
		{name: "a side that rounds to nothing", p: Params{Width: intp(640), Height: intp(54), MaxWidth: intp(5)}, sw: 640, sh: 360, pass: true},
		{name: "quality 90", p: Params{Quality: intp(90)}, sw: 640, sh: 360, pass: true},
		{name: "quality 89", p: Params{Quality: intp(89)}, sw: 640, sh: 360, w: 640, h: 360, quality: 89, qualityCheck: true},
		{name: "quality 0", p: Params{Quality: intp(0)}, sw: 640, sh: 360, w: 640, h: 360, quality: 1, qualityCheck: true},
		{name: "default quality", p: Params{MaxWidth: intp(100)}, sw: 640, sh: 360, w: 100, h: 56, quality: 100, qualityCheck: true},
		{name: "png", p: Params{Format: "Png"}, sw: 640, sh: 360, w: 640, h: 360, png: true},
		{name: "jpg", p: Params{Format: "jpg"}, sw: 640, sh: 360, pass: true},
		{name: "unknown format", p: Params{Format: "xyz"}, sw: 640, sh: 360, pass: true},
		{name: "gif is a jpeg", p: Params{Format: "gif"}, sw: 640, sh: 360, w: 640, h: 360},
		{name: "svg is never processed", p: Params{Format: "Svg", MaxWidth: intp(100)}, sw: 640, sh: 360, pass: true},
		{name: "format by number", p: Params{Format: "3", MaxWidth: intp(100)}, sw: 640, sh: 360, w: 100, h: 56, png: true},
	}
	for _, c := range cases {
		got := PlanFor(c.p, c.sw, c.sh)
		if got.Passthrough != c.pass {
			t.Errorf("%s: passthrough %v, want %v", c.name, got.Passthrough, c.pass)
			continue
		}
		if c.pass {
			continue
		}
		if got.W != c.w || got.H != c.h || got.PNG != c.png {
			t.Errorf("%s: %dx%d png=%v, want %dx%d png=%v", c.name, got.W, got.H, got.PNG, c.w, c.h, c.png)
		}
		if c.qualityCheck && got.Quality != c.quality {
			t.Errorf("%s: quality %d, want %d", c.name, got.Quality, c.quality)
		}
	}
}

func TestValidFormat(t *testing.T) {
	for _, s := range []string{"", "png", "xyz", "0", "5", " 3 "} {
		if !ValidFormat(s) {
			t.Errorf("%q should be accepted", s)
		}
	}
	for _, s := range []string{"6", "-1", "99"} {
		if ValidFormat(s) {
			t.Errorf("%q should be rejected", s)
		}
	}
}

func TestRender(t *testing.T) {
	dir := t.TempDir()
	still := filepath.Join(dir, "still.jpg")
	cmd := exec.Command("ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=s=320x180:d=1:r=1", "-frames:v", "1", still)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot generate the picture: %v\n%s", err, b)
	}
	g := New(nil, "", dir)
	mod := time.Unix(1700000000, 0).UTC()
	size := func(file string) (int, int) {
		f, err := os.Open(file)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		cfg, _, err := image.DecodeConfig(f)
		if err != nil {
			t.Fatal(err)
		}
		return cfg.Width, cfg.Height
	}
	file, ct, err := g.Render("item", still, mod, Params{})
	if err != nil || file != still || ct != "image/jpeg" {
		t.Fatalf("untouched: %q %q %v", file, ct, err)
	}
	file, ct, err = g.Render("item", still, mod, Params{MaxWidth: intp(100)})
	if err != nil || ct != "image/jpeg" || file == still {
		t.Fatalf("resized: %q %q %v", file, ct, err)
	}
	if w, h := size(file); w != 100 || h != 56 {
		t.Errorf("resized to %dx%d, want 100x56", w, h)
	}
	if st, err := os.Stat(file); err != nil || !st.ModTime().Equal(mod) {
		t.Errorf("the copy must carry the still's date: %v %v", st, err)
	}
	again, _, err := g.Render("item", still, mod, Params{MaxWidth: intp(100)})
	if err != nil || again != file {
		t.Errorf("the same request must find the same copy: %q vs %q (%v)", again, file, err)
	}
	file, ct, err = g.Render("item", still, mod, Params{Format: "png", MaxHeight: intp(45)})
	if err != nil || ct != "image/png" {
		t.Fatalf("png: %q %q %v", file, ct, err)
	}
	if w, h := size(file); w != 80 || h != 45 {
		t.Errorf("png is %dx%d, want 80x45", w, h)
	}
}

func TestPlanForOverlaysAndWebP(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	nan := math.NaN()
	cases := []struct {
		name string
		p    Params
		pass bool
		webp bool
	}{
		{"blur zero is still made again", Params{Blur: intp(0)}, false, false},
		{"percent in range", Params{PercentPlayed: f(50)}, false, false},
		{"percent 0", Params{PercentPlayed: f(0)}, true, false},
		{"percent 100", Params{PercentPlayed: f(100)}, true, false},
		{"percent NaN", Params{PercentPlayed: f(nan)}, false, false},
		{"unplayed 0", Params{UnplayedCount: intp(0)}, true, false},
		{"unplayed 1", Params{UnplayedCount: intp(1)}, false, false},
		{"background hex", Params{BackgroundColor: "#ff0000"}, false, false},
		{"background word", Params{BackgroundColor: "red"}, true, false},
		{"foreground given", Params{ForegroundLayer: "zzz"}, false, false},
		{"accept alone", Params{Webp: true}, true, false},
		{"accept and a resize", Params{Webp: true, MaxWidth: intp(100)}, false, true},
		{"accept, explicit jpg", Params{Webp: true, MaxWidth: intp(100), Format: "Jpg"}, false, false},
		{"accept, explicit bmp", Params{Webp: true, MaxWidth: intp(100), Format: "Bmp"}, false, false},
		{"format webp", Params{Format: "Webp"}, false, true},
	}
	for _, c := range cases {
		got := PlanFor(c.p, 640, 360)
		if got.Passthrough != c.pass || (!c.pass && got.WebP != c.webp) {
			t.Errorf("%s: passthrough %v webp %v, want %v %v", c.name, got.Passthrough, got.WebP, c.pass, c.webp)
		}
	}
	if o, _ := overlayOf(Params{ForegroundLayer: "80"}); o.Layer != 80 {
		t.Errorf("layer 80: %d", o.Layer)
	}
	if o, _ := overlayOf(Params{ForegroundLayer: "300"}); o.Layer != 44 {
		t.Errorf("layer 300: %d", o.Layer)
	}
}
