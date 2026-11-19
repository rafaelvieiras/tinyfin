package api

import (
	"context"
	"encoding/json"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/scan"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// library_monitor.go: the real-time monitor (spec: scan.monitor.*). A library
// whose LibraryOptions carry EnableRealtimeMonitor true has its folders
// watched; when something in a first-level folder changed and stayed quiet
// for monitorQuiet, that folder is re-walked. Off by default (the option has
// to be sent), one inotify watch per folder, nothing is read or kept
// otherwise.

const (
	// monitorQuiet is how long a folder must have been unchanged before it is
	// re-walked: files are usually still being copied when the first event
	// arrives.
	monitorQuiet = 30 * time.Second
	// monitorReconcile is how often the set of watched libraries is brought in
	// line with the libraries and their options.
	monitorReconcile = 20 * time.Second
)

// realtimeMonitorOn reports whether lib's options turn the monitor on.
func realtimeMonitorOn(lib storage.Library) bool {
	switch lib.CollectionType {
	case storage.CollectionTypeMovies, storage.CollectionTypeTVShows, storage.CollectionTypeHomeVideos, storage.CollectionTypeMusicVideos:
	default:
		return false
	}
	var o struct {
		EnableRealtimeMonitor bool `json:"EnableRealtimeMonitor"`
	}
	return len(lib.LibraryOptions) > 0 && json.Unmarshal(lib.LibraryOptions, &o) == nil && o.EnableRealtimeMonitor
}

type watched struct {
	signature string
	monitor   *scan.Monitor
}

// runLibraryMonitor keeps one scan.Monitor per library that asks for one.
func (s *Server) runLibraryMonitor(ctx context.Context) {
	if !scan.MonitorSupported {
		return
	}
	active := map[storage.LibraryID]*watched{}
	defer func() {
		for _, w := range active {
			w.monitor.Close()
		}
	}()
	tick := time.NewTicker(monitorReconcile)
	defer tick.Stop()
	for {
		libs, err := s.store.QueryLibraries(ctx)
		if err == nil {
			seen := map[storage.LibraryID]bool{}
			for _, lib := range libs {
				if !realtimeMonitorOn(lib) {
					continue
				}
				seen[lib.ID] = true
				sig := strings.Join(lib.Paths, "\x00")
				if w := active[lib.ID]; w != nil {
					if w.signature == sig {
						continue
					}
					w.monitor.Close()
					delete(active, lib.ID)
				}
				id := lib.ID
				m, err := scan.NewMonitor(lib.Paths, monitorQuiet, func(root, top string) { s.rescanChanged(id, root, top) })
				if err != nil {
					log.Printf("api: monitoring library %q: %v", lib.Name, err)
					continue
				}
				active[lib.ID] = &watched{signature: sig, monitor: m}
			}
			for id, w := range active {
				if !seen[id] {
					w.monitor.Close()
					delete(active, id)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-s.monitorKick:
		}
	}
}

// kickMonitor makes the monitor reconcile its watched libraries now instead
// of at the next tick, so a library created or changed starts being watched
// before the first change under it (a change made between the library's
// creation and the next tick would otherwise be missed).
func (s *Server) kickMonitor() {
	select {
	case s.monitorKick <- struct{}{}:
	default:
	}
}

// scanTracked walks a library and queues what the walk changed, so a change
// the monitor found is pushed exactly like one an explicit scan found. Split
// out from rescanChanged so the tracking is testable without a media tree.
func (s *Server) scanTracked(ctx context.Context, lib storage.Library, walk func() error) error {
	var err error
	s.trackLibraryChanges(ctx, lib.ID, func() { err = walk() })
	return err
}

// rescanChanged re-walks the part of a library a monitor reported. The walk
// is tracked like any other scan: what the monitor finds is a change the
// clients have to hear about (LibraryChanged), and a change picked up here and
// then not pushed was lost for good, because the explicit scan that follows
// finds nothing new to report.
func (s *Server) rescanChanged(id storage.LibraryID, root, top string) {
	ctx := context.Background()
	lib, err := s.store.GetLibrary(ctx, id)
	if err != nil || !realtimeMonitorOn(lib) {
		return
	}
	scanErr := s.scanTracked(ctx, lib, func() error {
		return s.scanner.RescanPath(ctx, lib, filepath.Clean(root), top)
	})
	if scanErr != nil {
		log.Printf("api: rescanning %q after a change in %s: %v", lib.Name, filepath.Join(root, top), scanErr)
	}
}
