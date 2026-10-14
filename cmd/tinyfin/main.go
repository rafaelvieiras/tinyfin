// Command tinyfin runs the Tinyfin server: API/metadata role, transcode
// worker role, and (single-node default) embedded storage, all in one
// process (docs/03).
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/api"
	"github.com/rafaelvieiras/tinyfin/internal/guid"
	"github.com/rafaelvieiras/tinyfin/internal/storage/sqlite"
)

// envOrDefault reads key from the environment, falling back to def if
// unset or empty — lets TINYFIN_WEB_DIR (an operational deployment knob,
// not application config) set a flag's default without a separate env-var
// parsing path for every flag.
func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envInt reads a non-negative integer env var, 0 when unset or invalid.
func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return def
}

func main() {
	dataPath := flag.String("data", "tinyfin.db", "path to the SQLite database file")
	addr := flag.String("addr", ":8096", "address to listen on")
	webDir := flag.String("web-dir", envOrDefault("TINYFIN_WEB_DIR", "/web"),
		"directory containing a built jellyfin-web (dist/) to serve at /web/; "+
			"skipped if the directory doesn't exist. Overridable via TINYFIN_WEB_DIR.")
	// Default matches the Dockerfile's COPY destination for the bundled
	// jellyfin-ffmpeg portable build (docs/00-decision-log.md's D10) — if
	// that path doesn't actually exist (a non-Docker/local dev run), main
	// falls back to internal/transcode.Config's own "ffmpeg on $PATH"
	// behavior below rather than pointing it at a file that isn't there.
	ffmpegPath := flag.String("ffmpeg-path", envOrDefault("TINYFIN_FFMPEG_PATH", "/usr/lib/jellyfin-ffmpeg/ffmpeg"),
		"path to the jellyfin-ffmpeg binary (docs/07's transcode worker role); "+
			"falls back to \"ffmpeg\" on $PATH if this path doesn't exist. Overridable via TINYFIN_FFMPEG_PATH.")
	// jellyfin-ffmpeg's portable build (D10, same bundle as ffmpegPath
	// above) ships ffprobe alongside ffmpeg in the same directory — reuse
	// that install rather than requiring a second explicit flag/env var
	// pointing at what is, in practice, always the same directory.
	ffprobePath := flag.String("ffprobe-path", envOrDefault("TINYFIN_FFPROBE_PATH", "/usr/lib/jellyfin-ffmpeg/ffprobe"),
		"path to the ffprobe binary internal/scan uses to catalog media info (docs/08's scanner); "+
			"falls back to \"ffprobe\" on $PATH if this path doesn't exist. Overridable via TINYFIN_FFPROBE_PATH.")
	tmdbAPIKey := flag.String("tmdb-api-key", envOrDefault("TINYFIN_TMDB_API_KEY", ""),
		"TMDB API key for the direct metadata provider (docs/08's D3 reference provider); "+
			"empty disables remote enrichment — the scanner still catalogs every item from "+
			"filename-derived data alone (internal/scan's package doc comment). Overridable via TINYFIN_TMDB_API_KEY.")
	noQuickConnect := flag.Bool("no-quickconnect", envOrDefault("TINYFIN_NO_QUICKCONNECT", "") != "",
		"disable the QuickConnect sign-in flow (enabled by default). Set TINYFIN_NO_QUICKCONNECT to any non-empty value for the same effect.")
	scanConc := flag.Int("scan-concurrency", envInt("TINYFIN_SCAN_CONCURRENCY", 0),
		"library scan workers per library (default 8; I/O bound, raise for slow remote filesystems). Overridable via TINYFIN_SCAN_CONCURRENCY.")
	scanProbeConc := flag.Int("scan-probe-concurrency", envInt("TINYFIN_SCAN_PROBE_CONCURRENCY", 0),
		"max simultaneous ffprobe processes across all scans (default: same as scan concurrency). Overridable via TINYFIN_SCAN_PROBE_CONCURRENCY.")
	scanLibPar := flag.Int("scan-library-parallelism", envInt("TINYFIN_SCAN_LIBRARY_PARALLELISM", 0),
		"libraries scanned at the same time during a refresh (default 3). Overridable via TINYFIN_SCAN_LIBRARY_PARALLELISM.")
	scanForceProbe := flag.Bool("scan-force-probe", envOrDefault("TINYFIN_SCAN_FORCE_PROBE", "") != "",
		"re-run ffprobe on every file even when size and mtime are unchanged. Overridable via TINYFIN_SCAN_FORCE_PROBE.")
	flag.Parse()

	store, err := sqlite.New(*dataPath)
	if err != nil {
		log.Fatalf("tinyfin: opening storage at %s: %v", *dataPath, err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			log.Printf("tinyfin: closing storage: %v", err)
		}
	}()

	serverID, err := loadOrCreateServerID(*dataPath)
	if err != nil {
		log.Fatalf("tinyfin: loading server ID: %v", err)
	}

	resolvedFFmpegPath := ""
	if info, err := os.Stat(*ffmpegPath); err == nil && !info.IsDir() {
		resolvedFFmpegPath = *ffmpegPath
	} else {
		log.Printf("tinyfin: %s not found, falling back to \"ffmpeg\" on $PATH", *ffmpegPath)
	}

	resolvedFFprobePath := ""
	if info, err := os.Stat(*ffprobePath); err == nil && !info.IsDir() {
		resolvedFFprobePath = *ffprobePath
	} else {
		log.Printf("tinyfin: %s not found, falling back to \"ffprobe\" on $PATH", *ffprobePath)
	}

	// Cached scan-time artwork lives next to the database, not inside the
	// data directory (which the sqlite package treats as one file, not a
	// directory of its own) — same relationship loadOrCreateServerID's
	// sidecar file already has to *dataPath.
	scanImageDir := *dataPath + ".images"

	resolvedWebDir := ""
	if info, err := os.Stat(*webDir); err == nil && info.IsDir() {
		resolvedWebDir = *webDir
	}

	server := api.NewServer(store, api.Config{
		ServerID:               serverID,
		TranscodeFFmpegPath:    resolvedFFmpegPath,
		ScanFFprobePath:        resolvedFFprobePath,
		ScanImageDir:           scanImageDir,
		ScanConcurrency:        *scanConc,
		ScanProbeConcurrency:   *scanProbeConc,
		ScanLibraryParallelism: *scanLibPar,
		ScanForceProbe:         *scanForceProbe,
		TMDBAPIKey:             *tmdbAPIKey,
		DataDir:                filepath.Dir(*dataPath),
		WebDir:                 resolvedWebDir,
		QuickConnectDisabled:   *noQuickConnect,
	})

	handler := server.Handler()
	if resolvedWebDir != "" {
		log.Printf("tinyfin: serving jellyfin-web from %s at /web/", resolvedWebDir)
		handler = withWebUI(handler, resolvedWebDir)
	}

	httpServer := &http.Server{
		Addr:    *addr,
		Handler: handler,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		log.Printf("tinyfin: listening on %s (data: %s)", *addr, *dataPath)
		serveErr <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("tinyfin: server error: %v", err)
		}
	case <-ctx.Done():
		log.Print("tinyfin: shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("tinyfin: shutdown error: %v", err)
		}
	}
}

