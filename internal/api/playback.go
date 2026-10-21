// playback.go implements docs/04's "playback/streaming" group: PlaybackInfo
// negotiation across the full Direct Play/Remux/Direct Stream/Transcode
// ladder (docs/07's decision — internal/playback.Decide) and the two data
// paths that follow from it: the raw Direct Play byte stream (this file)
// and the HLS routes the other three modes hand off to (hls.go). Trickplay
// is still explicitly out of scope — a later increment, not silently
// expanded into. Media segments and playstate reporting (resume position,
// watched status) used to be listed here too, but turned out to be
// load-bearing for the native player rather than deferrable — see
// sessions_playback.go.
package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/deviceprofile"
	"github.com/rafaelvieiras/tinyfin/internal/guid"
	"github.com/rafaelvieiras/tinyfin/internal/playback"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// mediaSourceInfoDto mirrors the OpenAPI schema's MediaSourceInfo. There is
// no "stream URL" field for Direct Play on the real schema at all (checked
// against the OpenAPI MediaSourceInfo properties) — a real client builds
// GET /Videos/{itemId}/stream(.{container}) itself from Id + Container, it
// isn't handed a ready-made URL. TranscodingUrl is exactly that "ready-made
// URL" for the other three modes, though (docs/07's Remux/Direct
// Stream/Transcode) — see handlePlaybackInfo.
//
// ReadAtNativeFramerate through HasSegments are NOT omitempty: found
// missing by reading jellyfin-sdk-kotlin's MediaSourceInfo.kt directly
// (not just testing) after the native Android player's PlaybackInfo call
// kept silently failing to resolve a media source with no crash and no
// server-visible error — kotlinx.serialization requires these keys
// present (they have no default), same failure class as
// DisplayPreferencesDto's ScrollDirection/SortOrder below, but this one is
// on the very first call the native player makes, so it blocks Direct
// Play for every item unconditionally.
type mediaSourceInfoDto struct {
	ID           string `json:"Id"`
	Protocol     string `json:"Protocol"`
	Path         string `json:"Path,omitempty"`
	Type         string `json:"Type"`
	Container    string `json:"Container,omitempty"`
	Size         int64  `json:"Size,omitempty"`
	Name         string `json:"Name,omitempty"`
	IsRemote     bool   `json:"IsRemote"`
	RunTimeTicks *int64 `json:"RunTimeTicks,omitempty"`
	// Bitrate is the rate of the whole file (its size over its length).
	Bitrate               *int64 `json:"Bitrate,omitempty"`
	ReadAtNativeFramerate bool   `json:"ReadAtNativeFramerate"`
	IgnoreDts             bool   `json:"IgnoreDts"`
	IgnoreIndex           bool   `json:"IgnoreIndex"`
	GenPtsInput           bool   `json:"GenPtsInput"`
	SupportsTranscoding   bool   `json:"SupportsTranscoding"`
	SupportsDirectStream  bool   `json:"SupportsDirectStream"`
	SupportsDirectPlay    bool   `json:"SupportsDirectPlay"`
	IsInfiniteStream      bool   `json:"IsInfiniteStream"`
	RequiresOpening       bool   `json:"RequiresOpening"`
	RequiresClosing       bool   `json:"RequiresClosing"`
	RequiresLooping       bool   `json:"RequiresLooping"`
	SupportsProbing       bool   `json:"SupportsProbing"`
	VideoType             string `json:"VideoType,omitempty"`
	// Timestamp is the timestamp state of a transport stream (None, Zero, Valid).
	Timestamp string `json:"Timestamp,omitempty"`
	IsoType   string `json:"IsoType,omitempty"`
	// TranscodingUrl/TranscodingContainer are set only for Remux/Direct
	// Stream/Transcode (docs/07) — empty (omitted) for Direct Play, which
	// needs no transcode-worker involvement at all.
	TranscodingUrl       string `json:"TranscodingUrl,omitempty"`
	TranscodingContainer string `json:"TranscodingContainer,omitempty"`
	// TranscodingSubProtocol is a required enum with no "none" member even
	// for Direct Play, where nothing is actually transcoding — "http" is
	// the value the reference server itself reports for a Direct Play
	// source; "hls" is reported for the other three modes, matching
	// TranscodingUrl's own protocol (hls.go serves HLS exclusively — no
	// progressive-remux "http" transcoding path exists in this project).
	TranscodingSubProtocol string `json:"TranscodingSubProtocol"`
	// DefaultAudioStreamIndex/DefaultSubtitleStreamIndex are the streams the
	// caller's preferences select (absent when there is none).
	DefaultAudioStreamIndex    *int             `json:"DefaultAudioStreamIndex,omitempty"`
	DefaultSubtitleStreamIndex *int             `json:"DefaultSubtitleStreamIndex,omitempty"`
	HasSegments                bool             `json:"HasSegments"`
	MediaStreams               []mediaStreamDto `json:"MediaStreams,omitempty"`
}

// MarshalJSON adds the members the reference always writes and Tinyfin has no
// data for: no attachments, no formats, no request headers, and
// UseMostCompatibleTranscodingProfile false.
func (m mediaSourceInfoDto) MarshalJSON() ([]byte, error) {
	type plain mediaSourceInfoDto
	return json.Marshal(struct {
		plain
		MediaAttachments                    []struct{}        `json:"MediaAttachments"`
		Formats                             []string          `json:"Formats"`
		RequiredHttpHeaders                 map[string]string `json:"RequiredHttpHeaders"`
		UseMostCompatibleTranscodingProfile bool              `json:"UseMostCompatibleTranscodingProfile"`
	}{plain: plain(m), MediaAttachments: []struct{}{}, Formats: []string{}, RequiredHttpHeaders: map[string]string{}})
}

