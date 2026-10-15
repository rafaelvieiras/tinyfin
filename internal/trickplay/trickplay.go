// Package trickplay generates and locates seek-thumbnail sprite sheets
// (docs/contract/specs/trickplay.md): one JPEG per Tile grid of
// thumbnails, taken at a fixed interval, per item and per width.
//
// Generation is opt-in per library (LibraryOptions.EnableTrickplayImageExtraction),
// runs in the background with bounded concurrency (one ffmpeg at a time by
// default, single-threaded) and is incremental: an item whose file is
// unchanged since the last run is not processed again.
package trickplay

import (
	"context"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// Defaults of the reference server's trickplay options (not configurable
// here, see the spec's "Known differences").
const (
	DefaultWidth      = 320
	DefaultTileWidth  = 10
	DefaultTileHeight = 10
	DefaultIntervalMS = 10000
	jpegQScale        = 4
	stampName         = ".stamp"
)

// LibraryOptions are the members of a library's LibraryOptions that drive
// trickplay.
type LibraryOptions struct {
	// Enable is EnableTrickplayImageExtraction.
	Enable bool
	// DuringScan is ExtractTrickplayImagesDuringLibraryScan: generation
	// follows each library scan instead of waiting for an explicit request.
	DuringScan bool
	// SaveWithMedia is SaveTrickplayWithMedia: the sheets are written beside
	// the video instead of in the data directory.
	SaveWithMedia bool
}

// ParseLibraryOptions reads the trickplay members of the raw LibraryOptions
// JSON; absent or malformed means everything off.
func ParseLibraryOptions(raw []byte) LibraryOptions {
	var in struct {
		Enable        bool `json:"EnableTrickplayImageExtraction"`
		DuringScan    bool `json:"ExtractTrickplayImagesDuringLibraryScan"`
		SaveWithMedia bool `json:"SaveTrickplayWithMedia"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &in) != nil {
		return LibraryOptions{}
	}
	return LibraryOptions{Enable: in.Enable, DuringScan: in.DuringScan, SaveWithMedia: in.SaveWithMedia}
}

// Plan is the geometry of the sheets of one video.
type Plan struct {
	Width, Height         int // one thumbnail
	TileWidth, TileHeight int
	IntervalMS            int
}

// PlanFor works out the geometry for a video of srcW x srcH. ok is false when
// the video cannot have sheets (no picture size, no duration).
func PlanFor(srcW, srcH int, durationTicks int64) (Plan, bool) {
	if srcW <= 0 || srcH <= 0 || durationTicks <= 0 {
		return Plan{}, false
	}
	w := DefaultWidth
	if srcW < w {
		w = srcW // never upscale
	}
	w = w / 2 * 2
	h := int(float64(w)*float64(srcH)/float64(srcW)/2) * 2
	if w < 2 || h < 2 {
		return Plan{}, false
	}
	return Plan{Width: w, Height: h, TileWidth: DefaultTileWidth, TileHeight: DefaultTileHeight, IntervalMS: DefaultIntervalMS}, true
}

// Bandwidth is the peak bits per second a sheet costs to fetch while it
// plays: the biggest file over the time its grid covers, rounded up.
func Bandwidth(p Plan, maxFileBytes int64) int {
	per := float64(p.TileWidth*p.TileHeight) * float64(p.IntervalMS) / 1000
	return int(math.Ceil(float64(maxFileBytes) * 8 / per))
}

// Generator produces sheets in the background.
type Generator struct {
	store   storage.Store
	ffmpeg  string
	dataDir string
	sem     chan struct{}

	mu       sync.Mutex
	inflight map[storage.ItemID]bool
	wg       sync.WaitGroup
}

// New returns a Generator that writes under dataDir/trickplay and runs at
// most concurrency ffmpeg processes at a time (<= 0 means 1).
func New(store storage.Store, ffmpegPath, dataDir string, concurrency int) *Generator {
	if concurrency <= 0 {
		concurrency = 1
	}
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	return &Generator{store: store, ffmpeg: ffmpegPath, dataDir: dataDir, sem: make(chan struct{}, concurrency), inflight: map[storage.ItemID]bool{}}
}

// Wait blocks until every queued job has finished (tests).
func (g *Generator) Wait() { g.wg.Wait() }

// DataRoot is the directory sheets are kept in unless a library asks to
// save them with the media.
func (g *Generator) DataRoot() string { return filepath.Join(g.dataDir, "trickplay") }

func widthDir(root string, p Plan) string { return filepath.Join(root, strconv.Itoa(p.Width)) }

// DirFor locates the directory that holds the sheets of item at width, the
// data directory first and the one beside the media second. "" when neither
// exists.
func (g *Generator) DirFor(item storage.Item, info storage.TrickplayInfo) string {
	cands := []string{
		filepath.Join(g.DataRoot(), string(item.ID), strconv.Itoa(info.Width)),
		sidecarDir(item, info.Width, info.TileWidth, info.TileHeight),
	}
	for _, c := range cands {
		if c == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(c, "0.jpg")); err == nil {
			return c
		}
	}
	return ""
}

func sidecarDir(item storage.Item, w, tw, th int) string {
	if item.Path == "" {
		return ""
	}
	base := strings.TrimSuffix(filepath.Base(item.Path), filepath.Ext(item.Path))
	return filepath.Join(filepath.Dir(item.Path), base+".trickplay", fmt.Sprintf("%d - %dx%d", w, tw, th))
}

// EnqueueLibrary starts a background pass over the library's videos. When the
// library has trickplay switched off, what an earlier pass wrote is removed.
// force regenerates even an unchanged item and ignores DuringScan.
func (g *Generator) EnqueueLibrary(lib storage.Library, force bool) {
	opts := ParseLibraryOptions(lib.LibraryOptions)
	if !opts.Enable {
		g.wg.Add(1)
		go func() {
			defer g.wg.Done()
			g.eachItem(context.Background(), lib, func(it storage.Item) { g.remove(context.Background(), it) })
		}()
		return
	}
	if !opts.DuringScan && !force {
		return
	}
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		g.eachItem(context.Background(), lib, func(it storage.Item) { g.process(context.Background(), it, opts, force) })
	}()
}

// EnqueueItem starts a background job for one item of lib (a forced
// regeneration, the RegenerateTrickplay refresh).
func (g *Generator) EnqueueItem(lib storage.Library, item storage.Item, force bool) {
	opts := ParseLibraryOptions(lib.LibraryOptions)
	if !opts.Enable {
		return
	}
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		g.process(context.Background(), item, opts, force)
	}()
}

func (g *Generator) eachItem(ctx context.Context, lib storage.Library, fn func(storage.Item)) {
	id := lib.ID
	for off := 0; ; off += 500 {
		page, err := g.store.QueryItems(ctx, storage.ItemFilter{
			LibraryID: &id, Recursive: true, Limit: 500, Offset: off,
			ItemTypes: []storage.ItemType{storage.ItemTypeMovie, storage.ItemTypeEpisode, storage.ItemTypeVideo},
		})
		if err != nil {
			log.Printf("trickplay: listing library %q: %v", lib.Name, err)
			return
		}
		for _, it := range page.Items {
			fn(it)
		}
		if len(page.Items) < 500 {
			return
		}
	}
}

func (g *Generator) remove(ctx context.Context, item storage.Item) {
	infos, err := g.store.QueryTrickplay(ctx, item.ID)
	if err != nil || len(infos) == 0 {
		return
	}
	for _, in := range infos {
		if d := g.DirFor(item, in); d != "" {
			_ = os.RemoveAll(d)
		}
		_ = os.RemoveAll(filepath.Join(g.DataRoot(), string(item.ID), strconv.Itoa(in.Width)))
		_ = g.store.DeleteTrickplayInfo(ctx, item.ID, in.Width)
	}
	_ = os.Remove(filepath.Join(g.DataRoot(), string(item.ID)))
}

func videoSize(item storage.Item) (w, h int) {
	for _, s := range item.MediaStreams {
		if s.Type == storage.StreamTypeVideo && s.Width != nil && s.Height != nil {
			return *s.Width, *s.Height
		}
	}
	return 0, 0
}

func stampOf(fi os.FileInfo, p Plan) string {
	return fmt.Sprintf("%d:%d:%d:%dx%d:%d", fi.Size(), fi.ModTime().UnixNano(), p.Width, p.TileWidth, p.TileHeight, p.IntervalMS)
}

// process makes the sheets of one item when they are missing or stale.
func (g *Generator) process(ctx context.Context, item storage.Item, opts LibraryOptions, force bool) {
	if item.RemoteURL != "" || item.VideoType != "" || item.ExtraType != "" || item.Path == "" {
		return
	}
	srcW, srcH := videoSize(item)
	var ticks int64
	if item.RunTimeTicks != nil {
		ticks = *item.RunTimeTicks
	}
	p, ok := PlanFor(srcW, srcH, ticks)
	if !ok {
		return
	}
	fi, err := os.Stat(item.Path)
	if err != nil || fi.IsDir() {
		return
	}
	g.mu.Lock()
	if g.inflight[item.ID] {
		g.mu.Unlock()
		return
	}
	g.inflight[item.ID] = true
	g.mu.Unlock()
	defer func() { g.mu.Lock(); delete(g.inflight, item.ID); g.mu.Unlock() }()

	dir := widthDir(filepath.Join(g.DataRoot(), string(item.ID)), p)
	if opts.SaveWithMedia {
		dir = sidecarDir(item, p.Width, p.TileWidth, p.TileHeight)
	}
	stamp := stampOf(fi, p)
	if !force {
		if b, err := os.ReadFile(filepath.Join(dir, stampName)); err == nil && string(b) == stamp {
			if infos, _ := g.store.QueryTrickplay(ctx, item.ID); hasWidth(infos, p.Width) {
				return
			}
		}
	}

	g.sem <- struct{}{}
	defer func() { <-g.sem }()
	info, err := g.generate(ctx, item.Path, dir, p, stamp)
	if err != nil {
		log.Printf("trickplay: %s: %v", item.Path, err)
		return
	}
	// A pass that switched sides (data dir <-> beside the media) drops the old copy.
	other := filepath.Join(g.DataRoot(), string(item.ID), strconv.Itoa(p.Width))
	if opts.SaveWithMedia {
		_ = os.RemoveAll(other)
	}
	if err := g.store.UpsertTrickplayInfo(ctx, item.ID, info); err != nil {
		log.Printf("trickplay: %s: recording: %v", item.Path, err)
	}
}

func hasWidth(infos []storage.TrickplayInfo, w int) bool {
	for _, i := range infos {
		if i.Width == w {
			return true
		}
	}
	return false
}

func (g *Generator) generate(ctx context.Context, input, dir string, p Plan, stamp string) (storage.TrickplayInfo, error) {
	tmp := dir + ".tmp"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return storage.TrickplayInfo{}, err
	}
	defer os.RemoveAll(tmp)
	ctx, cancel := context.WithTimeout(ctx, 6*time.Hour)
	defer cancel()
	// First the thumbnails one by one (their number is what ffmpeg's fps
	// filter makes of the video, not something to compute), then the sheets.
	thumbs := filepath.Join(tmp, "thumbs")
	if err := os.MkdirAll(thumbs, 0o755); err != nil {
		return storage.TrickplayInfo{}, err
	}
	vf := fmt.Sprintf("fps=1000/%d,scale=%d:%d", p.IntervalMS, p.Width, p.Height)
	if err := g.run(ctx, "-i", input, "-map", "0:v:0", "-an", "-sn", "-vf", vf, "-threads", "1",
		"-c:v", "mjpeg", "-q:v", strconv.Itoa(jpegQScale), "-start_number", "0",
		"-f", "image2", filepath.Join(thumbs, "%d.jpg")); err != nil {
		return storage.TrickplayInfo{}, err
	}
	count := 0
	for {
		if _, err := os.Stat(filepath.Join(thumbs, strconv.Itoa(count)+".jpg")); err != nil {
			break
		}
		count++
	}
	if count == 0 {
		return storage.TrickplayInfo{}, fmt.Errorf("ffmpeg produced no thumbnail")
	}
	if err := g.run(ctx, "-start_number", "0", "-i", filepath.Join(thumbs, "%d.jpg"),
		"-vf", fmt.Sprintf("tile=%dx%d", p.TileWidth, p.TileHeight), "-threads", "1",
		"-c:v", "mjpeg", "-q:v", strconv.Itoa(jpegQScale), "-start_number", "0",
		"-f", "image2", filepath.Join(tmp, "%d.jpg")); err != nil {
		return storage.TrickplayInfo{}, err
	}
	_ = os.RemoveAll(thumbs)
	files, _ := filepath.Glob(filepath.Join(tmp, "*.jpg"))
	per := p.TileWidth * p.TileHeight
	if len(files) != (count+per-1)/per {
		return storage.TrickplayInfo{}, fmt.Errorf("ffmpeg made %d sheets for %d thumbnails", len(files), count)
	}
	var maxSize int64
	for _, f := range files {
		if fi, err := os.Stat(f); err == nil && fi.Size() > maxSize {
			maxSize = fi.Size()
		}
	}
	p2 := p
	if err := os.WriteFile(filepath.Join(tmp, stampName), []byte(stamp), 0o644); err != nil {
		return storage.TrickplayInfo{}, err
	}
	if err := os.RemoveAll(dir); err != nil {
		return storage.TrickplayInfo{}, err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return storage.TrickplayInfo{}, err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return storage.TrickplayInfo{}, err
	}
	if f, err := os.Open(filepath.Join(dir, "0.jpg")); err == nil {
		if cfg, err := jpeg.DecodeConfig(f); err == nil && cfg.Width%p.TileWidth == 0 && cfg.Height%p.TileHeight == 0 {
			p2.Width, p2.Height = cfg.Width/p.TileWidth, cfg.Height/p.TileHeight
		}
		f.Close()
	}
	return storage.TrickplayInfo{
		Width: p2.Width, Height: p2.Height, TileWidth: p.TileWidth, TileHeight: p.TileHeight,
		ThumbnailCount: count, Interval: p.IntervalMS, Bandwidth: Bandwidth(p, maxSize),
	}, nil
}

func (g *Generator) run(ctx context.Context, args ...string) error {
	full := append([]string{"-v", "error", "-nostdin", "-threads", "1"}, args...)
	if out, err := exec.CommandContext(ctx, g.ffmpeg, full...).CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg: %v: %s", err, tail(string(out)))
	}
	return nil
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		s = s[len(s)-300:]
	}
	return s
}
