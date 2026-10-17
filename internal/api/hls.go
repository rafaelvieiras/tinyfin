// hls.go implements the HLS half of docs/07's Remux/Direct Stream/Transcode
// ladder: docs/03's transcode worker role, wired in-process via
// internal/transcode (single-node deployment — see that package's doc
// comment for why nothing here assumes it's the only Manager instance).
// Direct Play never reaches this file at all — playback.go's
// handleVideoStream serves it directly, no subprocess involved.
package api

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/playback"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
	"github.com/rafaelvieiras/tinyfin/internal/transcode"
)

// hlsPlaylistWaitTimeout bounds how long handleHLSPlaylist blocks waiting
// for jellyfin-ffmpeg to write its first segment before giving up. A real
// client (ExoPlayer, hls.js) treats a slow-but-eventually-successful
// response the same as an instant one — there is no reason to fail fast
// here, as long as the wait has *some* bound, since an ffmpeg process that
// never produces output (bad input, missing codec) needs to eventually
// surface as an error rather than hang the request forever.
const hlsPlaylistWaitTimeout = 15 * time.Second

// segmentFilePattern matches exactly the filenames internal/transcode's
// buildArgs produces via -hls_segment_filename ("seg_%05d.<container>") —
// checked before handleVideoStream (playback.go) joins the {file} wildcard
// segment into a filesystem path, since unlike item.Path (never built from
// request input — see handleVideoStream's own doc comment on that trust
// boundary) this value comes directly off the wire.
var segmentFilePattern = regexp.MustCompile(`^seg_\d{5}\.[a-zA-Z0-9]+$`)

// hlsRequest is everything a master/main/segment request resolves to: the
// media source, the playback decision and the key of its transcode job.
type hlsRequest struct {
	item     storage.Item
	decision playback.Decision
	key      string
	req      transcode.Request
}

// resolveHLS works out what an HLS request asks for. The codecs, track
// selection and subtitle method in the query (what PlaybackInfo's
// TranscodingUrl carries) are authoritative; a bare URL falls back to the
// decision the device profile gives.
func (s *Server) resolveHLS(r *http.Request, item storage.Item) hlsRequest {
	q := caseInsensitiveQuery(r)
	if v, ok := s.versionFor(r.Context(), r, item); ok {
		item = v
	}
	profile := s.resolveDeviceProfile(r)
	audioIdx := atoiPtr(q.Get("audiostreamindex"))
	subIdx := atoiPtr(q.Get("subtitlestreamindex"))
	burn := strings.EqualFold(q.Get("subtitlemethod"), "Encode") && subIdx != nil
	var track *transcode.Subtitle
	if burn {
		track = burnRequest(item, *subIdx)
		burn = track != nil
	}

	dec := playback.DecideWith(item, profile, playback.Options{AudioStreamIndex: audioIdx, BurnSubtitle: burn})
	if vcq, acq := q.Get("videocodec"), q.Get("audiocodec"); vcq != "" || acq != "" {
		video, audio := streamsFor(item, audioIdx)
		vc := planVideoCodec(vcq, video, q, burn)
		ac := planAudioCodec(acq, audio, q)
		dec = playback.Decision{Mode: playback.ModeTranscode, Container: hlsContainer(profile, q.Get("segmentcontainer")), VideoCodec: vc, AudioCodec: ac}
		if vc == "copy" {
			dec.Mode = playback.ModeDirectStream
		}
	} else if dec.Mode == playback.ModeNone || dec.Mode == playback.ModeDirectPlay {
		// Nothing asks for a conversion: the playlists carry the streams as they are.
		dec = playback.Decision{Mode: playback.ModeRemux, Container: hlsContainer(profile, q.Get("segmentcontainer")), VideoCodec: "copy", AudioCodec: "copy"}
	} else if c := q.Get("segmentcontainer"); c != "" {
		dec.Container = c
	}
	if dec.Mode != playback.ModeNone && dec.Mode != playback.ModeDirectPlay {
		dec.Container = hlsContainer(profile, dec.Container)
	}

	out := hlsRequest{item: item, decision: dec, key: hlsJobKey(item.ID, q)}
	out.req = transcode.Request{
		InputPath:  mediaInput(item),
		VideoCodec: dec.VideoCodec,
		AudioCodec: dec.AudioCodec,
		Container:  dec.Container,

		VideoBitrate: numParam(q, "videobitrate"),
		AudioBitrate: numParam(q, "audiobitrate"),

		SegmentSeconds: hlsSegmentSeconds(q),
	}
	if dec.VideoCodec != "copy" && dec.VideoCodec != "none" {
		out.req.MaxWidth = effectiveMaxWidth(q)
	}
	if dec.VideoCodec == "h264" {
		out.req.H264Profile = firstKnownProfile(q.Get("h264-profile"), "baseline", "constrainedbaseline", "main", "high")
		if out.req.H264Profile == "" {
			out.req.H264Profile = "constrainedbaseline"
		}
		out.req.H264Level = numParam(q, "h264-level")
		if out.req.H264Level == 0 {
			out.req.H264Level = 41
		}
	}
	if audioIdx != nil && streamOfType(item.MediaStreams, *audioIdx, storage.StreamTypeAudio) {
		f := fileStreamIndex(item, *audioIdx)
		out.req.AudioStreamIndex = &f
	}
	if burn {
		out.req.Subtitle = track
	}
	return out
}

