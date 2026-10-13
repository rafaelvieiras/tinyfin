package api

import (
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// TestVideoDisplayTitleNoOwnTitle covers docs/12's "Video streams" section
// with no explicit Title: resolution + codec + dynamic-range label,
// space-joined.
func TestVideoDisplayTitleNoOwnTitle(t *testing.T) {
	tests := []struct {
		name string
		s    storage.MediaStreamRef
		want string
	}{
		{
			name: "SDR HEVC 360p",
			s: storage.MediaStreamRef{
				Type: storage.StreamTypeVideo, Codec: "hevc",
				Width: intPtr(640), Height: intPtr(360),
			},
			want: "360p HEVC SDR",
		},
		{
			name: "HDR HEVC 1080p (smpte2084)",
			s: storage.MediaStreamRef{
				Type: storage.StreamTypeVideo, Codec: "hevc",
				Width: intPtr(1920), Height: intPtr(1080), ColorTransfer: "smpte2084",
			},
			want: "1080p HEVC HDR",
		},
		{
			name: "HLG HDR (arib-std-b67), case-insensitive match",
			s: storage.MediaStreamRef{
				Type: storage.StreamTypeVideo, Codec: "h264",
				Width: intPtr(1280), Height: intPtr(720), ColorTransfer: "ARIB-STD-B67",
			},
			want: "720p H264 HDR",
		},
		{
			name: "interlaced uses i suffix",
			s: storage.MediaStreamRef{
				Type: storage.StreamTypeVideo, Codec: "mpeg2video",
				Width: intPtr(720), Height: intPtr(404), IsInterlaced: true,
			},
			want: "404i MPEG2VIDEO SDR",
		},
		{
			name: "4K has no p/i suffix",
			s: storage.MediaStreamRef{
				Type: storage.StreamTypeVideo, Codec: "hevc",
				Width: intPtr(3840), Height: intPtr(2160),
			},
			want: "4K HEVC SDR",
		},
		{
			name: "no width/height -> no resolution label, still codec+range",
			s: storage.MediaStreamRef{
				Type: storage.StreamTypeVideo, Codec: "hevc",
			},
			want: "HEVC SDR",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := displayTitleFor(tc.s)
			if got != tc.want {
				t.Errorf("displayTitleFor() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestVideoDisplayTitleWithOwnTitle covers the "explicit Title" merge rule:
// start from Title, append only attributes not already a substring of it.
func TestVideoDisplayTitleWithOwnTitle(t *testing.T) {
	s := storage.MediaStreamRef{
		Type: storage.StreamTypeVideo, Codec: "hevc",
		Width: intPtr(1920), Height: intPtr(1080),
		Title: "1080p HEVC Remux",
	}
	// "1080p" and "HEVC" are already substrings of the Title
	// (case-insensitive) — only the dynamic-range attribute ("SDR") is new.
	want := "1080p HEVC Remux - SDR"
	if got := displayTitleFor(s); got != want {
		t.Errorf("displayTitleFor() = %q, want %q", got, want)
	}
}

// TestVideoDisplayTitleWithOwnTitleNoNewAttrs covers a Title that already
// mentions every attribute — DisplayTitle should equal the Title verbatim,
// no trailing " - " for anything.
func TestVideoDisplayTitleWithOwnTitleNoNewAttrs(t *testing.T) {
	s := storage.MediaStreamRef{
		Type: storage.StreamTypeVideo, Codec: "hevc",
		Width: intPtr(1920), Height: intPtr(1080),
		Title: "My 1080p HEVC SDR Cut",
	}
	want := "My 1080p HEVC SDR Cut"
	if got := displayTitleFor(s); got != want {
		t.Errorf("displayTitleFor() = %q, want %q", got, want)
	}
}

// TestAudioDisplayTitleFriendlyCodecName covers docs/12's "Audio streams"
// point 2's friendly-name branch (no usable Profile) plus language and
// channel count.
func TestAudioDisplayTitleFriendlyCodecName(t *testing.T) {
	s := storage.MediaStreamRef{
		Type: storage.StreamTypeAudio, Codec: "ac3", Language: "eng",
		Channels: intPtr(6),
	}
	want := "English - Dolby Digital - 6 ch"
	if got := displayTitleFor(s); got != want {
		t.Errorf("displayTitleFor() = %q, want %q", got, want)
	}
}

// TestAudioDisplayTitleEAC3AndDTS covers the rest of the friendly-name
// table.
func TestAudioDisplayTitleEAC3AndDTS(t *testing.T) {
	tests := []struct {
		codec string
		want  string
	}{
		{"eac3", "Dolby Digital+"},
		{"dts", "DTS"},
		{"dca", "DTS"},
		{"flac", "FLAC"},
	}
	for _, tc := range tests {
		s := storage.MediaStreamRef{Type: storage.StreamTypeAudio, Codec: tc.codec}
		want := tc.want
		if got := displayTitleFor(s); got != want {
			t.Errorf("displayTitleFor(codec=%q) = %q, want %q", tc.codec, got, want)
		}
	}
}

// TestAudioDisplayTitleUsesProfile covers docs/12's "Audio streams" point
// 2's other branch: a usable Profile is shown verbatim instead of a
// friendly codec name.
func TestAudioDisplayTitleUsesProfile(t *testing.T) {
	s := storage.MediaStreamRef{
		Type: storage.StreamTypeAudio, Codec: "aac", Profile: "HE-AAC",
		ChannelLayout: "stereo",
	}
	want := "HE-AAC - Stereo"
	if got := displayTitleFor(s); got != want {
		t.Errorf("displayTitleFor() = %q, want %q", got, want)
	}
}

// TestAudioDisplayTitleGenericLCProfileIgnored covers the "lc" special case
// — AAC's generic Low Complexity profile is treated the same as no profile
// at all, falling back to the friendly codec name.
func TestAudioDisplayTitleGenericLCProfileIgnored(t *testing.T) {
	s := storage.MediaStreamRef{
		Type: storage.StreamTypeAudio, Codec: "aac", Profile: "lc",
	}
	want := "AAC"
	if got := displayTitleFor(s); got != want {
		t.Errorf("displayTitleFor() = %q, want %q", got, want)
	}
}

// TestAudioDisplayTitleFlags covers docs/12's "Audio streams" points 4-6:
// Default/External/Original.
func TestAudioDisplayTitleFlags(t *testing.T) {
	s := storage.MediaStreamRef{
		Type: storage.StreamTypeAudio, Codec: "aac",
		IsDefault: true, IsExternal: true, IsOriginal: true,
	}
	want := "AAC - Default - External - Original"
	if got := displayTitleFor(s); got != want {
		t.Errorf("displayTitleFor() = %q, want %q", got, want)
	}
}

// TestAudioDisplayTitleUnsetLanguageOmitted covers docs/12's "Audio
// streams" point 1 — "und"/empty language is a placeholder, not shown.
func TestAudioDisplayTitleUnsetLanguageOmitted(t *testing.T) {
	s := storage.MediaStreamRef{Type: storage.StreamTypeAudio, Codec: "aac", Language: "und"}
	want := "AAC"
	if got := displayTitleFor(s); got != want {
		t.Errorf("displayTitleFor() = %q, want %q", got, want)
	}
}

// TestAudioDisplayTitleLanguageName covers docs/12's "Audio streams" point
// 1's language-name lookup: the ISO 639-2/B code is converted to its English
// display name, not shown raw — regression guard for a real gap found via
// live comparison (a Example Show (1986) S01E006 audio track, language "por",
// title "Português (Alpha)"): the reference server's DisplayTitle included
// "Portuguese" as an attribute; Tinyfin's did not, because it appended the
// raw code "por" and that raw code disappeared into the merge-with-title
// substring check ("por" is itself a substring of "Português").
// The Portuguese title below is deliberate non-English test data: a captured
// localized embedded track title.
func TestAudioDisplayTitleLanguageName(t *testing.T) {
	s := storage.MediaStreamRef{
		Type: storage.StreamTypeAudio, Codec: "aac", Profile: "LC",
		Language: "por", ChannelLayout: "stereo", IsDefault: true,
		Title: "Português (Alpha)",
	}
	want := "Português (Alpha) - Portuguese - AAC - Stereo - Default"
	if got := displayTitleFor(s); got != want {
		t.Errorf("displayTitleFor() = %q, want %q", got, want)
	}
}

// TestAudioDisplayTitleUnknownLanguageCodeFallback covers languageName's
// fallback for a code with no entry in the lookup table — same
// map-plus-uppercase-fallback pattern as friendlyAudioCodecName: an unknown
// code renders as itself, uppercased, rather than failing or guessing at a
// name.
func TestAudioDisplayTitleUnknownLanguageCodeFallback(t *testing.T) {
	s := storage.MediaStreamRef{
		Type: storage.StreamTypeAudio, Codec: "aac", Language: "xyz",
	}
	want := "XYZ - AAC"
	if got := displayTitleFor(s); got != want {
		t.Errorf("displayTitleFor() = %q, want %q", got, want)
	}
}

// TestAudioDisplayTitleWithOwnTitleRealExample regression-guards live
// validation against the reference server for a real
// media file (a synthetic series, S01E01): its audio track's embedded
// Title ("Indefinido Estéreo") isn't a substring match for either "AAC" or
// "Stereo" (accented "Estéreo" != "stereo"), so both attributes append; the
// reference server's own DisplayTitle for this exact stream was
// "Indefinido Estéreo - AAC - Stereo - Padrão" — "Padrão" is Portuguese for
// "Default", the one accepted divergence (docs/12's scope note: no
// localization system here, "Default" stays English).
// "Indefinido Estéreo" and "Padrão" are deliberate non-English test data
// (captured localized wire values).
func TestAudioDisplayTitleWithOwnTitleRealExample(t *testing.T) {
	s := storage.MediaStreamRef{
		Type: storage.StreamTypeAudio, Codec: "aac", Profile: "LC",
		ChannelLayout: "stereo", IsDefault: true,
		Title: "Indefinido Estéreo",
	}
	want := "Indefinido Estéreo - AAC - Stereo - Default"
	if got := displayTitleFor(s); got != want {
		t.Errorf("displayTitleFor() = %q, want %q", got, want)
	}
}

// TestSubtitleDisplayTitle covers docs/12's "Subtitle streams" section
// (recorded there for completeness, out of scope for the movie/episode gap
// that motivated this file, but implemented via the same mechanism).
func TestSubtitleDisplayTitle(t *testing.T) {
	tests := []struct {
		name string
		s    storage.MediaStreamRef
		want string
	}{
		{
			name: "undefined language",
			s:    storage.MediaStreamRef{Type: storage.StreamTypeSubtitle, Codec: "subrip"},
			want: "Und - SUBRIP",
		},
		{
			name: "language, default, forced",
			s: storage.MediaStreamRef{
				Type: storage.StreamTypeSubtitle, Codec: "ass", Language: "por",
				IsDefault: true, IsForced: true,
			},
			want: "Portuguese - Default - Forced - ASS",
		},
		{
			name: "external",
			s: storage.MediaStreamRef{
				Type: storage.StreamTypeSubtitle, Codec: "srt", Language: "eng",
				IsExternal: true,
			},
			want: "English - SRT - External",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := displayTitleFor(tc.s); got != tc.want {
				t.Errorf("displayTitleFor() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestResolutionLabelBreakpoints spot-checks resolutionLabel's table
// (docs/12's "Video streams" point 1) beyond what the DisplayTitle-level
// tests above already exercise.
func TestResolutionLabelBreakpoints(t *testing.T) {
	tests := []struct {
		width, height int
		interlaced    bool
		want          string
	}{
		{256, 144, false, "144p"},
		{426, 240, false, "240p"},
		{682, 384, false, "384p"},
		{854, 480, false, "480p"},
		{960, 540, false, "540p"},
		{1024, 576, false, "576p"},
		{2560, 1080, false, "1080p"},
		{8192, 6144, false, "8K"},
		{9000, 7000, false, ""}, // past the last breakpoint: no guess

		// Non-16:9 aspect ratios that fall inside a breakpoint's height cap
		// but past its own label resolution — regression guard for a bug
		// where these height caps were implemented as the label's own round
		// number (540/720/1080) instead of the reference server's actual,
		// non-obvious GetResolutionText() values (544/962/1440).
		{960, 544, false, "540p"},    // height cap itself: still 540p, not past it
		{1280, 900, false, "720p"},   // would wrongly become 1080p under the old, buggy 720-height cap
		{2560, 1200, false, "1080p"}, // ultrawide 1200h: still under the real 1440 cap
	}
	for _, tc := range tests {
		w, h := tc.width, tc.height
		got := resolutionLabel(&w, &h, tc.interlaced)
		if got != tc.want {
			t.Errorf("resolutionLabel(%d, %d, %v) = %q, want %q", tc.width, tc.height, tc.interlaced, got, tc.want)
		}
	}
}
