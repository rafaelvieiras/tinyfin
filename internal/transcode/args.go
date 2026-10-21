package transcode

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ticksPerSecond is Jellyfin's tick unit: 100ns per tick, so
// StartTimeTicks/ticksPerSecond gives whole+fractional seconds (docs/04's
// wire format uses the same unit for PositionTicks elsewhere).
const ticksPerSecond = 10_000_000

// buildArgs builds the jellyfin-ffmpeg argument list (everything after the
// binary name) for req. Output paths (-hls_segment_filename, the playlist
// path) are deliberately plain relative filenames, not joined against a job
// directory: Start runs the subprocess with cmd.Dir set to the job
// directory instead, so that the HLS playlist ffmpeg writes references its
// segments by plain relative name ("seg_00000.ts") rather than an absolute
// filesystem path. That matters beyond tidiness — a real HLS client
// resolves a playlist's segment URIs relative to the playlist's own URL, so
// an absolute filesystem path baked into the manifest would resolve to a
// bogus, unservable URL on the client side (internal/api's handleHLSPlaylist/
// handleHLSSegment rely on exactly this relative-naming behavior).
//
// Split out from Start so the argument-construction logic — the part with
// actual branching to get right — is testable without spawning a
// subprocess.
//
// Mode (Remux/Direct Stream/Transcode, docs/07) isn't a parameter: it falls
// out of req.VideoCodec/req.AudioCodec being "copy" or not, which is also
// exactly what determines which encode flags are valid to add (stream-copy
// flags like -b:v have no meaning applied to a copied stream).
func buildArgs(req Request) []string {
	args := encodeArgs(req)
	if v := req.VideoCodec; v != "" && v != "copy" && v != "none" {
		// cut on the segment grid, so the media playlist can be laid out
		// before the first segment exists
		seg := req.SegmentSeconds
		if seg <= 0 {
			seg = defaultSegmentSeconds
		}
		args = append(args, "-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", seg))
		if v == "h264" || v == "hevc" {
			// no extra key frames on a scene change: the segment grid is the plan
			args = append(args, "-sc_threshold", "0")
		}
	}
	return append(args, hlsOutputArgs(req)...)
}

// ProgressiveArgs builds the ffmpeg arguments of a transcode written to
// stdout as one stream in format (mp4, matroska, mpegts, ...): the same
// mapping and codecs as an HLS job, then the muxer.
func ProgressiveArgs(req Request, format string) []string {
	args := append([]string{"-v", "error", "-nostdin"}, encodeArgs(req)...)
	if format == "mp4" {
		// a non-seekable output: fragments instead of a trailing index
		args = append(args, "-movflags", "frag_keyframe+empty_moov+default_base_moof")
	}
	return append(args, "-f", format, "pipe:1")
}