// playbackInfoResponse mirrors the OpenAPI schema's PlaybackInfoResponse.
// PlaySessionId is a fresh opaque id per request — Tinyfin has no
// session/realtime state tracking a play session's lifecycle yet (docs/06
// is out of scope here), so this is just an identifier a client can echo
// back, not backed by any server-side session record.
type playbackInfoResponse struct {
	MediaSources  []mediaSourceInfoDto `json:"MediaSources"`
	PlaySessionID string               `json:"PlaySessionId"`
	ErrorCode     string               `json:"ErrorCode,omitempty"`
}

// playbackInfoRequestBody mirrors the OpenAPI schema's PlaybackInfoDto
// request body, trimmed to the one field this slice's decision actually
// consumes. Every other field on the real schema (EnableDirectPlay,
// MaxStreamingBitrate, AudioStreamIndex, ...) is a transcode-negotiation
// knob outside this slice's scope (no transcode worker exists yet).
type playbackInfoRequestBody struct {
	DeviceProfile *deviceprofile.DeviceProfile `json:"DeviceProfile"`
	// The selection fields may also come as query parameters; the body
	// is read first, the query fills what it left unset.
	MediaSourceID       string `json:"MediaSourceId,omitempty"`
	AudioStreamIndex    *int   `json:"AudioStreamIndex,omitempty"`
	SubtitleStreamIndex *int   `json:"SubtitleStreamIndex,omitempty"`
	// MaxStreamingBitrate bounds the rate of what the client takes (audio:
	// direct play above it becomes a transcode).
	MaxStreamingBitrate *int64 `json:"MaxStreamingBitrate,omitempty"`
	// AllowVideoStreamCopy / AllowAudioStreamCopy, when false, are written
	// to the end of the TranscodingUrl (they default to true).
	AllowVideoStreamCopy *bool `json:"AllowVideoStreamCopy,omitempty"`
	// AlwaysBurnInSubtitleWhenTranscoding (body only) burns the selected
	// subtitle into a transcoded picture whatever the profile delivers.
	AlwaysBurnInSubtitleWhenTranscoding *bool `json:"AlwaysBurnInSubtitleWhenTranscoding,omitempty"`
	AllowAudioStreamCopy                *bool `json:"AllowAudioStreamCopy,omitempty"`
}

// UnmarshalJSON reads the stream indexes and the bitrate ceiling as the
// reference does: a whole number or a string of digits (the web client sends
// "1"); null or an empty string is "not set"; anything else is an error
// wrapping deviceprofile.ErrBadNumber.
func (b *playbackInfoRequestBody) UnmarshalJSON(data []byte) error {
	type plain playbackInfoRequestBody
	aux := struct {
		*plain
		AudioStreamIndex    json.RawMessage `json:"AudioStreamIndex"`
		SubtitleStreamIndex json.RawMessage `json:"SubtitleStreamIndex"`
		MaxStreamingBitrate json.RawMessage `json:"MaxStreamingBitrate"`
	}{plain: (*plain)(b)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	b.AudioStreamIndex, b.SubtitleStreamIndex, b.MaxStreamingBitrate = nil, nil, nil
	for _, f := range []struct {
		raw json.RawMessage
		set func(n int64)
	}{
		{aux.AudioStreamIndex, func(n int64) { v := int(n); b.AudioStreamIndex = &v }},
		{aux.SubtitleStreamIndex, func(n int64) { v := int(n); b.SubtitleStreamIndex = &v }},
		{aux.MaxStreamingBitrate, func(n int64) { b.MaxStreamingBitrate = &n }},
	} {
		n, err := deviceprofile.LenientInt(f.raw, true)
		if err != nil {
			return err
		}
		if n != nil {
			f.set(*n)
		}
	}
	return nil
}

// resolveDeviceProfile implements docs/07 O2's negotiation order: a
// client-submitted inline profile (POST body) is authoritative whenever
// present; otherwise fall back to the built-in registry keyed by the
// Client string off the Authorization header; otherwise the conservative
// Default().
func (s *Server) resolveDeviceProfile(r *http.Request) deviceprofile.DeviceProfile {
	p, _ := s.resolveDeviceProfileInline(r)
	return p
}

// resolveDeviceProfileInline is resolveDeviceProfile that also reports
// whether the profile came from the request body: the subtitle delivery
// members of PlaybackInfo are filled only then (the reference sets none
// for a request without a device profile).
func (s *Server) resolveDeviceProfileInline(r *http.Request) (deviceprofile.DeviceProfile, bool) {
	return s.profileFor(r, readPlaybackBody(r))
}

// readPlaybackBody decodes a POSTed PlaybackInfo body; anything else, or a
// body that is not JSON, is the zero value.
func readPlaybackBody(r *http.Request) playbackInfoRequestBody {
	var body playbackInfoRequestBody
	if r.Method == http.MethodPost && r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return playbackInfoRequestBody{}
		}
	}
	return body
}

// profileFor picks the device profile of a request given its decoded body:
// the inline one (inline true), else the client's built-in one, else the
// default.
func (s *Server) profileFor(r *http.Request, body playbackInfoRequestBody) (deviceprofile.DeviceProfile, bool) {
	if body.DeviceProfile != nil {
		return *body.DeviceProfile, true
	}
	if ac, ok := AuthFromContext(r.Context()); ok && ac != nil {
		if p, found := deviceprofile.Lookup(ac.Client); found {
			return p, false
		}
	}
	return deviceprofile.Default(), false
}

