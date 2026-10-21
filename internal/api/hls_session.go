package api

import (
	"net/http"
	"net/url"
	"strings"
)

// noteTranscodeSession ties a transcode job that has just started to the
// session of the device that asked for it, with the TranscodingInfo the
// session shows from then on (playstate.session.R10). The device is the
// DeviceId of the request URL, else the one the request authenticated with.
func (s *Server) noteTranscodeSession(r *http.Request, h hlsRequest) {
	q := hlsQuery(r)
	dev := q.Get("deviceid")
	if dev == "" {
		if ac, ok := AuthFromContext(r.Context()); ok && ac != nil {
			dev = ac.DeviceID
		}
	}
	s.sessions.setTranscode(dev, h.key, h.item.ID, q.Get("playsessionid"), transcodingInfoFor(h, q))
}

// transcodingInfoFor describes the stream a job serves, in the terms of its
// master playlist variant: the codecs (the source's when copied), the picture
// size after the width limit, the variant bandwidth as the bit rate and the
// channel count of the sound.
func transcodingInfoFor(h hlsRequest, q url.Values) *transcodingInfoDto {
	audioIdx := atoiPtr(q.Get("audiostreamindex"))
	m := describeHLS(h.item, q, h.decision.VideoCodec, h.decision.AudioCodec, audioIdx)
	info := &transcodingInfoDto{
		AudioCodec:               m.acodec,
		VideoCodec:               m.vcodec,
		Container:                h.decision.Container,
		IsVideoDirect:            m.videoCopy,
		IsAudioDirect:            m.audioCopy,
		Bitrate:                  m.bandwidth,
		Width:                    m.width,
		Height:                   m.height,
		HardwareAccelerationType: "none",
		TranscodeReasons:         []string{},
	}
	if m.audio != nil {
		switch {
		case m.audioCopy && m.audio.Channels != nil:
			info.AudioChannels = *m.audio.Channels
		case !m.audioCopy:
			info.AudioChannels, _, _ = audioPlan(m.audio, q)
		}
	}
	// the segment container the URL names is the one reported
	if c := strings.ToLower(strings.TrimSpace(q.Get("segmentcontainer"))); c != "" {
		info.Container = c
	}
	if info.AudioCodec == "" && h.req.AudioCodec != "" && h.req.AudioCodec != "copy" {
		info.AudioCodec = h.req.AudioCodec
	}
	for _, r := range strings.Split(q.Get("transcodereasons"), ",") {
		if r = strings.TrimSpace(r); r != "" {
			info.TranscodeReasons = append(info.TranscodeReasons, r)
		}
	}
	return info
}