// encodeArgs is the input, stream mapping and codec part shared by the HLS
// and the progressive output.
func encodeArgs(req Request) []string {
	var args []string

	// -ss before -i is fast (input-side) seek: ffmpeg seeks by demuxing
	// straight to the target timestamp instead of decoding and discarding
	// every frame before it, which matters for an HLS job where the
	// client is waiting on the first segment.
	if req.StartTimeTicks > 0 {
		// rounded up to the millisecond: a seek to the time of a key frame must
		// not land a hair before it, on the key frame before
		ms := (req.StartTimeTicks + ticksPerSecond/1000 - 1) / (ticksPerSecond / 1000)
		args = append(args, "-ss", strconv.FormatFloat(float64(ms)/1000, 'f', 3, 64))
	}

	burn := req.Subtitle != nil && !(req.VideoCodec == "" || req.VideoCodec == "copy")
	args = append(args, canvasArgs(req.Subtitle)...)
	args = append(args, "-i", req.InputPath)
	subInput := 0
	if burn && req.Subtitle.Bitmap && req.Subtitle.External {
		// the pictures of a sidecar are a second input, seeked like the first
		if req.StartTimeTicks > 0 {
			ms := (req.StartTimeTicks + ticksPerSecond/1000 - 1) / (ticksPerSecond / 1000)
			args = append(args, "-ss", strconv.FormatFloat(float64(ms)/1000, 'f', 3, 64))
		}
		args = append(args, canvasArgs(req.Subtitle)...)
		args = append(args, "-i", req.Subtitle.Path)
		subInput = 1
	}

	// no tags, language names or chapters in the output: the transport stream's
	// stream table carries no language descriptor
	args = append(args, "-map_metadata", "-1", "-map_chapters", "-1")
	// Burn-in (docs/contract/specs/subtitles.md, "Burned-in subtitles") is a
	// filter on the video, so it needs a re-encode: a copied video stream
	// cannot be filtered (the caller promotes the mode, see
	// internal/playback.Options.BurnSubtitle).
	videoMap := "0:v:0"
	if burn && req.Subtitle.Bitmap {
		// Bitmap tracks (PGS, DVD, DVB) are decoded to pictures, scaled to
		// the output picture and overlaid on it.
		args = append(args, "-filter_complex", bitmapGraph(req, subInput))
		videoMap = "[v]"
	}
	audioOnly := req.VideoCodec == "none"
	if audioOnly {
		// an audio file: nothing to map or encode on the picture side
		args = append(args, "-vn")
	} else {
		args = append(args, "-map", videoMap)
	}
	if req.AudioStreamIndex != nil {
		args = append(args, "-map", fmt.Sprintf("0:%d", *req.AudioStreamIndex))
	} else {
		args = append(args, "-map", "0:a:0")
	}

	videoCopy := req.VideoCodec == "" || req.VideoCodec == "copy" || req.VideoCodec == "none"
	audioCopy := req.AudioCodec == "" || req.AudioCodec == "copy"

	if audioOnly {
		// no picture
	} else if videoCopy {
		args = append(args, "-c:v", "copy")
	} else {
		args = append(args, videoEncodeArgs(req)...)
		if vf := videoFilter(req, burn); vf != "" {
			args = append(args, "-vf", vf)
		}
	}

	if audioCopy {
		args = append(args, "-c:a", "copy")
		if req.StartTimeTicks > 0 && !videoCopy {
			// after a seek the copied sound would start at the key frame before
			// the position, ahead of the re-encoded picture
			args = append(args, "-copypriorss:a", "0")
			if req.AudioDropTicks > 0 {
				// the picture of a seek close to the end of the file begins before
				// the position asked for, the sound does not
				args = append(args, "-bsf:a", fmt.Sprintf("noise=drop='lt(pts*tb\\,%s)'", strconv.FormatFloat(float64(req.AudioDropTicks)/ticksPerSecond, 'f', 3, 64)))
			}
		}
	} else {
		enc := req.AudioEncoder
		if enc == "" {
			enc = req.AudioCodec
		}
		args = append(args, "-c:a", enc)
		if enc == "libopus" {
			args = append(args, "-strict", "-2")
		}
		// Without a channel plan from the caller the sound is downmixed to
		// stereo: the common case for a client that needed audio transcoded in
		// the first place is a browser/mobile decoder without multichannel
		// support.
		ch := req.AudioChannels
		if ch <= 0 {
			ch = 2
		}
		args = append(args, "-ac", strconv.Itoa(ch))
		if req.AudioBitrate > 0 {
			args = append(args, "-b:a", strconv.Itoa(req.AudioBitrate))
		}
		if req.AudioBoost {
			// a surround mix folded down to stereo comes out quieter
			args = append(args, "-af", "volume=2")
		}
	}

	return args
}

// hlsTimestampOffset is the shift (seconds) the segments' timestamps carry
// against the source's: the reference writes every HLS segment 10 s later than
// the picture's own time, whatever the position the transcode starts at.
const hlsTimestampOffset = 10

