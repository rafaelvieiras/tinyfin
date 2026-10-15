package api

import (
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
