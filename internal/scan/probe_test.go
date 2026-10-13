package scan

import (
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// TestMediaStreamsFromProbeVideo covers the video-stream fields
// docs/12's "Episode/movie detail page — MediaStream.DisplayTitle" needs
// mediaStreamsFromProbe to extract from ffprobe's own JSON shape: Profile,
// IsInterlaced (from field_order), TimeBase, AspectRatio, PixelFormat,
// RefFrames, Level, ColorTransfer, IsOriginal (from disposition.original).
func TestMediaStreamsFromProbeVideo(t *testing.T) {
	st := ffprobeStream{
		CodecType:          "video",
		CodecName:          "hevc",
		Profile:            "Main 10",
		Width:              1920,
		Height:             1080,
		TimeBase:           "1/1000",
		DisplayAspectRatio: "16:9",
		RefFrames:          4,
		PixFmt:             "yuv420p10le",
		Level:              153,
		FieldOrder:         "progressive",
		ColorTransfer:      "smpte2084",
	}
	st.Disposition.Original = 1
	info := ffprobeOutput{Streams: []ffprobeStream{st}}
	streams := mediaStreamsFromProbe(info)
	if len(streams) != 1 {
		t.Fatalf("mediaStreamsFromProbe() returned %d streams, want 1", len(streams))
	}
	s := streams[0]

	if s.Type != storage.StreamTypeVideo {
		t.Errorf("Type = %v, want Video", s.Type)
	}
	if s.Profile != "Main 10" {
		t.Errorf("Profile = %q, want %q", s.Profile, "Main 10")
	}
	if s.IsInterlaced {
		t.Errorf("IsInterlaced = true for field_order=progressive, want false")
	}
	if s.TimeBase != "1/1000" {
		t.Errorf("TimeBase = %q, want %q", s.TimeBase, "1/1000")
	}
	if s.AspectRatio != "16:9" {
		t.Errorf("AspectRatio = %q, want %q", s.AspectRatio, "16:9")
	}
	if s.PixelFormat != "yuv420p10le" {
		t.Errorf("PixelFormat = %q, want %q", s.PixelFormat, "yuv420p10le")
	}
	if s.RefFrames == nil || *s.RefFrames != 4 {
		t.Errorf("RefFrames = %v, want 4", s.RefFrames)
	}
	if s.Level == nil || *s.Level != 153 {
		t.Errorf("Level = %v, want 153", s.Level)
	}
	if s.ColorTransfer != "smpte2084" {
		t.Errorf("ColorTransfer = %q, want %q", s.ColorTransfer, "smpte2084")
	}
	if !s.IsOriginal {
		t.Errorf("IsOriginal = false, want true (disposition.original=1)")
	}
}

// TestMediaStreamsFromProbeVideoInterlaced covers field_order values that
// do indicate interlacing.
func TestMediaStreamsFromProbeVideoInterlaced(t *testing.T) {
	for _, fo := range []string{"tt", "bb", "tb", "bt"} {
		info := ffprobeOutput{Streams: []ffprobeStream{{CodecType: "video", CodecName: "mpeg2video", FieldOrder: fo}}}
		streams := mediaStreamsFromProbe(info)
		if !streams[0].IsInterlaced {
			t.Errorf("field_order=%q: IsInterlaced = false, want true", fo)
		}
	}
	for _, fo := range []string{"progressive", "unknown", ""} {
		info := ffprobeOutput{Streams: []ffprobeStream{{CodecType: "video", CodecName: "mpeg2video", FieldOrder: fo}}}
		streams := mediaStreamsFromProbe(info)
		if streams[0].IsInterlaced {
			t.Errorf("field_order=%q: IsInterlaced = true, want false", fo)
		}
	}
}

// TestMediaStreamsFromProbeVideoLevelSentinel covers ffprobe's own -99
// "not applicable" sentinel for Level — should not surface a Level pointer.
func TestMediaStreamsFromProbeVideoLevelSentinel(t *testing.T) {
	info := ffprobeOutput{Streams: []ffprobeStream{{CodecType: "video", CodecName: "vp9", Level: -99}}}
	streams := mediaStreamsFromProbe(info)
	if streams[0].Level != nil {
		t.Errorf("Level = %v, want nil for ffprobe's -99 sentinel", streams[0].Level)
	}
}

// TestMediaStreamsFromProbeAudio covers the audio-stream fields:
// ChannelLayout, Profile, SampleRate (parsed from ffprobe's numeric-string
// JSON representation), IsOriginal.
func TestMediaStreamsFromProbeAudio(t *testing.T) {
	st := ffprobeStream{
		CodecType:     "audio",
		CodecName:     "aac",
		Profile:       "LC",
		Channels:      2,
		ChannelLayout: "stereo",
		SampleRate:    "48000",
	}
	st.Disposition.Original = 0
	info := ffprobeOutput{Streams: []ffprobeStream{st}}
	streams := mediaStreamsFromProbe(info)
	if len(streams) != 1 {
		t.Fatalf("mediaStreamsFromProbe() returned %d streams, want 1", len(streams))
	}
	s := streams[0]
	if s.Type != storage.StreamTypeAudio {
		t.Errorf("Type = %v, want Audio", s.Type)
	}
	if s.Profile != "LC" {
		t.Errorf("Profile = %q, want %q", s.Profile, "LC")
	}
	if s.ChannelLayout != "stereo" {
		t.Errorf("ChannelLayout = %q, want %q", s.ChannelLayout, "stereo")
	}
	if s.SampleRate == nil || *s.SampleRate != 48000 {
		t.Errorf("SampleRate = %v, want 48000", s.SampleRate)
	}
	if s.IsOriginal {
		t.Errorf("IsOriginal = true, want false (disposition.original=0)")
	}
}

// "Indefinido Estéreo" is deliberate non-English test data (a captured
// localized embedded title).
// TestMediaStreamsFromProbeExtractsEmbeddedTitleAndLanguage covers a real
// gap found while live-validating DisplayTitle against the reference
// server for an actual file: its audio track
// carries an embedded tags.title ("Indefinido Estéreo") that
// mediaStreamsFromProbe used to never extract at all — storage.
// MediaStreamRef.Title/Language existed as fields already, but nothing in
// this package ever populated them from ffprobe's own tags object, so
// DisplayTitle's "explicit Title" merge-rule branch (docs/12) never fired
// for any real file.
func TestMediaStreamsFromProbeExtractsEmbeddedTitleAndLanguage(t *testing.T) {
	videoSt := ffprobeStream{CodecType: "video", CodecName: "hevc"}
	videoSt.Tags.Title = "Director's Cut"
	videoSt.Tags.Language = "eng"

	audioSt := ffprobeStream{CodecType: "audio", CodecName: "aac"}
	audioSt.Tags.Title = "Indefinido Estéreo"

	info := ffprobeOutput{Streams: []ffprobeStream{videoSt, audioSt}}
	streams := mediaStreamsFromProbe(info)
	if len(streams) != 2 {
		t.Fatalf("mediaStreamsFromProbe() returned %d streams, want 2", len(streams))
	}
	if streams[0].Title != "Director's Cut" || streams[0].Language != "eng" {
		t.Errorf("video Title/Language = %q/%q, want %q/%q", streams[0].Title, streams[0].Language, "Director's Cut", "eng")
	}
	if streams[1].Title != "Indefinido Estéreo" {
		t.Errorf("audio Title = %q, want %q", streams[1].Title, "Indefinido Estéreo")
	}
	if streams[1].Language != "" {
		t.Errorf("audio Language = %q, want empty (no language tag on this stream)", streams[1].Language)
	}
}

// TestMediaStreamsFromProbeAudioSampleRateMissing covers an empty/
// unparseable sample_rate — SampleRate should stay nil rather than panic
// or produce a bogus zero value.
func TestMediaStreamsFromProbeAudioSampleRateMissing(t *testing.T) {
	info := ffprobeOutput{Streams: []ffprobeStream{{CodecType: "audio", CodecName: "mp3", SampleRate: ""}}}
	streams := mediaStreamsFromProbe(info)
	if streams[0].SampleRate != nil {
		t.Errorf("SampleRate = %v, want nil", streams[0].SampleRate)
	}
}

// TestMediaStreamsFromProbeSubtitle covers a codec_type=subtitle stream —
// found dropped entirely during byte-for-byte comparison against the
// reference server for a real file that carries
// an embedded ASS subtitle track: mediaStreamsFromProbe used to only
// switch on "video"/"audio", silently discarding "subtitle" streams, which
// broke the reference client's Subtitles dropdown against tinyfin.
// The Portuguese subtitle title is deliberate non-English test data.
func TestMediaStreamsFromProbeSubtitle(t *testing.T) {
	st := ffprobeStream{
		CodecType: "subtitle",
		CodecName: "ass",
	}
	st.Tags.Title = "Português"
	st.Tags.Language = "por"
	st.Disposition.Default = 1
	info := ffprobeOutput{Streams: []ffprobeStream{st}}
	streams := mediaStreamsFromProbe(info)
	if len(streams) != 1 {
		t.Fatalf("mediaStreamsFromProbe() returned %d streams, want 1", len(streams))
	}
	s := streams[0]
	if s.Type != storage.StreamTypeSubtitle {
		t.Errorf("Type = %v, want Subtitle", s.Type)
	}
	if s.Codec != "ass" {
		t.Errorf("Codec = %q, want %q", s.Codec, "ass")
	}
	if s.Title != "Português" {
		t.Errorf("Title = %q, want %q", s.Title, "Português")
	}
	if s.Language != "por" {
		t.Errorf("Language = %q, want %q", s.Language, "por")
	}
	if !s.IsDefault {
		t.Errorf("IsDefault = false, want true (disposition.default=1)")
	}
	if s.IsForced {
		t.Errorf("IsForced = true, want false (disposition.forced=0)")
	}
}

// TestMediaStreamsFromProbeIsDefaultPerStream covers disposition.default
// being respected per-stream rather than hardcoded true for every kept
// stream: found via byte-for-byte comparison against the reference server
// (reference server) for a real file with two audio tracks where
// only the first has disposition.default=1 — the reference server's
// MediaStreams response reflects that per-track, tinyfin used to mark
// IsDefault true on both.
func TestMediaStreamsFromProbeIsDefaultPerStream(t *testing.T) {
	defaultAudio := ffprobeStream{CodecType: "audio", CodecName: "aac"}
	defaultAudio.Disposition.Default = 1
	nonDefaultAudio := ffprobeStream{CodecType: "audio", CodecName: "aac"}
	nonDefaultAudio.Disposition.Default = 0

	info := ffprobeOutput{Streams: []ffprobeStream{defaultAudio, nonDefaultAudio}}
	streams := mediaStreamsFromProbe(info)
	if len(streams) != 2 {
		t.Fatalf("mediaStreamsFromProbe() returned %d streams, want 2", len(streams))
	}
	if !streams[0].IsDefault {
		t.Errorf("streams[0].IsDefault = false, want true (disposition.default=1)")
	}
	if streams[1].IsDefault {
		t.Errorf("streams[1].IsDefault = true, want false (disposition.default=0)")
	}
}