// baseMediaSourceInfo builds the fields of a mediaSourceInfoDto that don't
// depend on any device-profile transcode negotiation — the plain "what file
// is this" description shared by handlePlaybackInfo (which layers its own
// Mode-driven fields, e.g. SupportsDirectPlay/TranscodingUrl, on top per
// decision.Mode below) and dto.go's mediaSourcesFor (baseItemDto.MediaSources
// on GET /Items/{itemId}, which has no device profile to negotiate against
// at all — see that function's doc comment). container is passed in rather
// than read off item directly since handlePlaybackInfo's caller wants
// decision.Container (a possibly profile-transcoded value) while
// mediaSourcesFor wants the item's own on-disk container.
func baseMediaSourceInfo(item storage.Item, container string) mediaSourceInfoDto {
	// The reference names a media source after its file, not after the item.
	name := item.Name
	if item.Path != "" {
		base := filepath.Base(item.Path)
		name = strings.TrimSuffix(base, filepath.Ext(base))
	}
	src := mediaSourceInfoDto{
		ID:              string(item.ID),
		Protocol:        "File",
		Path:            item.Path,
		Type:            "Default",
		Container:       container,
		Size:            item.Size,
		Name:            name,
		RunTimeTicks:    item.RunTimeTicks,
		SupportsProbing: true,
		MediaStreams:    mediaStreamDtos(item.MediaStreams),
	}
	switch item.Type {
	case storage.ItemTypeMovie, storage.ItemTypeEpisode, storage.ItemTypeVideo, storage.ItemTypeMusicVideo, storage.ItemTypeTrailer:
		src.VideoType = "VideoFile"
		src.Timestamp = item.TSTimestamp
		if item.RemoteURL == "" && item.VideoType == "" {
			if br := audioSourceBitrate(item); br > 0 {
				src.Bitrate = &br
			}
		} else if item.RemoteURL != "" && item.BitRate != nil {
			src.Bitrate = item.BitRate
		}
	}
	if item.RemoteURL != "" {
		// A .strm file that holds an address: the source is the address.
		src.Path = item.RemoteURL
		src.IsRemote = true
		src.Protocol = remoteProtocol(item.RemoteURL)
	}
	if item.VideoType != "" {
		src.VideoType, src.IsoType = item.VideoType, item.IsoType
		// A disc is named "<folder>/DVD" or "<folder>/Bluray" (an image: its
		// file name without the extension, then the kind of disc inside).
		kind := item.VideoType
		if kind == storage.VideoTypeIso {
			kind = item.IsoType
		} else if item.Path != "" {
			src.Name = filepath.Base(item.Path)
		}
		switch kind {
		case storage.VideoTypeDvd:
			src.Name += "/DVD"
		case storage.VideoTypeBluRay:
			src.Name += "/Bluray"
		default:
			if item.VideoType == storage.VideoTypeIso {
				src.Name += "/ISO"
			}
		}
	}
	return src
}

// remoteProtocol maps the scheme of an address to the MediaProtocol name.
func remoteProtocol(u string) string {
	scheme := strings.ToLower(u)
	if i := strings.Index(scheme, "://"); i > 0 {
		scheme = scheme[:i]
	}
	switch scheme {
	case "http", "https":
		return "Http"
	case "rtmp":
		return "Rtmp"
	case "rtsp":
		return "Rtsp"
	case "udp":
		return "Udp"
	case "rtp":
		return "Rtp"
	case "ftp":
		return "Ftp"
	}
	return "File"
}

