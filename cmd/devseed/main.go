// Command devseed is a throwaway manual-testing helper — NOT part of
// Tinyfin. It fakes what the future scanner (docs/08) will do: it walks a
// real movie folder on the host, ffprobes each file, and writes library
// and item rows straight into a tinyfin.db so the API layer has something
// real to serve while testing against an actual Jellyfin client. This file
// is not committed to the repository.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/rafaelvieiras/tinyfin/internal/guid"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
	"github.com/rafaelvieiras/tinyfin/internal/storage/sqlite"
)

// deterministicID derives a stable id from seed so re-seeding the same
// folder upserts instead of duplicating.
func deterministicID(seed string) string { return guid.Derive(seed) }

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
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_format", "-show_streams", path)
	data, err := cmd.Output()
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, err
	}
	return out, nil
}

func main() {
	hostDir := flag.String("host-dir", "", "host path to the movies folder (used to read/ffprobe files)")
	containerDir := flag.String("container-dir", "/media/movies", "path the same folder is mounted at inside the container (stored in Item.Path)")
	dbPath := flag.String("db", "", "path to the tinyfin sqlite file to seed")
	limit := flag.Int("limit", 6, "max number of movie folders to seed")
	username := flag.String("username", "admin", "admin username to create")
	password := flag.String("password", "", "admin password to create (required)")
	flag.Parse()

	if *hostDir == "" || *dbPath == "" || *password == "" {
		log.Fatal("devseed: -host-dir, -db and -password are required")
	}

	store, err := sqlite.New(*dbPath)
	if err != nil {
		log.Fatalf("devseed: opening store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	hash, err := bcrypt.GenerateFromPassword([]byte(*password), bcrypt.DefaultCost)
	if err != nil {
		log.Fatalf("devseed: hashing password: %v", err)
	}
	userID, err := store.CreateUser(ctx, storage.User{
		Name:            *username,
		PasswordHash:    string(hash),
		IsAdministrator: true,
	})
	if err != nil {
		log.Fatalf("devseed: creating user: %v", err)
	}
	log.Printf("devseed: created user %q (%s)", *username, userID)

	lib := storage.Library{
		ID:             storage.LibraryID(deterministicID("lib-movies")),
		Name:           "Movies",
		CollectionType: storage.CollectionTypeMovies,
		Paths:          []string{*containerDir},
	}
	if err := store.UpsertLibrary(ctx, lib); err != nil {
		log.Fatalf("devseed: upserting library: %v", err)
	}
	log.Printf("devseed: created library %q", lib.ID)

	entries, err := os.ReadDir(*hostDir)
	if err != nil {
		log.Fatalf("devseed: reading %s: %v", *hostDir, err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	seeded := 0
	for _, e := range entries {
		if seeded >= *limit || !e.IsDir() {
			continue
		}
		folder := e.Name()
		hostFolder := filepath.Join(*hostDir, folder)

		videoFile, size := largestVideoFile(hostFolder)
		if videoFile == "" {
			log.Printf("devseed: skipping %q, no video file found", folder)
			continue
		}

		name, year := parseNameYear(folder)
		itemID := storage.ItemID(deterministicID("item-" + slug(folder)))
		containerPath := filepath.Join(*containerDir, folder, videoFile)

		info, err := probe(filepath.Join(hostFolder, videoFile))
		if err != nil {
			log.Printf("devseed: ffprobe failed for %q: %v (seeding without media streams)", folder, err)
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

		item := storage.Item{
			ID:           itemID,
			Type:         storage.ItemTypeMovie,
			LibraryID:    lib.ID,
			Name:         name,
			SortName:     name,
			Path:         containerPath,
			Container:    strings.TrimPrefix(filepath.Ext(videoFile), "."),
			Size:         size,
			MediaStreams: streams,
			RunTimeTicks: runtimeTicks,
			DateCreated:  time.Now().UTC(),
		}
		if year > 0 {
			item.ProductionYear = &year
		}
		if err := store.UpsertItem(ctx, item); err != nil {
			log.Printf("devseed: upserting item %q: %v", folder, err)
			continue
		}

		seedImage(ctx, store, itemID, hostFolder, *containerDir, folder, "poster.jpg", "Primary")
		seedImage(ctx, store, itemID, hostFolder, *containerDir, folder, "fanart.jpg", "Backdrop")

		log.Printf("devseed: seeded %q as %s (container=%s, %d streams)", name, itemID, item.Container, len(streams))
		seeded++
	}

	log.Printf("devseed: done, %d items seeded", seeded)
}

func seedImage(ctx context.Context, store *sqlite.Store, itemID storage.ItemID, hostFolder, containerDir, folder, filename, kind string) {
	hostPath := filepath.Join(hostFolder, filename)
	fi, err := os.Stat(hostPath)
	if err != nil {
		return
	}
	ref := storage.ImageRef{
		ItemID: itemID,
		Kind:   storage.ImageKind(kind),
		Path:   filepath.Join(containerDir, folder, filename),
		Size:   fi.Size(),
	}
	if err := store.UpsertImageRef(ctx, ref); err != nil {
		log.Printf("devseed: upserting %s image for %s: %v", kind, itemID, err)
	}
}

var videoExts = map[string]bool{".mkv": true, ".mp4": true, ".avi": true, ".webm": true, ".m4v": true}

func largestVideoFile(dir string) (string, int64) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", 0
	}
	var best string
	var bestSize int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !videoExts[strings.ToLower(filepath.Ext(e.Name()))] {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		if fi.Size() > bestSize {
			best, bestSize = e.Name(), fi.Size()
		}
	}
	return best, bestSize
}

// parseNameYear splits "Title (Year)" into its two parts, the same
// convention the folders on disk already follow.
func parseNameYear(folder string) (string, int) {
	if i := strings.LastIndex(folder, " ("); i > 0 && strings.HasSuffix(folder, ")") {
		yearStr := folder[i+2 : len(folder)-1]
		if year, err := strconv.Atoi(yearStr); err == nil {
			return folder[:i], year
		}
	}
	return folder, 0
}

func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(fmt.Sprintf("%s", b.String()), "-")
}
