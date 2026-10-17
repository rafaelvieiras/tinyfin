package transcode

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
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

// kfEntry is a cached key frame scan of one file.
type kfEntry struct {
	size  int64
	mtime int64
	times []float64
}

// kfCache holds the key frame scans: in memory, and on disk under the job
// directory's parent so a restart does not pay for the scan again.
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
// path, ascending. The scan reads every packet header of the file, so the
// result is cached by path, size and modification time (a path that is not a
// local file is not scanned: an error).
func (m *Manager) KeyFrames(ctx context.Context, path string) ([]float64, error) {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return nil, fmt.Errorf("transcode: key frames: not a local file")
	}
	c := &m.kf
	c.mu.Lock()
	if e := c.entries[path]; e != nil && e.size == st.Size() && e.mtime == st.ModTime().UnixNano() {
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

	times, err := m.loadKeyFrames(path, st.Size(), st.ModTime().UnixNano())
	c.mu.Lock()
	delete(c.flights, path)
	if err == nil {
		c.entries[path] = &kfEntry{size: st.Size(), mtime: st.ModTime().UnixNano(), times: times}
	}
	c.mu.Unlock()
	f.times, f.err = times, err
	close(f.done)
	return times, err
}

func (m *Manager) kfFile(path string) string {
	sum := sha1.Sum([]byte(path))
	return filepath.Join(m.cfg.baseDir(), "keyframes", hex.EncodeToString(sum[:8])+".kf")
}

// loadKeyFrames reads the scan from disk, else runs ffprobe (the scan is not
// tied to the request that asked: it finishes and is cached even if that
// request goes away).
func (m *Manager) loadKeyFrames(path string, size, mtime int64) ([]float64, error) {
	file := m.kfFile(path)
	if data, err := os.ReadFile(file); err == nil && len(data) >= 16 &&
		int64(binary.LittleEndian.Uint64(data[0:8])) == size && int64(binary.LittleEndian.Uint64(data[8:16])) == mtime {
		var out []float64
		var cur int64
		rest := data[16:]
		for len(rest) > 0 {
			d, n := binary.Uvarint(rest)
			if n <= 0 {
				out = nil
				break
			}
			rest = rest[n:]
			cur += int64(d)
			out = append(out, float64(cur)/ticksPerSecond)
		}
		if len(out) > 0 {
			return out, nil
		}
	}
	times, err := m.scanKeyFrames(path)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, 16, 16+len(times)*3)
	binary.LittleEndian.PutUint64(buf[0:8], uint64(size))
	binary.LittleEndian.PutUint64(buf[8:16], uint64(mtime))
	var prev int64
	for _, t := range times {
		ticks := int64(t*ticksPerSecond + 0.5)
		buf = binary.AppendUvarint(buf, uint64(ticks-prev))
		prev = ticks
	}
	if os.MkdirAll(filepath.Dir(file), 0o755) == nil {
		tmp := file + ".tmp"
		if os.WriteFile(tmp, buf, 0o644) == nil {
			_ = os.Rename(tmp, file)
		}
	}
	return times, nil
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
