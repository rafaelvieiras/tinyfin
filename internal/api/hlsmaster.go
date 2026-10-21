package api

import (
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
	"github.com/rafaelvieiras/tinyfin/internal/subtitle"
	"github.com/rafaelvieiras/tinyfin/internal/transcode"
)

// This file builds the two playlists a video HLS request answers with, in
// the shape the reference writes them (playback.hls.R2 to R7): the master
// playlist (one variant, the subtitle group, codec string, bandwidth) and the
// VOD media playlist of numbered segments.

// queryPair is one parameter of a raw query string, kept in order.
type queryPair struct{ key, val string }

// splitRawQuery splits a raw query into its parameters, keeping order and
// the raw (still escaped) values.
func splitRawQuery(raw string) []queryPair {
	var out []queryPair
	for _, p := range strings.Split(raw, "&") {
		if p == "" {
			continue
		}
		k, v, _ := strings.Cut(p, "=")
		out = append(out, queryPair{k, v})
	}
	return out
}

func joinQuery(ps []queryPair) string {
	parts := make([]string, 0, len(ps))
	for _, p := range ps {
		parts = append(parts, p.key+"="+p.val)
	}
	return strings.Join(parts, "&")
}

// mainQuery is the query of the variant link. The reference rebuilds it from
// the parsed parameters: values of a repeated name (any letter case) are
// gathered at the first occurrence, and the audio codec is the one the
// variant plays (the passed-through "copy", else the chosen codec).
func mainQuery(raw string, audioCodec string) string {
	type group struct {
		key  string
		vals []string
	}
	var groups []*group
	idx := map[string]*group{}
	for _, p := range splitRawQuery(raw) {
		g := idx[strings.ToLower(p.key)]
		if g == nil {
			g = &group{key: p.key}
			idx[strings.ToLower(p.key)] = g
			groups = append(groups, g)
		}
		g.vals = append(g.vals, p.val)
	}
	if audioCodec != "" {
		g := idx["audiocodec"]
		if g == nil {
			g = &group{key: "AudioCodec"}
			groups = append(groups, g)
		}
		g.vals = []string{audioCodec}
	}
	var parts []string
	for _, g := range groups {
		for _, v := range g.vals {
			parts = append(parts, g.key+"="+v)
		}
	}
	return strings.Join(parts, "&")
}

// defaultTranscodeWidth is the width a re-encoded picture is limited to when
// the request names no video bit rate.
const defaultTranscodeWidth = 416

// effectiveMaxWidth is the width limit a re-encoded picture gets: the
// request's own, and without a video bit rate also the default one.
func effectiveMaxWidth(q url.Values) int {
	lim := numParam(q, "maxwidth", "width")
	if numParam(q, "videobitrate") == 0 && (lim == 0 || lim > defaultTranscodeWidth) {
		lim = defaultTranscodeWidth
	}
	return lim
}

// effectiveMaxSize is the width and height limits of a re-encoded picture. A
// request without a video bit rate is held to the default width, and when that
// default is what limits the picture the height limit is dropped.
func effectiveMaxSize(q url.Values) (w, h int) {
	w = numParam(q, "maxwidth", "width")
	h = numParam(q, "maxheight", "height")
	if numParam(q, "videobitrate") == 0 && (w == 0 || w > defaultTranscodeWidth) {
		return defaultTranscodeWidth, 0
	}
	return w, h
}

// encoderProfile is what the request pins on the encoder (-profile:v, -level),
// nothing when it names neither. Only the first profile of the list counts:
// baseline and constrainedbaseline are the baseline profile, a name that
// starts with high the high one, and an HEVC main10 is encoded as main; a name
// the encoder does not know leaves the choice to it. The level is a number the
// encoder takes as is, at most 51 and not for HEVC.
func encoderProfile(q url.Values, codec string) (profile, level string) {
	list := q.Get("profile")
	if list == "" {
		list = q.Get(codec + "-profile")
	}
	first, _, _ := strings.Cut(list, ",")
	first = strings.ToLower(strings.TrimSpace(first))
	switch {
	case codec == "hevc":
		if strings.HasPrefix(first, "main") {
			profile = "main"
		}
		return profile, ""
	case first == "baseline" || first == "constrainedbaseline":
		profile = "baseline"
	case first == "main":
		profile = "main"
	case strings.HasPrefix(first, "high"):
		profile = "high"
	}
	lv := strings.TrimSpace(q.Get("level"))
	if lv == "" {
		lv = strings.TrimSpace(q.Get("h264-level"))
	}
	if f, err := strconv.ParseFloat(lv, 64); err == nil && lv != "" {
		if f > 51 || f < 0 {
			f = 51
		}
		level = strconv.FormatFloat(f, 'f', -1, 64)
	}
	return profile, level
}

