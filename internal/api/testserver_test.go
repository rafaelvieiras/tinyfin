package api

import (
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// startTestServer is NewServer for tests: it registers Shutdown, so the socket
// watchdog, the library monitor, the task scheduler and any extraction batch
// stop before t.TempDir removes the directory they were writing under. A
// server left running there raced the removal (the CI failure this exists to
// prevent: "directory not empty" while t.TempDir cleaned up).
func startTestServer(t *testing.T, store storage.Store, cfg Config) *Server {
	t.Helper()
	srv := NewServer(store, cfg)
	t.Cleanup(srv.Shutdown)
	return srv
}