// loadOrCreateServerID reads the server's persistent identity from a
// sidecar file next to dataPath (e.g. "/data/tinyfin.db" ->
// "/data/tinyfin.db.server-id"), minting and persisting a new one on first
// run. This exists because api.Config.ServerID, left empty, mints a random
// one for the life of the process (server.go's doc comment) — fine for a
// single long-lived run, but every restart would then present a different
// Id to clients.
//
// That instability isn't cosmetic: real Jellyfin clients (jellyfin-web's
// legacy jellyfin-apiclient, credentials.js) key their stored server list
// by AuthenticationResult.ServerId, merging into an existing entry only on
// an exact Id match — a changed Id makes them append a new entry instead of
// updating the old one. The official Android app's WebView shell then
// reads back element [0] of that list (not the most recently used one) to
// decide whether the current session is valid, right after login. Found by
// reading jellyfin-android's JellyfinWebViewClient.kt and
// jellyfin-apiclient-javascript's credentials.js after repeated container
// rebuilds left stale, mismatched-Id entries in a real
// device's WebView storage: server-side logs showed a fully successful
// login on every rebuild, but the app kept reopening its native
// "can't connect" screen anyway, because it was reading a leftover entry
// from a previous rebuild's (now-nonexistent) server Id.
func loadOrCreateServerID(dataPath string) (string, error) {
	idPath := dataPath + ".server-id"

	if b, err := os.ReadFile(idPath); err == nil {
		// An id persisted in the older dashed form is folded to the
		// canonical 32-hex wire form (wire.id.R1).
		if n, ok := guid.Normalize(string(b)); ok {
			return n, nil
		}
		return string(b), nil
	} else if !os.IsNotExist(err) {
		return "", err
	}

	id := guid.New()

	if dir := filepath.Dir(idPath); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(idPath, []byte(id), 0o600); err != nil {
		return "", err
	}
	return id, nil
}