// handlePlaybackInfo serves both GET and POST /Items/{itemId}/PlaybackInfo.
// The GET form has no request body to carry a profile, so it always falls
// through to the registry/default lookup; the POST form is how a real
// client normally negotiates (docs/04).
func (s *Server) handlePlaybackInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && r.Body != nil {
		// A subtitle profile with an empty, null or unknown Method, or a
		// numeric member that is not a number, is a request the reference
		// refuses outright.
		raw, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var probe playbackInfoRequestBody
		if err := json.Unmarshal(raw, &probe); err != nil && (strings.Contains(err.Error(), "SubtitleProfile.Method") || errors.Is(err, deviceprofile.ErrBadNumber)) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	}
	itemID := storage.ItemID(r.PathValue("itemId"))
	item, err := s.store.GetItem(r.Context(), itemID)
	if err != nil || !s.itemReadableByCaller(r, item) {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	if !item.HasMediaFile() {
		writeNotPlayable(w)
		return
	}

	if item.RemoteURL != "" && len(item.MediaStreams) == 0 {
		// What an address holds is probed when it is played, and kept.
		if streams, ticks, c, br, err := s.scanner.ProbeMedia(r.Context(), item.RemoteURL); err == nil {
			item.MediaStreams, item.RunTimeTicks, item.Container, item.BitRate = streams, ticks, c, br
			if err := s.store.UpsertItem(r.Context(), item); err != nil {
				log.Printf("api: keeping the probe of %s: %v", item.ID, err)
			}
		}
	}
	if item.VideoType == storage.VideoTypeDvd || item.VideoType == storage.VideoTypeBluRay ||
		(IsStrmItem(item) && !(item.RemoteURL != "" && len(item.MediaStreams) > 0)) {
		// A disc folder, or a .strm file with nothing known about what it
		// points at, is always offered as it is: there is nothing to
		// negotiate (the reference does the same), unless the caller must
		// have remote sources transcoded.
		if IsStrmItem(item) && item.VideoType == "" && s.offerForcedStrm(w, r, item) {
			return
		}
		source := baseMediaSourceInfo(item, item.Container)
		source.SupportsDirectPlay, source.SupportsDirectStream, source.SupportsTranscoding = true, true, true
		source.TranscodingSubProtocol = "http"
		_, inline := s.profileFor(r, readPlaybackBody(r))
		s.maybeForceRemote(r, &source, item, inline)
		writeJSON(w, http.StatusOK, playbackInfoResponse{PlaySessionID: randomID(), MediaSources: []mediaSourceInfoDto{source}})
		return
	}

	body := readPlaybackBody(r)
	profile, inline := s.profileFor(r, body)
	if item.Type == storage.ItemTypeAudio {
		s.audioPlaybackInfo(w, r, item, profile, inline, body)
		return
	}
	sel := selectionFor(r, body)

	// A selection only applies to the media source it names (the reference
	// ignores AudioStreamIndex/SubtitleStreamIndex sent without a
	// MediaSourceId, or with another source's).
	if v, ok := s.versionFor(r.Context(), r, item); ok && sel.MediaSourceID == string(v.ID) {
		item = v
	}
	applies := sel.MediaSourceID != "" && sel.MediaSourceID == string(item.ID)

	var defaults mediaSourceInfoDto
	s.applyStreamDefaults(r.Context(), &defaults, item.MediaStreams)
	audioIdx := defaults.DefaultAudioStreamIndex
	subIdx := defaults.DefaultSubtitleStreamIndex
	if applies {
		if sel.Audio != nil && streamOfType(item.MediaStreams, *sel.Audio, storage.StreamTypeAudio) {
			audioIdx = sel.Audio
		}
		if sel.Subtitle != nil {
			// reported as sent, whether it names a stream or not
			subIdx = sel.Subtitle
		}
	}

	opts := playback.Options{AudioStreamIndex: audioIdx}
	if item.Type != storage.ItemTypeAudio {
		if item.Format != "" && item.VideoType == "" {
			// the container of a probed file is the format of the probe; a
			// profile that lists any of the names of a format with several
			// ("mkv,webm", "mov,mp4,m4a,3gp,3g2,mj2") gets that one (any of
			// them matches a direct play entry)
			item.Container = item.Format
			if inline {
				item.Container = formatSourceName(item.Format, profile)
			}
		} else if isMP4Family(item.Container) {
			item.Container = mp4SourceName(item.Container, profile, inline)
		} else if n := extFormatSourceName(item.Container, profile); n != "" {
			item.Container = n
		}
	}
	subMethod := ""
	subTrack, hasSub := subtitleStream(item.MediaStreams, subIdx)
	if inline && hasSub {
		m := playback.DeliverSubtitle(subTrack, profile.SubtitleProfiles, subtitleDelivery(item, true, true)).Method
		switch m {
		case "Encode":
			opts.BurnSubtitle = true
		case "Hls":
			opts.NoDirectPlay = true
		}
	}
	// A bitrate ceiling (the request's or profile's, capped by the caller's
	// RemoteClientBitrateLimit when remote) that the file's own rate
	// exceeds ends Direct Play; the picture may still be copied.
	overBitrate, bitrateOnly := false, false
	if inline {
		if maxBR := s.remoteBitrateCap(r, requestedMaxBitrate(r, body, profile)); maxBR > 0 && audioSourceBitrate(item) > maxBR {
			// only the rate keeps it from Direct Play: it is the one reason
			bitrateOnly = playback.DecideWith(item, profile, opts).Mode == playback.ModeDirectPlay
			overBitrate, opts.NoDirectPlay = true, true
		}
	}
	decision := playback.DecideWith(item, profile, opts)
	if !inline {
		// A request without a device profile states no limit: the reference
		// offers the file as it is, whatever its container and codecs.
		decision = playback.Decision{Mode: playback.ModeDirectPlay, Container: strings.ToLower(item.Container)}
	}
	if inline && hasSub && !opts.BurnSubtitle && decision.Mode != playback.ModeDirectPlay && decision.Mode != playback.ModeNone &&
		playback.DeliverSubtitle(subTrack, profile.SubtitleProfiles, subtitleDelivery(item, false, true)).Method == "Encode" {
		// the file is not served as is any more, so an embedded track
		// cannot stay in it: it has to be rendered into the picture
		opts.BurnSubtitle = true
		decision = playback.DecideWith(item, profile, opts)
	}
	if hasSub && inline {
		subMethod = playback.DeliverSubtitle(subTrack, profile.SubtitleProfiles, subtitleDelivery(item, decision.Mode == playback.ModeDirectPlay, decision.Mode != playback.ModeDirectPlay)).Method
	}
	if overBitrate && decision.Mode != playback.ModeDirectPlay {
		if bitrateOnly {
			decision.Reasons = nil
		}
		decision.Reasons = append(decision.Reasons, "ContainerBitrateExceedsLimit")
	}
	resp := playbackInfoResponse{PlaySessionID: randomID()}

	source := baseMediaSourceInfo(item, decision.Container)
	source.DefaultAudioStreamIndex = audioIdx
	source.DefaultSubtitleStreamIndex = subIdx
	if inline {
		s.applySubtitleDelivery(r, &source, item, profile.SubtitleProfiles, decision.Mode == playback.ModeDirectPlay)
	}

	switch decision.Mode {
	case playback.ModeDirectPlay:
		source.SupportsDirectPlay = true
		if item.Type == storage.ItemTypeAudio {
			// A track is also served as it is by /Audio/{id}/stream.
			source.SupportsDirectStream = true
		}
		source.TranscodingSubProtocol = "http"
		if item.Type != storage.ItemTypeAudio {
			// policy.playback.R5: a video the profile plays as it is also
			// lists what the flags leave on the table.
			ac, _ := AuthFromContext(r.Context())
			f := playbackFlagsFor(ac)
			if inline {
				source.SupportsDirectStream = true
				source.SupportsTranscoding = f.video || f.remux || f.audio
			} else {
				source.SupportsDirectStream = f.remux
				source.SupportsTranscoding = f.video
			}
		}
		resp.MediaSources = append([]mediaSourceInfoDto{source}, s.versionSources(r.Context(), item)...)

	case playback.ModeRemux, playback.ModeDirectStream, playback.ModeTranscode:
		if ac, _ := AuthFromContext(r.Context()); !mayTranscodeVideo(ac) {
			// policy.playback.R2: nothing but Direct Play is on offer. The
			// source is still listed, with no way to stream it.
			source.TranscodingSubProtocol = "http"
			resp.MediaSources = []mediaSourceInfoDto{source}
			break
		}
		// the reference never offers direct streaming for a source it transcodes
		// and keeps naming the container the source has
		if item.Type != storage.ItemTypeAudio {
			source.Container = strings.ToLower(item.Container)
		}
		source.SupportsDirectStream = false
		source.SupportsTranscoding = true
		if ac, _ := AuthFromContext(r.Context()); ac != nil {
			if f := playbackFlagsFor(ac); !f.video && !f.remux {
				// policy.playback.R6: only audio transcoding is allowed, so
				// the offer is the progressive stream, not HLS.
				source.SupportsDirectStream = false
				source.TranscodingUrl = buildProgressiveOffer(offerInput{
					item: item, reasons: decision.Reasons, deviceID: ac.DeviceID, token: ac.Token, playSession: resp.PlaySessionID,
					audioIdx: audioIdx, subIdx: subIdx,
					noVideoCopy: !allowedByRequest(r, "allowvideostreamcopy", body.AllowVideoStreamCopy),
					noAudioCopy: !allowedByRequest(r, "allowaudiostreamcopy", body.AllowAudioStreamCopy),
				})
				source.TranscodingSubProtocol = "http"
				resp.MediaSources = []mediaSourceInfoDto{source}
				break
			}
		}
		var token, deviceID string
		if ac, ok := AuthFromContext(r.Context()); ok && ac != nil {
			token, deviceID = ac.Token, ac.DeviceID
		}
		method := ""
		if hasSub && (subMethod == "Encode" || subMethod == "Hls") {
			method = subMethod
		} else if inline && !hasSub && subIdx != nil {
			// no subtitle (-1) or a stream that does not exist: the URL
			// names the Encode method without a track (playback.subtitle.R8)
			method = "Encode"
		}
		offer := buildTranscodeOffer(offerInput{
			item: item, profile: profile, tp: pickTranscodingProfile(profile, decision, item), reasons: decision.Reasons,
			deviceID: deviceID, token: token, playSession: resp.PlaySessionID,
			audioIdx: audioIdx, subIdx: subIdx, subMethod: method,
			maxBitrate:  s.remoteBitrateCap(r, requestedMaxBitrate(r, body, profile)),
			noVideoCopy: !allowedByRequest(r, "allowvideostreamcopy", body.AllowVideoStreamCopy) || forcesRemoteTranscoding(r, item),
			noAudioCopy: !allowedByRequest(r, "allowaudiostreamcopy", body.AllowAudioStreamCopy) || forcesRemoteTranscoding(r, item),
			alwaysBurn:  inline && body.AlwaysBurnInSubtitleWhenTranscoding != nil && *body.AlwaysBurnInSubtitleWhenTranscoding,
		})
		source.TranscodingUrl = offer.URL
		source.TranscodingContainer = offer.Container
		source.TranscodingSubProtocol = offer.SubProtocol
		resp.MediaSources = []mediaSourceInfoDto{source}

	default: // playback.ModeNone
		// Neither Direct Play nor any transcode fallback applies (the
		// resolved profile has no usable TranscodingProfiles entry — see
		// deviceprofile.DeviceProfile's doc comment). ErrorCode:
		// NoCompatibleStream matches the reference server's own
		// PlaybackErrorCode for this situation rather than inventing a new
		// one, and MediaSources stays empty rather than pointing the
		// client at a stream URL that wouldn't actually work.
		resp.ErrorCode = "NoCompatibleStream"
	}

	if len(resp.MediaSources) > 0 {
		s.maybeForceRemote(r, &resp.MediaSources[0], item, inline)
	}
	writeJSON(w, http.StatusOK, resp)
}

