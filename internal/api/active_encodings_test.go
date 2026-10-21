package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/transcode"
)

func TestHLSJobKeyFollowsDeviceAndSession(t *testing.T) {
	q := func(s string) url.Values { v, _ := url.ParseQuery(s); return v }
	base := "mediasourceid=a&videocodec=h264"
	k0 := hlsJobKey("item", q(base))
	k1 := hlsJobKey("item", q(base+"&deviceid=d&playsessionid=p"))
	k2 := hlsJobKey("item", q(base+"&deviceid=d&playsessionid=p&videobitrate=1"))
	k3 := hlsJobKey("item", q(base+"&deviceid=d&playsessionid=P"))
	k4 := hlsJobKey("item", q(base+"&deviceid=e&playsessionid=p"))
	seen := map[string]bool{}
	for _, k := range []string{k0, k1, k2, k3, k4} {
		if seen[k] {
			t.Fatalf("two requests share the key %s", k)
		}
		seen[k] = true
	}
	if k1 != hlsJobKey("item", q(base+"&playsessionid=p&deviceid=d")) {
		t.Fatal("the order of the parameters changes the key")
	}
}

func TestKillEncodingsStopsTheSessionOnly(t *testing.T) {
	sh, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "ffmpeg.sh")
	if err := os.WriteFile(script, []byte("#!"+sh+"\nlast=\"${!#}\"\ntouch \"$last\"\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Server{transcode: transcode.New(transcode.Config{FFmpegPath: script, WorkDir: dir})}
	req := transcode.Request{InputPath: "/dev/null", VideoCodec: "copy", AudioCodec: "copy", Container: "ts"}
	for _, k := range []string{"job-a", "job-b", "job-c"} {
		if _, err := s.transcode.Start(context.Background(), k, req); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		for _, k := range []string{"job-a", "job-b", "job-c"} {
			s.transcode.Stop(k)
		}
	}()
	s.noteHLSEncoding("job-a", url.Values{"playsessionid": {"PsOne"}})
	s.noteHLSEncoding("job-b", url.Values{"playsessionid": {"psone"}})
	s.noteHLSEncoding("job-c", url.Values{"playsessionid": {"other"}})
	r := httptest.NewRequest("GET", "/Videos/x/stream?PlaySessionId=PSONE", nil)
	ctx, release := s.trackProgressiveEncoding(r)
	defer release()

	if n := s.killEncodings("psONE"); n != 3 {
		t.Fatalf("stopped %d encodes, want 3", n)
	}
	for k, want := range map[string]bool{"job-a": false, "job-b": false, "job-c": true} {
		if _, ok := s.transcode.Get(k); ok != want {
			t.Errorf("job %s present = %v, want %v", k, ok, want)
		}
	}
	if ctx.Err() == nil {
		t.Error("the progressive stream of the session is still running")
	}
	if n := s.killEncodings("psone"); n != 0 {
		t.Errorf("a second kill stopped %d encodes, want 0", n)
	}
}

func TestDeleteActiveEncodingsValidation(t *testing.T) {
	s := &Server{transcode: transcode.New(transcode.Config{})}
	for _, c := range []struct {
		query string
		want  int
	}{
		{"", http.StatusBadRequest},
		{"?DeviceId=d", http.StatusBadRequest},
		{"?PlaySessionId=p", http.StatusBadRequest},
		{"?DeviceId=%20&PlaySessionId=p", http.StatusBadRequest},
		{"?deviceid=d&playsessionid=p", http.StatusNoContent},
	} {
		w := httptest.NewRecorder()
		s.handleDeleteActiveEncodings(w, httptest.NewRequest("DELETE", "/Videos/ActiveEncodings"+c.query, nil))
		if w.Code != c.want {
			t.Errorf("%q: status %d, want %d", c.query, w.Code, c.want)
		}
	}
}
