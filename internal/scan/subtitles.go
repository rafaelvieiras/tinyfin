package scan

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
	"github.com/rafaelvieiras/tinyfin/internal/subtitle"
)

// externalSubtitles lists the subtitle files that sit beside the video at
// videoPath and belong to it, as subtitle streams numbered from 0 in the
// reference's order (file name, case-insensitive, punctuation first). The
// folder comes from the scan's directory cache, so one listing serves every
// video of the folder.
//
// A file's streams come from probing its content, not from its extension: a
// ".srt" holding WebVTT is a webvtt track. The name still supplies the
// language (when the file carries none), the title and the default, forced
// and hearing-impaired flags. A VobSub ".idx" is listed once, and only when
// its ".sub" is there; a ".sub" that has an ".idx" is that pair's payload and
// is not listed on its own. A file ffprobe cannot read is not listed.
//
// prev returns the item's earlier streams: a file whose size and modified
// time are unchanged keeps its stored streams and is not probed again.
func (s *Scanner) externalSubtitles(ctx context.Context, videoPath string, prev func() []storage.MediaStreamRef) []storage.MediaStreamRef {
	dir := filepath.Dir(videoPath)
	idx := dirIndexOf(ctx, dir)
	if idx == nil {
		return nil
	}
	base := filepath.Base(videoPath)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	var names []string
	stems := map[string]bool{} // lower-cased "stem.ext" of every candidate
	for _, n := range idx.names {
		if _, ok := subtitle.ParseSidecar(base, n); ok {
			names = append(names, n)
			stems[strings.ToLower(n)] = true
		}
	}
	if len(names) == 0 {
		return nil
	}
	kept := names[:0]
	for _, n := range names {
		stem, ext := splitExt(n)
		switch ext {
		case "sub":
			if stems[stem+".idx"] {
				continue
			}
		case "idx":
			if !stems[stem+".sub"] {
				continue
			}
		}
		kept = append(kept, n)
	}
	names = kept
	sort.SliceStable(names, func(i, j int) bool { return subtitle.Compare(names[i], names[j]) < 0 })

	var cached map[string][]storage.MediaStreamRef
	loaded := false
	out := make([]storage.MediaStreamRef, 0, len(names))
	for _, n := range names {
		path := filepath.Join(dir, n)
		fi, err := os.Stat(path)
		if err != nil || fi.IsDir() {
			continue
		}
		size, mtime := fi.Size(), fi.ModTime().UnixNano()
		if !loaded && prev != nil {
			loaded = true
			cached = map[string][]storage.MediaStreamRef{}
			for _, st := range prev() {
				if st.IsExternal && st.Type == storage.StreamTypeSubtitle && st.FileMTimeNS != 0 {
					cached[st.Path] = append(cached[st.Path], st)
				}
			}
		}
		if old := cached[path]; len(old) > 0 && old[0].FileSize == size && old[0].FileMTimeNS == mtime {
			out = append(out, old...)
			continue
		}
		out = append(out, s.sidecarStreams(ctx, base, n, path, size, mtime)...)
	}
	for i := range out {
		out[i].Index = i
	}
	return out
}

// splitExt splits a lower-cased file name into the part before the last dot
// and the extension.
func splitExt(name string) (stem, ext string) {
	name = strings.ToLower(name)
	dot := strings.LastIndexByte(name, '.')
	if dot < 0 {
		return name, ""
	}
	return name[:dot], name[dot+1:]
}

// sidecarStreams builds the subtitle streams of one subtitle file.
func (s *Scanner) sidecarStreams(ctx context.Context, base, name, path string, size, mtime int64) []storage.MediaStreamRef {
	sc, _ := subtitle.ParseSidecar(base, name)
	var subs []storage.MediaStreamRef
	if info, err := s.probe(ctx, path); err == nil {
		// the language tags as the file spells them: a sidecar keeps "und",
		// which an embedded track does not
		var tags []string
		for _, st := range info.Streams {
			if st.CodecType == "subtitle" {
				tags = append(tags, st.Tags.Language)
			}
		}
		for _, st := range mediaStreamsFromProbe(info) {
			if st.Type == storage.StreamTypeSubtitle {
				if n := len(subs); n < len(tags) {
					st.Language = tags[n]
				}
				subs = append(subs, st)
			}
		}
	}
	if len(subs) == 0 {
		// ffprobe found no subtitle in it (it already weighs the extension
		// when it sniffs the content): the file is not listed.
		return nil
	}
	out := make([]storage.MediaStreamRef, 0, len(subs))
	for _, st := range subs {
		lang := st.Language
		if lang == "" {
			lang = sc.Language
		}
		title := st.Title
		if title == "" {
			title = sc.Title
		}
		out = append(out, storage.MediaStreamRef{
			Type:              storage.StreamTypeSubtitle,
			Codec:             st.Codec,
			Language:          lang,
			Title:             title,
			IsDefault:         sc.Default || (len(subs) > 1 && st.IsDefault),
			IsForced:          sc.Forced || st.IsForced,
			IsHearingImpaired: sc.HI || st.IsHearingImpaired,
			IsExternal:        true,
			Path:              path,
			TimeBase:          st.TimeBase,
			Width:             st.Width,
			Height:            st.Height,
			FileSize:          size,
			FileMTimeNS:       mtime,
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
	if !s.refreshSubtitles(ctx, &item) {
		return nil
	}
	return s.store.UpsertItem(ctx, item)
}

// refreshSubtitles recomputes item's external subtitle streams from the
// folder of its file and reports whether the stream list changed.
func (s *Scanner) refreshSubtitles(ctx context.Context, item *storage.Item) bool {
	if item.Path == "" || !item.HasMediaFile() {
		return false
	}
	switch item.Type {
	case storage.ItemTypeMovie, storage.ItemTypeEpisode, storage.ItemTypeVideo, storage.ItemTypeMusicVideo, storage.ItemTypeTrailer:
	default:
		return false
	}
	merged := withExternalSubtitles(stripExternalSubtitles(item.MediaStreams), s.externalSubtitles(ctx, item.Path, func() []storage.MediaStreamRef { return item.MediaStreams }))
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
			x.Title != y.Title || x.IsDefault != y.IsDefault || x.IsForced != y.IsForced || x.IsHearingImpaired != y.IsHearingImpaired ||
			x.TimeBase != y.TimeBase || x.FileSize != y.FileSize || x.FileMTimeNS != y.FileMTimeNS {
			return false
		}
	}
	return true
}
