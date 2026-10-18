package transcode

import (
	"fmt"
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
	}
	return append(args, hlsOutputArgs(req)...)
}

// ProgressiveArgs builds the ffmpeg arguments of a transcode written to
// stdout as one stream in format (mp4, matroska, mpegts, ...): the same
// mapping and codecs as an HLS job, then the muxer.
func ProgressiveArgs(req Request, format string) []string {
	args := append([]string{"-v", "error", "-nostdin"}, encodeArgs(req)...)
	args = append(args, "-map_metadata", "-1")
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
		args = append(args, "-c:v", req.VideoCodec)
		// The preset/profile/level triple is specifically an H.264
		// encode-quality/compatibility knob (docs/07's confirmed-good HLS
		// pattern); a non-H.264 target codec (hevc, etc.) doesn't share
		// this flag set and is left to its own codec-specific defaults —
		// out of scope until a broader hardware-accelerated ladder lands.
		if req.VideoCodec == "h264" {
			profile, level := "high", "4.1"
			switch req.H264Profile {
			case "baseline", "constrainedbaseline":
				profile = "baseline"
			case "main", "high":
				profile = req.H264Profile
			}
			if req.H264Level > 0 {
				level = fmt.Sprintf("%.1f", float64(min(req.H264Level, 51))/10)
			}
			args = append(args, "-preset", "veryfast", "-profile:v", profile, "-level", level)
		}
		if req.VideoBitrate > 0 {
			bitrate := strconv.Itoa(req.VideoBitrate)
			args = append(args, "-b:v", bitrate)
			args = append(args, "-maxrate", bitrate)
			args = append(args, "-bufsize", strconv.Itoa(2*req.VideoBitrate))
		}
		// Scaling only makes sense alongside a re-encode — a stream copy
		// can't be filtered, so this is nested under the videoCopy==false
		// branch rather than applied unconditionally whenever MaxHeight is
		// set.
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
		}
	} else {
		args = append(args, "-c:a", req.AudioCodec)
		if req.AudioBitrate > 0 {
			args = append(args, "-b:a", strconv.Itoa(req.AudioBitrate))
		}
		// Downmix to stereo: the common case for a client that needed
		// audio transcoded in the first place is a browser/mobile
		// decoder without multichannel support, not a surround-capable
		// target that just happens to want a different codec.
		args = append(args, "-ac", "2")
	}

	return args
}

// hlsTimestampOffset is the shift (seconds) the segments' timestamps carry
// against the source's: the reference writes every HLS segment 10 s later than
// the picture's own time, whatever the position the transcode starts at.
const hlsTimestampOffset = 10

// segmentTimeMargin is how far before a key frame's time a cut is placed, so
// the frame itself opens the new segment whatever the rounding of its time.
const segmentTimeMargin = 0.005

// hlsOutputArgs is the HLS muxer part of an HLS job. A job always numbers its
// segments from req.StartNumber and shifts the timestamps by the seek offset
// plus hlsTimestampOffset, so the segment n of a job that starts at the
// position of segment n looks like the one a transcode from the start of the
// file writes. A copied picture is cut at req.Cuts (the key frame plan) with
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
		"-muxdelay", "0", "-muxpreload", "0",
		"-output_ts_offset", strconv.FormatFloat(seek+hlsTimestampOffset, 'f', 3, 64),
	)

	if req.Cuts != nil && req.VideoCodec == "copy" {
		var times []string
		for _, c := range req.Cuts {
			if c > req.StartTimeTicks+ticksPerSecond/1000 {
				times = append(times, strconv.FormatFloat(float64(c)/ticksPerSecond+hlsTimestampOffset-segmentTimeMargin, 'f', 3, 64))
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
	var chain []string
	if req.MaxWidth > 0 && !(burn && req.Subtitle.Bitmap) {
		chain = append(chain, fmt.Sprintf("scale='min(iw,%d)':-2", req.MaxWidth))
	}
	if req.MaxHeight > 0 && !(burn && req.Subtitle.Bitmap) {
		chain = append(chain, fmt.Sprintf("scale=-2:%d", req.MaxHeight))
	}
	return strings.Join(chain, ",")
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
