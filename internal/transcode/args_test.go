package transcode

import (
	"reflect"
	"strings"
	"testing"
)

func TestBuildArgs_Remux(t *testing.T) {
	req := Request{
		InputPath:  "/media/movie.mkv",
		VideoCodec: "copy",
		AudioCodec: "copy",
		Container:  "ts",
	}
	got := buildArgs(req)

	want := []string{
		"-i", "/media/movie.mkv",
		"-map", "0:v:0", "-map", "0:a:0",
		"-c:v", "copy",
		"-c:a", "copy",
		"-f", "hls",
		"-hls_time", "6",
		"-hls_playlist_type", "event",
		"-hls_segment_type", "mpegts",
		"-hls_flags", "independent_segments",
		"-start_number", "0",
		"-hls_segment_filename", "seg_%05d.ts",
		"main.m3u8",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Remux args mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestBuildArgs_DirectStream_AudioOnlyEncode(t *testing.T) {
	// Video copy (client can decode source video), audio re-encoded
	// because the client can't decode the source audio codec — docs/07's
	// definition of Direct Stream. Only audio encode flags should appear;
	// no -preset/-b:v/-vf (those are video-encode-only).
	req := Request{
		InputPath:    "/media/movie.mkv",
		VideoCodec:   "copy",
		AudioCodec:   "aac",
		AudioBitrate: 192_000,
		Container:    "ts",
	}
	got := buildArgs(req)

	want := []string{
		"-i", "/media/movie.mkv",
		"-map", "0:v:0", "-map", "0:a:0",
		"-c:v", "copy",
		"-c:a", "aac",
		"-b:a", "192000",
		"-ac", "2",
		"-f", "hls",
		"-hls_time", "6",
		"-hls_playlist_type", "event",
		"-hls_segment_type", "mpegts",
		"-hls_flags", "independent_segments",
		"-start_number", "0",
		"-hls_segment_filename", "seg_%05d.ts",
		"main.m3u8",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Direct Stream args mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestBuildArgs_FullTranscode(t *testing.T) {
	req := Request{
		InputPath:      "/media/movie.mkv",
		VideoCodec:     "h264",
		AudioCodec:     "aac",
		Container:      "ts",
		VideoBitrate:   4_000_000,
		AudioBitrate:   128_000,
		MaxHeight:      720,
		StartTimeTicks: 12_345_0000, // 12.345s
		SegmentSeconds: 4,
	}
	got := buildArgs(req)

	want := []string{
		"-ss", "12.345",
		"-i", "/media/movie.mkv",
		"-map", "0:v:0", "-map", "0:a:0",
		"-c:v", "h264",
		"-preset", "veryfast", "-profile:v", "high", "-level", "4.1",
		"-b:v", "4000000",
		"-maxrate", "4000000",
		"-bufsize", "8000000",
		"-vf", "scale=-2:720",
		"-c:a", "aac",
		"-b:a", "128000",
		"-ac", "2",
		"-force_key_frames", "expr:gte(t,n_forced*4)",
		"-f", "hls",
		"-hls_time", "4",
		"-hls_playlist_type", "event",
		"-hls_segment_type", "mpegts",
		"-hls_flags", "independent_segments",
		"-start_number", "0",
		"-hls_segment_filename", "seg_%05d.ts",
		"main.m3u8",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Transcode args mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestBuildArgs_NoSeekWhenStartTimeTicksZero(t *testing.T) {
	req := Request{InputPath: "in.mp4", VideoCodec: "copy", AudioCodec: "copy", Container: "ts"}
	got := buildArgs(req)
	if len(got) == 0 || got[0] == "-ss" {
		t.Fatalf("expected no -ss flag when StartTimeTicks is 0, got args: %#v", got)
	}
}

func TestBuildArgs_ContainerFallback(t *testing.T) {
	// Container is documented as always set by the caller; this only
	// exercises the defensive fallback so a hand-built Request in a test
	// (or a future caller bug) doesn't produce an extension-less segment
	// filename.
	req := Request{InputPath: "in.mp4", VideoCodec: "copy", AudioCodec: "copy"}
	got := buildArgs(req)

	found := false
	for i, a := range got {
		if a == "-hls_segment_filename" && i+1 < len(got) {
			found = true
			want := "seg_%05d.ts"
			if got[i+1] != want {
				t.Fatalf("segment filename = %q, want %q", got[i+1], want)
			}
		}
	}
	if !found {
		t.Fatalf("no -hls_segment_filename flag found in args: %#v", got)
	}
}

func TestBuildArgs_MaxHeightIgnoredOnVideoCopy(t *testing.T) {
	// A stream copy can't be filtered — MaxHeight set alongside
	// VideoCodec=="copy" (shouldn't happen from a correct caller, but
	// buildArgs must not emit an invalid -vf on a copy stream regardless)
	// must not produce a -vf flag.
	req := Request{
		InputPath:  "in.mp4",
		VideoCodec: "copy",
		AudioCodec: "copy",
		Container:  "ts",
		MaxHeight:  480,
	}
	got := buildArgs(req)
	for _, a := range got {
		if a == "-vf" {
			t.Fatalf("did not expect -vf flag when VideoCodec is copy, got args: %#v", got)
		}
	}
}

func TestBuildArgs_NonH264TargetSkipsPresetFlags(t *testing.T) {
	req := Request{
		InputPath:  "in.mp4",
		VideoCodec: "hevc",
		AudioCodec: "copy",
		Container:  "ts",
	}
	got := buildArgs(req)
	for _, a := range got {
		if a == "-preset" || a == "-profile:v" {
			t.Fatalf("did not expect H.264-specific flags for hevc target, got args: %#v", got)
		}
	}
}

func TestBuildArgs_EmptyVideoAudioCodecTreatedAsCopy(t *testing.T) {
	req := Request{InputPath: "in.mp4", Container: "ts"}
	got := buildArgs(req)
	want := []string{
		"-i", "in.mp4",
		"-map", "0:v:0", "-map", "0:a:0",
		"-c:v", "copy",
		"-c:a", "copy",
		"-f", "hls",
		"-hls_time", "6",
		"-hls_playlist_type", "event",
		"-hls_segment_type", "mpegts",
		"-hls_flags", "independent_segments",
		"-start_number", "0",
		"-hls_segment_filename", "seg_%05d.ts",
		"main.m3u8",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("empty-codec args mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestBuildArgs_AudioStreamAndBurn(t *testing.T) {
	two := 2
	got := strings.Join(buildArgs(Request{
		InputPath: "in.mkv", VideoCodec: "h264", AudioCodec: "aac", Container: "ts",
		AudioStreamIndex: &two, MaxHeight: 480,
		Subtitle: &Subtitle{Path: "/m/it's (1).mkv", Embedded: true, Index: 1},
	}), " ")
	for _, want := range []string{
		"-map 0:v:0", "-map 0:2",
		`-vf subtitles=filename=/m/it\\\'s (1).mkv:si=1,scale=-2:480`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("args lack %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "0:a:0") {
		t.Errorf("default audio mapped as well:\n%s", got)
	}

	// copied video cannot be filtered: the burn is dropped, not half applied
	got = strings.Join(buildArgs(Request{InputPath: "in.mkv", VideoCodec: "copy", Subtitle: &Subtitle{Path: "s.srt"}}), " ")
	if strings.Contains(got, "subtitles=") {
		t.Errorf("burn-in on a copied picture:\n%s", got)
	}

	got = strings.Join(buildArgs(Request{
		InputPath: "in.mkv", VideoCodec: "h264", MaxHeight: 360,
		Subtitle: &Subtitle{Path: "in.mkv", Bitmap: true, StreamIndex: 3},
	}), " ")
	for _, want := range []string{"-filter_complex [0:v:0][0:3]overlay=", "scale=-2:360[v]", "-map [v]"} {
		if !strings.Contains(got, want) {
			t.Errorf("bitmap args lack %q:\n%s", want, got)
		}
	}
}

func TestProgressiveArgs(t *testing.T) {
	args := strings.Join(ProgressiveArgs(Request{InputPath: "/m/a.mkv", VideoCodec: "copy", AudioCodec: "aac"}, "mp4"), " ")
	for _, want := range []string{"-i /m/a.mkv", "-c:v copy", "-c:a aac", "-movflags frag_keyframe+empty_moov+default_base_moof", "-f mp4 pipe:1"} {
		if !strings.Contains(args, want) {
			t.Errorf("%q missing in %s", want, args)
		}
	}
	if strings.Contains(args, "hls") {
		t.Errorf("hls muxer in progressive args: %s", args)
	}
}
