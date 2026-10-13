package api

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/guid"
)

// idQuerySkip lists query parameters whose value is a credential, a tag or
// a client-chosen token: never rewritten, even when it looks like a GUID.
var idQuerySkip = map[string]bool{
	"apikey": true, "api_key": true, "tag": true, "deviceid": true,
	"playsessionid": true, "pin": true, "secret": true, "code": true,
	"app": true,
}

// withCanonicalIDs folds every identifier in the request path and query to
// the canonical form (32 lowercase hex digits) before routing, so handlers
// and the store only ever see one spelling (wire.id.R2). A path segment, a
// query value or one comma-separated token of a query value is rewritten
// only when, whole, it is a GUID in the dashed form or in upper case.
// Identifiers inside request bodies are normalized by the handlers that
// read them (normalizeGUID, canonicalIDs).
func withCanonicalIDs(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, changed := canonicalPath(r.URL.Path); changed {
			r2 := new(http.Request)
			*r2 = *r
			u := *r.URL
			u.Path, u.RawPath = p, ""
			r2.URL = &u
			r = r2
		}
		if q, changed := canonicalQuery(r.URL.RawQuery); changed {
			r2 := new(http.Request)
			*r2 = *r
			u := *r.URL
			u.RawQuery = q
			r2.URL = &u
			r = r2
		}
		next.ServeHTTP(w, r)
	})
}

// foldID returns the canonical spelling of s and true when s is a GUID
// written in another spelling.
func foldID(s string) (string, bool) {
	if len(s) != 32 && len(s) != 36 {
		return s, false
	}
	n, ok := guid.Normalize(s)
	if !ok || n == s {
		return s, false
	}
	return n, true
}

func canonicalPath(p string) (string, bool) {
	if !strings.ContainsAny(p, "-ABCDEF") {
		return p, false
	}
	segs := strings.Split(p, "/")
	changed := false
	for i, s := range segs {
		// The key in /Auth/Keys/{key} is a credential, matched exactly
		// (users.keys.R6).
		if i >= 3 && strings.EqualFold(segs[i-1], "Keys") && strings.EqualFold(segs[i-2], "Auth") {
			continue
		}
		if n, ok := foldID(s); ok {
			segs[i], changed = n, true
		}
	}
	if !changed {
		return p, false
	}
	return strings.Join(segs, "/"), true
}

func canonicalQuery(raw string) (string, bool) {
	if raw == "" {
		return raw, false
	}
	q, err := url.ParseQuery(raw)
	if err != nil {
		return raw, false
	}
	changed := false
	for k, vals := range q {
		if idQuerySkip[strings.ToLower(k)] {
			continue
		}
		for i, v := range vals {
			toks := strings.Split(v, ",")
			tokChanged := false
			for j, t := range toks {
				if n, ok := foldID(strings.TrimSpace(t)); ok {
					toks[j], tokChanged = n, true
				}
			}
			if tokChanged {
				vals[i], changed = strings.Join(toks, ","), true
			}
		}
	}
	if !changed {
		return raw, false
	}
	return q.Encode(), true
}
