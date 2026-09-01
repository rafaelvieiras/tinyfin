// Command tinyfin runs the Tinyfin server: API/metadata role, transcode
// worker role, and (single-node default) embedded storage, all in one
// process (docs/03).
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/api"
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

func main() {
	dataPath := flag.String("data", "tinyfin.db", "path to the SQLite database file")
	addr := flag.String("addr", ":8096", "address to listen on")
	webDir := flag.String("web-dir", envOrDefault("TINYFIN_WEB_DIR", "/web"),
		"directory containing a built jellyfin-web (dist/) to serve at /web/; "+
			"skipped if the directory doesn't exist. Overridable via TINYFIN_WEB_DIR.")
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

	server := api.NewServer(store, api.Config{ServerID: serverID})

	handler := server.Handler()
	if info, err := os.Stat(*webDir); err == nil && info.IsDir() {
		log.Printf("tinyfin: serving jellyfin-web from %s at /web/", *webDir)
		handler = withWebUI(handler, *webDir)
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
// rebuilds during staging left stale, mismatched-Id entries in a real
// device's WebView storage: server-side logs showed a fully successful
// login on every rebuild, but the app kept reopening its native
// "can't connect" screen anyway, because it was reading a leftover entry
// from a previous rebuild's (now-nonexistent) server Id.
func loadOrCreateServerID(dataPath string) (string, error) {
	idPath := dataPath + ".server-id"

	if b, err := os.ReadFile(idPath); err == nil {
		return string(b), nil
	} else if !os.IsNotExist(err) {
		return "", err
	}

	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	id := formatUUID(hex.EncodeToString(raw[:]))

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

// formatUUID inserts hyphens into a 32-character hex string at the
// canonical UUID positions (8-4-4-4-12) — see
// internal/storage/sqlite/id.go's copy of this same helper for why real
// Jellyfin clients require it.
func formatUUID(hex32 string) string {
	return hex32[0:8] + "-" + hex32[8:12] + "-" + hex32[12:16] + "-" + hex32[16:20] + "-" + hex32[20:32]
}
