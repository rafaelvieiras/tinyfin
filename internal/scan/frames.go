package scan

import (
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// Items with a video and no picture of their own get a Primary picture taken
// from the video (docs/contract/specs/images.md, images.frame.*): one frame
// at full size, so a card or a detail page always has something to show.

// frameDirName is the directory of imageDir that holds the extracted frames.
const frameDirName = "frames"

// frameSeconds is the position a frame is taken at: a little into the
// video, further for a longer one (about a tenth of it plus a fixed
// offset), and never at or past its end.
func frameSeconds(runTimeTicks *int64) float64 {
	if runTimeTicks == nil {
		return 0
	}
	rt := float64(*runTimeTicks) / 1e7
	if rt <= 0 {
		return 0
	}
	t := 0.25 + 0.11*rt
	if limit := rt * 0.9; t > limit {
		t = limit
	}
	return t
}

func (s *Scanner) framePath(itemID storage.ItemID) string {
	return filepath.Join(s.imageDir, frameDirName, string(itemID)+".jpg")
}

// isFrameImage reports whether path is a frame this scanner extracted.
func (s *Scanner) isFrameImage(path string) bool {
	return s.imageDir != "" && strings.HasPrefix(path, filepath.Join(s.imageDir, frameDirName)+string(filepath.Separator))
}

// seedVideoFrames gives every movie, episode, home video, music video and
// video extra (trailer, featurette, theme video, additional part) of
// lib that has no Primary picture one extracted from its video. A frame made
// by an earlier scan is kept while the video file is unchanged; a picture
// that sits beside the media, or that a provider supplied, always wins and
// replaces a frame.
func (s *Scanner) seedVideoFrames(ctx context.Context, lib storage.Library) {
	if s.imageDir == "" {
		return
	}
	id := lib.ID
	var todo []storage.Item
	for off := 0; ctx.Err() == nil; off += 500 {
		page, err := s.store.QueryItems(ctx, storage.ItemFilter{
			LibraryID: &id, Recursive: true, Limit: 500, Offset: off, IncludeExtras: true,
			ItemTypes: []storage.ItemType{storage.ItemTypeMovie, storage.ItemTypeEpisode, storage.ItemTypeVideo, storage.ItemTypeMusicVideo, storage.ItemTypeTrailer},
		})
		if err != nil {
			log.Printf("scan: listing library %q for frames: %v", lib.Name, err)
			return
		}
		for _, it := range page.Items {
			if it.HasMediaFile() && s.needsFrame(ctx, it) {
				todo = append(todo, it)
			}
		}
		if len(page.Items) < 500 {
			break
		}
	}
	if len(todo) == 0 {
		return
	}
	workers := s.concurrency
	if workers > len(todo) {
		workers = len(todo)
	}
	jobs := make(chan storage.Item)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := range jobs {
				s.extractFrame(ctx, it)
			}
		}()
	}
	for _, it := range todo {
		if ctx.Err() != nil {
			break
		}
		jobs <- it
	}
	close(jobs)
	wg.Wait()
}

// needsFrame reports whether the item has no Primary picture, or only a frame
// that is out of date.
func (s *Scanner) needsFrame(ctx context.Context, it storage.Item) bool {
	refs, err := s.store.GetImageRefs(ctx, it.ID, storage.ImageKind("Primary"))
	if err != nil {
		return false
	}
	if len(refs) == 0 {
		return true
	}
	if !s.isFrameImage(refs[0].Path) {
		return false
	}
	fv, err := os.Stat(it.Path)
	if err != nil {
		return false
	}
	ff, err := os.Stat(refs[0].Path)
	return err != nil || ff.ModTime().Before(fv.ModTime())
}

// extractFrame writes the frame file and stores it as the item's Primary.
func (s *Scanner) extractFrame(ctx context.Context, it storage.Item) {
	out := s.framePath(it.ID)
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return
	}
	tmp := out + ".part.jpg"
	cctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	args := []string{"-v", "error", "-nostdin", "-threads", "1",
		"-ss", strconv.FormatFloat(frameSeconds(it.RunTimeTicks), 'f', 3, 64), "-i", it.Path,
		"-an", "-sn", "-frames:v", "1", "-q:v", "4", "-y", tmp}
	if b, err := exec.CommandContext(cctx, s.ffmpegPath(), args...).CombinedOutput(); err != nil {
		if ctx.Err() == nil {
			log.Printf("scan: frame of %q: %v: %s", filepath.Base(it.Path), err, strings.TrimSpace(string(b)))
		}
		_ = os.Remove(tmp)
		return
	}
	if st, err := os.Stat(tmp); err != nil || st.Size() == 0 {
		_ = os.Remove(tmp)
		return
	}
	if err := os.Rename(tmp, out); err != nil {
		_ = os.Remove(tmp)
		return
	}
	fi, err := os.Stat(out)
	if err != nil {
		return
	}
	width, height := imageFileDimensions(out)
	ref := storage.ImageRef{
		ID:     storage.ImageRefID(deterministicID("image-" + string(it.ID) + "-Primary")),
		ItemID: it.ID, Kind: storage.ImageKind("Primary"),
		Path: out, Tag: imageTag(out, fi.Size(), fi.ModTime().UnixNano()), BlurHash: imageFileBlurHash(out),
		Size: fi.Size(), Width: width, Height: height,
	}
	if err := s.store.UpsertImageRef(ctx, ref); err != nil {
		log.Printf("scan: storing frame for %s: %v", it.ID, err)
	}
}
