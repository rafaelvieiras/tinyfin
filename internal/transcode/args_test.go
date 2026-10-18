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
		"-muxdelay", "0", "-muxpreload", "0", "-output_ts_offset", "10.000",
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
		"-muxdelay", "0", "-muxpreload", "0", "-output_ts_offset", "10.000",
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
		"-muxdelay", "0", "-muxpreload", "0", "-output_ts_offset", "22.345",
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
		"-muxdelay", "0", "-muxpreload", "0", "-output_ts_offset", "10.000",
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
		`-vf setparams=color_primaries=bt709:color_trc=bt709:colorspace=bt709,scale=-2:480,format=yuv420p,subtitles=f=/m/it\\\'s (1).mkv:si=1`,
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
	for _, want := range []string{"-filter_complex [0:3]null[sub];[0:v:0]setparams=", "scale=-2:360,format=yuv420p[main];[main][sub]overlay=", "[v] -map [v]"} {
		if !strings.Contains(got, want) {
			t.Errorf("bitmap args lack %q:\n%s", want, got)
		}
	}
}

func TestBuildArgs_BitmapGraph(t *testing.T) {
	base := Request{
		InputPath: "in.mkv", VideoCodec: "h264", Container: "ts",
		SourceWidth: 640, SourceHeight: 360,
		Subtitle: &Subtitle{Path: "in.mkv", Bitmap: true, StreamIndex: 2, Width: 640, Height: 360},
	}
	got := strings.Join(buildArgs(base), " ")
	for _, want := range []string{
		"-canvas_size 640x360 -i in.mkv",
		"[0:2]scale,scale=640:360:fast_bilinear[sub];[0:v:0]setparams=color_primaries=bt709:color_trc=bt709:colorspace=bt709,format=yuv420p[main];[main][sub]overlay=eof_action=pass:repeatlast=0[v]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("same-canvas args lack %q:\n%s", want, got)
		}
	}

	// another canvas shape: fitted to the height, cropped or padded to the size
	base.Subtitle = &Subtitle{Path: "in.mkv", Bitmap: true, StreamIndex: 2, Width: 320, Height: 240}
	got = strings.Join(buildArgs(base), " ")
	if want := `[0:2]scale,scale=-1:360:fast_bilinear,crop,pad=max(640\,iw):max(360\,ih):(ow-iw)/2:(oh-ih)/2:black@0,crop=640:360[sub]`; !strings.Contains(got, want) {
		t.Errorf("fitted args lack %q:\n%s", want, got)
	}

	// no canvas known: no -canvas_size
	base.Subtitle = &Subtitle{Path: "in.mkv", Bitmap: true, StreamIndex: 2}
	if got = strings.Join(buildArgs(base), " "); strings.Contains(got, "canvas_size") {
		t.Errorf("canvas_size without a canvas:\n%s", got)
	}

	// scaled output: the pictures follow the scaled size
	base.MaxWidth = 320
	base.Subtitle = &Subtitle{Path: "in.mkv", Bitmap: true, StreamIndex: 2, Width: 640, Height: 360}
	got = strings.Join(buildArgs(base), " ")
	if want := "scale,scale=320:180:fast_bilinear[sub]"; !strings.Contains(got, want) {
		t.Errorf("scaled args lack %q:\n%s", want, got)
	}

	// a sidecar is a second input with the canvas of its own
	base.MaxWidth = 0
	base.Subtitle = &Subtitle{Path: "s.idx", Bitmap: true, External: true, StreamIndex: 0, Width: 640, Height: 360}
	got = strings.Join(buildArgs(base), " ")
	if want := "-canvas_size 640x360 -i in.mkv -canvas_size 640x360 -i s.idx"; !strings.Contains(got, want) {
		t.Errorf("sidecar args lack %q:\n%s", want, got)
	}
	if want := "[1:0]scale,scale=640:360"; !strings.Contains(got, want) {
		t.Errorf("sidecar args lack %q:\n%s", want, got)
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

func TestBuildArgs_CopiedPictureIsCutAtTheKeyFramePlan(t *testing.T) {
	req := Request{
		InputPath: "in.mkv", VideoCodec: "copy", AudioCodec: "copy", Container: "ts",
		StartTimeTicks: 500_230_000, StartNumber: 6,
		Cuts: []int64{70230000, 520230000, 624400000, 1004400000},
	}
	got := strings.Join(buildArgs(req), " ")
	if !strings.Contains(got, "-f segment -segment_times ") || !strings.Contains(got, "-segment_start_number 6") {
		t.Fatalf("expected the segment muxer numbered from 6, got: %s", got)
	}
	if !strings.Contains(got, "-segment_times 62.018,72.435,110.435 ") {
		t.Fatalf("cuts at or before the start must be left out and the rest shifted by 10 s, got: %s", got)
	}
	if !strings.Contains(got, "-ss 50.023 ") || !strings.Contains(got, "-output_ts_offset 60.023") {
		t.Fatalf("seek and timestamp offset expected, got: %s", got)
	}
	// an encoded picture is cut by the HLS muxer on its grid, numbered from the start number
	req.VideoCodec = "h264"
	got = strings.Join(buildArgs(req), " ")
	if strings.Contains(got, "-f segment") || !strings.Contains(got, "-f hls") || !strings.Contains(got, "-start_number 6") {
		t.Fatalf("an encoded picture uses the hls muxer, got: %s", got)
	}
}

func TestBuildArgs_SeekRoundsUpToTheMillisecond(t *testing.T) {
	got := strings.Join(buildArgs(Request{InputPath: "in.mkv", VideoCodec: "copy", AudioCodec: "copy", StartTimeTicks: 300_234_500}), " ")
	if !strings.HasPrefix(got, "-ss 30.024 ") {
		t.Fatalf("a seek must not land before the key frame, got: %s", got)
	}
}

func TestSegmentCuts(t *testing.T) {
	// key frames of a file whose picture was cut at 0, 7, 13, 23.4, 31, 41.4, 50, 52, 62.4: the i-th
	// segment closes at the first key frame at or after (i+1)*6 s that follows the last cut
	kf := []float64{0.023, 7.023, 13.023, 23.44, 31.023, 41.44, 50.023, 52.023, 62.44}
	want := []int64{70230000, 130230000, 234400000, 310230000, 414400000, 500230000, 520230000, 624400000}
	got := SegmentCuts(kf, 6)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cuts: got %v want %v", got, want)
	}
	// a burst of key frames after a long gap: those before the next grid line are skipped
	got = SegmentCuts([]float64{0, 10.44, 20.023, 21.023, 22.023, 23.023, 33.44}, 6)
	want = []int64{104400000, 200230000, 210230000, 334400000}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cuts: got %v want %v", got, want)
	}
}