// forcesRemoteTranscoding reports whether the caller must have the remote
// (.strm address) source item transcoded.
func forcesRemoteTranscoding(r *http.Request, item storage.Item) bool {
	ac, _ := AuthFromContext(r.Context())
	return item.RemoteURL != "" && ac != nil && ac.Authenticated && !ac.IsAPIKey && storedUserPolicy(ac.User).ForceRemoteSourceTranscoding
}

// maybeForceRemote applies policy.remote.R2: a user who must have remote
// sources transcoded is not offered the address, whenever the client states
// a profile. A source that is transcoded anyway keeps its offer (which then
// forbids copying, see handlePlaybackInfo).
func (s *Server) maybeForceRemote(r *http.Request, source *mediaSourceInfoDto, item storage.Item, inline bool) {
	if !forcesRemoteTranscoding(r, item) || !inline || !source.SupportsDirectPlay {
		return
	}
	ac, _ := AuthFromContext(r.Context())
	s.forceRemoteTranscode(source, item, ac.Token, ac.DeviceID)
}

// hlsMasterURL builds the TranscodingUrl a client should fetch to begin
// consuming the HLS stream for itemID (handleHLSMaster, hls.go), mirroring
// the reference server's own master-playlist route shape (confirmed
// against the published @jellyfin/sdk client per docs/04's
// secondary-verification allowance — the OpenAPI schema itself omits the
// DynamicHls routes).
//
// apiKey is embedded as a query param for the same reason
// withAuthContext's query-string fallback exists (auth.go): a browser's
// hls.js resolves this URL directly (no ApiClient, no custom headers), so
// without a token in the URL itself every follow-up request — main.m3u8,
// then each segment — 401s. Confirmed by testing an actual transcode
// through jellyfin-web in a real browser: the very first master.m3u8
// fetch came back 401 until this was added. handleHLSMaster forwards its
// own RawQuery into the main.m3u8 link it emits, and handleHLSPlaylist
// rewrites each segment line with that same query — so one token embedded
// here propagates through the whole chain.
func // The list of TranscodeReasons keeps its commas, like the reference writes it.
hlsMasterURL(itemID storage.ItemID, q url.Values) string {
	return fmt.Sprintf("/Videos/%s/master.m3u8?%s", url.PathEscape(string(itemID)), strings.ReplaceAll(q.Encode(), "%2C", ","))
}

