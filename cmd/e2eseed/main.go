// Command e2eseed seeds a fresh Tinyfin database with fixed, synthetic
// library data for the Playwright e2e suite (e2e/). Unlike cmd/devseed
// (a throwaway helper that reads the operator's real movie drive and is
// deliberately not committed), this tool is part of the repo: it always
// writes the same known user/library/item, sourced from the small
// synthetic media fixtures under testdata/e2e/ rather than any real media
// file, so the test suite is reproducible on any machine (including CI)
// without depending on a particular person's drive layout or private
// files.
//
// The item's container/codec metadata (mp4/h264/aac) is deliberately
// chosen to match deviceprofile's Jellyfin-Web baseline (internal/
// deviceprofile), so the seeded item is actually Direct-Play-compatible
// with a browser client — the real HEVC/mkv files used for manual testing
// against the user's own library could never exercise that path, since no
// mainstream browser decodes HEVC natively.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"golang.org/x/crypto/bcrypt"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
	"github.com/rafaelvieiras/tinyfin/internal/storage/sqlite"
)

// Fixed, documented test credentials — not a secret, this is a throwaway
// database seeded fresh for every e2e run.
const (
	Username = "e2e"
	Password = "E2eTest123!"

	LibraryID = storage.LibraryID("lib-e2e-movies")
	ItemID    = storage.ItemID("item-e2e-sample")
)

type ffprobeOutput struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		Channels  int    `json:"channels"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

func probe(path string) (ffprobeOutput, error) {
	var out ffprobeOutput
	data, err := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_format", "-show_streams", path).Output()
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(data, &out)
	return out, err
}

func main() {
	dbPath := flag.String("db", "", "path to the sqlite file to seed (required)")
	fixturesDir := flag.String("fixtures-dir", "testdata/e2e", "directory containing sample.mp4 and poster.jpg")
	videoPath := flag.String("video-path", "", "path the video file is reachable at from the running server process (defaults to <fixtures-dir>/sample.mp4)")
	flag.Parse()

	if *dbPath == "" {
		log.Fatal("e2eseed: -db is required")
	}
	if *videoPath == "" {
		*videoPath = filepath.Join(*fixturesDir, "sample.mp4")
	}
	posterPath := filepath.Join(*fixturesDir, "poster.jpg")

	store, err := sqlite.New(*dbPath)
	if err != nil {
		log.Fatalf("e2eseed: opening store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	hash, err := bcrypt.GenerateFromPassword([]byte(Password), bcrypt.DefaultCost)
	if err != nil {
		log.Fatalf("e2eseed: hashing password: %v", err)
	}
	userID, err := store.CreateUser(ctx, storage.User{
		Name:            Username,
		PasswordHash:    string(hash),
		IsAdministrator: true,
	})
	if err != nil {
		log.Fatalf("e2eseed: creating user: %v", err)
	}
	log.Printf("e2eseed: created user %q (%s)", Username, userID)

	lib := storage.Library{
		ID:             LibraryID,
		Name:           "E2E Movies",
		CollectionType: storage.CollectionTypeMovies,
		Paths:          []string{*fixturesDir},
	}
	if err := store.UpsertLibrary(ctx, lib); err != nil {
		log.Fatalf("e2eseed: upserting library: %v", err)
	}
	log.Printf("e2eseed: created library %q", lib.ID)

	info, err := probe(filepath.Join(*fixturesDir, "sample.mp4"))
	if err != nil {
		log.Fatalf("e2eseed: ffprobe on fixture video failed: %v", err)
	}

	var streams []storage.MediaStreamRef
	idx := 0
	for _, s := range info.Streams {
		switch s.CodecType {
		case "video":
			w, h := s.Width, s.Height
			streams = append(streams, storage.MediaStreamRef{
				Index: idx, Type: storage.StreamTypeVideo, Codec: s.CodecName,
				IsDefault: true, Width: &w, Height: &h,
			})
			idx++
		case "audio":
			ch := s.Channels
			streams = append(streams, storage.MediaStreamRef{
				Index: idx, Type: storage.StreamTypeAudio, Codec: s.CodecName,
				IsDefault: true, Channels: &ch,
			})
			idx++
		}
	}

	var runtimeTicks *int64
	if d, err := strconv.ParseFloat(info.Format.Duration, 64); err == nil && d > 0 {
		ticks := int64(d * 10_000_000)
		runtimeTicks = &ticks
	}

	fi, err := os.Stat(filepath.Join(*fixturesDir, "sample.mp4"))
	if err != nil {
		log.Fatalf("e2eseed: stat fixture video: %v", err)
	}

	item := storage.Item{
		ID:           ItemID,
		Type:         storage.ItemTypeMovie,
		LibraryID:    lib.ID,
		Name:         "Tinyfin E2E Sample",
		SortName:     "Tinyfin E2E Sample",
		Path:         *videoPath,
		Container:    "mp4",
		Size:         fi.Size(),
		MediaStreams: streams,
		RunTimeTicks: runtimeTicks,
	}
	if err := store.UpsertItem(ctx, item); err != nil {
		log.Fatalf("e2eseed: upserting item: %v", err)
	}
	log.Printf("e2eseed: seeded item %q (%d streams)", item.ID, len(streams))

	posterInfo, err := os.Stat(posterPath)
	if err != nil {
		log.Fatalf("e2eseed: stat fixture poster: %v", err)
	}
	if err := store.UpsertImageRef(ctx, storage.ImageRef{
		ItemID: ItemID,
		Kind:   storage.ImageKind("Primary"),
		Path:   posterPath,
		Tag:    "e2e-poster",
		Size:   posterInfo.Size(),
	}); err != nil {
		log.Fatalf("e2eseed: upserting image ref: %v", err)
	}

	log.Print("e2eseed: done")
}