// hlsMuxDelay is the MPEG-TS muxer delay that produces that shift. The muxer
// adds twice its delay to every timestamp (and starts its clock reference
// that delay before the first one), after the format layer has moved
// timestamps that would be negative up to zero. That order is what the
// reference's segments show: the source's own start (a few milliseconds
// of audio priming or picture reordering ahead of the first frame, which
// MP4 and Matroska files hold as negative times) is lifted to zero first
// and the 10 s come on top, so the first sound of a file is at exactly 10 s,
// where an output offset of 10 s would leave the priming at 9.98 s.
const hlsMuxDelay = hlsTimestampOffset / 2

// segmentTimeMargin is how far before a key frame's time a cut is placed, so
// the frame itself opens the new segment whatever the rounding of its time.
const segmentTimeMargin = 0.005

// hlsOutputArgs is the HLS muxer part of an HLS job. A job always numbers its
// segments from req.StartNumber and shifts the timestamps by the seek offset
// (the muxer delay adds hlsTimestampOffset), so the segment n of a job that
// starts at the position of segment n looks like the one a transcode from the
// start of the file writes. A copied picture is cut at req.Cuts (the key frame plan) with
// the segment muxer; everything else is cut by the HLS muxer on its grid.
func hlsOutputArgs(req Request) []string {
	var args []string
	segmentSeconds := req.SegmentSeconds
	if segmentSeconds <= 0 {
		segmentSeconds = defaultSegmentSeconds
	}
	container := req.Container
	if container == "" {
		// Container is documented as always set by the caller; this is a
		// defensive fallback only, so a Request built by hand (e.g. in a
		// test) doesn't produce an extension-less segment filename.
		container = "ts"
	}
	seek := float64(req.StartTimeTicks) / ticksPerSecond
	args = append(args,
		"-muxdelay", strconv.Itoa(hlsMuxDelay), "-muxpreload", "0",
		"-output_ts_offset", strconv.FormatFloat(seek, 'f', 3, 64),
	)

	if req.Cuts != nil && req.VideoCodec == "copy" {
		var times []string
		for _, c := range req.Cuts {
			if c > req.StartTimeTicks+ticksPerSecond/1000 {
				times = append(times, strconv.FormatFloat(float64(c)/ticksPerSecond-segmentTimeMargin, 'f', 3, 64))
			}
		}
		if len(times) == 0 {
			times = []string{"1000000"} // one segment to the end
		}
		return append(args,
			"-f", "segment",
			"-segment_times", strings.Join(times, ","),
			"-segment_format", "mpegts",
			"-segment_start_number", strconv.Itoa(req.StartNumber),
			"-reset_timestamps", "0",
			"-segment_list", "main.m3u8",
			"-segment_list_type", "m3u8",
			"seg_%05d."+container,
		)
	}

	args = append(args,
		"-f", "hls",
		"-hls_time", strconv.Itoa(segmentSeconds),
		// "event", not "vod": confirmed directly against a real movie file
		// (not just a short synthetic clip) that ffmpeg's hls muxer holds
		// "vod"'s playlist entirely in memory and writes main.m3u8 to disk
		// only once, at the very end of the whole encode. "event" writes the
		// playlist progressively after every segment (still keeping every
		// segment, unlike the default "live" type's sliding window) and
		// appends #EXT-X-ENDLIST once the source is fully consumed.
		"-hls_playlist_type", "event",
		"-hls_segment_type", "mpegts",
		"-hls_flags", "independent_segments",
		"-start_number", strconv.Itoa(req.StartNumber),
		"-hls_segment_filename", "seg_%05d."+container,
		"main.m3u8",
	)

	return args
}

