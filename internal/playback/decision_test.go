package playback

import (
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

	got := DecideDirectPlay(item, profile)
	if !got.DirectPlaySupported {
		t.Fatal("DirectPlaySupported = false, want true")
	}
	if got.Container != "mp4" {
		t.Errorf("Container = %q, want %q", got.Container, "mp4")
	}
}

func TestDecideDirectPlayIncompatibleContainer(t *testing.T) {
	item := storage.Item{
		Container: "mkv", // webProfile does not include mkv
		MediaStreams: []storage.MediaStreamRef{
			{Type: storage.StreamTypeVideo, Codec: "h264", IsDefault: true},
			{Type: storage.StreamTypeAudio, Codec: "aac", IsDefault: true},
		},
	}
	profile, _ := deviceprofile.Lookup("Jellyfin Web")

	got := DecideDirectPlay(item, profile)
	if got.DirectPlaySupported {
		t.Fatal("DirectPlaySupported = true, want false (mkv not in Jellyfin Web's container list)")
	}
	if got.Container != "" {
		t.Errorf("Container = %q, want empty on unsupported decision", got.Container)
	}
}

func TestDecideDirectPlayIncompatibleVideoCodec(t *testing.T) {
	item := storage.Item{
		Container: "mp4",
		MediaStreams: []storage.MediaStreamRef{
			{Type: storage.StreamTypeVideo, Codec: "hevc", IsDefault: true}, // not in webProfile
			{Type: storage.StreamTypeAudio, Codec: "aac", IsDefault: true},
		},
	}
	profile, _ := deviceprofile.Lookup("Jellyfin Web")

	got := DecideDirectPlay(item, profile)
	if got.DirectPlaySupported {
		t.Fatal("DirectPlaySupported = true, want false (hevc not in Jellyfin Web's video codec list)")
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

	got := DecideDirectPlay(item, profile)
	if !got.DirectPlaySupported {
		t.Fatal("DirectPlaySupported = false, want true (mobile profile supports mkv/hevc/ac3)")
	}
}

func TestDecideDirectPlayDefaultFallbackConservative(t *testing.T) {
	item := storage.Item{
		Container: "mkv",
		MediaStreams: []storage.MediaStreamRef{
			{Type: storage.StreamTypeVideo, Codec: "hevc", IsDefault: true},
		},
	}
	got := DecideDirectPlay(item, deviceprofile.Default())
	if got.DirectPlaySupported {
		t.Fatal("DirectPlaySupported = true, want false against the conservative default profile")
	}
}

func TestDecideDirectPlayNonDefaultExtraAudioTrackDoesNotBlock(t *testing.T) {
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

	got := DecideDirectPlay(item, profile)
	if !got.DirectPlaySupported {
		t.Fatal("DirectPlaySupported = false, want true (non-default incompatible track shouldn't block)")
	}
}

func TestDecideDirectPlayEmptyContainer(t *testing.T) {
	item := storage.Item{Container: ""}
	profile, _ := deviceprofile.Lookup("Jellyfin Web")
	got := DecideDirectPlay(item, profile)
	if got.DirectPlaySupported {
		t.Fatal("DirectPlaySupported = true, want false for an item with no known container")
	}
}