// ensureTranscodeJob returns the running job for h, starting it when there
// is none (or the previous one failed). A request that belongs to a play
// session (PlaySessionId) replaces the job that session was served from
// before: choosing another audio or subtitle track within a session starts a
// new transcode and stops the old one.
func (s *Server) ensureTranscodeJob(r *http.Request, h hlsRequest) (*transcode.Job, error) {
	if ps := caseInsensitiveQuery(r).Get("playsessionid"); ps != "" {
		sk := string(h.item.ID) + "|" + ps
		s.hlsMu.Lock()
		if s.hlsSessions == nil {
			s.hlsSessions = map[string]string{}
		}
		prev := s.hlsSessions[sk]
		s.hlsSessions[sk] = h.key
		s.hlsMu.Unlock()
		if prev != "" && prev != h.key {
			s.transcode.Stop(prev)
		}
	}
	if job, ok := s.transcode.Get(h.key); ok && (!job.Done() || job.Err() == nil) {
		return job, nil
	}
	return s.transcode.Start(context.Background(), h.key, h.req)
}

// writeHLSFailure is the opaque plain-text failure the reference answers an
// HLS request it cannot serve with.
func writeHLSFailure(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(status)
	_, _ = w.Write([]byte("Error processing request."))
}

// hlsSegmentSeconds is the segment length a request asks for (default 6).
func hlsSegmentSeconds(q url.Values) int {
	if n := numParam(q, "segmentlength"); n > 0 {
		return n
	}
	return 6
}

// hlsPreflight validates what the master and media playlists share
// (playback.hls.R6): the item id, the numeric query values and, for the
// master, the media source. It writes the failure and returns false when
// the request cannot be served.
func (s *Server) hlsPreflight(w http.ResponseWriter, r *http.Request, needSource bool) (storage.Item, bool) {
	rawID := r.PathValue("itemId")
	id, ok := normalizeGUID(rawID)
	if !ok {
		writeInvalidID(w, "itemId", rawID)
		return storage.Item{}, false
	}
	q := caseInsensitiveQuery(r)
	errs := map[string][]string{}
	for _, f := range [][2]string{{"segmentlength", "segmentLength"}, {"starttimeticks", "startTimeTicks"}, {"audiostreamindex", "audioStreamIndex"}, {"subtitlestreamindex", "subtitleStreamIndex"}} {
		if v := q.Get(f[0]); v != "" {
			if _, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err != nil {
				errs[f[1]] = []string{"The value '" + v + "' is not valid."}
			}
		}
	}
	if needSource && q.Get("mediasourceid") == "" {
		errs["mediaSourceId"] = []string{"The mediaSourceId field is required."}
	}
	if len(errs) > 0 {
		writeValidationErrors(w, errs)
		return storage.Item{}, false
	}
	if id == "00000000000000000000000000000000" {
		writeHLSFailure(w, http.StatusBadRequest)
		return storage.Item{}, false
	}
	item, err := s.store.GetItem(r.Context(), storage.ItemID(id))
	if err != nil { // policy.stream.R1: no library/tag/rating gate
		writeHLSFailure(w, http.StatusNotFound)
		return storage.Item{}, false
	}
	if raw := q.Get("mediasourceid"); raw != "" {
		norm, ok := normalizeGUID(raw)
		if !ok {
			writeHLSFailure(w, http.StatusInternalServerError)
			return storage.Item{}, false
		}
		if norm != string(item.ID) {
			if _, ok := s.versionFor(r.Context(), r, item); !ok {
				writeHLSFailure(w, http.StatusBadRequest)
				return storage.Item{}, false
			}
		}
	}
	if !item.HasMediaFile() {
		writeHLSFailure(w, http.StatusBadRequest)
		return storage.Item{}, false
	}
	return item, true
}