// hlsMedia is what a master playlist says about the variant: the streams
// the request plays and whether each is copied.
type hlsMedia struct {
	video, audio           *storage.MediaStreamRef
	videoCopy, audioCopy   bool
	vcodec, acodec         string // target codec names (the source's when copied)
	width, height          int
	fps                    float64
	bandwidth              int
	videoRange             string
	h264Profile, h264Level string
	hevcProfile, hevcLevel string
}

func numParam(q url.Values, names ...string) int {
	for _, n := range names {
		if v := strings.TrimSpace(q.Get(n)); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				return int(f)
			}
		}
	}
	return 0
}

func floatParam(q url.Values, name string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(q.Get(name)), 64)
	return f
}

// streamsFor picks the video stream and the audio stream a request plays.
func streamsFor(item storage.Item, audioIdx *int) (video, audio *storage.MediaStreamRef) {
	var firstAudio, defAudio *storage.MediaStreamRef
	for i := range item.MediaStreams {
		st := &item.MediaStreams[i]
		switch st.Type {
		case storage.StreamTypeVideo:
			if video == nil {
				video = st
			}
		case storage.StreamTypeAudio:
			if audioIdx != nil && st.Index == *audioIdx {
				audio = st
			}
			if firstAudio == nil {
				firstAudio = st
			}
			if st.IsDefault && defAudio == nil {
				defAudio = st
			}
		}
	}
	if audio == nil {
		audio = defAudio
	}
	if audio == nil {
		audio = firstAudio
	}
	return video, audio
}

func listHas(list, codec string) bool {
	for _, c := range strings.Split(list, ",") {
		c = strings.ToLower(strings.TrimSpace(c))
		if c == "copy" || (c != "" && c == codec) {
			return true
		}
	}
	return false
}

// videoCopyAllowed reports whether the picture can pass through for the
// limits the request names: a codec the request lists, a source that
// provably fits the bit rate, the width, height and frame rate.
func videoCopyAllowed(v *storage.MediaStreamRef, q url.Values) bool {
	if v == nil {
		return false
	}
	if br := numParam(q, "videobitrate"); br > 0 && (v.BitRate == nil || *v.BitRate > int64(br)) {
		return false
	}
	w, h := 0, 0
	if v.Width != nil {
		w = *v.Width
	}
	if v.Height != nil {
		h = *v.Height
	}
	if m := numParam(q, "maxwidth", "width"); m > 0 && w > m {
		return false
	}
	if m := numParam(q, "maxheight", "height"); m > 0 && h > m {
		return false
	}
	if m := floatParam(q, "maxframerate"); m > 0 && v.FrameRate != nil && *v.FrameRate > m+0.01 {
		return false
	}
	return videoCopyFits(v, q)
}

