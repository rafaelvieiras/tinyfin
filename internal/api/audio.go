package api

import (
	"bufio"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/deviceprofile"
	"github.com/rafaelvieiras/tinyfin/internal/guid"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
	"github.com/rafaelvieiras/tinyfin/internal/transcode"
)

// audio.go implements audio transcoding (docs/contract/specs/music.md,
// "Transcoding"): /Audio/{id}/stream without static=true, /Audio/{id}/universal
// and the audio side of PlaybackInfo.

// defaultAudioBitrate is what an audio encode is held to when nothing asks
// for another rate.
const defaultAudioBitrate = 128000

// audioTarget is one output a track can be encoded to.
type audioTarget struct {
	container   string // the name a client uses (mp3, aac, ogg, ...)
	codec       string // the codec name clients use (mp3, aac, opus, ...)
	encoder     string
	format      string
	contentType string
	fragmented  bool
	lossless    bool
	hlsCodecs   string
}

var audioTargets = map[string]audioTarget{
	"mp3":  {container: "mp3", codec: "mp3", encoder: "libmp3lame", format: "mp3", contentType: "audio/mpeg", hlsCodecs: "mp4a.40.34"},
	"aac":  {container: "aac", codec: "aac", encoder: "aac", format: "adts", contentType: "audio/aac", hlsCodecs: "mp4a.40.2"},
	"m4a":  {container: "m4a", codec: "aac", encoder: "aac", format: "mp4", contentType: "audio/mp4", fragmented: true, hlsCodecs: "mp4a.40.2"},
	"mp4":  {container: "mp4", codec: "aac", encoder: "aac", format: "mp4", contentType: "video/mp4", fragmented: true, hlsCodecs: "mp4a.40.2"},
	"ts":   {container: "ts", codec: "aac", encoder: "aac", format: "mpegts", contentType: "video/mp2t", hlsCodecs: "mp4a.40.2"},
	"ogg":  {container: "ogg", codec: "opus", encoder: "libopus", format: "ogg", contentType: "audio/ogg"},
	"oga":  {container: "oga", codec: "opus", encoder: "libopus", format: "ogg", contentType: "audio/ogg"},
	"opus": {container: "opus", codec: "opus", encoder: "libopus", format: "ogg", contentType: "audio/ogg"},
	"webm": {container: "webm", codec: "opus", encoder: "libopus", format: "webm", contentType: "audio/webm"},
	"flac": {container: "flac", codec: "flac", encoder: "flac", format: "flac", contentType: "audio/flac", lossless: true},
	"wav":  {container: "wav", codec: "wav", encoder: "pcm_s16le", format: "wav", contentType: "audio/wav", lossless: true},
}

// containerOfCodec is the container an encode to a codec is wrapped in when
// the request names no container.
var containerOfCodec = map[string]string{
	"mp3": "mp3", "aac": "aac", "opus": "ogg", "vorbis": "ogg", "flac": "flac", "wav": "wav", "pcm_s16le": "wav",
}

// pickAudioTarget resolves a request's container and codec to an output: the
// container decides when it is one Tinyfin can write, else the codec does,
// else AAC.
func pickAudioTarget(container, codec string) audioTarget {
	container = strings.ToLower(strings.TrimSpace(container))
	if t, ok := audioTargets[container]; ok {
		return t
	}
	codec = strings.ToLower(strings.TrimSpace(strings.Split(codec, ",")[0]))
	if c, ok := containerOfCodec[codec]; ok {
		return audioTargets[c]
	}
	return audioTargets["aac"]
}

// audioSourceBitrate is the bit rate of the file: its size over its length.
func audioSourceBitrate(item storage.Item) int64 {
	if item.RunTimeTicks == nil || *item.RunTimeTicks <= 0 || item.Size <= 0 {
		return 0
	}
	return item.Size * 8 * 10_000_000 / *item.RunTimeTicks
}

func firstToken(list string) string {
	return strings.ToLower(strings.TrimSpace(strings.Split(list, ",")[0]))
}

