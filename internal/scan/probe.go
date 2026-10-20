package scan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
	"github.com/rafaelvieiras/tinyfin/internal/subtitle"
)

// ffprobeStream is one entry of ffprobe -show_streams' JSON output. Field
// names/shapes (including Disposition and the string-typed
// Level/RFrames/SampleRate — ffprobe emits all three as JSON numbers, but
// also as empty strings for a stream where the value doesn't apply, so a
// plain int/float64 json tag would fail to unmarshal on those streams) are
// ffprobe's own documented -show_streams output, not anything specific to
// Jellyfin's reference server — public, well-known FFmpeg CLI behavior, not
// a clean-room concern (same reasoning routing.go's normalizePathCase doc
// comment already applies to ASP.NET Core's own case-insensitive routing).
type ffprobeStream struct {
	CodecType     string `json:"codec_type"`
	CodecName     string `json:"codec_name"`
	Profile       string `json:"profile"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	Channels      int    `json:"channels"`
	ChannelLayout string `json:"channel_layout"`
	// SampleRate: ffprobe's JSON output reports this as a numeric string
	// (e.g. "48000"), not a JSON number — well-known, documented FFmpeg CLI
	// output quirk, not a clean-room concern (see this type's doc comment).
	SampleRate         string `json:"sample_rate"`
	TimeBase           string `json:"time_base"`
	DisplayAspectRatio string `json:"display_aspect_ratio"`
	RefFrames          int    `json:"refs"`
	PixFmt             string `json:"pix_fmt"`
	// Level is a pointer so that an absent member differs from the 0 and the
	// -99 ("not applicable") that ffprobe prints for codecs without a level
	// concept; the stream listings carry whatever ffprobe printed.
	Level      *int   `json:"level"`
	FieldOrder string `json:"field_order"`
	// BitRate is ffprobe's bit_rate (a numeric string, absent when the
	// container does not record one); AvgFrameRate a "num/den" fraction.
	BitRate       string `json:"bit_rate"`
	AvgFrameRate  string `json:"avg_frame_rate"`
	ColorTransfer string `json:"color_transfer"`
	// ColorPrimaries/ColorSpace, the raw frame rate, the sample aspect ratio,
	// the bit depth of lossless audio and the H.264 NAL framing are ffprobe's
	// own -show_streams members, passed through to the stream listings.
	ColorPrimaries    string `json:"color_primaries"`
	ColorSpace        string `json:"color_space"`
	RFrameRate        string `json:"r_frame_rate"`
	SampleAspectRatio string `json:"sample_aspect_ratio"`
	BitsPerRawSample  string `json:"bits_per_raw_sample"`
	// BitsPerSample is the coded sample size (PCM, ADPCM), 0 when none.
	BitsPerSample int `json:"bits_per_sample"`
	// SideData lists the stream's side data; the Dolby Vision configuration
	// record is read from it.
	SideData       []ffprobeSideData `json:"side_data_list"`
	CodecTagString string            `json:"codec_tag_string"`
	IsAVC          string            `json:"is_avc"`
	NalLengthSize  string            `json:"nal_length_size"`
	Disposition    struct {
		Default  int `json:"default"`
		Forced   int `json:"forced"`
		Original int `json:"original"`
		// HearingImpaired is the disposition flag of an SDH track.
		HearingImpaired int `json:"hearing_impaired"`
		// AttachedPic marks a picture embedded in an audio file.
		AttachedPic int `json:"attached_pic"`
	} `json:"disposition"`
	// Tags.Title/Language: an embedded per-track title/language tag, when
	// the container carries one (e.g. Matroska's TrackEntry/Name and
	// Language elements) — confirmed load-bearing, not decorative, by
	// comparing DisplayTitle output byte-for-byte against the reference
	// server for a real file: its audio track's
	// embedded title ("Indefinido Estéreo") is exactly what puts that
	// stream's DisplayTitle on docs/12's "explicit Title" merge-rule branch
	// instead of the plain attribute-join branch — leaving these two
	// unextracted meant DisplayTitle never actually exercised that branch
	// for any real file.
	Tags streamTags `json:"tags"`
}

// streamTags are the tags of one stream: the title and language the stream
// listings use, and every tag by lower-cased name (an Ogg file carries the
// file's tags on its stream).
type streamTags struct {
	Title    string
	Language string
	All      map[string]string
}

func (t *streamTags) UnmarshalJSON(data []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil // not an object: no tags
	}
	t.All = make(map[string]string, len(raw))
	for k, v := range raw {
		if str, ok := v.(string); ok {
			t.All[strings.ToLower(k)] = str
		}
	}
	t.Title, t.Language = t.All["title"], t.All["language"]
	return nil
}

// ffprobeSideData is one member of a stream's or frame's side_data_list: the
// type and, for a Dolby Vision configuration record, its fields.
type ffprobeSideData struct {
	Type            string `json:"side_data_type"`
	VersionMajor    *int   `json:"dv_version_major"`
	VersionMinor    *int   `json:"dv_version_minor"`
	Profile         *int   `json:"dv_profile"`
	Level           *int   `json:"dv_level"`
	RPUPresent      *int   `json:"rpu_present_flag"`
	ELPresent       *int   `json:"el_present_flag"`
	BLPresent       *int   `json:"bl_present_flag"`
	CompatibilityID *int   `json:"dv_bl_signal_compatibility_id"`
}

// ffprobeFrame is one entry of -show_frames: only the side data of the first
// video frame is kept.
type ffprobeFrame struct {
	MediaType string            `json:"media_type"`
	SideData  []ffprobeSideData `json:"side_data_list"`
}

type ffprobeOutput struct {
	Streams  []ffprobeStream  `json:"streams"`
	Format   ffprobeFormat    `json:"format"`
	Chapters []ffprobeChapter `json:"chapters"`
	Frames   []ffprobeFrame   `json:"frames"`
}

// ffprobeChapter is one entry of ffprobe -show_chapters' JSON output.
type ffprobeChapter struct {
	StartTime string            `json:"start_time"`
	Tags      map[string]string `json:"tags"`
}

// chaptersFromProbe converts ffprobe's chapter list into storage chapters in
// start order. An entry whose start time does not parse is dropped; the title
// is the "title" tag whatever the case of its key.
func chaptersFromProbe(info ffprobeOutput) []storage.ChapterRef {
	var out []storage.ChapterRef
	for _, c := range info.Chapters {
		secs, err := strconv.ParseFloat(strings.TrimSpace(c.StartTime), 64)
		if err != nil || secs < 0 {
			continue
		}
		var name string
		for k, v := range c.Tags {
			if strings.EqualFold(k, "title") {
				name = strings.TrimSpace(v)
			}
		}
		out = append(out, storage.ChapterRef{StartTicks: int64(math.Round(secs * 1e7)), Name: name})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartTicks < out[j].StartTicks })
	return out
}

type ffprobeFormat struct {
	Duration   string `json:"duration"`
	FormatName string `json:"format_name"`
	// BitRate is the bit rate of the whole container (a numeric string).
	BitRate string            `json:"bit_rate"`
	Tags    map[string]string `json:"tags"`
}

// title returns the title tag of the container, whatever the case of its key.
func (f ffprobeFormat) title() string {
	for k, v := range f.Tags {
		if strings.EqualFold(k, "title") {
			return strings.TrimSpace(v)
		}
	}
	return ""
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
	// Global bound on simultaneous ffprobe processes (scans may overlap).
	select {
	case s.probeSem <- struct{}{}:
		defer func() { <-s.probeSem }()
	case <-ctx.Done():
		return out, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	// The first video frame is decoded for the side data a stream header does
	// not carry (HDR10+ metadata) and for the reference-frame count a decoder
	// reports; an audio file has no such frame and is not asked for them.
	frames := !audioExtensions[strings.ToLower(videoExt(path))] && !s.noFirstFrame.Load()
	data, err := s.runProbe(ctx, path, frames)
	if err != nil && frames {
		var ee *exec.ExitError
		if errors.As(err, &ee) && bytes.Contains(ee.Stderr, []byte("only_first_vframe")) {
			// a build without the option (the stock ffmpeg ones): no frame
			// data, and not asked again
			s.noFirstFrame.Store(true)
			data, err = s.runProbe(ctx, path, false)
		}
	}
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, err
	}
	return out, nil
}

// runProbe runs ffprobe against path; withFrame adds the first video frame to
// its output.
func (s *Scanner) runProbe(ctx context.Context, path string, withFrame bool) ([]byte, error) {
	// A local file is read as the reference does (200 MB of analysis, 1 GB
	// probe size, decoder threads chosen by ffmpeg): the per-stream bit rate
	// ffprobe derives for formats that do not state one (MPEG-TS) depends on
	// how much is read. An
	// address (a .strm target) stays at FFmpeg's own defaults (5 MB, 5 s) so a
	// remote file is never read further than that.
	probesize, analyze := "1000000000", "200000000"
	if strings.Contains(path, "://") {
		probesize, analyze = "5000000", "5000000"
	}
	args := []string{"-v", "error", "-threads", "0", "-probesize", probesize, "-analyzeduration", analyze,
		"-print_format", "json", "-show_format", "-show_streams", "-show_chapters"}
	if withFrame {
		args = append(args, "-show_frames", "-only_first_vframe")
	}
	return exec.CommandContext(ctx, s.ffprobePath, append(args, path)...).Output()
}

// isInterlacedFieldOrder reports whether ffprobe's field_order value
// indicates an interlaced stream — "tt"/"bb"/"tb"/"bt" (top/bottom field
// first, in ffprobe's own naming). "progressive", "unknown", and the empty
// string (the field is entirely absent for many codecs/containers) all
// report false rather than guessing, same "don't guess past what the
// prober actually told us" convention the rest of this package already
// follows.
func isInterlacedFieldOrder(fieldOrder string) bool {
	switch fieldOrder {
	case "tt", "bb", "tb", "bt":
		return true
	default:
		return false
	}
}

// mediaStreamsFromProbe converts ffprobe's stream list into
// storage.MediaStreamRef entries for video, audio, and subtitle tracks.
// IsDefault/IsForced reflect each stream's own disposition.default/
// disposition.forced flag (ffprobe reports these per-stream — e.g. a file
// with two audio tracks can have disposition.default=1 on only one of
// them — rather than being hardcoded true for every stream of a kept
// type).
func mediaStreamsFromProbe(info ffprobeOutput) []storage.MediaStreamRef {
	var streams []storage.MediaStreamRef
	idx := 0
	seenVideo := false
	for _, st := range info.Streams {
		if st.CodecType == "video" && st.Disposition.AttachedPic == 1 {
			// The cover of an audio file, not a video track.
			streams = append(streams, storage.MediaStreamRef{
				Index: idx, Type: storage.StreamTypeEmbeddedImage, Codec: st.CodecName,
				Title: st.Tags.Title,
			})
			idx++
			continue
		}
		switch st.CodecType {
		case "video":
			w, h := st.Width, st.Height
			ref := storage.MediaStreamRef{
				Index: idx, Type: storage.StreamTypeVideo, Codec: st.CodecName,
				IsDefault:         st.Disposition.Default == 1,
				IsForced:          st.Disposition.Forced == 1,
				Width:             &w,
				Height:            &h,
				Title:             st.Tags.Title,
				Language:          st.Tags.Language,
				Profile:           st.Profile,
				IsOriginal:        st.Disposition.Original != 0,
				IsInterlaced:      isInterlacedFieldOrder(st.FieldOrder),
				TimeBase:          st.TimeBase,
				AspectRatio:       st.DisplayAspectRatio,
				PixelFormat:       st.PixFmt,
				ColorTransfer:     st.ColorTransfer,
				ColorPrimaries:    knownColor(st.ColorPrimaries),
				ColorSpace:        knownColor(st.ColorSpace),
				SampleAspectRatio: st.SampleAspectRatio,
				CodecTag:          codecTagOf(st.CodecTagString),
				IsAVC:             st.IsAVC == "true",
				NalLengthSize:     st.NalLengthSize,
			}
			if d := bitDepthOfPixFmt(st.PixFmt); d > 0 {
				ref.BitDepth = &d
			}
			if rr := parseFrameRate(st.RFrameRate); rr > 0 {
				ref.RealFrameRate = &rr
			}
			if st.RefFrames > 0 {
				rf := st.RefFrames
				ref.RefFrames = &rf
			}
			// whatever ffprobe printed, the -99 "not applicable" and the 0 of
			// a codec without levels included
			if st.Level != nil {
				lvl := float64(*st.Level)
				ref.Level = &lvl
			}
			ref.DoVi = doviOf(st.SideData)
			if !seenVideo {
				seenVideo = true
				ref.Hdr10Plus = hasHDR10Plus(info.Frames)
			}
			if fr := parseFrameRate(st.AvgFrameRate); fr > 0 {
				ref.FrameRate = &fr
			}
			ref.BitRate = streamBitRate(st, info.Format)
			streams = append(streams, ref)
			idx++
		case "audio":
			ch := st.Channels
			ref := storage.MediaStreamRef{
				Index: idx, Type: storage.StreamTypeAudio, Codec: st.CodecName,
				IsDefault:     st.Disposition.Default == 1,
				IsForced:      st.Disposition.Forced == 1,
				Channels:      &ch,
				Title:         st.Tags.Title,
				Language:      st.Tags.Language,
				Profile:       st.Profile,
				ChannelLayout: plainChannelLayout(st.ChannelLayout),
				IsOriginal:    st.Disposition.Original != 0,
				TimeBase:      st.TimeBase,
				CodecTag:      codecTagOf(st.CodecTagString),
			}
			if sr, err := strconv.Atoi(st.SampleRate); err == nil && sr > 0 {
				ref.SampleRate = &sr
			}
			if d, err := strconv.Atoi(st.BitsPerRawSample); err == nil && d > 0 {
				ref.BitDepth = &d
			} else if st.BitsPerSample > 0 {
				// PCM and ADPCM state their sample size only as the coded one
				d := st.BitsPerSample
				ref.BitDepth = &d
			}
			ref.BitRate = streamBitRate(st, info.Format)
			if ref.BitRate == nil {
				ref.BitRate = estimatedAudioBitRate(st.CodecName, ch)
			}
			streams = append(streams, ref)
			idx++
		case "data":
			streams = append(streams, storage.MediaStreamRef{
				Index: idx, Type: storage.StreamTypeData, Codec: st.CodecName,
				IsDefault: st.Disposition.Default == 1, IsForced: st.Disposition.Forced == 1,
				Title: st.Tags.Title, Language: st.Tags.Language,
			})
			idx++
		case "subtitle":
			lang := st.Tags.Language
			if strings.EqualFold(lang, "und") {
				// An undetermined language is no language for a subtitle track.
				lang = ""
			}
			ref := storage.MediaStreamRef{
				Index: idx, Type: storage.StreamTypeSubtitle, Codec: subtitle.NormalizeCodec(st.CodecName),
				IsDefault:         st.Disposition.Default == 1,
				IsForced:          st.Disposition.Forced == 1,
				IsHearingImpaired: st.Disposition.HearingImpaired == 1,
				Title:             st.Tags.Title,
				Language:          lang,
				IsOriginal:        st.Disposition.Original != 0,
				TimeBase:          st.TimeBase,
			}
			if st.Width > 0 && st.Height > 0 {
				// picture subtitles carry the size of the canvas they draw on
				w, h := st.Width, st.Height
				ref.Width, ref.Height = &w, &h
			}
			streams = append(streams, ref)
			idx++
		}
	}
	deriveVideoBitRate(streams, info.Format)
	return streams
}

// deriveVideoBitRate gives the video stream of a file that states no rate for
// it the rate left over from the container's: the container rate less the
// rate of every other stream, when there is one video stream, the container
// states a rate and each other stream has a rate of its own (an estimate
// counts). A remainder that is not positive leaves the stream without one.
func deriveVideoBitRate(streams []storage.MediaStreamRef, f ffprobeFormat) {
	total, err := strconv.ParseInt(strings.TrimSpace(f.BitRate), 10, 64)
	if err != nil || total <= 0 {
		return
	}
	video := -1
	rest := total
	for i, s := range streams {
		switch s.Type {
		case storage.StreamTypeVideo:
			if video >= 0 {
				return
			}
			video = i
		case storage.StreamTypeAudio:
			if s.BitRate == nil {
				return
			}
			rest -= *s.BitRate
		}
	}
	if video < 0 || streams[video].BitRate != nil || rest <= 0 {
		return
	}
	streams[video].BitRate = &rest
}

// doviOf reads the Dolby Vision configuration record out of a stream's side
// data; nil when it has none.
func doviOf(list []ffprobeSideData) *storage.DoViConfig {
	for _, sd := range list {
		if sd.Type != "DOVI configuration record" || sd.Profile == nil {
			continue
		}
		val := func(p *int) int {
			if p == nil {
				return 0
			}
			return *p
		}
		return &storage.DoViConfig{
			VersionMajor: val(sd.VersionMajor), VersionMinor: val(sd.VersionMinor),
			Profile: val(sd.Profile), Level: val(sd.Level),
			RPUPresent: val(sd.RPUPresent), ELPresent: val(sd.ELPresent), BLPresent: val(sd.BLPresent),
			CompatibilityID: val(sd.CompatibilityID),
		}
	}
	return nil
}

// hasHDR10Plus reports whether the first video frame carries HDR10+ (SMPTE
// 2094-40) dynamic metadata.
func hasHDR10Plus(frames []ffprobeFrame) bool {
	for _, fr := range frames {
		if fr.MediaType != "video" {
			continue
		}
		for _, sd := range fr.SideData {
			if strings.HasPrefix(sd.Type, "HDR Dynamic Metadata SMPTE2094-40") {
				return true
			}
		}
		return false
	}
	return false
}

// plainChannelLayout is a channel layout without the arrangement ffprobe adds
// in parentheses ("5.1(side)" is "5.1", "quad(side)" "quad").
func plainChannelLayout(v string) string {
	if i := strings.IndexByte(v, '('); i > 0 {
		return v[:i]
	}
	return v
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

// embeddedArt is a picture attached to an audio file: the index of its
// stream in the file and its codec.
type embeddedArt struct {
	index int
	codec string
}

// attachedPicture finds the picture embedded in an audio file, if any.
func attachedPicture(info ffprobeOutput) *embeddedArt {
	for i, st := range info.Streams {
		if st.CodecType == "video" && st.Disposition.AttachedPic == 1 {
			return &embeddedArt{index: i, codec: st.CodecName}
		}
	}
	return nil
}

// audioTags returns the tags of an audio file: the container's, completed
// with the first audio stream's (Ogg keeps them there). Names are lower-cased.
func audioTags(info ffprobeOutput) map[string]string {
	out := map[string]string{}
	for _, st := range info.Streams {
		if st.CodecType == "audio" {
			for k, v := range st.Tags.All {
				out[k] = v
			}
			break
		}
	}
	for k, v := range info.Format.Tags {
		out[strings.ToLower(k)] = v
	}
	return out
}

// parseFrameRate reads ffprobe's "num/den" frame rate; 0 when it is absent or
// not a usable rate.
func parseFrameRate(v string) float64 {
	num, den, ok := strings.Cut(v, "/")
	if !ok {
		return 0
	}
	n, err1 := strconv.ParseFloat(num, 64)
	d, err2 := strconv.ParseFloat(den, 64)
	if err1 != nil || err2 != nil || d == 0 || n <= 0 {
		return 0
	}
	return n / d
}

// knownColor is an ffprobe color value, "" for the "unknown" it prints when
// the stream does not say.
func knownColor(v string) string {
	if v == "unknown" || v == "reserved" {
		return ""
	}
	return v
}

// codecTagOf is the container's codec tag ("avc1"), "" when there is none
// (ffprobe prints "[0][0][0][0]").
func codecTagOf(v string) string {
	if strings.Contains(v, "[") || strings.HasPrefix(v, "0x") {
		return ""
	}
	return v
}

// bitDepthOfPixFmt reads the bit depth of an ffprobe pixel format name
// (yuv420p10le -> 10, gray12le -> 12, yuv420p -> 8); 0 for no format and for
// the planar RGB formats of 8 bits (gbrp), which the reference gives no depth.
func bitDepthOfPixFmt(pixFmt string) int {
	switch {
	case pixFmt == "":
		return 0
	case pixFmt == "gbrp" || pixFmt == "gbrap":
		return 0
	case pixFmt == "p010" || strings.HasPrefix(pixFmt, "p010"):
		return 10
	case strings.HasPrefix(pixFmt, "p012"):
		return 12
	case strings.HasPrefix(pixFmt, "p016"):
		return 16
	}
	if strings.HasSuffix(pixFmt, "le") || strings.HasSuffix(pixFmt, "be") {
		t := strings.TrimRight(pixFmt[:len(pixFmt)-2], "abcdefghijklmnopqrstuvwxyz")
		i := len(t)
		for i > 0 && t[i-1] >= '0' && t[i-1] <= '9' {
			i--
		}
		if d, err := strconv.Atoi(t[i:]); err == nil && d >= 9 && d <= 16 {
			return d
		}
	}
	return 8
}

// streamBitRate is the bit rate of one stream: ffprobe's own, else the
// muxer's statistics tags that Matroska files carry (BPS, else
// NUMBER_OF_BYTES over DURATION, the stream's, else the container's), the way
// the reference derives a stream rate the container does not state.
func streamBitRate(st ffprobeStream, f ffprobeFormat) *int64 {
	if br, err := strconv.ParseInt(st.BitRate, 10, 64); err == nil && br > 0 {
		return &br
	}
	if br, err := strconv.ParseInt(strings.TrimSpace(st.Tags.All["bps"]), 10, 64); err == nil && br > 0 {
		return &br
	}
	bytes, err := strconv.ParseInt(strings.TrimSpace(st.Tags.All["number_of_bytes"]), 10, 64)
	if err != nil || bytes <= 0 {
		return nil
	}
	secs := parseTagDuration(st.Tags.All["duration"])
	if secs <= 0 {
		secs, _ = strconv.ParseFloat(f.Duration, 64)
	}
	if secs <= 0 {
		return nil
	}
	br := int64(float64(bytes) * 8 / secs)
	if br <= 0 {
		return nil
	}
	return &br
}

// parseTagDuration reads a Matroska DURATION tag ("00:00:03.023000000").
func parseTagDuration(v string) float64 {
	parts := strings.Split(strings.TrimSpace(v), ":")
	if len(parts) != 3 {
		return 0
	}
	var total float64
	for _, p := range parts {
		f, err := strconv.ParseFloat(p, 64)
		if err != nil {
			return 0
		}
		total = total*60 + f
	}
	return total
}

// estimatedAudioBitRate is the rate the reference assigns an audio stream
// whose container states none and carries no statistics tags, from the codec
// and the channel count; nil for a codec it assigns nothing to.
func estimatedAudioBitRate(codec string, channels int) *int64 {
	var br int64
	switch strings.ToLower(codec) {
	case "aac":
		br = 192000
		if channels > 2 {
			br = 320000
		}
	case "flac", "alac":
		br = 480000 * int64(channels)
	case "opus":
		br = 128000
		if channels > 2 {
			br = 256000
		}
	case "vorbis":
		br = 160000
		if channels > 2 {
			br = 320000
		}
	case "truehd", "mlp":
		br = 700000 * int64(channels)
	default:
		return nil
	}
	if br <= 0 {
		return nil
	}
	return &br
}

// normalizeFormat names the container of a probed file the way the reference
// does for the Container of an item: ffprobe's format_name with MPEG-TS called
// ts, and Matroska told apart from WebM by what it holds: "mkv,webm" when every
// stream is a VP8, VP9 or AV1 picture or a Vorbis or Opus sound (and nothing
// else, subtitles included), "mkv" otherwise. Every other name is kept as
// ffprobe prints it ("mov,mp4,m4a,3gp,3g2,mj2", "asf", "mpeg", "ogg", "avi",
// ...).
func normalizeFormat(formatName string, streams []storage.MediaStreamRef) string {
	switch formatName {
	case "":
		return ""
	case "mpegts":
		return "ts"
	case "matroska,webm":
		if len(streams) == 0 {
			return "mkv"
		}
		for _, st := range streams {
			switch {
			case st.Type == storage.StreamTypeVideo && (st.Codec == "vp8" || st.Codec == "vp9" || st.Codec == "av1"):
			case st.Type == storage.StreamTypeAudio && (st.Codec == "vorbis" || st.Codec == "opus"):
			default:
				return "mkv"
			}
		}
		return "mkv,webm"
	}
	return formatName
}

// Timestamp states of a transport stream.
const (
	tsNone  = "None"
	tsZero  = "Zero"
	tsValid = "Valid"
)

// transportStreamTimestamp tells how the packets of an MPEG transport stream
// file are framed: plain 188-byte packets carry no timestamp (None); the
// 192-byte packets of an M2TS file start with a 4-byte header, whose first
// packet is all zero (Zero) or not (Valid).
func transportStreamTimestamp(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	var b [197]byte
	n, _ := io.ReadFull(f, b[:])
	if n >= 197 && b[4] == 0x47 && b[196] == 0x47 && !(b[0] == 0x47 && b[188] == 0x47) {
		if b[0]|b[1]|b[2]|b[3] == 0 {
			return tsZero
		}
		return tsValid
	}
	return tsNone
}
