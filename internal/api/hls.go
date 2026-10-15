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
	"net/http"
	"os"
	"path/filepath"
	"regexp"
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
	if vc, ac := q.Get("videocodec"), q.Get("audiocodec"); vc != "" || ac != "" {
		dec = playback.Decision{Mode: playback.ModeTranscode, Container: hlsContainer(profile, q.Get("segmentcontainer")), VideoCodec: strings.ToLower(vc), AudioCodec: strings.ToLower(ac)}
		if vc == "" || strings.EqualFold(vc, "copy") && !burn {
			dec.Mode = playback.ModeDirectStream
		}
		if burn && (dec.VideoCodec == "" || dec.VideoCodec == "copy") {
			// a copied picture cannot be filtered
			dec.VideoCodec = "h264"
		}
	} else if c := q.Get("segmentcontainer"); c != "" && dec.Mode != playback.ModeNone && dec.Mode != playback.ModeDirectPlay {
		dec.Container = c
	}
	if dec.Mode != playback.ModeNone && dec.Mode != playback.ModeDirectPlay {
		dec.Container = hlsContainer(profile, dec.Container)
	}

	out := hlsRequest{item: item, decision: dec, key: hlsJobKey(item.ID, q)}
	out.req = transcode.Request{
		InputPath:  item.Path,
		VideoCodec: dec.VideoCodec,
		AudioCodec: dec.AudioCodec,
		Container:  dec.Container,
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

// handleHLSMaster is GET /Videos/{itemId}/master.m3u8 — the entry point a
// real client fetches first to begin HLS playback (docs/04's Playback
// negotiation contract; route shape confirmed against the published
// @jellyfin/sdk client per docs/04's secondary-verification allowance,
// since the OpenAPI schema itself omits the DynamicHls routes — a known
// upstream gap, not something this project's own testing missed). This is
// also where the transcode job actually starts — see ensureTranscodeJob.
//
// Tinyfin produces exactly one rendition per item (no ABR ladder — out of
// scope for this slice, see ROADMAP.md), so the master playlist always
// points at a single variant (main.m3u8) rather than the multiple
// BANDWIDTH-tiered entries a real ABR-capable server would offer.
func (s *Server) handleHLSMaster(w http.ResponseWriter, r *http.Request) {
	itemID := storage.ItemID(r.PathValue("itemId"))
	item, err := s.store.GetItem(r.Context(), itemID)
	if err != nil || !s.itemReadableByCaller(r, item) {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	h := s.resolveHLS(r, item)
	if h.decision.Mode == playback.ModeNone || h.decision.Mode == playback.ModeDirectPlay {
		// Direct Play never reaches HLS at all (handleVideoStream serves
		// it instead); ModeNone has no compatible source to transcode
		// either.
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if _, err := s.ensureTranscodeJob(r, h); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	// BANDWIDTH is a required EXT-X-STREAM-INF attribute; this project
	// does not compute a real per-item estimate (no MaxStreamingBitrate
	// negotiation — see playbackInfoRequestBody's doc comment in
	// playback.go) so a fixed, conservative-high placeholder is reported
	// instead of omitting the attribute outright, which would make the
	// playlist invalid.
	fmt.Fprintf(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=8000000\nmain.m3u8?%s\n", r.URL.RawQuery)
}

// handleHLSPlaylist is GET /Videos/{itemId}/main.m3u8 — the media playlist
// jellyfin-ffmpeg writes progressively as it produces segments
// (internal/transcode.Job.PlaylistPath). Also calls ensureTranscodeJob
// itself (not just handleHLSMaster) so a client that requests this
// directly still works — see resolveTranscodeDecision's doc comment.
func (s *Server) handleHLSPlaylist(w http.ResponseWriter, r *http.Request) {
	itemID := storage.ItemID(r.PathValue("itemId"))
	item, err := s.store.GetItem(r.Context(), itemID)
	if err != nil || !s.itemReadableByCaller(r, item) {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	h := s.resolveHLS(r, item)
	if h.decision.Mode == playback.ModeNone || h.decision.Mode == playback.ModeDirectPlay {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	job, err := s.ensureTranscodeJob(r, h)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if !waitForPlaylist(job, hlsPlaylistWaitTimeout) {
		// Either ffmpeg crashed before writing anything (job.Err() != nil)
		// or it's simply still starting up past the wait budget — either
		// way there is nothing servable yet, and a real client backs off
		// and retries on a non-2xx response rather than hanging itself.
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	playlist, err := os.ReadFile(job.PlaylistPath())
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Write(rewritePlaylistSegmentQueries(playlist, r.URL.RawQuery))
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
