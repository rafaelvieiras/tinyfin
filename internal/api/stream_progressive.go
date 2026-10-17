package api

import (
	"bufio"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
	"github.com/rafaelvieiras/tinyfin/internal/transcode"
)

// wantsProgressiveTranscode reports whether a /Videos/{id}/stream request is
// the progressive transcode a TranscodingUrl of an `http` transcoding profile
// leads to: it carries the reasons PlaybackInfo gave and is not a request for
// the file as it is (Static=true).
func wantsProgressiveTranscode(r *http.Request) bool {
	q := caseInsensitiveQuery(r)
	return q.Get("transcodereasons") != "" && !strings.EqualFold(q.Get("static"), "true")
}

// progressiveFormat maps the container of stream.<container> to the ffmpeg
// muxer and the content type served.
func progressiveFormat(container string) (format, contentType string) {
	switch strings.ToLower(container) {
	case "", "mp4", "m4v":
		return "mp4", "video/mp4"
	case "mkv", "matroska":
		return "matroska", "video/x-matroska"
	case "webm":
		return "webm", "video/webm"
	case "ts", "mpegts":
		return "mpegts", "video/mp2t"
	case "mov":
		return "mov", "video/quicktime"
	case "avi":
		return "avi", "video/x-msvideo"
	}
	return strings.ToLower(container), "video/" + strings.ToLower(container)
}

// streamVideoTranscode answers /Videos/{id}/stream.<container> with the
// source transcoded as one progressive stream: the codecs, bit rates, tracks
// and burned-in subtitle of the URL PlaybackInfo handed out (a codec the
// source already has is copied).
func (s *Server) streamVideoTranscode(w http.ResponseWriter, r *http.Request, item storage.Item, file string) {
	q := caseInsensitiveQuery(r)
	container := ""
	if _, ext, ok := strings.Cut(file, "."); ok {
		container = ext
	}
	format, contentType := progressiveFormat(container)

	audioIdx := atoiPtr(q.Get("audiostreamindex"))
	subIdx := atoiPtr(q.Get("subtitlestreamindex"))
	burn := strings.EqualFold(q.Get("subtitlemethod"), "Encode") && subIdx != nil
	var track *transcode.Subtitle
	if burn {
		track = burnRequest(item, *subIdx)
		burn = track != nil
	}
	acq := q.Get("audiocodec")
	if acq == "" && strings.Contains(q.Get("transcodereasons"), "AudioCodecNotSupported") {
		acq = "aac"
	}
	req := transcode.Request{
		InputPath:  mediaInput(item),
		VideoCodec: pickTargetCodec(q.Get("videocodec"), sourceCodec(item, storage.StreamTypeVideo, nil), burn),
		AudioCodec: pickTargetCodec(acq, sourceCodec(item, storage.StreamTypeAudio, audioIdx), false),
	}
	if audioIdx != nil && streamOfType(item.MediaStreams, *audioIdx, storage.StreamTypeAudio) {
		f := fileStreamIndex(item, *audioIdx)
		req.AudioStreamIndex = &f
	}
	if burn {
		req.Subtitle = track
	}
	if n, err := strconv.Atoi(q.Get("videobitrate")); err == nil && n > 0 && req.VideoCodec != "copy" {
		req.VideoBitrate = n
	}
	if n, err := strconv.Atoi(q.Get("audiobitrate")); err == nil && n > 0 {
		req.AudioBitrate = n
	}
	if n, err := strconv.ParseInt(q.Get("starttimeticks"), 10, 64); err == nil && n > 0 {
		req.StartTimeTicks = n
	}

	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Accept-Ranges", "none")
		w.WriteHeader(http.StatusOK)
		return
	}
	start := func() (*transcode.AudioStream, *bufio.Reader, bool) {
		vs, err := s.transcode.StreamVideo(r.Context(), req, format)
		if err != nil {
			return nil, nil, false
		}
		br := bufio.NewReader(vs)
		if _, err := br.Peek(1); err != nil {
			vs.Close()
			return nil, nil, false
		}
		return vs, br, true
	}
	vs, br, ok := start()
	if !ok && req.Subtitle != nil {
		// an ffmpeg that cannot render the track (no libass): the picture
		// without it is better than no answer
		req.Subtitle = nil
		if req.VideoCodec == "h264" {
			req.VideoCodec = pickTargetCodec(q.Get("videocodec"), sourceCodec(item, storage.StreamTypeVideo, nil), false)
		}
		vs, br, ok = start()
	}
	if !ok {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer vs.Close()
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Accept-Ranges", "none")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, br)
}