// videoFilter builds the -vf chain of a re-encode: the text-subtitle burn-in
// first, at the source resolution (libass lays the text out for the picture
// it is given, so rendering before the scale keeps the proportions of the
// authored style), then the scale. A bitmap burn-in is a filter graph of its
// own (see buildArgs) and contributes nothing here. After an input-side seek
// the frames restart at zero while the cues keep their absolute times, so the
// chain shifts the timestamps by the seek offset around the renderer.
func videoFilter(req Request, burn bool) string {
	if burn && !req.Subtitle.Bitmap {
		// colour tags, scaling and pixel format first: the renderer lays the
		// text out on the picture the viewer gets
		chain := []string{burnVideoFilter(req)}
		seek := req.StartTimeTicks > 0
		if seek {
			chain = append(chain, fmt.Sprintf("setpts=PTS+%s/TB", strconv.FormatFloat(float64(req.StartTimeTicks)/ticksPerSecond, 'f', 3, 64)))
		}
		f := "subtitles=f=" + quoteFilterValue(req.Subtitle.Path)
		if req.Subtitle.Embedded {
			f += fmt.Sprintf(":si=%d", req.Subtitle.Index)
		}
		if req.Subtitle.Charset != "" {
			f += ":charenc=" + req.Subtitle.Charset
		}
		if req.FontsDir != "" {
			f += ":fontsdir=" + quoteFilterValue(req.FontsDir)
		}
		chain = append(chain, f)
		if seek {
			chain = append(chain, "setpts=PTS-STARTPTS")
		}
		return strings.Join(chain, ",")
	}
	if burn && req.Subtitle.Bitmap {
		return ""
	}
	chain := []string{"setparams=color_primaries=bt709:color_trc=bt709:colorspace=bt709"}
	if sc := scaleFilter(req); sc != "" {
		chain = append(chain, sc)
	}
	chain = append(chain, "format=yuv420p")
	return strings.Join(chain, ",")
}

// scaleFilter is the scale of a re-encoded picture, written the way the
// reference writes it: the width and height are limits on the picture's
// display size (the pixel size times the sample aspect ratio, "a"), rounded
// down to even numbers, and the other side follows from the aspect. A limit
// on the width alone, on the height alone, and on both have each their own
// form; a request with no limit at all is held to the size of the source.
func scaleFilter(req Request) string {
	w, h := req.MaxWidth, req.MaxHeight
	if w <= 0 && h <= 0 {
		w, h = req.SourceWidth, req.SourceHeight
	}
	switch {
	case w > 0 && h > 0:
		return fmt.Sprintf(`scale=trunc(min(max(iw\,ih*a)\,min(%d\,%d*a))/2)*2:trunc(min(max(iw/a\,ih)\,min(%d/a\,%d))/2)*2`, w, h, w, h)
	case w > 0:
		return fmt.Sprintf(`scale=trunc(min(max(iw\,ih*a)\,%d)/2)*2:trunc(ow/a/2)*2`, w)
	case h > 0:
		return fmt.Sprintf(`scale=trunc(oh*a/2)*2:min(max(iw/a\,ih)\,%d)`, h)
	}
	return ""
}

// videoEncodeArgs is the encoder part of a re-encoded picture. H.264 and HEVC
// are written with the reference's encoder settings: constant quality capped
// by the requested bit rate, the "veryfast" preset with a light motion search,
// closed GOPs, and the profile and level only when the request names them. The
// B-frames of those presets are what puts the decode time of the first picture
// of a segment ahead of its presentation time.
func videoEncodeArgs(req Request) []string {
	maxrate := CapVideoBitrate(req.VideoBitrate, req.VideoSourceBitrate, req.VideoSourceCodec, req.VideoCodec, req.VideoSourceBitDepth)
	rate := []string{"-maxrate", strconv.Itoa(maxrate), "-bufsize", strconv.Itoa(2 * maxrate)}
	switch req.VideoCodec {
	case "h264":
		args := []string{"-c:v", "libx264", "-preset", "veryfast", "-crf", "23"}
		args = append(args, rate...)
		if req.H264Profile != "" {
			args = append(args, "-profile:v", req.H264Profile)
		}
		if req.H264Level != "" {
			args = append(args, "-level", req.H264Level)
		}
		return append(args, "-x264opts", "subme=0:me_range=16:rc_lookahead=10:me=hex:open_gop=0")
	case "hevc":
		args := []string{"-c:v", "libx265", "-tag:v", "hvc1", "-preset", "veryfast", "-crf", "28"}
		args = append(args, rate...)
		if req.H264Profile != "" {
			args = append(args, "-profile:v", req.H264Profile)
		}
		return append(args, "-x265-params", "no-scenecut=1:no-open-gop=1:no-info=1:subme=3:merange=25:rc-lookahead=10:me=star:ctu=32:max-tu-size=32:min-cu-size=16:rskip=2:rskip-edge-threshold=2:no-sao=1:no-strong-intra-smoothing=1")
	}
	args := []string{"-c:v", req.VideoCodec}
	if req.VideoBitrate > 0 {
		b := strconv.Itoa(req.VideoBitrate)
		args = append(args, "-b:v", b, "-maxrate", b, "-bufsize", strconv.Itoa(2*req.VideoBitrate))
	}
	return args
}