// handleVideoStream serves GET /Videos/{itemId}/stream and
// /Videos/{itemId}/stream.{container} — the Direct Play data path — and, as
// of hls.go, the HLS segment files the other three modes produce. All are
// registered as a single "GET /Videos/{itemId}/{file}" mux pattern
// (server.go): http.ServeMux wildcards must occupy a whole path segment, so
// neither "stream.{container}" nor a segment's own extension can be its own
// pattern — this handler dispatches on the {file} segment's shape itself
// instead of relying on the router to split it further.
//
// Direct Play: no re-encoding, no ffmpeg subprocess (that's the transcode
// worker's job, docs/07, only reached for the other three modes) — just the
// file bytes, via http.ServeFile so Range/conditional requests and HEAD are
// handled correctly for seeking.
//
// Path-traversal trust boundary: item.Path comes from storage.Item, set by
// whatever wrote that row (the future scanner, docs/08) — never built by
// concatenating any request-supplied path fragment (itemId is only ever
// used as an opaque lookup key into storage.ItemStore, and {file} is only
// ever compared against a literal prefix, never joined into a filesystem
// path). This is the same trust boundary the rest of this codebase already
// relies on for Item.Path/ImageRef.Path; it does not need its own new
// sandboxing layer to stay consistent with that. The HLS-segment branch
// below is the one place {file} genuinely is joined into a filesystem path
// (handleHLSSegment, hls.go) — segmentFilePattern validates it strictly
// before that happens, since unlike item.Path this value comes directly
// off the wire.
// writeNotPlayable answers a playback request for an item that has no media
// file (a series, season or folder) with the reference's opaque 400.
func writeNotPlayable(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = w.Write([]byte("Error processing request."))
}

