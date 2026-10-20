package api

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/deviceprofile"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
	"github.com/rafaelvieiras/tinyfin/internal/subtitle"
	"github.com/rafaelvieiras/tinyfin/internal/transcode"
)

// selection is what a client asks for within one media source: the audio
// and subtitle tracks (stream Index values of the media source).
type selection struct {
	MediaSourceID string // normalized (32 hex, no dashes) or ""
	Audio         *int
	Subtitle      *int
}

// selectionFor reads a PlaybackInfo request's selection: the body fields,
// then the query (any letter case).
func selectionFor(r *http.Request, body playbackInfoRequestBody) selection {
	q := caseInsensitiveQuery(r)
	sel := selection{Audio: body.AudioStreamIndex, Subtitle: body.SubtitleStreamIndex}
	raw := body.MediaSourceID
	if raw == "" {
		raw = q.Get("mediasourceid")
	}
	if n, ok := normalizeGUID(raw); ok {
		sel.MediaSourceID = n
	}
	if sel.Audio == nil {
		sel.Audio = atoiPtr(q.Get("audiostreamindex"))
	}
	if sel.Subtitle == nil {
		sel.Subtitle = atoiPtr(q.Get("subtitlestreamindex"))
	}
	return sel
}

func atoiPtr(s string) *int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return &n
	}
	return nil
}

func streamOfType(streams []storage.MediaStreamRef, idx int, t storage.StreamType) bool {
	for _, st := range streams {
		if st.Index == idx && st.Type == t {
			return true
		}
	}
	return false
}

// subtitleStream returns the subtitle stream with the given Index.
func subtitleStream(streams []storage.MediaStreamRef, idx *int) (storage.MediaStreamRef, bool) {
	if idx == nil {
		return storage.MediaStreamRef{}, false
	}
	for _, st := range streams {
		if st.Index == *idx && st.Type == storage.StreamTypeSubtitle {
			return st, true
		}
	}
	return storage.MediaStreamRef{}, false
}

// hlsContainer is the segment container of an HLS job: the first video
// transcoding profile's (HLS output is always packaged by the transcoder, so
// a direct-play container such as "mkv" says nothing about it), else the
// decision's.
func hlsContainer(profile deviceprofile.DeviceProfile, fallback string) string {
	for _, tp := range profile.TranscodingProfiles {
		if tp.Type != "" && tp.Type != "Video" {
			continue
		}
		if c, _, _ := strings.Cut(tp.Container, ","); strings.TrimSpace(c) != "" {
			return strings.ToLower(strings.TrimSpace(c))
		}
	}
	if fallback == "" {
		return "ts"
	}
	return fallback
}

// hlsKeyParams are the query parameters that change what a transcode job
// produces; two requests that agree on all of them share one job.
var hlsKeyParams = []string{"mediasourceid", "videocodec", "audiocodec", "audiostreamindex", "subtitlestreamindex", "subtitlemethod"}

// hlsJobKey names the transcode job a request belongs to. A request with
// none of the parameters keeps the item id, the name jobs had before track
// selection existed.
func hlsJobKey(itemID storage.ItemID, q url.Values) string {
	var b strings.Builder
	for _, k := range hlsKeyParams {
		b.WriteString(k + "=" + q.Get(k) + "\n")
	}
	empty := true
	for _, k := range hlsKeyParams {
		if q.Get(k) != "" {
			empty = false
		}
	}
	if empty {
		return string(itemID)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return string(itemID) + "-" + hex.EncodeToString(sum[:6])
}

// burnRequest turns a SubtitleStreamIndex/SubtitleMethod=Encode selection
// into the transcoder's description of what to render, or nil when nothing
// is to be burned (no such text/bitmap track).
func burnRequest(item storage.Item, idx int) *transcode.Subtitle {
	var track storage.MediaStreamRef
	found := false
	external := 0
	ordinal := 0
	for _, st := range item.MediaStreams {
		if st.Type == storage.StreamTypeSubtitle && st.IsExternal {
			external++
		}
	}
	for _, st := range item.MediaStreams {
		if st.Type != storage.StreamTypeSubtitle || st.IsExternal {
			continue
		}
		if st.Index == idx {
			track, found = st, true
			break
		}
		ordinal++
	}
	if !found {
		for _, st := range item.MediaStreams {
			if st.Type == storage.StreamTypeSubtitle && st.IsExternal && st.Index == idx {
				if !subtitle.IsTextCodec(st.Codec) {
					// a picture sidecar (VobSub, PGS, Matroska with pictures): the
					// transcoder reads it as a second input and probes its canvas
					return &transcode.Subtitle{Path: st.Path, Bitmap: true, External: true}
				}
				return &transcode.Subtitle{Path: st.Path}
			}
		}
		return nil
	}
	if !subtitle.IsTextCodec(track.Codec) {
		return &transcode.Subtitle{Path: item.Path, Bitmap: true, StreamIndex: track.Index - external}
	}
	return &transcode.Subtitle{Path: item.Path, Embedded: true, Index: ordinal}
}

// fileStreamIndex converts a media-source stream Index into the index of the
// stream inside the media file: external subtitle files are listed first, so
// the embedded streams are shifted by their count.
func fileStreamIndex(item storage.Item, idx int) int {
	external := 0
	for _, st := range item.MediaStreams {
		if st.IsExternal {
			external++
		}
	}
	return idx - external
}

// hlsQuery is the request's query the way the reference reads it for the HLS
// routes: names in any letter case (lower-cased here) and, when a name is
// repeated, its first value.
func hlsQuery(r *http.Request) url.Values {
	out := url.Values{}
	for _, p := range splitRawQuery(r.URL.RawQuery) {
		k, err1 := url.QueryUnescape(p.key)
		v, err2 := url.QueryUnescape(p.val)
		if err1 != nil || err2 != nil {
			continue
		}
		k = strings.ToLower(k)
		if _, ok := out[k]; !ok {
			out[k] = []string{v}
		}
	}
	return out
}
