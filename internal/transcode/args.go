package transcode

import (
	"fmt"
	"strconv"
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
	var args []string

	// -ss before -i is fast (input-side) seek: ffmpeg seeks by demuxing
	// straight to the target timestamp instead of decoding and discarding
	// every frame before it, which matters for an HLS job where the
	// client is waiting on the first segment.
	if req.StartTimeTicks > 0 {
		seconds := float64(req.StartTimeTicks) / float64(ticksPerSecond)
		args = append(args, "-ss", strconv.FormatFloat(seconds, 'f', 3, 64))
	}

	args = append(args, "-i", req.InputPath)
	args = append(args, "-map", "0:v:0", "-map", "0:a:0")

	videoCopy := req.VideoCodec == "" || req.VideoCodec == "copy"
	audioCopy := req.AudioCodec == "" || req.AudioCodec == "copy"

	if videoCopy {
		args = append(args, "-c:v", "copy")
	} else {
		args = append(args, "-c:v", req.VideoCodec)
		// The preset/profile/level triple is specifically an H.264
		// encode-quality/compatibility knob (docs/07's confirmed-good HLS
		// pattern); a non-H.264 target codec (hevc, etc.) doesn't share
		// this flag set and is left to its own codec-specific defaults —
		// out of scope until a broader hardware-accelerated ladder lands.
		if req.VideoCodec == "h264" {
			args = append(args, "-preset", "veryfast", "-profile:v", "high", "-level", "4.1")
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
		if req.MaxHeight > 0 {
			args = append(args, "-vf", fmt.Sprintf("scale=-2:%d", req.MaxHeight))
		}
	}

	if audioCopy {
		args = append(args, "-c:a", "copy")
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

	args = append(args,
		"-f", "hls",
		"-hls_time", strconv.Itoa(segmentSeconds),
		// "event", not "vod": confirmed directly against a real movie file
		// (not just a short synthetic clip) that ffmpeg's hls muxer holds
		// "vod"'s playlist entirely in memory and writes main.m3u8 to disk
		// only once, at the very end of the whole encode — worthless for a
		// feature-length file, since internal/api's handleHLSPlaylist polls
		// for exactly that file to appear while the job is still running.
		// "event" writes the playlist progressively after every segment
		// (still keeping every segment, unlike the default "live" type's
		// sliding window) and appends #EXT-X-ENDLIST once the source is
		// fully consumed — the actual "growing VOD" behavior this package
		// needs.
		"-hls_playlist_type", "event",
		"-hls_segment_type", "mpegts",
		"-hls_flags", "independent_segments",
		"-start_number", "0",
		"-hls_segment_filename", "seg_%05d."+container,
		"main.m3u8",
	)

	return args
}
