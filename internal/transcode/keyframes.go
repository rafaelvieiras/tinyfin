package transcode

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// SegmentCuts lays a copied picture out in segments: the i-th segment ends at
// the first key frame at or after (i+1)*segSeconds that follows the previous
// cut, so a long gap between key frames makes one long segment and the
// following key frames, already past their grid line, each close one. It
// returns the cut points (100 ns ticks); the last segment runs to the end of
// the file. keyFrames are the key frame times in seconds, ascending.
func SegmentCuts(keyFrames []float64, segSeconds int) []int64 {
	if segSeconds <= 0 {
		segSeconds = defaultSegmentSeconds
	}
	cuts := []int64{} // empty, not nil: the scan worked and found no place to cut
	var last int64
	for _, k := range keyFrames {
		t := int64(k*ticksPerSecond + 0.5)
		due := int64(len(cuts)+1) * int64(segSeconds) * ticksPerSecond
		if t > last && t >= due {
			cuts = append(cuts, t)
			last = t
		}
	}
	return cuts
}

// KeyFrameStore is where a file's key frame times are kept between runs: the
// catalog database, passed in by the caller. It is optional — without one the
// times live in memory only — and it is keyed by catalog item, with the file's
// size and modification time as the guard, so a stale row is never used.
type KeyFrameStore interface {
	LoadKeyFrames(ctx context.Context, itemID string) (KeyFrameRecord, bool, error)
	SaveKeyFrames(ctx context.Context, itemID string, rec KeyFrameRecord) error
}

// KeyFrameRecord is one file's recorded key frame times: 100 ns ticks of the
// file's own time, ascending, with the size and modification time they were
// read at.
type KeyFrameRecord struct {
	Size  int64
	Mtime int64
	Ticks []int64
}

// kfEntry is a cached key frame scan of one file, held in memory for the life
// of the process.
type kfEntry struct {
	size  int64
	mtime int64
	times []float64
}

// kfCache holds the key frame scans in memory, plus one flight per path so two
// requests for the same file pay for one read.
type kfCache struct {
	mu      sync.Mutex
	entries map[string]*kfEntry
	flights map[string]*kfFlight
}

type kfFlight struct {
	done  chan struct{}
	times []float64
	err   error
}

func (c Config) ffprobePath() string {
	if c.FFprobePath != "" {
		return c.FFprobePath
	}
	if c.FFmpegPath != "" {
		sib := filepath.Join(filepath.Dir(c.FFmpegPath), "ffprobe")
		if st, err := os.Stat(sib); err == nil && !st.IsDir() {
			return sib
		}
	}
	return "ffprobe"
}

// KeyFrames returns the key frame times (seconds) of the first video stream of
// path, ascending. itemID is the catalog item the file belongs to and is what
// the persistent record is keyed by (an empty id keeps the answer in memory
// only).
//
// Four answers, cheapest first: this process's memory, the catalog's record of
// the file, the container's own index, and only when the container has no
// usable index the packet sweep, which reads every packet header of the file.
// The cache is keyed by path, size and modification time, so a file that
// changed is read again.
func (m *Manager) KeyFrames(ctx context.Context, itemID, path string) ([]float64, error) {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return nil, fmt.Errorf("transcode: key frames: not a local file")
	}
	size, mtime := st.Size(), st.ModTime().UnixNano()
	c := &m.kf
	c.mu.Lock()
	if e := c.entries[path]; e != nil && e.size == size && e.mtime == mtime {
		c.mu.Unlock()
		return e.times, nil
	}
	if f := c.flights[path]; f != nil {
		c.mu.Unlock()
		select {
		case <-f.done:
			return f.times, f.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f := &kfFlight{done: make(chan struct{})}
	if c.flights == nil {
		c.flights = map[string]*kfFlight{}
		c.entries = map[string]*kfEntry{}
	}
	c.flights[path] = f
	c.mu.Unlock()

	times, err := m.loadKeyFrames(ctx, itemID, path, size, mtime)
	c.mu.Lock()
	delete(c.flights, path)
	if err == nil {
		c.entries[path] = &kfEntry{size: size, mtime: mtime, times: times}
	}
	c.mu.Unlock()
	f.times, f.err = times, err
	close(f.done)
	return times, err
}

// loadKeyFrames answers a cache miss: the catalog's record of the file, else
// the container's index, else the packet sweep. A successful read is recorded
// in the catalog, so the next process does not pay for it again — a container
// recreate, which is what used to throw the answer away, now costs nothing.
func (m *Manager) loadKeyFrames(ctx context.Context, itemID, path string, size, mtime int64) ([]float64, error) {
	if times, ok := m.storedKeyFrames(ctx, itemID, size, mtime); ok {
		return times, nil
	}
	times, err := readKeyFrameIndex(path)
	if err != nil {
		times, err = m.scanKeyFrames(path)
		if err != nil {
			return nil, err
		}
	}
	m.saveKeyFrames(ctx, itemID, size, mtime, times)
	return times, nil
}

// storedKeyFrames returns the key frames the catalog recorded for the item, if
// they are still about this version of the file.
func (m *Manager) storedKeyFrames(ctx context.Context, itemID string, size, mtime int64) ([]float64, bool) {
	if m.cfg.KeyFrames == nil || itemID == "" {
		return nil, false
	}
	rec, ok, err := m.cfg.KeyFrames.LoadKeyFrames(ctx, itemID)
	if err != nil || !ok || rec.Size != size || rec.Mtime != mtime || len(rec.Ticks) == 0 {
		return nil, false
	}
	times := make([]float64, len(rec.Ticks))
	for i, t := range rec.Ticks {
		times[i] = float64(t) / ticksPerSecond
	}
	return times, true
}

// saveKeyFrames records the answer in the catalog. A failure here is not a
// failure of the request: the times are in memory for this process, and the
// next read simply pays for them again.
func (m *Manager) saveKeyFrames(ctx context.Context, itemID string, size, mtime int64, times []float64) {
	if m.cfg.KeyFrames == nil || itemID == "" || len(times) == 0 {
		return
	}
	rec := KeyFrameRecord{Size: size, Mtime: mtime, Ticks: make([]int64, len(times))}
	for i, t := range times {
		rec.Ticks[i] = int64(t*ticksPerSecond + 0.5)
	}
	_ = m.cfg.KeyFrames.SaveKeyFrames(ctx, itemID, rec)
}

// readKeyFrameIndex reads the key frame times from the container's own index
// (Matroska Cues): a few small reads, so the cost follows the number of key
// frames and not the size of the file. A container without an index is an
// error, and the caller sweeps the packets instead.
func readKeyFrameIndex(path string) ([]float64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	return matroskaKeyFrames(f, st.Size())
}

func (m *Manager) scanKeyFrames(path string) ([]float64, error) {
	cmd := exec.Command(m.cfg.ffprobePath(), "-v", "error", "-select_streams", "v:0",
		"-show_entries", "packet=pts_time,flags", "-of", "csv=p=0", path)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("transcode: key frames: %w", err)
	}
	var times []float64
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		ts, flags, ok := strings.Cut(sc.Text(), ",")
		if !ok || !strings.Contains(flags, "K") {
			continue
		}
		if v, err := strconv.ParseFloat(ts, 64); err == nil {
			times = append(times, v)
		}
	}
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("transcode: key frames: %w", err)
	}
	sort.Float64s(times)
	if len(times) == 0 {
		return nil, fmt.Errorf("transcode: key frames: none found")
	}
	return times, nil
}