// loadStreamableTrack resolves {itemId} of an /Audio route.
func (s *Server) loadStreamableTrack(w http.ResponseWriter, r *http.Request) (storage.Item, bool) {
	item, err := s.store.GetItem(r.Context(), storage.ItemID(r.PathValue("itemId")))
	if err != nil || !s.itemReadableByCaller(r, item) {
		w.WriteHeader(http.StatusNotFound)
		return storage.Item{}, false
	}
	if item.Type != storage.ItemTypeAudio || !item.HasMediaFile() {
		writeNotPlayable(w)
		return storage.Item{}, false
	}
	return item, true
}

// serveAudioFile sends the file as it is, with range support.
func serveAudioFile(w http.ResponseWriter, r *http.Request, item storage.Item) {
	if ct, ok := audioContentTypes[strings.ToLower(extOf(item.Path))]; ok {
		w.Header().Set("Content-Type", ct)
	}
	http.ServeFile(w, r, item.Path)
}

// streamAudioTranscode encodes the track and streams the result: no length,
// no ranges (the bytes are made as they are sent).
func (s *Server) streamAudioTranscode(w http.ResponseWriter, r *http.Request, item storage.Item, t audioTarget, bitrate int) {
	req := transcode.AudioRequest{InputPath: item.Path, Encoder: t.encoder, Format: t.format, Fragmented: t.fragmented}
	if !t.lossless {
		req.Bitrate = bitrate
	}
	for _, st := range item.MediaStreams {
		if st.Type == storage.StreamTypeAudio {
			if st.Channels != nil && *st.Channels > 2 {
				req.Channels = 2
			}
			break
		}
	}
	as, err := s.transcode.StreamAudio(r.Context(), req)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer as.Close()
	br := bufio.NewReader(as)
	if _, err := br.Peek(1); err != nil && req.Encoder == "libopus" && strings.Contains(as.Stderr(), "Unknown encoder") {
		// an ffmpeg without libopus: the built-in encoder does the same job
		as.Close()
		req.Encoder = "opus"
		if as, err = s.transcode.StreamAudio(r.Context(), req); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		defer as.Close()
		br = bufio.NewReader(as)
		_, err = br.Peek(1)
	}
	if _, err := br.Peek(1); err != nil {
		// nothing came out: the encode failed before it produced a byte
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", t.contentType)
	w.Header().Set("Accept-Ranges", "none")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, br)
}

