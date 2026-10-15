package transcode

import (
	"reflect"
	"testing"
)

func TestAudioArgs(t *testing.T) {
	got := AudioArgs(AudioRequest{InputPath: "/m/a.flac", Encoder: "libmp3lame", Format: "mp3", Bitrate: 96000})
	want := []string{"-v", "error", "-nostdin", "-i", "/m/a.flac", "-vn", "-sn", "-dn", "-map", "0:a:0",
		"-map_metadata", "-1", "-c:a", "libmp3lame", "-b:a", "96000", "-f", "mp3", "pipe:1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %v\nwant %v", got, want)
	}
	idx := 2
	got = AudioArgs(AudioRequest{InputPath: "x", Encoder: "aac", Format: "mp4", Fragmented: true, Channels: 2, StreamIndex: &idx})
	has := func(a, b string) bool {
		for i := 0; i+1 < len(got); i++ {
			if got[i] == a && got[i+1] == b {
				return true
			}
		}
		return false
	}
	if !has("-map", "0:2") || !has("-ac", "2") || !has("-movflags", "frag_keyframe+empty_moov+default_base_moof") {
		t.Fatalf("args = %v", got)
	}
}

func TestBuildArgsAudioOnly(t *testing.T) {
	args := buildArgs(Request{InputPath: "a.flac", VideoCodec: "none", AudioCodec: "aac", AudioBitrate: 128000, Container: "ts"})
	for i, a := range args {
		if a == "-c:v" || (a == "-map" && args[i+1] == "0:v:0") {
			t.Fatalf("video options in an audio-only job: %v", args)
		}
	}
	found := false
	for _, a := range args {
		found = found || a == "-vn"
	}
	if !found {
		t.Fatalf("no -vn: %v", args)
	}
}
