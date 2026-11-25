package api

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// TestAccessLogClientSuffix: the access log names the client that made the
// request, so a line like "GET /Items?... 200 1.6s" can be traced back to an
// app instead of a guess. Written after a real triage: a 130 MB library sweep
// repeated every 15 minutes on a production server and nothing in the log said
// which of the four clients on the network was doing it.
func TestAccessLogClientSuffix(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
		absent string
	}{
		{name: "no header", header: "", want: ""},
		{
			name:   "the Android TV client's own header",
			header: `MediaBrowser Client="Jellyfin Android TV", Device="TV da sala", DeviceId="deadbeef", Version="0.19.10", Token="abc"`,
			want:   ` client="Jellyfin Android TV" version="0.19.10" device="TV da sala"`,
		},
		{
			name:   "jellyfin-web's percent-encoded client",
			header: `MediaBrowser Client="Jellyfin%20Web", Device="Chrome", DeviceId="x", Version="13.0.0", Token="abc"`,
			want:   ` client="Jellyfin Web" version="13.0.0" device="Chrome"`,
		},
		{
			name:   "only a client name",
			header: `MediaBrowser Client="Tailscale", Token="abc"`,
			want:   ` client="Tailscale"`,
		},
		{
			name:   "only a version",
			header: `MediaBrowser Version="1.2.3", Token="abc"`,
			want:   ` version="1.2.3"`,
		},
		{
			name:   "another authorization scheme is not a client",
			header: `Bearer abc123`,
			want:   "",
		},
		{
			name:   "a scheme without a space is not a client",
			header: `MediaBrowserThing Client="x"`,
			want:   "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/Items", nil)
			if tc.header != "" {
				r.Header.Set("Authorization", tc.header)
			}
			if got := accessLogClient(r); got != tc.want {
				t.Errorf("accessLogClient = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestAccessLogClientCannotBreakTheLine: the client name is the client's own
// bytes, and a log line that a client can break in two is a log line a client
// can forge — every field is quoted and escaped, and neither part is allowed to
// grow without a bound.
func TestAccessLogClientCannotBreakTheLine(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/Items", nil)
	r.Header.Set("Authorization", "MediaBrowser Client=\"a\\\"\nb\tc\", Device=\"d\ne\", Version=\"1\"")
	got := accessLogClient(r)
	if strings.ContainsAny(got, "\n\r\t") {
		t.Fatalf("accessLogClient contains a raw control character: %q", got)
	}
	// Every value is a Go-quoted string, so unquoting the part after the
	// "=" has to succeed — that is what proves the escaping is real rather
	// than a value that merely happens not to break this line.
	_, quoted, ok := strings.Cut(got, "client=")
	if !ok {
		t.Fatalf("accessLogClient = %q, want a client field", got)
	}
	if _, err := strconv.Unquote(quoted); err != nil {
		t.Errorf("client value %s is not a quoted string: %v", quoted, err)
	}

	long := strings.Repeat("x", 300)
	r.Header.Set("Authorization", `MediaBrowser Client="`+long+`", Version="1"`)
	got = accessLogClient(r)
	if len(got) > 120 {
		t.Errorf("len(accessLogClient) = %d, want the value truncated", len(got))
	}
	if !strings.Contains(got, "...") {
		t.Errorf("accessLogClient = %q, want the truncation marked", got)
	}
}

// TestAccessLogLineNamesTheClient: the suffix reaches the real log line, with
// the status and the duration still where they were.
func TestAccessLogLineNamesTheClient(t *testing.T) {
	ts, _ := newTestServer(t)

	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/System/Info/Public", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", `MediaBrowser Client="Jellyfin Android TV", Device="TV da sala", DeviceId="deadbeef", Version="0.19.10", Token="abc"`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	line := buf.String()
	if !strings.Contains(line, "GET /System/Info/Public 200 ") {
		t.Fatalf("log line = %q, want the method, path and status", line)
	}
	if !strings.Contains(line, `client="Jellyfin Android TV" version="0.19.10" device="TV da sala"`) {
		t.Errorf("log line = %q, want the client, version and device", line)
	}
}