// requestAPIKey is the token the follow-up links of a playlist carry: the
// request's own ApiKey, else the token it was authenticated with.
func requestAPIKey(r *http.Request, q url.Values) string {
	if k := q.Get("apikey"); k != "" {
		return k
	}
	if ac, ok := AuthFromContext(r.Context()); ok && ac != nil {
		return ac.Token
	}
	return ""
}

// handleHLSMaster is GET /Videos/{itemId}/master.m3u8 — the entry point a
// real client fetches first to begin HLS playback. It is also where the
// transcode job starts (ensureTranscodeJob). Tinyfin produces one rendition
// per item, so the playlist has a single variant (playback.hls.R2): the
// subtitle group when the request's SubtitleMethod is Hls, then the variant
// with its bandwidth, codecs, resolution and frame rate.
func (s *Server) handleHLSMaster(w http.ResponseWriter, r *http.Request) {
	item, ok := s.hlsPreflight(w, r, true)
	if !ok {
		return
	}
	q := caseInsensitiveQuery(r)
	h := s.resolveHLS(r, item)
	if _, err := s.ensureTranscodeJob(r, h); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	audioIdx, subIdx := atoiPtr(q.Get("audiostreamindex")), atoiPtr(q.Get("subtitlestreamindex"))
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	_, _ = io.WriteString(w, buildMaster(h.item, q, r.URL.RawQuery, requestAPIKey(r, q), h.decision.VideoCodec, h.decision.AudioCodec, audioIdx, subIdx))
}

