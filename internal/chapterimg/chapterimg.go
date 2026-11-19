// Package chapterimg extracts and locates chapter pictures
// (docs/contract/specs/chapters.md): one still per chapter mark of a video,
// taken at the chapter's start.
//
// Extraction is opt-in per library (LibraryOptions.EnableChapterImageExtraction),
// follows each library scan when ExtractChapterImagesDuringLibraryScan is set
// and otherwise only happens in the "Extract Chapter Images" task. It is
// incremental: an item whose file is unchanged since the last run is not
// processed again.
package chapterimg

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

const (
	stampName = ".stamp"
	// partialName marks an item whose extraction stopped at a picture that
	// could not be taken; the pictures before it are kept.
	partialName = ".partial"
	// firstChapterSeconds is where the picture of a chapter that starts at
	// time zero is taken (a black opening is not worth a picture).
	firstChapterSeconds = 15
	ticksPerSecond      = int64(10_000_000)
	// sampleFrames is how many frames the picture of a chapter is chosen
	// from: ffmpeg's thumbnail filter reads that many from the chapter's
	// start and keeps the most representative one, so a chapter that opens
	// on a black frame, a fade or a duplicate still gets a usable picture.
	// The reference samples the same number.
	sampleFrames = 24
)

// LibraryOptions are the members of a library's LibraryOptions that drive
// chapter pictures.
type LibraryOptions struct {
	// Enable is EnableChapterImageExtraction.
	Enable bool
	// DuringScan is ExtractChapterImagesDuringLibraryScan.
	DuringScan bool
}