// quoteFilterValue escapes v for use as a filter option value inside a
// -vf chain. ffmpeg parses the argument twice (ffmpeg-filters(1), "Notes on
// filtergraph escaping"): first the option separators of the filter, then the
// graph's own, so a value is escaped for the option level (\ ' :) and the
// result again for the graph level (\ ' [ ] , ;).
func quoteFilterValue(v string) string {
	esc := func(s, special string) string {
		var b strings.Builder
		for _, r := range s {
			if r == '\\' || strings.ContainsRune(special, r) {
				b.WriteByte('\\')
			}
			b.WriteRune(r)
		}
		return b.String()
	}
	return esc(esc(v, `':`), `'[],;`)
}

// CapVideoBitrate is the rate the reference holds an encoded picture to: the
// requested rate, lowered to what the source's own picture is judged to need.
// A source at up to 2 Mb/s counts 2.5 times, up to 3 Mb/s twice, anything
// above once; that figure is then scaled by 4 up to 500 kb/s, 3 up to 1 Mb/s,
// 2.5 up to 2 Mb/s and 2 up to 3 Mb/s (at least the ratio of the two codecs'
// efficiency, hevc and av1 counting 0.6), and never exceeds the request.
// A source deeper than 8 bits is judged to need more: the figure is raised by
// BitDepthFactor of the source rate before the request and the codec ladder
// apply. Without a request (0) or a known source rate the request stands.
func CapVideoBitrate(requested, source int, sourceCodec, outCodec string, bitDepth int) int {
	if requested <= 0 || source <= 0 {
		return requested
	}
	// (each step rounds to a whole number, halves to even)
	x := float64(source)
	switch {
	case source <= 2000000:
		x = math.RoundToEven(x * 2.5)
	case source <= 3000000:
		x = math.RoundToEven(x * 2)
	}
	if bitDepth > 8 {
		x = math.RoundToEven(x * BitDepthFactor(source))
	}
	x = min(x, float64(requested))
	// (a target more efficient than the source never lowers the figure)
	f := max(1, codecEfficiency(outCodec)/codecEfficiency(sourceCodec))
	switch {
	case x <= 500000:
		f = max(f, 4)
	case x <= 1000000:
		f = max(f, 3)
	case x <= 2000000:
		f = max(f, 2.5)
	case x <= 3000000:
		f = max(f, 2)
	}
	return min(int(math.RoundToEven(f*x)), requested)
}

// BitDepthFactor is how much more the reference allows a picture deeper than
// 8 bits, by the source rate (bits/s): 1.5 up to 4 Mb/s, 1.2 up to 8 Mb/s,
// 1.1 up to 12 Mb/s, 1.05 above (measured on 10-bit H.264 and HEVC sources
// from 40 kb/s to 34 Mb/s).
func BitDepthFactor(source int) float64 {
	switch {
	case source <= 4000000:
		return 1.5
	case source <= 8000000:
		return 1.2
	case source <= 12000000:
		return 1.1
	}
	return 1.05
}

func codecEfficiency(codec string) float64 {
	switch strings.ToLower(codec) {
	case "hevc", "h265", "av1":
		return 0.6
	}
	return 1
}
