package scan

import (
	"encoding/json"
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
		Level:              intPtr(153),
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

// TestMediaStreamsFromProbeVideoLevel covers the Level member: ffprobe's -99
// "not applicable" and 0 are kept as printed, an absent level stays absent.
func TestMediaStreamsFromProbeVideoLevel(t *testing.T) {
	for _, tc := range []struct {
		level *int
		want  *float64
	}{{intPtr(-99), f64(-99)}, {intPtr(0), f64(0)}, {nil, nil}} {
		info := ffprobeOutput{Streams: []ffprobeStream{{CodecType: "video", CodecName: "vp9", Level: tc.level}}}
		got := mediaStreamsFromProbe(info)[0].Level
		if (got == nil) != (tc.want == nil) || got != nil && *got != *tc.want {
			t.Errorf("level %v: Level = %v, want %v", tc.level, got, tc.want)
		}
	}
}

func f64(v float64) *float64 { return &v }

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

func TestStreamBitRateFromMatroskaTags(t *testing.T) {
	f := ffprobeFormat{Duration: "4"}
	mk := func(tags map[string]string) ffprobeStream {
		return ffprobeStream{Tags: streamTags{All: tags}}
	}
	if br := streamBitRate(mk(map[string]string{"bps": "1000000"}), f); br == nil || *br != 1000000 {
		t.Errorf("BPS: %v", br)
	}
	if br := streamBitRate(mk(map[string]string{"number_of_bytes": "500000", "duration": "00:00:02.023000000"}), f); br == nil || *br != 1977261 {
		t.Errorf("bytes/duration: %v", br)
	}
	if br := streamBitRate(mk(map[string]string{"bps": "abc"}), f); br != nil {
		t.Errorf("bad BPS: %v", *br)
	}
	if br := streamBitRate(mk(map[string]string{"number_of_bytes": "500000"}), f); br == nil || *br != 1000000 {
		t.Errorf("container duration: %v", br)
	}
}

func TestEstimatedAudioBitRate(t *testing.T) {
	for _, c := range []struct {
		codec string
		ch    int
		want  int64
	}{{"aac", 1, 192000}, {"aac", 2, 192000}, {"aac", 6, 320000}, {"flac", 2, 960000}, {"alac", 6, 2880000}, {"opus", 2, 128000}, {"vorbis", 2, 160000}, {"truehd", 2, 1400000}} {
		if br := estimatedAudioBitRate(c.codec, c.ch); br == nil || *br != c.want {
			t.Errorf("%s/%d: %v, want %d", c.codec, c.ch, br, c.want)
		}
	}
	if estimatedAudioBitRate("wavpack", 2) != nil {
		t.Error("wavpack has no estimate")
	}
}

func intPtr(v int) *int { return &v }

func TestPlainChannelLayout(t *testing.T) {
	for in, want := range map[string]string{"5.1(side)": "5.1", "quad(side)": "quad", "6.1(back)": "6.1", "stereo": "stereo", "": ""} {
		if got := plainChannelLayout(in); got != want {
			t.Errorf("plainChannelLayout(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCodecTagOf(t *testing.T) {
	for in, want := range map[string]string{"avc1": "avc1", "[0][0][0][0]": "", "a[1][0][0]": "", "0x0161": "", "dvh1": "dvh1"} {
		if got := codecTagOf(in); got != want {
			t.Errorf("codecTagOf(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestDeriveVideoBitRate: the video stream of a file that states no rate for
// it gets the container's rate less the other streams' (estimates included).
func TestDeriveVideoBitRate(t *testing.T) {
	br := func(v int64) *int64 { return &v }
	mk := func(a ...*int64) []storage.MediaStreamRef {
		out := []storage.MediaStreamRef{{Type: storage.StreamTypeVideo}}
		for _, r := range a {
			out = append(out, storage.MediaStreamRef{Type: storage.StreamTypeAudio, BitRate: r})
		}
		return out
	}
	f := ffprobeFormat{BitRate: "500000"}
	for name, tc := range map[string]struct {
		streams []storage.MediaStreamRef
		want    *int64
	}{
		"remainder":         {mk(br(192000)), br(308000)},
		"two audio tracks":  {mk(br(192000), br(128000)), br(180000)},
		"no audio":          {mk(), br(500000)},
		"audio without one": {mk(br(192000), nil), nil},
		"not positive":      {mk(br(600000)), nil},
	} {
		deriveVideoBitRate(tc.streams, f)
		got := tc.streams[0].BitRate
		if (got == nil) != (tc.want == nil) || got != nil && *got != *tc.want {
			t.Errorf("%s: video BitRate = %v, want %v", name, got, tc.want)
		}
	}
	// a stated rate stays; a container without one derives nothing
	s := mk(br(1))
	s[0].BitRate = br(7)
	deriveVideoBitRate(s, f)
	if *s[0].BitRate != 7 {
		t.Errorf("stated rate replaced: %d", *s[0].BitRate)
	}
	s = mk(br(1))
	deriveVideoBitRate(s, ffprobeFormat{})
	if s[0].BitRate != nil {
		t.Errorf("derived from a container without a rate")
	}
}

func TestDoviAndHDR10Plus(t *testing.T) {
	var out ffprobeOutput
	raw := `{"streams":[{"codec_type":"video","codec_name":"hevc","side_data_list":[{"side_data_type":"DOVI configuration record","dv_version_major":1,"dv_version_minor":0,"dv_profile":8,"dv_level":6,"rpu_present_flag":1,"el_present_flag":0,"bl_present_flag":1,"dv_bl_signal_compatibility_id":1}]}],
"frames":[{"media_type":"video","side_data_list":[{"side_data_type":"HDR Dynamic Metadata SMPTE2094-40 (HDR10+)"}]}]}`
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	s := mediaStreamsFromProbe(out)[0]
	if s.DoVi == nil || s.DoVi.Profile != 8 || s.DoVi.Level != 6 || s.DoVi.CompatibilityID != 1 || s.DoVi.RPUPresent != 1 || s.DoVi.ELPresent != 0 || s.DoVi.BLPresent != 1 {
		t.Errorf("DoVi = %+v", s.DoVi)
	}
	if !s.Hdr10Plus {
		t.Errorf("Hdr10Plus = false")
	}
	if hasHDR10Plus([]ffprobeFrame{{MediaType: "audio", SideData: []ffprobeSideData{{Type: "HDR Dynamic Metadata SMPTE2094-40 (HDR10+)"}}}}) {
		t.Errorf("an audio frame counts")
	}
}

func TestEstimatedAudioBitRateMultichannel(t *testing.T) {
	for _, tc := range []struct {
		codec string
		ch    int
		want  int64
	}{{"opus", 2, 128000}, {"opus", 6, 256000}, {"vorbis", 1, 160000}, {"vorbis", 6, 320000}, {"aac", 6, 320000}} {
		if got := estimatedAudioBitRate(tc.codec, tc.ch); got == nil || *got != tc.want {
			t.Errorf("estimatedAudioBitRate(%s, %d) = %v, want %d", tc.codec, tc.ch, got, tc.want)
		}
	}
}
