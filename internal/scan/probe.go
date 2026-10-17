package scan

import (
	"context"
	"encoding/json"
	"math"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
	"github.com/rafaelvieiras/tinyfin/internal/subtitle"
)

// ffprobeStream is one entry of ffprobe -show_streams' JSON output. Field
// names/shapes (including Disposition and the string-typed
// Level/RFrames/SampleRate — ffprobe emits all three as JSON numbers, but
// also as empty strings for a stream where the value doesn't apply, so a
// plain int/float64 json tag would fail to unmarshal on those streams) are
// ffprobe's own documented -show_streams output, not anything specific to
// Jellyfin's reference server — public, well-known FFmpeg CLI behavior, not
// a clean-room concern (same reasoning routing.go's normalizePathCase doc
// comment already applies to ASP.NET Core's own case-insensitive routing).
type ffprobeStream struct {
	CodecType     string `json:"codec_type"`
	CodecName     string `json:"codec_name"`
	Profile       string `json:"profile"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	Channels      int    `json:"channels"`
	ChannelLayout string `json:"channel_layout"`
	// SampleRate: ffprobe's JSON output reports this as a numeric string
	// (e.g. "48000"), not a JSON number — well-known, documented FFmpeg CLI
	// output quirk, not a clean-room concern (see this type's doc comment).
	SampleRate         string `json:"sample_rate"`
	TimeBase           string `json:"time_base"`
	DisplayAspectRatio string `json:"display_aspect_ratio"`
	RefFrames          int    `json:"refs"`
	PixFmt             string `json:"pix_fmt"`
	// Level: -99 is ffprobe's own "not applicable" sentinel for a codec
	// with no level concept; 0 is indistinguishable from "field absent"
	// either way, so mediaStreamsFromProbe below only exposes a Level
	// pointer for a value outside {0, -99}.
	Level      int    `json:"level"`
	FieldOrder string `json:"field_order"`
	// BitRate is ffprobe's bit_rate (a numeric string, absent when the
	// container does not record one); AvgFrameRate a "num/den" fraction.
	BitRate       string `json:"bit_rate"`
	AvgFrameRate  string `json:"avg_frame_rate"`
	ColorTransfer string `json:"color_transfer"`
	Disposition   struct {
		Default  int `json:"default"`
		Forced   int `json:"forced"`
		Original int `json:"original"`
		// HearingImpaired is the disposition flag of an SDH track.
		HearingImpaired int `json:"hearing_impaired"`
		// AttachedPic marks a picture embedded in an audio file.
		AttachedPic int `json:"attached_pic"`
	} `json:"disposition"`
	// Tags.Title/Language: an embedded per-track title/language tag, when
	// the container carries one (e.g. Matroska's TrackEntry/Name and
	// Language elements) — confirmed load-bearing, not decorative, by
	// comparing DisplayTitle output byte-for-byte against the reference
	// server for a real file: its audio track's
	// embedded title ("Indefinido Estéreo") is exactly what puts that
	// stream's DisplayTitle on docs/12's "explicit Title" merge-rule branch
	// instead of the plain attribute-join branch — leaving these two
	// unextracted meant DisplayTitle never actually exercised that branch
	// for any real file.
	Tags streamTags `json:"tags"`
}

// streamTags are the tags of one stream: the title and language the stream
// listings use, and every tag by lower-cased name (an Ogg file carries the
// file's tags on its stream).
type streamTags struct {
	Title    string
	Language string
	All      map[string]string
}

func (t *streamTags) UnmarshalJSON(data []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil // not an object: no tags
	}
	t.All = make(map[string]string, len(raw))
	for k, v := range raw {
		if str, ok := v.(string); ok {
			t.All[strings.ToLower(k)] = str
		}
	}
	t.Title, t.Language = t.All["title"], t.All["language"]
	return nil
}

type ffprobeOutput struct {
	Streams  []ffprobeStream  `json:"streams"`
	Format   ffprobeFormat    `json:"format"`
	Chapters []ffprobeChapter `json:"chapters"`
}

// ffprobeChapter is one entry of ffprobe -show_chapters' JSON output.
type ffprobeChapter struct {
	StartTime string            `json:"start_time"`
	Tags      map[string]string `json:"tags"`
}

// chaptersFromProbe converts ffprobe's chapter list into storage chapters in
// start order. An entry whose start time does not parse is dropped; the title
// is the "title" tag whatever the case of its key.
func chaptersFromProbe(info ffprobeOutput) []storage.ChapterRef {
	var out []storage.ChapterRef
	for _, c := range info.Chapters {
		secs, err := strconv.ParseFloat(strings.TrimSpace(c.StartTime), 64)
		if err != nil || secs < 0 {
			continue
		}
		var name string
		for k, v := range c.Tags {
			if strings.EqualFold(k, "title") {
				name = strings.TrimSpace(v)
			}
		}
		out = append(out, storage.ChapterRef{StartTicks: int64(math.Round(secs * 1e7)), Name: name})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartTicks < out[j].StartTicks })
	return out
}

type ffprobeFormat struct {
	Duration   string `json:"duration"`
	FormatName string `json:"format_name"`
	// BitRate is the bit rate of the whole container (a numeric string).
	BitRate string            `json:"bit_rate"`
	Tags    map[string]string `json:"tags"`
}

// title returns the title tag of the container, whatever the case of its key.
func (f ffprobeFormat) title() string {
	for k, v := range f.Tags {
		if strings.EqualFold(k, "title") {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// probe runs ffprobe against path and returns its parsed JSON output. A
// probe failure (missing binary, corrupt/unreadable file) is returned to
// the caller, which — per Scan's doc comment — logs it into Stats.Errors
// and still catalogs the item (with empty MediaStreams/RunTimeTicks)
// rather than skipping it outright: a file a real client can still be
// told about, even imperfectly, beats one silently dropped from the
// catalog.
func (s *Scanner) probe(ctx context.Context, path string) (ffprobeOutput, error) {
	var out ffprobeOutput
	// Global bound on simultaneous ffprobe processes (scans may overlap).
	select {
	case s.probeSem <- struct{}{}:
		defer func() { <-s.probeSem }()
	case <-ctx.Done():
		return out, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	// -probesize/-analyzeduration are pinned to FFmpeg's own defaults (5 MB,
	// 5 s) so a remote file is never read further than that by a build with
	// different defaults; output fields are unchanged.
	cmd := exec.CommandContext(ctx, s.ffprobePath, "-v", "error", "-probesize", "5000000", "-analyzeduration", "5000000",
		"-print_format", "json", "-show_format", "-show_streams", "-show_chapters", path)
	data, err := cmd.Output()
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, err
	}
	return out, nil
}

// isInterlacedFieldOrder reports whether ffprobe's field_order value
// indicates an interlaced stream — "tt"/"bb"/"tb"/"bt" (top/bottom field
// first, in ffprobe's own naming). "progressive", "unknown", and the empty
// string (the field is entirely absent for many codecs/containers) all
// report false rather than guessing, same "don't guess past what the
// prober actually told us" convention the rest of this package already
// follows.
func isInterlacedFieldOrder(fieldOrder string) bool {
	switch fieldOrder {
	case "tt", "bb", "tb", "bt":
		return true
	default:
		return false
	}
}

// mediaStreamsFromProbe converts ffprobe's stream list into
// storage.MediaStreamRef entries for video, audio, and subtitle tracks.
// IsDefault/IsForced reflect each stream's own disposition.default/
// disposition.forced flag (ffprobe reports these per-stream — e.g. a file
// with two audio tracks can have disposition.default=1 on only one of
// them — rather than being hardcoded true for every stream of a kept
// type).
func mediaStreamsFromProbe(info ffprobeOutput) []storage.MediaStreamRef {
	var streams []storage.MediaStreamRef
	idx := 0
	for _, st := range info.Streams {
		if st.CodecType == "video" && st.Disposition.AttachedPic == 1 {
			// The cover of an audio file, not a video track.
			streams = append(streams, storage.MediaStreamRef{
				Index: idx, Type: storage.StreamTypeEmbeddedImage, Codec: st.CodecName,
				Title: st.Tags.Title,
			})
			idx++
			continue
		}
		switch st.CodecType {
		case "video":
			w, h := st.Width, st.Height
			ref := storage.MediaStreamRef{
				Index: idx, Type: storage.StreamTypeVideo, Codec: st.CodecName,
				IsDefault:     st.Disposition.Default == 1,
				IsForced:      st.Disposition.Forced == 1,
				Width:         &w,
				Height:        &h,
				Title:         st.Tags.Title,
				Language:      st.Tags.Language,
				Profile:       st.Profile,
				IsOriginal:    st.Disposition.Original != 0,
				IsInterlaced:  isInterlacedFieldOrder(st.FieldOrder),
				TimeBase:      st.TimeBase,
				AspectRatio:   st.DisplayAspectRatio,
				PixelFormat:   st.PixFmt,
				ColorTransfer: st.ColorTransfer,
			}
			if st.RefFrames > 0 {
				rf := st.RefFrames
				ref.RefFrames = &rf
			}
			// ffprobe's own "not applicable" sentinel (this type's doc
			// comment on Level) — only expose a value outside {0, -99}.
			if st.Level != 0 && st.Level != -99 {
				lvl := float64(st.Level)
				ref.Level = &lvl
			}
			if fr := parseFrameRate(st.AvgFrameRate); fr > 0 {
				ref.FrameRate = &fr
			}
			if br, err := strconv.ParseInt(st.BitRate, 10, 64); err == nil && br > 0 {
				ref.BitRate = &br
			}
			streams = append(streams, ref)
			idx++
		case "audio":
			ch := st.Channels
			ref := storage.MediaStreamRef{
				Index: idx, Type: storage.StreamTypeAudio, Codec: st.CodecName,
				IsDefault:     st.Disposition.Default == 1,
				IsForced:      st.Disposition.Forced == 1,
				Channels:      &ch,
				Title:         st.Tags.Title,
				Language:      st.Tags.Language,
				Profile:       st.Profile,
				ChannelLayout: st.ChannelLayout,
				IsOriginal:    st.Disposition.Original != 0,
			}
			if sr, err := strconv.Atoi(st.SampleRate); err == nil && sr > 0 {
				ref.SampleRate = &sr
			}
			if br, err := strconv.ParseInt(st.BitRate, 10, 64); err == nil && br > 0 {
				ref.BitRate = &br
			}
			streams = append(streams, ref)
			idx++
		case "data":
			streams = append(streams, storage.MediaStreamRef{
				Index: idx, Type: storage.StreamTypeData, Codec: st.CodecName,
				IsDefault: st.Disposition.Default == 1, IsForced: st.Disposition.Forced == 1,
				Title: st.Tags.Title, Language: st.Tags.Language,
			})
			idx++
		case "subtitle":
			lang := st.Tags.Language
			if strings.EqualFold(lang, "und") {
				// An undetermined language is no language for a subtitle track.
				lang = ""
			}
			ref := storage.MediaStreamRef{
				Index: idx, Type: storage.StreamTypeSubtitle, Codec: subtitle.NormalizeCodec(st.CodecName),
				IsDefault:         st.Disposition.Default == 1,
				IsForced:          st.Disposition.Forced == 1,
				IsHearingImpaired: st.Disposition.HearingImpaired == 1,
				Title:             st.Tags.Title,
				Language:          lang,
				IsOriginal:        st.Disposition.Original != 0,
				TimeBase:          st.TimeBase,
			}
			streams = append(streams, ref)
			idx++
		}
	}
	return streams
}

// runtimeTicksFromProbe converts ffprobe's format.duration (decimal
// seconds, as a string) to the reference server's RunTimeTicks unit
// (100ns ticks) — nil if duration is missing/unparseable/non-positive.
func runtimeTicksFromProbe(info ffprobeOutput) *int64 {
	d, err := strconv.ParseFloat(info.Format.Duration, 64)
	if err != nil || d <= 0 {
		return nil
	}
	ticks := int64(d * 10_000_000)
	return &ticks
}

// embeddedArt is a picture attached to an audio file: the index of its
// stream in the file and its codec.
type embeddedArt struct {
	index int
	codec string
}

// attachedPicture finds the picture embedded in an audio file, if any.
func attachedPicture(info ffprobeOutput) *embeddedArt {
	for i, st := range info.Streams {
		if st.CodecType == "video" && st.Disposition.AttachedPic == 1 {
			return &embeddedArt{index: i, codec: st.CodecName}
		}
	}
	return nil
}

// audioTags returns the tags of an audio file: the container's, completed
// with the first audio stream's (Ogg keeps them there). Names are lower-cased.
func audioTags(info ffprobeOutput) map[string]string {
	out := map[string]string{}
	for _, st := range info.Streams {
		if st.CodecType == "audio" {
			for k, v := range st.Tags.All {
				out[k] = v
			}
			break
		}
	}
	for k, v := range info.Format.Tags {
		out[strings.ToLower(k)] = v
	}
	return out
}

// parseFrameRate reads ffprobe's "num/den" frame rate; 0 when it is absent or
// not a usable rate.
func parseFrameRate(v string) float64 {
	num, den, ok := strings.Cut(v, "/")
	if !ok {
		return 0
	}
	n, err1 := strconv.ParseFloat(num, 64)
	d, err2 := strconv.ParseFloat(den, 64)
	if err1 != nil || err2 != nil || d == 0 || n <= 0 {
		return 0
	}
	return n / d
}