// videoCopyFits are the checks on the picture's coding: profile, level,
// reference frames, bit depth and sample aspect, and the switch that forbids
// copying at all. The profile counts as fitting when it is not above the
// highest one the request names (a Main source passes a High request, a
// High 10 source does not).
func videoCopyFits(v *storage.MediaStreamRef, q url.Values) bool {
	if strings.EqualFold(strings.TrimSpace(q.Get("allowvideostreamcopy")), "false") {
		return false
	}
	codec := strings.ToLower(v.Codec)
	profiles, level := q.Get("profile"), q.Get("level")
	switch codec {
	case "h264":
		if p := q.Get("h264-profile"); p != "" {
			profiles = p
		}
		if l := q.Get("h264-level"); l != "" {
			level = l
		}
	case "hevc":
		if p := q.Get("hevc-profile"); p != "" {
			profiles = p
		}
		if l := q.Get("hevc-level"); l != "" {
			level = l
		}
	default:
		profiles, level = "", ""
	}
	if limit, ok := highestProfileRank(codec, profiles); ok {
		if r, known := profileRank(codec, v.Profile); !known || r > limit {
			return false
		}
	}
	if l, err := strconv.ParseFloat(strings.TrimSpace(level), 64); err == nil && v.Level != nil && *v.Level > l {
		return false
	}
	if raw := strings.TrimSpace(q.Get("maxrefframes")); raw != "" && v.RefFrames != nil {
		if n, err := strconv.Atoi(raw); err == nil && *v.RefFrames > n {
			return false
		}
	}
	if raw := strings.TrimSpace(q.Get("maxvideobitdepth")); raw != "" && v.BitDepth != nil {
		if n, err := strconv.Atoi(raw); err == nil && *v.BitDepth > n {
			return false
		}
	}
	if strings.EqualFold(strings.TrimSpace(q.Get("requirenonanamorphic")), "true") && isAnamorphicSAR(v.SampleAspectRatio) {
		return false
	}
	return true
}

var profileRanks = map[string][]string{
	"h264": {"baseline|constrainedbaseline", "extended|main", "high", "high10|high10intra", "high422|high422intra", "high444|high444predictive|high444intra"},
	"hevc": {"main|mainstillpicture", "main10"},
}

// profileRank orders a profile name ("High 10", "main10") among the ones of
// its codec; known is false for a name the codec's list does not have.
func profileRank(codec, name string) (rank int, known bool) {
	name = strings.ToLower(strings.NewReplacer(" ", "", ":", "", "-", "", "_", "").Replace(name))
	for i, group := range profileRanks[codec] {
		for _, n := range strings.Split(group, "|") {
			if n == name {
				return i, true
			}
		}
	}
	return 0, false
}

// highestProfileRank is the rank of the highest profile a "a|b,c" list names
// (ok is false when it names none the codec knows).
func highestProfileRank(codec, list string) (rank int, ok bool) {
	for _, p := range strings.FieldsFunc(list, func(r rune) bool { return r == '|' || r == ',' }) {
		if r, known := profileRank(codec, p); known && (!ok || r > rank) {
			rank, ok = r, true
		}
	}
	return rank, ok
}

// audioChannels is the channel count the audio leaves with: the source's,
// capped by every limit the request names.
func audioChannels(a *storage.MediaStreamRef, q url.Values) int {
	ch := 2
	if a != nil && a.Channels != nil && *a.Channels > 0 {
		ch = *a.Channels
	}
	for _, n := range []string{"transcodingmaxaudiochannels", "maxaudiochannels", "audiochannels"} {
		if m := numParam(q, n); m > 0 && m < ch {
			ch = m
		}
	}
	return ch
}

// audioPlan is how the sound is re-encoded: the channel count (the limits
// of the request, three and four channels fold down to two, and five channels
// are written as six when the source has more), the bit rate (the request's
// when it is lower than the default for those channels) and whether a surround
// mix folded down to stereo is made louder.
func audioPlan(a *storage.MediaStreamRef, q url.Values) (channels, bitrate int, boost bool) {
	src := 2
	if a != nil && a.Channels != nil && *a.Channels > 0 {
		src = *a.Channels
	}
	channels = audioChannels(a, q)
	switch {
	case channels == 3 || channels == 4:
		channels = 2
	case channels == 5 && src > 5:
		channels = src
		if src > 6 {
			channels = 6
		}
	}
	if channels > 2 && strings.HasPrefix(strings.ToLower(q.Get("audiocodec")), "mp3") {
		channels = 2 // MP3 carries two channels at most
	}
	bitrate = defaultAudioBandwidth(channels, src)
	if br := numParam(q, "audiobitrate"); br > 0 && br < bitrate {
		bitrate = br
	}
	return channels, bitrate, channels == 2 && src > 5
}

