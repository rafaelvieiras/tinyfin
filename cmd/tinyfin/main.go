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
	"os/signal"
	"syscall"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/api"
	"github.com/rafaelvieiras/tinyfin/internal/storage/sqlite"
)

func main() {
	dataPath := flag.String("data", "tinyfin.db", "path to the SQLite database file")
	addr := flag.String("addr", ":8096", "address to listen on")
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

	server := api.NewServer(store, api.Config{})

	httpServer := &http.Server{
		Addr:    *addr,
		Handler: server.Handler(),
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
