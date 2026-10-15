//go:build linux

package scan

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// monitor_linux.go: the real-time monitor of a library (spec: scan.monitor.*).
// One inotify watch per folder below the library's folders; a change marks
// the first-level folder of the library it happened in and, once that folder
// has been quiet for a while (a file is probably still being copied), the
// owner is told to re-walk it.

// monitorMask is what is watched for.
const monitorMask = unix.IN_CREATE | unix.IN_DELETE | unix.IN_MOVED_FROM | unix.IN_MOVED_TO |
	unix.IN_CLOSE_WRITE | unix.IN_MODIFY | unix.IN_DELETE_SELF | unix.IN_MOVE_SELF

// MonitorSupported reports whether this build can watch folders.
const MonitorSupported = true

type changeKey struct{ root, top string }

// Monitor watches the folders of a library.
type Monitor struct {
	fd       int
	file     *os.File
	roots    []string
	quiet    time.Duration
	onChange func(root, top string)

	mu      sync.Mutex
	wds     map[int]string // watch descriptor -> folder
	pending map[changeKey]time.Time
	closed  bool
	done    chan struct{}
}

// NewMonitor starts watching every folder under roots. onChange is called,
// one call at a time, with a library folder and the name of the entry directly
// below it (empty when the change was in the folder itself) after that entry
// has had no change for quiet.
func NewMonitor(roots []string, quiet time.Duration, onChange func(root, top string)) (*Monitor, error) {
	fd, err := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
	if err != nil {
		return nil, err
	}
	m := &Monitor{
		fd: fd, file: os.NewFile(uintptr(fd), "inotify"), roots: roots, quiet: quiet, onChange: onChange,
		wds: map[int]string{}, pending: map[changeKey]time.Time{}, done: make(chan struct{}),
	}
	failed := 0
	for _, r := range roots {
		if n := m.addTree(filepath.Clean(r)); n > 0 {
			failed += n
		}
	}
	if failed > 0 {
		log.Printf("scan: monitor: %d folder(s) could not be watched (inotify limit?)", failed)
	}
	go m.readLoop()
	go m.flushLoop()
	return m, nil
}

// Close stops the monitor.
func (m *Monitor) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.mu.Unlock()
	_ = m.file.Close()
	close(m.done)
}

// addTree watches dir and every folder below it; it returns how many could not
// be watched.
func (m *Monitor) addTree(dir string) (failed int) {
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if p != dir && fixedIgnoredDir(d.Name()) {
			return filepath.SkipDir
		}
		wd, err := unix.InotifyAddWatch(m.fd, p, monitorMask)
		if err != nil {
			failed++
			return nil
		}
		m.mu.Lock()
		m.wds[wd] = p
		m.mu.Unlock()
		return nil
	})
	return failed
}

// mark records a change at path.
func (m *Monitor) mark(path string) {
	for _, r := range m.roots {
		r = filepath.Clean(r)
		if !within(path, r) {
			continue
		}
		top := ""
		if path != r {
			rel := strings.TrimPrefix(path, r+string(os.PathSeparator))
			top = strings.SplitN(rel, string(os.PathSeparator), 2)[0]
		}
		m.mu.Lock()
		m.pending[changeKey{r, top}] = time.Now()
		m.mu.Unlock()
		return
	}
}

func (m *Monitor) readLoop() {
	buf := make([]byte, 64*1024)
	for {
		n, err := m.file.Read(buf)
		if err != nil {
			if errors.Is(err, os.ErrClosed) {
				return
			}
			select {
			case <-m.done:
				return
			default:
			}
			log.Printf("scan: monitor: %v", err)
			return
		}
		for off := 0; off+unix.SizeofInotifyEvent <= n; {
			ev := (*unix.InotifyEvent)(unsafe.Pointer(&buf[off]))
			name := ""
			if ev.Len > 0 {
				raw := buf[off+unix.SizeofInotifyEvent : off+unix.SizeofInotifyEvent+int(ev.Len)]
				if i := strings.IndexByte(string(raw), 0); i >= 0 {
					raw = raw[:i]
				}
				name = string(raw)
			}
			off += unix.SizeofInotifyEvent + int(ev.Len)
			m.mu.Lock()
			dir, ok := m.wds[int(ev.Wd)]
			if ev.Mask&unix.IN_IGNORED != 0 {
				delete(m.wds, int(ev.Wd))
			}
			m.mu.Unlock()
			if !ok {
				continue
			}
			path := dir
			if name != "" {
				path = filepath.Join(dir, name)
			}
			if ev.Mask&unix.IN_ISDIR != 0 && ev.Mask&(unix.IN_CREATE|unix.IN_MOVED_TO) != 0 {
				m.addTree(path)
			}
			// A change to a folder's own entry (name != "") is a change of
			// the folder; a change of a folder itself (delete/move self) is
			// one of its parent's entry.
			if name == "" {
				path = filepath.Dir(dir)
			}
			m.mark(path)
		}
	}
}

func (m *Monitor) flushLoop() {
	tick := time.NewTicker(m.quiet / 4)
	defer tick.Stop()
	for {
		select {
		case <-m.done:
			return
		case <-tick.C:
		}
		var due []changeKey
		m.mu.Lock()
		for k, t := range m.pending {
			if time.Since(t) >= m.quiet {
				due = append(due, k)
				delete(m.pending, k)
			}
		}
		m.mu.Unlock()
		for _, k := range due {
			m.onChange(k.root, k.top)
		}
	}
}
