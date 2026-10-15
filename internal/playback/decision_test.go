package playback

import (
	"reflect"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/deviceprofile"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

func intPtr(i int) *int { return &i }

func TestDecideDirectPlaySupported(t *testing.T) {
	item := storage.Item{
		Container: "mp4",
		MediaStreams: []storage.MediaStreamRef{
			{Type: storage.StreamTypeVideo, Codec: "h264", IsDefault: true, Width: intPtr(1920), Height: intPtr(1080)},
			{Type: storage.StreamTypeAudio, Codec: "aac", IsDefault: true},
		},
	}
	profile, ok := deviceprofile.Lookup("Jellyfin Web")
	if !ok {
		t.Fatal("Jellyfin Web profile not found")
	}

	got := Decide(item, profile)
	if got.Mode != ModeDirectPlay {
		t.Fatalf("Mode = %v, want ModeDirectPlay", got.Mode)
	}
	if got.Container != "mp4" {
		t.Errorf("Container = %q, want %q", got.Container, "mp4")
	}
}

func TestDecideRemuxWhenOnlyContainerMismatches(t *testing.T) {
	// h264/aac are both in webProfile's DirectPlayProfiles, but mkv isn't —
	// nothing needs re-encoding, just repackaging into a container the
	// client accepts.
	item := storage.Item{
		Container: "mkv",
		MediaStreams: []storage.MediaStreamRef{
			{Type: storage.StreamTypeVideo, Codec: "h264", IsDefault: true},
			{Type: storage.StreamTypeAudio, Codec: "aac", IsDefault: true},
		},
	}
	profile, _ := deviceprofile.Lookup("Jellyfin Web")

	got := Decide(item, profile)
	if got.Mode != ModeRemux {
		t.Fatalf("Mode = %v, want ModeRemux", got.Mode)
	}
	if got.VideoCodec != "copy" || got.AudioCodec != "copy" {
		t.Errorf("VideoCodec/AudioCodec = %q/%q, want copy/copy", got.VideoCodec, got.AudioCodec)
	}
	if got.Container == "" {
		t.Error("Container is empty, want one of webProfile's accepted containers")
	}
}

func TestDecideDirectStreamWhenOnlyAudioCodecMismatches(t *testing.T) {
	// h264 is in webProfile's DirectPlayProfiles, but dts isn't — video can
	// be copied, only the audio needs re-encoding.
	item := storage.Item{
		Container: "mp4",
		MediaStreams: []storage.MediaStreamRef{
			{Type: storage.StreamTypeVideo, Codec: "h264", IsDefault: true},
			{Type: storage.StreamTypeAudio, Codec: "dts", IsDefault: true},
		},
	}
	profile, _ := deviceprofile.Lookup("Jellyfin Web")

	got := Decide(item, profile)
	if got.Mode != ModeDirectStream {
		t.Fatalf("Mode = %v, want ModeDirectStream", got.Mode)
	}
	if got.VideoCodec != "copy" {
		t.Errorf("VideoCodec = %q, want copy", got.VideoCodec)
	}
	if got.AudioCodec == "" || got.AudioCodec == "copy" {
		t.Errorf("AudioCodec = %q, want a concrete re-encode target", got.AudioCodec)
	}
}

func TestDecideTranscodeWhenVideoCodecUnsupported(t *testing.T) {
	item := storage.Item{
		Container: "mp4",
		MediaStreams: []storage.MediaStreamRef{
			{Type: storage.StreamTypeVideo, Codec: "hevc", IsDefault: true}, // not in webProfile
			{Type: storage.StreamTypeAudio, Codec: "aac", IsDefault: true},
		},
	}
	profile, _ := deviceprofile.Lookup("Jellyfin Web")

	got := Decide(item, profile)
	if got.Mode != ModeTranscode {
		t.Fatalf("Mode = %v, want ModeTranscode", got.Mode)
	}
	if got.Container == "" || got.VideoCodec == "" || got.AudioCodec == "" {
		t.Errorf("Transcode decision missing target fields: %+v", got)
	}
}

func TestDecideNoneWhenProfileHasNoTranscodingFallback(t *testing.T) {
	item := storage.Item{
		Container: "mkv",
		MediaStreams: []storage.MediaStreamRef{
			{Type: storage.StreamTypeVideo, Codec: "hevc", IsDefault: true},
		},
	}
	profile := deviceprofile.DeviceProfile{
		Name: "No Transcode Fallback",
		DirectPlayProfiles: []deviceprofile.DirectPlayProfile{
			{Type: "Video", Container: "mp4", VideoCodec: "h264"},
		},
	}

	got := Decide(item, profile)
	if got.Mode != ModeNone {
		t.Fatalf("Mode = %v, want ModeNone", got.Mode)
	}
	if got.Container != "" || got.VideoCodec != "" || got.AudioCodec != "" {
		t.Errorf("expected a zero Decision beyond Mode, got %+v", got)
	}
}

func TestDecideDirectPlayMobileSupportsHEVC(t *testing.T) {
	item := storage.Item{
		Container: "mkv",
		MediaStreams: []storage.MediaStreamRef{
			{Type: storage.StreamTypeVideo, Codec: "hevc", IsDefault: true},
			{Type: storage.StreamTypeAudio, Codec: "ac3", IsDefault: true},
		},
	}
	profile, _ := deviceprofile.Lookup("Jellyfin Android")

	got := Decide(item, profile)
	if got.Mode != ModeDirectPlay {
		t.Fatalf("Mode = %v, want ModeDirectPlay (mobile profile supports mkv/hevc/ac3)", got.Mode)
	}
}

func TestDecideDefaultFallbackReachesTranscode(t *testing.T) {
	// Phase 1's defaultProfile had no transcoding fallback at all, so this
	// case used to land on "unsupported"; Phase 2 gave defaultProfile a
	// TranscodingProfiles entry specifically so this now reaches Transcode
	// instead (see deviceprofile.defaultProfile's doc comment).
	item := storage.Item{
		Container: "mkv",
		MediaStreams: []storage.MediaStreamRef{
			{Type: storage.StreamTypeVideo, Codec: "hevc", IsDefault: true},
		},
	}
	got := Decide(item, deviceprofile.Default())
	if got.Mode != ModeTranscode {
		t.Fatalf("Mode = %v, want ModeTranscode against the conservative default profile", got.Mode)
	}
}

func TestDecideNonDefaultExtraAudioTrackDoesNotBlockDirectPlay(t *testing.T) {
	// A commentary track in an unsupported codec must not block Direct
	// Play of the default audio track a client would actually select.
	item := storage.Item{
		Container: "mp4",
		MediaStreams: []storage.MediaStreamRef{
			{Type: storage.StreamTypeVideo, Codec: "h264", IsDefault: true},
			{Type: storage.StreamTypeAudio, Codec: "aac", IsDefault: true},
			{Type: storage.StreamTypeAudio, Codec: "dts", IsDefault: false},
		},
	}
	profile, _ := deviceprofile.Lookup("Jellyfin Web")

	got := Decide(item, profile)
	if got.Mode != ModeDirectPlay {
		t.Fatalf("Mode = %v, want ModeDirectPlay (non-default incompatible track shouldn't block)", got.Mode)
	}
}

func TestDecideEmptyContainerSkipsToTranscode(t *testing.T) {
	// No known container (a scanner-less item, docs/08) can't be Direct
	// Played/Remuxed/Direct-Streamed — Decide falls straight to whatever
	// TranscodingProfiles offers.
	item := storage.Item{Container: ""}
	profile, _ := deviceprofile.Lookup("Jellyfin Web")
	got := Decide(item, profile)
	if got.Mode != ModeTranscode {
		t.Fatalf("Mode = %v, want ModeTranscode for an item with no known container", got.Mode)
	}
}

func TestDecideWith_AudioTrackAndBurnIn(t *testing.T) {
	item := storage.Item{Container: "mkv", MediaStreams: []storage.MediaStreamRef{
		{Index: 0, Type: storage.StreamTypeVideo, Codec: "h264"},
		{Index: 1, Type: storage.StreamTypeAudio, Codec: "aac", IsDefault: true},
		{Index: 2, Type: storage.StreamTypeAudio, Codec: "mp3"},
	}}
	profile := deviceprofile.DeviceProfile{
		DirectPlayProfiles:  []deviceprofile.DirectPlayProfile{{Container: "mkv", Type: "Video", VideoCodec: "h264", AudioCodec: "aac"}},
		TranscodingProfiles: []deviceprofile.TranscodingProfile{{Container: "ts", Type: "Video", VideoCodec: "h264", AudioCodec: "aac"}},
	}
	two := 2
	if d := DecideWith(item, profile, Options{}); d.Mode != ModeDirectPlay || d.AudioStream == nil || *d.AudioStream != 1 {
		t.Fatalf("default: %+v", d)
	}
	d := DecideWith(item, profile, Options{AudioStreamIndex: &two})
	if d.Mode != ModeDirectStream || d.AudioCodec != "aac" || *d.AudioStream != 2 || !reflect.DeepEqual(d.Reasons, []string{"AudioCodecNotSupported"}) {
		t.Fatalf("mp3 track: %+v", d)
	}
	d = DecideWith(item, profile, Options{BurnSubtitle: true})
	if d.Mode != ModeTranscode || d.VideoCodec != "h264" || !reflect.DeepEqual(d.Reasons, []string{"SubtitleCodecNotSupported"}) {
		t.Fatalf("burn-in: %+v", d)
	}
	d = DecideWith(item, profile, Options{AudioStreamIndex: &two, BurnSubtitle: true})
	if !reflect.DeepEqual(d.Reasons, []string{"AudioCodecNotSupported", "SubtitleCodecNotSupported"}) {
		t.Fatalf("both: %+v", d)
	}
	nine := 9
	if d := DecideWith(item, profile, Options{AudioStreamIndex: &nine}); *d.AudioStream != 1 {
		t.Fatalf("unknown index must fall back to the default: %+v", d)
	}
}