func audioCopyAllowed(a *storage.MediaStreamRef, q url.Values) bool {
	if a == nil {
		return false
	}
	if a.Channels != nil && audioChannels(a, q) < *a.Channels {
		return false
	}
	if br := numParam(q, "audiobitrate"); br > 0 && (a.BitRate == nil || *a.BitRate > int64(br)) {
		return false
	}
	return true
}

// defaultAudioBandwidth is what the reference counts for an audio stream
// that is not capped: 128 kb/s a channel for one or two channels, two
// channels' worth for three and four, 640 kb/s for six and more, and for a
// source with exactly five channels (no LFE) that stays at five, 768 kb/s.
// ch is the channel count the sound leaves with, src the source's.
func defaultAudioBandwidth(ch, src int) int {
	switch {
	case ch <= 2:
		return 128000 * ch
	case ch <= 4:
		return 256000
	case ch == 5 && src == 5:
		return 768000
	}
	return 640000
}

// h264CodecString writes the avc1 codec string. A copied picture reports
// its own profile and level; an encoded one the first recognised profile of
// h264-profile and the level of h264-level (default constrained baseline,
// 4.1; capped at 5.1).
func h264CodecString(profile string, level int) string {
	prof := "4240"
	switch strings.ToLower(strings.ReplaceAll(profile, " ", "")) {
	case "baseline":
		prof = "42E0"
	case "constrainedbaseline":
		prof = "4240"
	case "main":
		prof = "4D40"
	case "high":
		prof = "6400"
	case "extended":
		prof = "5840"
	}
	return fmt.Sprintf("avc1.%s%02X", prof, level)
}

// firstKnownProfile is the first name of a "a|b" profile list that the
// encoder knows.
func firstKnownProfile(list string, known ...string) string {
	for _, p := range strings.FieldsFunc(list, func(r rune) bool { return r == '|' || r == ',' }) {
		p = strings.ToLower(strings.TrimSpace(p))
		for _, k := range known {
			if p == k {
				return p
			}
		}
	}
	return ""
}

func videoCodecString(m hlsMedia) string {
	switch m.vcodec {
	case "h264":
		if m.videoCopy {
			level := 41
			if m.video != nil && m.video.Level != nil {
				level = int(*m.video.Level)
			}
			prof := ""
			if m.video != nil {
				prof = m.video.Profile
			}
			return h264CodecString(prof, level)
		}
		level := 41
		if n, err := strconv.Atoi(m.h264Level); err == nil && n > 0 {
			level = n
		}
		if level > 51 {
			level = 51
		}
		return h264CodecString(firstKnownProfile(m.h264Profile, "baseline", "constrainedbaseline", "main", "high"), level)
	case "hevc", "h265":
		prof, level := 1, 120
		if p := firstKnownProfile(m.hevcProfile, "main", "main10"); p == "main10" {
			prof = 2
		}
		if m.videoCopy && m.video != nil {
			prof = 1
			if strings.Contains(strings.ToLower(m.video.Profile), "10") {
				prof = 2
			}
			if m.video.Level != nil {
				level = int(*m.video.Level)
			}
		} else if n, err := strconv.Atoi(m.hevcLevel); err == nil && n > 0 {
			level = n
			if level > 150 {
				level = 150
			}
		}
		return fmt.Sprintf("hvc1.%d.4.L%d.B0", prof, level)
	case "av1":
		return "av01.0.15M.08"
	}
	return ""
}

func audioCodecString(codec string, m hlsMedia) string {
	switch codec {
	case "aac":
		if m.audioCopy && m.audio != nil && strings.Contains(strings.ToLower(m.audio.Profile), "he") {
			return "mp4a.40.5"
		}
		return "mp4a.40.2"
	case "mp3":
		return "mp4a.40.34"
	case "ac3":
		return "ac-3"
	case "eac3":
		return "ec-3"
	case "opus":
		return "Opus"
	case "flac":
		return "fLaC"
	}
	return ""
}

func formatFrameRate(f float64) string {
	s := strconv.FormatFloat(math.Round(f*1000)/1000, 'f', 3, 64)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	return s
}

