package scan

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
	"github.com/rafaelvieiras/tinyfin/internal/subtitle"
)

// subtitleTimeBase is the time base the reference reports for an external
// subtitle stream of a codec.
var subtitleTimeBase = map[string]string{
	"subrip": "1/1000",
	"ass":    "1/100",
	"webvtt": "1/1000",
	"sami":   "1/1000",
	"PGSSUB": "1/90000",
}

// externalSubtitles lists the subtitle files that sit beside the video at
// videoPath and belong to it, as subtitle streams numbered from 0 in the
// reference's order (file name, case-insensitive, punctuation first). It
// reads the folder through the scan's directory cache, so one listing serves
// every video of the folder and no file is stat'ed.
func externalSubtitles(ctx context.Context, videoPath string) []storage.MediaStreamRef {
	dir := filepath.Dir(videoPath)
	idx := dirIndexOf(ctx, dir)
	if idx == nil {
		return nil
	}
	base := filepath.Base(videoPath)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	var names []string
	for _, n := range idx.names {
		if _, ok := subtitle.ParseSidecar(base, n); ok {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.SliceStable(names, func(i, j int) bool { return subtitle.Compare(names[i], names[j]) < 0 })
	out := make([]storage.MediaStreamRef, 0, len(names))
	for i, n := range names {
		sc, _ := subtitle.ParseSidecar(base, n)
		out = append(out, storage.MediaStreamRef{
			Index:             i,
			Type:              storage.StreamTypeSubtitle,
			Codec:             sc.Codec,
			Language:          sc.Language,
			Title:             sc.Title,
			IsDefault:         sc.Default,
			IsForced:          sc.Forced,
			IsHearingImpaired: sc.HI,
			IsExternal:        true,
			Path:              filepath.Join(dir, n),
			TimeBase:          subtitleTimeBase[sc.Codec],
		})
	}
	return out
}

// stripExternalSubtitles removes the external subtitle streams from a
// stream list and renumbers what is left from zero (the embedded streams,
// as ffprobe listed them).
func stripExternalSubtitles(streams []storage.MediaStreamRef) []storage.MediaStreamRef {
	n := 0
	for _, s := range streams {
		if s.IsExternal && s.Type == storage.StreamTypeSubtitle {
			n++
		}
	}
	if n == 0 {
		return streams
	}
	out := make([]storage.MediaStreamRef, 0, len(streams)-n)
	for _, s := range streams {
		if s.IsExternal && s.Type == storage.StreamTypeSubtitle {
			continue
		}
		s.Index -= n
		out = append(out, s)
	}
	return out
}

// withExternalSubtitles puts the external subtitle streams in front of the
// embedded ones, which move up by their number. embedded must not contain
// external streams.
func withExternalSubtitles(embedded, external []storage.MediaStreamRef) []storage.MediaStreamRef {
	if len(external) == 0 {
		return embedded
	}
	out := make([]storage.MediaStreamRef, 0, len(external)+len(embedded))
	out = append(out, external...)
	for _, s := range embedded {
		s.Index += len(external)
		out = append(out, s)
	}
	return out
}

// RefreshExternalSubtitles re-reads the subtitle files beside the item's
// video and stores the item with the updated stream list. It is what an
// upload or a deletion of a subtitle file calls; a scan does the same work
// as part of cataloging.
func (s *Scanner) RefreshExternalSubtitles(ctx context.Context, itemID storage.ItemID) error {
	item, err := s.store.GetItem(ctx, itemID)
	if err != nil {
		return err
	}
	if !refreshSubtitles(ctx, &item) {
		return nil
	}
	return s.store.UpsertItem(ctx, item)
}

// refreshSubtitles recomputes item's external subtitle streams from the
// folder of its file and reports whether the stream list changed.
func refreshSubtitles(ctx context.Context, item *storage.Item) bool {
	if item.Path == "" || !item.HasMediaFile() {
		return false
	}
	switch item.Type {
	case storage.ItemTypeMovie, storage.ItemTypeEpisode, storage.ItemTypeVideo, storage.ItemTypeTrailer:
	default:
		return false
	}
	merged := withExternalSubtitles(stripExternalSubtitles(item.MediaStreams), externalSubtitles(ctx, item.Path))
	if sameStreams(merged, item.MediaStreams) {
		return false
	}
	item.MediaStreams = merged
	return true
}

func sameStreams(a, b []storage.MediaStreamRef) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.Index != y.Index || x.Type != y.Type || x.Codec != y.Codec || x.Path != y.Path || x.Language != y.Language ||
			x.Title != y.Title || x.IsDefault != y.IsDefault || x.IsForced != y.IsForced || x.IsHearingImpaired != y.IsHearingImpaired {
			return false
		}
	}
	return true
}
