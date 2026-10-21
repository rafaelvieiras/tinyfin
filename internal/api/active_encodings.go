package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// activeEncodings remembers which play session each running encode belongs
// to, so that DELETE /Videos/ActiveEncodings can stop them
// (playback.encodings.R1). Transcode jobs of HLS are named by their key,
// progressive streams by a number; the play session is compared without
// regard to case.
type activeEncodings struct {
	hls  map[string]string // job key -> play session (lower case)
	prog map[uint64]progressiveEncoding
	next uint64
}

type progressiveEncoding struct {
	session string
	cancel  context.CancelFunc
}

// noteHLSEncoding records the play session a job was started for.
func (s *Server) noteHLSEncoding(key string, q url.Values) {
	ps := strings.ToLower(strings.TrimSpace(q.Get("playsessionid")))
	if ps == "" {
		return
	}
	s.encMu.Lock()
	defer s.encMu.Unlock()
	if s.encodings.hls == nil {
		s.encodings.hls = map[string]string{}
	}
	s.encodings.hls[key] = ps
}

// trackProgressiveEncoding ties a progressive transcode to its play session:
// the returned context is canceled when the session is killed, release forgets
// the encode once the response is over. A request without a play session is
// returned as it is.
func (s *Server) trackProgressiveEncoding(r *http.Request) (ctx context.Context, release func()) {
	ps := strings.ToLower(strings.TrimSpace(caseInsensitiveQuery(r).Get("playsessionid")))
	if ps == "" {
		return r.Context(), func() {}
	}
	ctx, cancel := context.WithCancel(r.Context())
	s.encMu.Lock()
	if s.encodings.prog == nil {
		s.encodings.prog = map[uint64]progressiveEncoding{}
	}
	s.encodings.next++
	id := s.encodings.next
	s.encodings.prog[id] = progressiveEncoding{session: ps, cancel: cancel}
	s.encMu.Unlock()
	return ctx, func() {
		s.encMu.Lock()
		delete(s.encodings.prog, id)
		s.encMu.Unlock()
		cancel()
	}
}

// killEncodings stops every encode of the play session (whatever the device,
// item or caller) and returns how many there were.
func (s *Server) killEncodings(playSession string) int {
	ps := strings.ToLower(strings.TrimSpace(playSession))
	var keys []string
	var cancels []context.CancelFunc
	s.encMu.Lock()
	for k, v := range s.encodings.hls {
		if v == ps {
			keys = append(keys, k)
			delete(s.encodings.hls, k)
		}
	}
	for id, p := range s.encodings.prog {
		if p.session == ps {
			cancels = append(cancels, p.cancel)
			delete(s.encodings.prog, id)
		}
	}
	s.encMu.Unlock()
	s.hlsMu.Lock()
	for sk, k := range s.hlsSessions {
		for _, dead := range keys {
			if k == dead {
				delete(s.hlsSessions, sk)
			}
		}
	}
	s.hlsMu.Unlock()
	for _, k := range keys {
		s.transcode.Stop(k)
	}
	for _, c := range cancels {
		c()
	}
	return len(keys) + len(cancels)
}

// handleDeleteActiveEncodings is DELETE /Videos/ActiveEncodings
// (playback.encodings.R1): stops the encodes of a play session.
func (s *Server) handleDeleteActiveEncodings(w http.ResponseWriter, r *http.Request) {
	q := caseInsensitiveQuery(r)
	errs := map[string][]string{}
	if strings.TrimSpace(q.Get("deviceid")) == "" {
		errs["deviceId"] = []string{"The deviceId field is required."}
	}
	if strings.TrimSpace(q.Get("playsessionid")) == "" {
		errs["playSessionId"] = []string{"The playSessionId field is required."}
	}
	if len(errs) > 0 {
		writeValidationErrors(w, errs)
		return
	}
	s.killEncodings(q.Get("playsessionid"))
	w.WriteHeader(http.StatusNoContent)
}