// describeHLS works out what the variant of a request is. vc and ac are the
// codecs the transcode job will use ("copy" for a pass-through).
func describeHLS(item storage.Item, q url.Values, vc, ac string, audioIdx *int) hlsMedia {
	v, a := streamsFor(item, audioIdx)
	m := hlsMedia{video: v, audio: a, videoCopy: vc == "copy", audioCopy: ac == "copy"}
	m.h264Profile, m.h264Level = firstNonEmpty(q.Get("h264-profile"), q.Get("profile")), firstNonEmpty(q.Get("h264-level"), q.Get("level"))
	m.hevcProfile, m.hevcLevel = q.Get("hevc-profile"), firstNonEmpty(q.Get("hevc-level"), q.Get("level"))

	m.vcodec = vc
	if m.videoCopy && v != nil {
		m.vcodec = strings.ToLower(v.Codec)
	}
	m.acodec = ac
	if m.audioCopy && a != nil {
		m.acodec = strings.ToLower(a.Codec)
	}

	if v != nil {
		if v.Width != nil {
			m.width = *v.Width
		}
		if v.Height != nil {
			m.height = *v.Height
		}
		if v.FrameRate != nil {
			m.fps = *v.FrameRate
		}
		switch strings.ToLower(v.ColorTransfer) {
		case "smpte2084":
			m.videoRange = "PQ"
		case "arib-std-b67":
			m.videoRange = "HLG"
		}
	}
	if m.videoRange == "" {
		m.videoRange = "SDR"
	}
	// The picture is scaled by the width limit only (the reference ignores
	// the height ones in what it announces).
	if lim := effectiveMaxWidth(q); lim > 0 && m.width > lim && !m.videoCopy {
		m.height = int(math.RoundToEven(float64(m.height) * float64(lim) / float64(m.width)))
		m.width = lim
	}
	if f := floatParam(q, "maxframerate"); f > 0 && !m.videoCopy {
		m.fps = f
	}

	vbr := 0
	if !m.videoCopy || numParam(q, "videobitrate") > 0 {
		vbr = numParam(q, "videobitrate")
		if v != nil && v.BitRate != nil {
			vbr = transcode.CapVideoBitrate(vbr, int(*v.BitRate), v.Codec, m.vcodec, streamBitDepth(v))
		}
	}
	ch := audioChannels(a, q)
	src := 0
	if a != nil && a.Channels != nil {
		src = *a.Channels
	}
	if first, _, _ := strings.Cut(strings.ToLower(strings.ReplaceAll(q.Get("audiocodec"), " ", "")), ","); first == "mp3" {
		ch = min(ch, 2) // the first codec asked for is stereo at most
	}
	abr := defaultAudioBandwidth(ch, src)
	if m.acodec == "flac" {
		// flac is counted at the rate of the source's own sound, whatever the
		// limits (checked against the reference for flac and other sources)
		if a != nil && a.BitRate != nil && *a.BitRate > 0 {
			abr = int(*a.BitRate)
		} else {
			abr = 192000 * ch
		}
		// a flac track that has to be re-encoded writes no audio codec
		if !m.audioCopy && a != nil && strings.EqualFold(a.Codec, "flac") {
			m.acodec = ""
		}
	} else if p := numParam(q, "audiobitrate"); p > 0 && p < abr {
		abr = p
	}
	if a == nil {
		abr = 0 // a file without sound adds nothing for it
	}
	m.bandwidth = vbr + abr
	return m
}

// hlsSubtitleTracks are the text tracks of item, in stream order, that the
// subtitle group lists.
func hlsSubtitleTracks(item storage.Item) []storage.MediaStreamRef {
	var out []storage.MediaStreamRef
	for _, st := range item.MediaStreams {
		if st.Type == storage.StreamTypeSubtitle && subtitle.IsTextCodec(st.Codec) {
			out = append(out, st)
		}
	}
	return out
}