// handleAudioStream serves GET /Audio/{itemId}/stream and
// /Audio/{itemId}/stream.{container}: the file as it is for static=true,
// else an encode to the container and codec asked for (AAC by default).
// Segment files of an audio HLS stream come through here too.
func (s *Server) handleAudioStream(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("file")
	if segmentFilePattern.MatchString(file) {
		s.serveAudioSegment(w, r, file)
		return
	}
	if file != "stream" && !strings.HasPrefix(file, "stream.") {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	item, ok := s.loadStreamableTrack(w, r)
	if !ok {
		return
	}
	q := caseInsensitiveQuery(r)
	if v := strings.ToLower(q.Get("static")); v == "true" {
		serveAudioFile(w, r, item)
		return
	}
	container := q.Get("container")
	if ext := strings.TrimPrefix(file, "stream."); ext != file {
		container = ext
	}
	t := pickAudioTarget(container, q.Get("audiocodec"))
	// The rate of this route is fixed: the reference ignores AudioBitRate
	// and MaxStreamingBitrate here.
	s.streamAudioTranscode(w, r, item, t, defaultAudioBitrate)
}

// universalBitrate is the rate of a universal encode: AudioBitRate when
// given, else the default, held under MaxStreamingBitrate.
func universalBitrate(q url.Values) int {
	br := defaultAudioBitrate
	for _, k := range []string{"audiobitrate"} {
		if n, err := strconv.Atoi(q.Get(k)); err == nil && n > 0 {
			br = n
		}
	}
	if n, err := strconv.Atoi(q.Get("maxstreamingbitrate")); err == nil && n > 0 && n < br {
		br = n
	}
	return br
}

// handleAudioUniversal serves GET /Audio/{itemId}/universal: the file as it
// is when its container is one of Container (and fits MaxStreamingBitrate),
// else an encode to TranscodingContainer, over http or as an HLS playlist.
func (s *Server) handleAudioUniversal(w http.ResponseWriter, r *http.Request) {
	item, ok := s.loadStreamableTrack(w, r)
	if !ok {
		return
	}
	q := caseInsensitiveQuery(r)
	maxBR, _ := strconv.Atoi(q.Get("maxstreamingbitrate"))
	srcContainer := strings.ToLower(item.Container)
	if srcContainer == "" {
		srcContainer = strings.TrimPrefix(strings.ToLower(extOf(item.Path)), ".")
	}
	fits := maxBR <= 0 || audioSourceBitrate(item) <= int64(maxBR)
	for _, c := range itemsListParam(q, "container") {
		if strings.EqualFold(c, srcContainer) && fits {
			serveAudioFile(w, r, item)
			return
		}
	}
	tcontainer := q.Get("transcodingcontainer")
	t := pickAudioTarget(tcontainer, q.Get("audiocodec"))
	br := universalBitrate(q)
	if strings.EqualFold(q.Get("transcodingprotocol"), "hls") {
		seg := strings.ToLower(tcontainer)
		if seg == "" {
			seg = "ts"
		}
		query := r.URL.RawQuery + "&SegmentContainer=" + url.QueryEscape(seg) +
			"&TranscodeReasons=ContainerNotSupported,VideoCodecNotSupported,AudioCodecNotSupported"
		s.writeAudioMaster(w, r, item, t, br, query)
		return
	}
	s.streamAudioTranscode(w, r, item, t, br)
}

// writeAudioMaster starts the HLS encode and answers the master playlist.
func (s *Server) writeAudioMaster(w http.ResponseWriter, r *http.Request, item storage.Item, t audioTarget, br int, query string) {
	q := caseInsensitiveQuery(r)
	if _, err := s.ensureAudioJob(r, item, t, br, q); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	codecs := ""
	if t.hlsCodecs != "" {
		codecs = fmt.Sprintf(",CODECS=%q", t.hlsCodecs)
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	fmt.Fprintf(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=%d,AVERAGE-BANDWIDTH=%d%s\nmain.m3u8?%s\n", br, br, codecs, query)
}

// audioHLSParams reads the target of an audio HLS request: the parameters of
// a PlaybackInfo TranscodingUrl (AudioCodec, AudioBitrate, SegmentContainer)
// or of a universal request (AudioCodec, TranscodingContainer, AudioBitRate,
// MaxStreamingBitrate).
func audioHLSParams(q url.Values) (audioTarget, int) {
	t := pickAudioTarget("", q.Get("audiocodec"))
	if t.container == "ogg" {
		t = audioTargets["aac"]
	}
	if c := q.Get("transcodingcontainer"); c != "" {
		t = pickAudioTarget(c, q.Get("audiocodec"))
	}
	return t, universalBitrate(q)
}

// audioJobKey names the transcode job of an audio HLS request.
func audioJobKey(id storage.ItemID, t audioTarget, br int) string {
	sum := sha1.Sum([]byte(fmt.Sprintf("%s|%s|%d", t.container, t.encoder, br)))
	return string(id) + "-a" + hex.EncodeToString(sum[:5])
}

// serveAudioSegment sends one segment of a running audio HLS job.
func (s *Server) serveAudioSegment(w http.ResponseWriter, r *http.Request, file string) {
	t, br := audioHLSParams(caseInsensitiveQuery(r))
	job, ok := s.transcode.Get(audioJobKey(storage.ItemID(r.PathValue("itemId")), t, br))
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if strings.HasSuffix(file, ".ts") {
		w.Header().Set("Content-Type", "video/mp2t")
	}
	http.ServeFile(w, r, filepath.Join(job.Dir(), file))
}

func (s *Server) ensureAudioJob(r *http.Request, item storage.Item, t audioTarget, br int, q url.Values) (*transcode.Job, error) {
	key := audioJobKey(item.ID, t, br)
	if job, ok := s.transcode.Get(key); ok && (!job.Done() || job.Err() == nil) {
		return job, nil
	}
	seg := strings.ToLower(q.Get("segmentcontainer"))
	if seg == "" {
		seg = "ts"
	}
	return s.transcode.Start(r.Context(), key, transcode.Request{
		InputPath: item.Path, VideoCodec: "none", AudioCodec: t.encoder, AudioBitrate: br, Container: seg,
	})
}

// handleAudioHLSMaster is GET /Audio/{itemId}/master.m3u8, the TranscodingUrl
// PlaybackInfo gives for an HLS profile.
func (s *Server) handleAudioHLSMaster(w http.ResponseWriter, r *http.Request) {
	item, ok := s.loadStreamableTrack(w, r)
	if !ok {
		return
	}
	t, br := audioHLSParams(caseInsensitiveQuery(r))
	s.writeAudioMaster(w, r, item, t, br, r.URL.RawQuery)
}

// handleAudioHLSPlaylist is GET /Audio/{itemId}/main.m3u8.
func (s *Server) handleAudioHLSPlaylist(w http.ResponseWriter, r *http.Request) {
	item, ok := s.loadStreamableTrack(w, r)
	if !ok {
		return
	}
	q := caseInsensitiveQuery(r)
	t, br := audioHLSParams(q)
	job, err := s.ensureAudioJob(r, item, t, br, q)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if !waitForPlaylist(job, hlsPlaylistWaitTimeout) {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	playlist, err := os.ReadFile(job.PlaylistPath())
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	_, _ = w.Write(rewritePlaylistSegmentQueries(playlist, r.URL.RawQuery))
}

// audioPlaybackInfo answers PlaybackInfo for a track: direct play when a
// DirectPlayProfile of type Audio takes the file (container, codec and, when
// the client states one, the bit rate), else the first usable Audio
// transcoding profile as a TranscodingUrl. A request without a device
// profile is direct play.
func (s *Server) audioPlaybackInfo(w http.ResponseWriter, r *http.Request, item storage.Item, profile deviceprofile.DeviceProfile, inline bool, body playbackInfoRequestBody) {
	resp := playbackInfoResponse{PlaySessionID: randomID()}
	source := baseMediaSourceInfo(item, item.Container)
	source.TranscodingSubProtocol = "http"
	ac, _ := AuthFromContext(r.Context())

	if !inline {
		source.SupportsDirectPlay, source.SupportsDirectStream, source.SupportsTranscoding = true, true, mayTranscodeAudio(ac)
		resp.MediaSources = []mediaSourceInfoDto{source}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	maxBR := int64(0)
	if body.MaxStreamingBitrate != nil {
		maxBR = *body.MaxStreamingBitrate
	} else if n, err := strconv.ParseInt(caseInsensitiveQuery(r).Get("maxstreamingbitrate"), 10, 64); err == nil {
		maxBR = n
	} else {
		maxBR = profile.MaxStreamingBitrate
	}
	codec := ""
	for _, st := range item.MediaStreams {
		if st.Type == storage.StreamTypeAudio {
			codec = strings.ToLower(st.Codec)
			break
		}
	}
	container := strings.ToLower(item.Container)
	inList := func(list, v string) bool {
		if strings.TrimSpace(list) == "" {
			return true
		}
		for _, c := range strings.Split(list, ",") {
			if strings.EqualFold(strings.TrimSpace(c), v) {
				return true
			}
		}
		return false
	}
	// a usable transcoding profile
	var tp *deviceprofile.TranscodingProfile
	for i := range profile.TranscodingProfiles {
		p := &profile.TranscodingProfiles[i]
		if !strings.EqualFold(p.Type, "Audio") || strings.EqualFold(p.Context, "Static") {
			continue
		}
		if p.Protocol != "" && !strings.EqualFold(p.Protocol, "http") && !strings.EqualFold(p.Protocol, "hls") {
			continue
		}
		tp = p
		break
	}
	source.SupportsTranscoding = tp != nil

	var reasons []string
	direct := false
	containerOK, codecOK := false, false
	for _, d := range profile.DirectPlayProfiles {
		if !strings.EqualFold(d.Type, "Audio") {
			continue
		}
		c, k := inList(d.Container, container), inList(d.AudioCodec, codec)
		containerOK = containerOK || c
		codecOK = codecOK || (c && k)
		if c && k {
			direct = true
			break
		}
	}
	if direct && maxBR > 0 && audioSourceBitrate(item) > maxBR {
		direct = false
		reasons = []string{"ContainerBitrateExceedsLimit"}
	} else if !direct && !containerOK {
		reasons = []string{"ContainerNotSupported", "VideoCodecNotSupported", "AudioCodecNotSupported"}
	} else if !direct {
		reasons = []string{"AudioCodecNotSupported"}
	}
	if !mayTranscodeAudio(ac) {
		// policy.playback.R3: with audio transcoding off the track is
		// offered as it is, whatever the profile says.
		source.SupportsTranscoding = false
		direct = true
	}
	if direct {
		source.SupportsDirectPlay, source.SupportsDirectStream = true, true
		resp.MediaSources = []mediaSourceInfoDto{source}
		writeJSON(w, http.StatusOK, resp)
		return
	}
	if tp != nil {
		hls := strings.EqualFold(tp.Protocol, "hls")
		cont := strings.ToLower(strings.TrimSpace(tp.Container))
		single := cont != "" && !strings.Contains(cont, ",")
		codecName := firstToken(tp.AudioCodec)
		if codecName == "" && single {
			codecName = pickAudioTarget(cont, "").codec
		}
		br := defaultAudioBitrate
		if profile.MusicStreamingTranscodingBitrate > 0 {
			br = int(profile.MusicStreamingTranscodingBitrate)
		}
		if maxBR > 0 && maxBR < int64(br) {
			br = int(maxBR)
		}
		q := url.Values{}
		if ac != nil && ac.DeviceID != "" {
			q.Set("DeviceId", ac.DeviceID)
		}
		q.Set("MediaSourceId", source.ID)
		q.Set("AudioCodec", codecName)
		q.Set("AudioBitrate", strconv.Itoa(br))
		path := "stream"
		if hls {
			path = "master.m3u8"
			seg := cont
			if !single {
				seg = "ts"
			}
			q.Set("SegmentContainer", seg)
		} else if single {
			path = "stream." + cont
		}
		q.Set("PlaySessionId", resp.PlaySessionID)
		if ac != nil && ac.Token != "" {
			q.Set("ApiKey", ac.Token)
		}
		q.Set("RequireAvc", "false")
		q.Set("EnableAudioVbrEncoding", "true")
		tag := sha1.Sum([]byte(string(item.ID) + strconv.FormatInt(item.Size, 10)))
		q.Set("Tag", hex.EncodeToString(tag[:])[:32])
		q.Set("TranscodeReasons", strings.Join(reasons, ","))
		source.TranscodingUrl = "/audio/" + guid.Dashed(string(item.ID)) + "/" + path + "?" + encodeKeepCommas(q)
		if single {
			source.TranscodingContainer = cont
		}
		if hls {
			source.TranscodingSubProtocol = "hls"
		}
	}
	resp.MediaSources = []mediaSourceInfoDto{source}
	writeJSON(w, http.StatusOK, resp)
}

// encodeKeepCommas is url.Values.Encode with the parameters in the order the
// reference writes them and commas left as they are.
func encodeKeepCommas(q url.Values) string {
	order := []string{"DeviceId", "MediaSourceId", "AudioCodec", "AudioBitrate", "SegmentContainer", "PlaySessionId", "ApiKey", "RequireAvc", "EnableAudioVbrEncoding", "Tag", "TranscodeReasons"}
	var parts []string
	for _, k := range order {
		if v, ok := q[k]; ok {
			parts = append(parts, k+"="+strings.ReplaceAll(url.QueryEscape(v[0]), "%2C", ","))
		}
	}
	return strings.Join(parts, "&")
}