func (s *Server) handleVideoStream(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("file")
	itemID := storage.ItemID(r.PathValue("itemId"))

	if segmentFilePattern.MatchString(file) {
		s.handleHLSSegment(w, r, itemID, file)
		return
	}
	if file != "stream" && !strings.HasPrefix(file, "stream.") {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	item, err := s.store.GetItem(r.Context(), itemID)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if !item.HasMediaFile() {
		writeNotPlayable(w)
		return
	}
	if v, ok := s.versionFor(r.Context(), r, item); ok {
		item = v
	}
	switch {
	case item.VideoType == storage.VideoTypeDvd || item.VideoType == storage.VideoTypeBluRay:
		// A disc folder has no single file to send. One that could not be
		// read at all is an error.
		if len(item.MediaStreams) == 0 {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Error processing request."))
			return
		}
		w.Header().Set("Content-Type", "video/mpeg")
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusOK)
		return
	case wantsProgressiveTranscode(r) && (item.RemoteURL != "" || !IsStrmItem(item)):
		s.streamVideoTranscode(w, r, item, file)
		return
	case item.RemoteURL != "":
		s.proxyRemote(w, r, item.RemoteURL)
		return
	case IsStrmItem(item):
		// The file itself, not a video.
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	http.ServeFile(w, r, item.Path)
}

// IsStrmItem reports whether item is a .strm file.
func IsStrmItem(item storage.Item) bool {
	return item.Path != "" && strings.EqualFold(filepath.Ext(item.Path), ".strm")
}

// remoteClient fetches the address of a .strm file for playback; only the
// wait for the response headers is bounded, the body streams for as long as
// the viewer watches.
var remoteClient = &http.Client{Transport: &http.Transport{
	ResponseHeaderTimeout: 30 * time.Second,
	Proxy:                 http.ProxyFromEnvironment,
}}

// proxyRemote sends the bytes of an address to the caller: the whole body,
// without range support (the reference answers the same).
func (s *Server) proxyRemote(w http.ResponseWriter, r *http.Request, target string) {
	if remoteProtocol(target) != "Http" {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Error processing request."))
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, nil)
	var resp *http.Response
	if err == nil {
		resp, err = remoteClient.Do(req)
	}
	if err != nil || resp.StatusCode/100 != 2 {
		if resp != nil {
			resp.Body.Close()
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Error processing request."))
		return
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	if resp.ContentLength >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}
	w.Header().Set("Accept-Ranges", "none")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = io.Copy(w, resp.Body)
	}
}

// bitrateTestDefaultSize is the response size GET /Playback/BitrateTest
// uses when the client omits Size — confirmed against a real Jellyfin
// server (reference server), not assumed: a request with no Size param at
// all came back byte-for-byte the same Content-Length as an explicit
// Size=100000, both landing on 131072 once bitrateTestRoundedSize's
// rounding (below) is applied.
const bitrateTestDefaultSize = 100000

// bitrateTestMinSize/bitrateTestMaxSize are the Size bounds the real
// server enforces — confirmed by binary search against it, not guessed: a
// request carrying an out-of-range Size comes back 400 with a body whose
// validation message spells the bounds out explicitly ("must be greater
// than or equal to 1 and less than or equal to 100000000"), and every
// value up to and including that upper bound succeeds (Size=100000000
// itself returns 200), so the range is inclusive on both ends.
const (
	bitrateTestMinSize = 1
	bitrateTestMaxSize = 100_000_000
)

// bitrateTestFloor is the smallest response bitrateTestRoundedSize will
// ever produce. Confirmed against the real server: every Size from 1
// through 16 inclusive came back with Content-Length 16, not a smaller
// number and not literally echoing Size — this is a genuine floor, not
// coincidental (rounding Size=1 up to the next power of two on its own
// would give 1, not 16).
const bitrateTestFloor = 16

// bitrateTestRoundedSize maps a validated Size query value to the actual
// response length the real server sends — confirmed by requesting a wide
// spread of values against it (reference server) and finding every one of
// them landed exactly on a power of two: 1..16 -> 16, 17..32 -> 32,
// 33..64 -> 64, ..., 100000 -> 131072, 100000000 -> 134217728. Bandwidth
// estimation only needs *some* fixed number of bytes downloaded in a
// measured time, so a real client is never expected to depend on getting
// back exactly the Size it asked for — but this project mirrors the
// rounding anyway (AGENTS.md principle 4: wire compatibility is the
// product) rather than serving the literal requested byte count, in case
// some client does compare Content-Length against what it asked for as a
// sanity check.
func bitrateTestRoundedSize(size int) int {
	n := bitrateTestFloor
	for n < size {
		n <<= 1
	}
	return n
}

func writeBitrateTestValidationError(w http.ResponseWriter, message string) {
	writeValidationProblem(w, "size", message)
}

// handlePlaybackBitrateTest serves GET /Playback/BitrateTest?Size={bytes} —
// the bandwidth probe every official client fires before starting a
// playback session, timing how long the download takes to estimate
// throughput. Found 404ing, not just unpolished, by testing a real
// jellyfin-web build via Playwright.
//
// Response bytes are random — confirmed not to matter to the real server
// either: two back-to-back requests with the same Size came back with
// different bytes. crypto/rand (same package server.go's randomID already
// uses) rather than a non-cryptographic generator: simpler than adding a
// second, separately-seeded RNG source to the project for a value nothing
// about this endpoint's purpose actually needs to be unpredictable, and
// the largest possible response (bitrateTestMaxSize, ~100MB before
// rounding) is small enough that crypto/rand's cost here is negligible
// next to actually writing that many bytes to the response over the
// network. Content-Type is deliberately "application/octet-stream" — a
// browser client parses this as an opaque byte count, not JSON, unlike
// almost every other route in this file.
func (s *Server) handlePlaybackBitrateTest(w http.ResponseWriter, r *http.Request) {
	size := bitrateTestDefaultSize
	if v := r.URL.Query().Get("Size"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeBitrateTestValidationError(w, fmt.Sprintf("The value '%s' is not valid.", v))
			return
		}
		size = n
	}
	if size < bitrateTestMinSize || size > bitrateTestMaxSize {
		writeBitrateTestValidationError(w, fmt.Sprintf(
			"The requested size must be greater than or equal to %d and less than or equal to %d",
			bitrateTestMinSize, bitrateTestMaxSize))
		return
	}

	out := make([]byte, bitrateTestRoundedSize(size))
	if _, err := rand.Read(out); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(out)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

// splitLanguages splits a language preference ("eng", "eng,por", "eng|por").
func splitLanguages(pref string) []string {
	return strings.FieldsFunc(pref, func(r rune) bool { return r == ',' || r == '|' || r == ' ' })
}

// applyStreamDefaults sets the default audio and subtitle stream of src as
// the caller's preferences (SubtitleMode, SubtitleLanguagePreference,
// AudioLanguagePreference) select them.
func (s *Server) applyStreamDefaults(ctx context.Context, src *mediaSourceInfoDto, streams []storage.MediaStreamRef) {
	if len(streams) == 0 {
		return
	}
	mode, subLangs, audioPref := "Default", []string(nil), ""
	if ac, ok := AuthFromContext(ctx); ok && ac != nil && !ac.IsAPIKey && ac.User.ID != "" {
		cfg := defaultUserConfiguration()
		if raw, err := s.store.GetUserConfiguration(ctx, ac.User.ID); err == nil && raw != "" {
			if json.Unmarshal([]byte(raw), &cfg) != nil {
				cfg = defaultUserConfiguration()
			}
			cfg.normalize()
		}
		mode = string(cfg.SubtitleMode)
		subLangs = splitLanguages(cfg.SubtitleLanguagePreference)
		if cfg.AudioLanguagePreference != nil {
			audioPref = *cfg.AudioLanguagePreference
		}
	}
	src.DefaultAudioStreamIndex = playback.DefaultAudioIndex(streams, audioPref)
	audioLang := ""
	if src.DefaultAudioStreamIndex != nil {
		for _, st := range streams {
			if st.Type == storage.StreamTypeAudio && st.Index == *src.DefaultAudioStreamIndex {
				audioLang = st.Language
			}
		}
	}
	src.DefaultSubtitleStreamIndex = playback.DefaultSubtitleIndex(streams, mode, subLangs, audioLang)
}

// subtitleDelivery is the situation a subtitle of item is delivered in.
func subtitleDelivery(item storage.Item, allowEmbed, allowHls bool) playback.Delivery {
	return playback.Delivery{
		AllowEmbed: allowEmbed,
		AllowHls:   allowHls,
		Container:  strings.ToLower(strings.TrimPrefix(filepath.Ext(item.Path), ".")),
	}
}

// subtitleDeliveryURL is the path of a subtitle stream served as format. The
// item is written in its dashed form, the media source id as it is.
func subtitleDeliveryURL(itemID, sourceID string, index int, format, apiKey string) string {
	u := fmt.Sprintf("/Videos/%s/%s/Subtitles/%d/0/Stream.%s", guid.Dashed(itemID), sourceID, index, format)
	if apiKey != "" {
		u += "?ApiKey=" + url.QueryEscape(apiKey)
	}
	return u
}

// applySubtitleDelivery sets DeliveryMethod/DeliveryUrl on the subtitle
// streams of src for the subtitle profiles of the caller's device profile.
func (s *Server) applySubtitleDelivery(r *http.Request, src *mediaSourceInfoDto, item storage.Item, profiles []deviceprofile.SubtitleProfile, served bool) {
	var token string
	if ac, ok := AuthFromContext(r.Context()); ok && ac != nil {
		token = ac.Token
	}
	byIndex := map[int]storage.MediaStreamRef{}
	for _, st := range item.MediaStreams {
		byIndex[st.Index] = st
	}
	// When the stream the caller's preferences select cannot be delivered
	// without transcoding (burned in or through the HLS manifest), the source
	// is transcoded and no embedded track can stay in the container.
	// An embedded track can stay in the container only when the file is
	// served as is (subtitles.delivery.R4).
	allowEmbed := served
	if src.DefaultSubtitleStreamIndex != nil {
		if def, ok := byIndex[*src.DefaultSubtitleStreamIndex]; ok {
			m := playback.DeliverSubtitle(def, profiles, subtitleDelivery(item, true, true)).Method
			allowEmbed = allowEmbed && m != "Encode" && m != "Hls"
		}
	}
	for i := range src.MediaStreams {
		d := &src.MediaStreams[i]
		if d.Type != string(storage.StreamTypeSubtitle) {
			continue
		}
		del := playback.DeliverSubtitle(byIndex[d.Index], profiles, subtitleDelivery(item, allowEmbed, !allowEmbed))
		d.DeliveryMethod = del.Method
		if del.Method == "External" {
			d.DeliveryURL = subtitleDeliveryURL(string(item.ID), src.ID, d.Index, del.Format, token)
			f := false
			d.IsExternalURL = &f
		}
	}
}

// forceRemoteTranscode turns the offer for a remote (.strm address) source
// into the progressive transcode the reference gives a user with
// ForceRemoteSourceTranscoding.
func (s *Server) forceRemoteTranscode(src *mediaSourceInfoDto, item storage.Item, token, deviceID string) {
	src.SupportsDirectPlay, src.SupportsDirectStream, src.SupportsTranscoding = false, false, true
	q := &orderedQuery{}
	if deviceID != "" {
		q.add("DeviceId", deviceID)
	}
	q.add("MediaSourceId", src.ID)
	q.add("Static", "true")
	var vc, ac, aidx string
	for _, st := range item.MediaStreams {
		switch {
		case st.Type == storage.StreamTypeVideo && vc == "":
			vc = strings.ToLower(st.Codec)
		case st.Type == storage.StreamTypeAudio && ac == "":
			ac, aidx = strings.ToLower(st.Codec), strconv.Itoa(st.Index)
		}
	}
	if vc != "" {
		q.add("VideoCodec", vc)
	}
	if ac != "" {
		q.add("AudioCodec", ac)
		q.add("AudioStreamIndex", aidx)
	}
	q.add("PlaySessionId", randomID())
	if token != "" {
		q.add("ApiKey", token)
	}
	q.add("Tag", offerTag(item))
	q.add("allowVideoStreamCopy", "false")
	q.add("allowAudioStreamCopy", "false")
	src.TranscodingUrl = "/videos/" + guid.Dashed(string(item.ID)) + "/stream.mp4?" + q.encode()
	src.TranscodingContainer = "mp4"
	src.TranscodingSubProtocol = "http"
}

// mp4FormatName is the name ffprobe gives the MP4 family of containers.
const mp4FormatName = "mov,mp4,m4a,3gp,3g2,mj2"

func isMP4Family(container string) bool {
	switch strings.ToLower(container) {
	case "mp4", "m4v", "mov", "3gp", "3g2":
		return true
	}
	return false
}

// extFormatSourceName is the Container of a non-MP4 video source that was not probed (a
// .strm address), named after the
// format of the probe rather than the file extension: Matroska files are "mkv",
// WebM files "mkv,webm" (or the one of the two a Video direct play entry lists),
// MPEG-TS and M2TS files "ts", ASF and WMV files "asf". Empty keeps the
// extension.
func extFormatSourceName(ext string, profile deviceprofile.DeviceProfile) string {
	switch strings.ToLower(ext) {
	case "mkv":
		return "mkv"
	case "webm":
		for _, dp := range profile.DirectPlayProfiles {
			if dp.Type != "" && !strings.EqualFold(dp.Type, "Video") {
				continue
			}
			for _, n := range splitCodecs(dp.Container) {
				if n == "mkv" || n == "webm" {
					return n
				}
			}
		}
		return "mkv,webm"
	case "ts", "m2ts":
		return "ts"
	case "wmv", "asf":
		return "asf"
	case "mpg", "mpeg":
		return "mpeg"
	case "ogv", "ogg":
		return "ogg"
	}
	return ""
}

// formatSourceName is the Container of a probed video source: its format name,
// or the one of its names that a Video direct play entry of the profile lists
// when the format has several.
func formatSourceName(format string, profile deviceprofile.DeviceProfile) string {
	names := splitCodecs(format)
	if len(names) < 2 {
		return format
	}
	for _, dp := range profile.DirectPlayProfiles {
		if dp.Type != "" && !strings.EqualFold(dp.Type, "Video") {
			continue
		}
		for _, n := range splitCodecs(dp.Container) {
			if inList(names, n) {
				return n
			}
		}
	}
	return format
}

// mp4SourceName is the Container of an MP4-family video source: the one of the
// format names that a Video direct play entry of the profile lists, else the
// whole format name (also when the request states no profile).
func mp4SourceName(container string, profile deviceprofile.DeviceProfile, inline bool) string {
	if inline {
		if n := remoteMP4Name(profile); n != "" {
			return n
		}
	}
	return mp4FormatName
}

// remoteMP4Name is the one of the format names of an MP4 address
// ("mov,mp4,m4a,3gp,3g2,mj2") that a direct play entry of the profile lists.
func remoteMP4Name(profile deviceprofile.DeviceProfile) string {
	names := []string{"mov", "mp4", "m4a", "3gp", "3g2", "mj2"}
	for _, dp := range profile.DirectPlayProfiles {
		if dp.Type != "" && !strings.EqualFold(dp.Type, "Video") {
			continue
		}
		for _, n := range splitCodecs(dp.Container) {
			if inList(names, n) {
				return n
			}
		}
	}
	return ""
}