// buildMaster writes the master playlist (playback.hls.R2).
func buildMaster(item storage.Item, q url.Values, rawQuery, apiKey, vc, ac string, audioIdx, subIdx *int) string {
	m := describeHLS(item, q, vc, ac, audioIdx)
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	group := strings.EqualFold(q.Get("subtitlemethod"), "Hls")
	var tracks []storage.MediaStreamRef
	if group {
		tracks = hlsSubtitleTracks(item)
	}
	for _, st := range tracks {
		yn := func(v bool) string {
			if v {
				return "YES"
			}
			return "NO"
		}
		uri := fmt.Sprintf("%s/Subtitles/%d/subtitles.m3u8?SegmentLength=30", item.ID, st.Index)
		if apiKey != "" {
			uri += "&ApiKey=" + url.QueryEscape(apiKey)
		}
		fmt.Fprintf(&b, "#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID=\"subs\",NAME=\"%s\",DEFAULT=%s,FORCED=%s,AUTOSELECT=YES,URI=\"%s\"",
			strings.ReplaceAll(subtitleDisplayTitle(st), `"`, "'"), yn(subIdx != nil && *subIdx == st.Index), yn(st.IsForced), uri)
		if lang := subtitleLanguage(st); lang != "" {
			fmt.Fprintf(&b, ",LANGUAGE=\"%s\"", lang)
		}
		b.WriteString("\n")
	}
	var codecs []string
	if c := videoCodecString(m); c != "" {
		codecs = append(codecs, c)
	}
	if c := audioCodecString(m.acodec, m); c != "" {
		codecs = append(codecs, c)
	}
	fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,AVERAGE-BANDWIDTH=%d,VIDEO-RANGE=%s", m.bandwidth, m.bandwidth, m.videoRange)
	if len(codecs) > 0 {
		fmt.Fprintf(&b, ",CODECS=\"%s\"", strings.Join(codecs, ","))
	}
	if m.width > 0 && m.height > 0 {
		fmt.Fprintf(&b, ",RESOLUTION=%dx%d", m.width, m.height)
	}
	if m.fps > 0 {
		fmt.Fprintf(&b, ",FRAME-RATE=%s", formatFrameRate(m.fps))
	}
	if len(tracks) > 0 {
		b.WriteString(",SUBTITLES=\"subs\"")
	}
	ac2 := ac
	b.WriteString("\nmain.m3u8?" + mainQuery(rawQuery, ac2) + "\n")
	return b.String()
}

// subtitleLanguage is the language tag of a track as the group writes it.
func subtitleLanguage(st storage.MediaStreamRef) string {
	return strings.ToLower(st.Language)
}

// buildMediaPlaylist writes the VOD media playlist (playback.hls.R3):
// durations are the lengths of the segments in seconds.
func buildMediaPlaylist(durations []float64, target int, ext, rawQuery string) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-VERSION:3\n")
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n#EXT-X-MEDIA-SEQUENCE:0\n", target)
	var start int64
	for i, d := range durations {
		ticks := int64(math.Round(d * float64(ticksPerSecond)))
		fmt.Fprintf(&b, "#EXTINF:%.6f, nodesc\nhls1/main/%d.%s?", d, i, ext)
		if rawQuery != "" {
			b.WriteString(rawQuery + "&")
		}
		fmt.Fprintf(&b, "runtimeTicks=%d&actualSegmentLengthTicks=%d\n", start, ticks)
		start += ticks
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return b.String()
}

const ticksPerSecond = 10_000_000

// gridDurations splits a run time into segments of segSeconds, the last one
// shorter.
func gridDurations(runtimeTicks int64, segSeconds int) []float64 {
	if runtimeTicks <= 0 || segSeconds <= 0 {
		return nil
	}
	seg := int64(segSeconds) * ticksPerSecond
	var out []float64
	for left := runtimeTicks; left > 0; left -= seg {
		n := seg
		if left < seg {
			n = left
		}
		out = append(out, float64(n)/ticksPerSecond)
	}
	return out
}

// playlistDurations reads the segment durations out of an ffmpeg media
// playlist.
func playlistDurations(playlist []byte) []float64 {
	var out []float64
	for _, line := range strings.Split(string(playlist), "\n") {
		if rest, ok := strings.CutPrefix(line, "#EXTINF:"); ok {
			v, _, _ := strings.Cut(rest, ",")
			if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
				out = append(out, f)
			}
		}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// streamBitDepth is the sample depth of a stream (0 when unknown).
func streamBitDepth(v *storage.MediaStreamRef) int {
	if v != nil && v.BitDepth != nil {
		return *v.BitDepth
	}
	return 0
}
