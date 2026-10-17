package api

import (
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestHLSJobKey(t *testing.T) {
	bare := hlsJobKey("abc", url.Values{})
	if bare != "abc" {
		t.Fatalf("a bare URL keeps the item id, got %q", bare)
	}
	a := hlsJobKey("abc", url.Values{"audiostreamindex": {"1"}, "playsessionid": {"x"}})
	b := hlsJobKey("abc", url.Values{"audiostreamindex": {"2"}, "playsessionid": {"x"}})
	c := hlsJobKey("abc", url.Values{"audiostreamindex": {"1"}, "playsessionid": {"y"}, "apikey": {"k"}})
	if a == b || a == bare {
		t.Fatalf("another track must be another job: %q %q %q", a, b, bare)
	}
	if a != c {
		t.Fatalf("session and token do not change the job: %q vs %q", a, c)
	}
}

func TestHLSQueryFirstValueWins(t *testing.T) {
	r := httptest.NewRequest("GET", "/Videos/x/master.m3u8?VideoBitrate=1&videobitrate=2&AudioCodec=aac&AudioCodec=mp3&a%20b=c%2Fd", nil)
	q := hlsQuery(r)
	if q.Get("videobitrate") != "1" || q.Get("audiocodec") != "aac" || q.Get("a b") != "c/d" {
		t.Fatalf("got %v", q)
	}
}