// ParseLibraryOptions reads the chapter members of the raw LibraryOptions
// JSON; absent or malformed means everything off.
func ParseLibraryOptions(raw []byte) LibraryOptions {
	var in struct {
		Enable     bool `json:"EnableChapterImageExtraction"`
		DuringScan bool `json:"ExtractChapterImagesDuringLibraryScan"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &in) != nil {
		return LibraryOptions{}
	}
	return LibraryOptions{Enable: in.Enable, DuringScan: in.DuringScan}
}

// ExtractTicks is the position in the video a chapter's picture is taken at:
// its start, or 15 s for a chapter that starts at 0.
func ExtractTicks(c storage.ChapterRef) int64 {
	if c.StartTicks <= 0 {
		return firstChapterSeconds * ticksPerSecond
	}
	return c.StartTicks
}

// Generator extracts pictures, one ffmpeg at a time.
type Generator struct {
	store   storage.Store
	ffmpeg  string
	dataDir string
	sem     chan struct{}

	wg sync.WaitGroup

	taskMu     sync.Mutex
	taskJobs   int
	taskCancel context.CancelFunc
	taskLast   TaskResult
}

// TaskResult is the outcome of the last task batch.
type TaskResult struct {
	Start, End time.Time
	Cancelled  bool
}

// New returns a Generator writing under dataDir.
func New(store storage.Store, ffmpegPath, dataDir string) *Generator {
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	return &Generator{store: store, ffmpeg: ffmpegPath, dataDir: dataDir, sem: make(chan struct{}, 1)}
}

// Dir is the directory holding the pictures of an item.
func (g *Generator) Dir(id storage.ItemID) string {
	return filepath.Join(g.dataDir, "chapters", string(id))
}

// Path is the file of the picture of the chapter starting at startTicks.
func (g *Generator) Path(id storage.ItemID, startTicks int64) string {
	return filepath.Join(g.Dir(id), strconv.FormatInt(startTicks, 10)+".jpg")
}

// Picture returns the file of a chapter's picture and its modification time;
// ok is false when the chapter has none.
func (g *Generator) Picture(id storage.ItemID, c storage.ChapterRef) (path string, mod time.Time, ok bool) {
	path = g.Path(id, c.StartTicks)
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() || fi.Size() == 0 {
		return "", time.Time{}, false
	}
	return path, fi.ModTime().UTC(), true
}

// Tag is the ImageTag of a picture: a digest of its file name and date.
func Tag(path string, mod time.Time) string {
	sum := md5.Sum([]byte(path + "\n" + strconv.FormatInt(mod.UnixNano(), 10)))
	return hex.EncodeToString(sum[:])
}

// TaskRunning reports whether a task-initiated batch is in flight.
func (g *Generator) TaskRunning() bool {
	g.taskMu.Lock()
	defer g.taskMu.Unlock()
	return g.taskJobs > 0
}

// TaskLast returns the last finished batch (zero Start when none yet).
func (g *Generator) TaskLast() TaskResult {
	g.taskMu.Lock()
	defer g.taskMu.Unlock()
	return g.taskLast
}

// CancelTask stops the in-flight task batch; it reports whether there was one.
func (g *Generator) CancelTask() bool {
	g.taskMu.Lock()
	defer g.taskMu.Unlock()
	if g.taskJobs == 0 || g.taskCancel == nil {
		return false
	}
	g.taskCancel()
	return true
}

// RunTask extracts the missing pictures of every library that has extraction
// enabled, whatever its DuringScan option, as the "Extract Chapter Images"
// task does. It returns at once; the work runs in the background.
func (g *Generator) RunTask(libs []storage.Library) {
	g.taskMu.Lock()
	if g.taskJobs > 0 {
		g.taskMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	g.taskCancel = cancel
	g.taskJobs = 1
	start := time.Now().UTC()
	g.taskMu.Unlock()
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		for _, lib := range libs {
			if !ParseLibraryOptions(lib.LibraryOptions).Enable || ctx.Err() != nil {
				continue
			}
			g.eachItem(ctx, lib, func(it storage.Item) {
				if ctx.Err() == nil {
					g.process(ctx, it)
				}
			})
		}
		g.taskMu.Lock()
		g.taskLast = TaskResult{Start: start, End: time.Now().UTC(), Cancelled: ctx.Err() != nil}
		g.taskJobs = 0
		g.taskMu.Unlock()
		cancel()
	}()
}

// EnqueueLibrary extracts the pictures of a just-scanned library in the
// background when it asks for it during the scan.
func (g *Generator) EnqueueLibrary(lib storage.Library) {
	opts := ParseLibraryOptions(lib.LibraryOptions)
	if !opts.Enable || !opts.DuringScan {
		return
	}
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		g.eachItem(context.Background(), lib, func(it storage.Item) { g.process(context.Background(), it) })
	}()
}

// Wait blocks until every queued job has finished (tests).
func (g *Generator) Wait() { g.wg.Wait() }

func (g *Generator) eachItem(ctx context.Context, lib storage.Library, fn func(storage.Item)) {
	id := lib.ID
	for off := 0; ; off += 500 {
		page, err := g.store.QueryItems(ctx, storage.ItemFilter{
			LibraryID: &id, Recursive: true, Limit: 500, Offset: off,
			ItemTypes: []storage.ItemType{storage.ItemTypeMovie, storage.ItemTypeEpisode, storage.ItemTypeVideo, storage.ItemTypeMusicVideo},
		})
		if err != nil {
			log.Printf("chapterimg: listing library %q: %v", lib.Name, err)
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

// StillArgs is the ffmpeg invocation that takes the picture of a chapter
// starting at startTicks out of path and writes it to out. The decoder runs
// with ffmpeg's own thread count: pinning it to one thread cost about twice
// the time on a 1080p source for no gain, and the reference does not pin it
// either.
func StillArgs(path string, startTicks int64, out string) []string {
	secs := strconv.FormatFloat(float64(ExtractTicks(storage.ChapterRef{StartTicks: startTicks}))/
		float64(ticksPerSecond), 'f', 3, 64)
	return []string{"-v", "error", "-nostdin", "-ss", secs, "-i", path,
		"-an", "-sn", "-vf", fmt.Sprintf("thumbnail=n=%d", sampleFrames),
		"-frames:v", "1", "-q:v", "4", "-y", out}
}

func stampOf(fi os.FileInfo) string {
	return fmt.Sprintf("%d %d", fi.Size(), fi.ModTime().UnixNano())
}

// process extracts the pictures of one item unless they are up to date. A
// picture that cannot be taken (the position lies past the end of the video,
// or too close to its end for a frame) stops the item there, like the
// reference: the pictures before it are kept, the ones after it are not made.
// Such an item is not tried again until its file changes.
func (g *Generator) process(ctx context.Context, item storage.Item) {
	if len(item.Chapters) == 0 || item.Path == "" || !item.HasMediaFile() {
		return
	}
	fi, err := os.Stat(item.Path)
	if err != nil {
		return
	}
	dir := g.Dir(item.ID)
	stamp := stampOf(fi)
	if b, err := os.ReadFile(filepath.Join(dir, stampName)); err == nil && string(b) == stamp {
		complete := true
		if _, err := os.Stat(filepath.Join(dir, partialName)); err != nil {
			for _, c := range item.Chapters {
				if _, _, ok := g.Picture(item.ID, c); !ok {
					complete = false
					break
				}
			}
		}
		if complete {
			return
		}
	}
	select {
	case g.sem <- struct{}{}:
		defer func() { <-g.sem }()
	case <-ctx.Done():
		return
	}
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("chapterimg: %v", err)
		return
	}
	partial := false
	for _, c := range item.Chapters {
		if ctx.Err() != nil {
			_ = os.RemoveAll(dir)
			return
		}
		out := g.Path(item.ID, c.StartTicks)
		tmp := out + ".part.jpg"
		args := StillArgs(item.Path, c.StartTicks, tmp)
		secs := strconv.FormatFloat(float64(ExtractTicks(c))/float64(ticksPerSecond), 'f', 3, 64)
		if b, err := exec.CommandContext(ctx, g.ffmpeg, args...).CombinedOutput(); err != nil {
			if ctx.Err() != nil {
				_ = os.RemoveAll(dir)
				return
			}
			log.Printf("chapterimg: %s at %ss: %v: %s", filepath.Base(item.Path), secs, err, strings.TrimSpace(string(b)))
			partial = true
			break
		}
		if st, err := os.Stat(tmp); err != nil || st.Size() == 0 {
			// ffmpeg exits cleanly when the position is past the end, with
			// no frame written
			_ = os.Remove(tmp)
			partial = true
			break
		}
		if err := os.Rename(tmp, out); err != nil {
			_ = os.Remove(tmp)
			partial = true
			break
		}
	}
	if partial {
		_ = os.WriteFile(filepath.Join(dir, partialName), nil, 0o644)
	}
	_ = os.WriteFile(filepath.Join(dir, stampName), []byte(stamp), 0o644)
}