// handleHLSPlaylist is GET /Videos/{itemId}/main.m3u8 — the VOD media
// playlist (playback.hls.R3). The segment list is laid out on the grid of
// SegmentLength seconds over the run time; a picture that is copied has its
// cuts at key frames, so its list is the one ffmpeg wrote once it finished.
// Also starts the job, so a client that requests this directly still works.
func (s *Server) handleHLSPlaylist(w http.ResponseWriter, r *http.Request) {
	item, ok := s.hlsPreflight(w, r, false)
	if !ok {
		return
	}
	q := caseInsensitiveQuery(r)
	h := s.resolveHLS(r, item)
	job, err := s.ensureTranscodeJob(r, h)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	seg := hlsSegmentSeconds(q)
	var durations []float64
	if h.item.RunTimeTicks != nil {
		durations = gridDurations(*h.item.RunTimeTicks, seg)
	}
	if h.decision.VideoCodec == "copy" || len(durations) == 0 {
		if !waitForFinished(job, 2*hlsPlaylistWaitTimeout) {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if data, err := os.ReadFile(job.PlaylistPath()); err == nil {
			durations = playlistDurations(data)
		}
		if len(durations) == 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
	}
	target := seg
	for _, d := range durations {
		if c := int(math.Ceil(d)); c > target {
			target = c
		}
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	_, _ = io.WriteString(w, buildMediaPlaylist(durations, target, h.decision.Container, r.URL.RawQuery))
}

// waitForFinished blocks until the job's playlist is complete (ffmpeg exited
// and wrote it), the job fails or timeout elapses.
func waitForFinished(job *transcode.Job, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if job.Done() {
			if job.Err() != nil {
				return false
			}
			_, err := os.Stat(job.PlaylistPath())
			return err == nil
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// hlsSegmentWaitTimeout bounds how long a segment request waits for ffmpeg
// to reach it.
const hlsSegmentWaitTimeout = 60 * time.Second

// handleHLSNumberedSegment is GET /Videos/{itemId}/hls1/{playlistId}/{N}.{ext},
// the segments the media playlist names (playback.hls.R4). The transcode runs
// from the start of the file: a segment is served once ffmpeg has finished
// writing it, and a request for one further on waits for it.
func (s *Server) handleHLSNumberedSegment(w http.ResponseWriter, r *http.Request) {
	rawID := r.PathValue("itemId")
	id, ok := normalizeGUID(rawID)
	if !ok {
		writeInvalidID(w, "itemId", rawID)
		return
	}
	q := caseInsensitiveQuery(r)
	errs := map[string][]string{}
	for _, f := range [][2]string{{"runtimeticks", "runtimeTicks"}, {"actualsegmentlengthticks", "actualSegmentLengthTicks"}} {
		v := q.Get(f[0])
		if v == "" {
			errs[f[1]] = []string{"The " + f[1] + " field is required."}
		} else if _, err := strconv.ParseInt(v, 10, 64); err != nil {
			errs[f[1]] = []string{"The value '" + v + "' is not valid."}
		}
	}
	name := r.PathValue("segment")
	num, _, _ := strings.Cut(name, ".")
	n, err := strconv.Atoi(num)
	if err != nil {
		errs["segmentId"] = []string{"The value '" + num + "' is not valid."}
	}
	if len(errs) > 0 {
		writeValidationErrors(w, errs)
		return
	}
	if v := q.Get("starttimeticks"); v != "" && v != "0" {
		writeHLSFailure(w, http.StatusBadRequest)
		return
	}
	item, err := s.store.GetItem(r.Context(), storage.ItemID(id))
	if err != nil || n < 0 {
		writeHLSFailure(w, http.StatusNotFound)
		return
	}
	h := s.resolveHLS(r, item)
	job, err := s.ensureTranscodeJob(r, h)
	if err != nil {
		writeHLSFailure(w, http.StatusInternalServerError)
		return
	}
	file := filepath.Join(job.Dir(), fmt.Sprintf("seg_%05d.%s", n, h.decision.Container))
	next := filepath.Join(job.Dir(), fmt.Sprintf("seg_%05d.%s", n+1, h.decision.Container))
	deadline := time.Now().Add(hlsSegmentWaitTimeout)
	for {
		_, errF := os.Stat(file)
		_, errN := os.Stat(next)
		if errF == nil && (errN == nil || job.Done()) {
			break
		}
		if job.Done() && (job.Err() != nil || errF != nil) {
			if errF == nil {
				break
			}
			status := http.StatusNotFound
			if job.Err() != nil {
				status = http.StatusInternalServerError
			}
			writeHLSFailure(w, status)
			return
		}
		if time.Now().After(deadline) || r.Context().Err() != nil {
			writeHLSFailure(w, http.StatusServiceUnavailable)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	if strings.HasSuffix(name, ".ts") {
		w.Header().Set("Content-Type", "video/mp2t")
	}
	http.ServeFile(w, r, file)
}

// planVideoCodec is the codec a TranscodingUrl's VideoCodec list resolves to:
// "copy" when the picture can pass through (its codec is on the list and it
// fits every limit the request names), else the first codec on the list
// ("h264" when it is there or the list names none).
func planVideoCodec(list string, v *storage.MediaStreamRef, q url.Values, noCopy bool) string {
	if !noCopy && v != nil && (strings.TrimSpace(list) == "" || listHas(list, strings.ToLower(v.Codec))) && videoCopyAllowed(v, q) {
		return "copy"
	}
	c := pickTargetCodec(list, "", true)
	if c != "h264" && strings.Contains(","+strings.ToLower(strings.ReplaceAll(list, " ", ""))+",", ",vp9,") {
		c = "vp9"
	}
	if c == "copy" {
		return "h264"
	}
	return c
}

// planAudioCodec is planVideoCodec for the sound: copied when the codec is on
// the list, the channel count and the bit rate fit.
func planAudioCodec(list string, a *storage.MediaStreamRef, q url.Values) string {
	if a != nil && (strings.TrimSpace(list) == "" || listHas(list, strings.ToLower(a.Codec))) && audioCopyAllowed(a, q) {
		return "copy"
	}
	for _, c := range strings.Split(list, ",") {
		if c = strings.ToLower(strings.TrimSpace(c)); c != "" && c != "copy" {
			return c
		}
	}
	return "aac"
}

// rewritePlaylistSegmentQueries appends rawQuery (this request's own
// PlaySessionId/ApiKey — see hlsMasterURL's doc comment) to every segment
// line in playlist. ffmpeg writes segment URIs as plain relative filenames
// with no query string (internal/transcode's buildArgs doc comment), so
// without this a real HLS client resolves each one to a bare
// "/Videos/{itemId}/seg_00000.ts" — carrying none of the auth token that
// got it this far — and 401s on the very first segment fetch.
func rewritePlaylistSegmentQueries(playlist []byte, rawQuery string) []byte {
	if rawQuery == "" {
		return playlist
	}
	lines := strings.Split(string(playlist), "\n")
	for i, line := range lines {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines[i] = line + "?" + rawQuery
	}
	return []byte(strings.Join(lines, "\n"))
}

// waitForPlaylist polls for job's playlist file to exist, giving up early
// if the job has already exited with an error (no point waiting out the
// full timeout for a file that will never be written) or once timeout
// elapses. Returns whether the file is present.
func waitForPlaylist(job *transcode.Job, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(job.PlaylistPath()); err == nil {
			return true
		}
		if job.Done() && job.Err() != nil {
			return false
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// handleHLSSegment serves one HLS segment file from an already-running
// transcode job's output directory — reached via playback.go's
// handleVideoStream, which routes any {file} matching segmentFilePattern
// here rather than treating it as a Direct Play stream request. Segment
// filenames in the playlist handleHLSPlaylist serves are plain relative
// names (internal/transcode runs jellyfin-ffmpeg with its working
// directory set to the job dir — see internal/transcode's buildArgs/Start),
// which is exactly what lets a real HLS client resolve them as siblings of
// main.m3u8's own URL and land here without this project needing its own
// URL-rewriting layer.
func (s *Server) handleHLSSegment(w http.ResponseWriter, r *http.Request, itemID storage.ItemID, file string) {
	job, ok := s.transcode.Get(hlsJobKey(itemID, caseInsensitiveQuery(r)))
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	// internal/transcode.buildArgs only ever produces MPEG-TS segments
	// today (-hls_segment_type mpegts) — video/mp2t is that container's
	// registered MIME type. Set explicitly rather than relying on
	// http.ServeFile's extension-based sniffing, which has no built-in
	// mapping for ".ts" and would otherwise fall back to a generic
	// application/octet-stream that some HLS client implementations reject.
	if strings.HasSuffix(file, ".ts") {
		w.Header().Set("Content-Type", "video/mp2t")
	}
	http.ServeFile(w, r, filepath.Join(job.Dir(), file))
}

// mediaInput is what ffmpeg reads for item: the file, or the address a .strm
// file holds.
func mediaInput(item storage.Item) string {
	if item.RemoteURL != "" {
		return item.RemoteURL
	}
	return item.Path
}

// sourceCodec is the codec of the stream of type t the request plays (the
// selected audio track, else the default one of the type); "" when none.
func sourceCodec(item storage.Item, t storage.StreamType, audioIdx *int) string {
	var first, def string
	for _, st := range item.MediaStreams {
		if st.Type != t {
			continue
		}
		if t == storage.StreamTypeAudio && audioIdx != nil && st.Index == *audioIdx {
			return strings.ToLower(st.Codec)
		}
		if first == "" {
			first = strings.ToLower(st.Codec)
		}
		if st.IsDefault && def == "" {
			def = strings.ToLower(st.Codec)
		}
	}
	if def != "" {
		return def
	}
	return first
}

// pickTargetCodec reads a codec list of a TranscodingUrl (VideoCodec,
// AudioCodec): "copy" when the source codec is on it (the stream passes
// through as it is; never for a picture that is filtered), else the first
// codec it names. An empty list copies.
func pickTargetCodec(list, source string, noCopy bool) string {
	var first string
	for _, c := range strings.Split(list, ",") {
		c = strings.ToLower(strings.TrimSpace(c))
		if c == "" {
			continue
		}
		if first == "" || c == "h264" {
			// h264 is the encoder every build has: it wins over the other names
			if first == "" || first != "h264" {
				first = c
			}
		}
		if !noCopy && (c == "copy" || c == source) {
			return "copy"
		}
	}
	if first == "" || first == "copy" {
		if noCopy {
			return "h264"
		}
		return "copy"
	}
	return first
}
