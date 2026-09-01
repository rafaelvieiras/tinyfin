package scan

import (
	"context"
	"encoding/json"
	"os/exec"
	"strconv"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

type ffprobeOutput struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		Channels  int    `json:"channels"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// probe runs ffprobe against path and returns its parsed JSON output. A
// probe failure (missing binary, corrupt/unreadable file) is returned to
// the caller, which — per Scan's doc comment — logs it into Stats.Errors
// and still catalogs the item (with empty MediaStreams/RunTimeTicks)
// rather than skipping it outright: a file a real client can still be
// told about, even imperfectly, beats one silently dropped from the
// catalog.
func (s *Scanner) probe(ctx context.Context, path string) (ffprobeOutput, error) {
	var out ffprobeOutput
	cmd := exec.CommandContext(ctx, s.ffprobePath, "-v", "error", "-print_format", "json", "-show_format", "-show_streams", path)
	data, err := cmd.Output()
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, err
	}
	return out, nil
}

// mediaStreamsFromProbe converts ffprobe's stream list into
// storage.MediaStreamRef entries, keeping only video/audio tracks (subtitle
// tracks embedded in a container aren't surfaced by ffprobe's default
// -show_streams shape used here without further flags, matching devseed's
// original scope).
func mediaStreamsFromProbe(info ffprobeOutput) []storage.MediaStreamRef {
	var streams []storage.MediaStreamRef
	idx := 0
	for _, st := range info.Streams {
		switch st.CodecType {
		case "video":
			w, h := st.Width, st.Height
			streams = append(streams, storage.MediaStreamRef{
				Index: idx, Type: storage.StreamTypeVideo, Codec: st.CodecName,
				IsDefault: true, Width: &w, Height: &h,
			})
			idx++
		case "audio":
			ch := st.Channels
			streams = append(streams, storage.MediaStreamRef{
				Index: idx, Type: storage.StreamTypeAudio, Codec: st.CodecName,
				IsDefault: true, Channels: &ch,
			})
			idx++
		}
	}
	return streams
}

// runtimeTicksFromProbe converts ffprobe's format.duration (decimal
// seconds, as a string) to the reference server's RunTimeTicks unit
// (100ns ticks) — nil if duration is missing/unparseable/non-positive.
func runtimeTicksFromProbe(info ffprobeOutput) *int64 {
	d, err := strconv.ParseFloat(info.Format.Duration, 64)
	if err != nil || d <= 0 {
		return nil
	}
	ticks := int64(d * 10_000_000)
	return &ticks
}
