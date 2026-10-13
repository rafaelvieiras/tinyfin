package conformance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestClockTemplates(t *testing.T) {
	c := NewClient("http://unused")
	// Runner at 23:30:00 UTC, server zone UTC+3: local 02:30 the next day.
	c.Now = func() time.Time { return time.Date(2026, 10, 13, 23, 30, 0, 0, time.UTC) } // Tuesday
	c.clockCalibrated, c.clockOffset = true, 3*time.Hour
	for in, want := range map[string]string{
		"{{clock.hour}}":     "2.500000",
		"{{clock.hour+0.5}}": "3.000000",
		"{{clock.hour-3}}":   "-0.500000",
		"{{clock.day}}":      "Wednesday",
		"{{clock.day+1}}":    "Thursday",
		"{{clock.day-4}}":    "Saturday",
		"{{clock.daygroup}}": "Weekday", "{{clock.otherdaygroup}}": "Weekend",
	} {
		got, err := c.expand(in, nil)
		if err != nil || got != want {
			t.Errorf("%s = %q, %v; want %q", in, got, err, want)
		}
	}
	v, err := c.expandAny(map[string]any{"h": "{{clock.hour+1|num}}", "s": "x{{clock.day}}"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := v.(map[string]any)
	if m["h"] != 3.5 || m["s"] != "xWednesday" {
		t.Errorf("expandAny = %#v", m)
	}
	c.Vars["x"] = "0123456789abcdef0123456789abcdef"
	if got, _ := c.expand("{{x|dashed|upper}}", nil); got != "01234567-89AB-CDEF-0123-456789ABCDEF" {
		t.Errorf("dashed|upper = %q", got)
	}
	if _, err := c.expand("{{clock.week}}", nil); err == nil {
		t.Error("unknown clock template accepted")
	}
}

// A fake server in zone UTC-3 (local = UTC - 3h) that enforces the schedule
// of one account; calibration must recover the offset.
func TestClockCalibration(t *testing.T) {
	const zone = -3 * time.Hour
	var mu sync.Mutex
	var end float64
	deleted := false
	mux := http.NewServeMux()
	mux.HandleFunc("POST /Users/New", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"Id": "u1"})
	})
	mux.HandleFunc("GET /Users/u1", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"Policy": map[string]any{"AuthenticationProviderId": "a"}})
	})
	mux.HandleFunc("POST /Users/u1/Policy", func(w http.ResponseWriter, r *http.Request) {
		var p struct{ AccessSchedules []struct{ EndHour float64 } }
		if json.NewDecoder(r.Body).Decode(&p) != nil || len(p.AccessSchedules) != 1 {
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		end = p.AccessSchedules[0].EndHour
		mu.Unlock()
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /Users/AuthenticateByName", func(w http.ResponseWriter, r *http.Request) {
		l := time.Now().UTC().Add(zone)
		h := float64(l.Hour()) + float64(l.Minute())/60 + float64(l.Second())/3600
		mu.Lock()
		defer mu.Unlock()
		if h <= end {
			w.WriteHeader(200)
		} else {
			w.WriteHeader(403)
		}
	})
	mux.HandleFunc("DELETE /Users/u1", func(w http.ResponseWriter, r *http.Request) {
		deleted = true
		w.WriteHeader(204)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := NewClient(srv.URL)
	c.Tokens["admin"] = "t"
	off, err := c.calibrateClock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if off != zone {
		t.Errorf("offset = %v, want %v", off, zone)
	}
	if !deleted {
		t.Error("probe account not removed")
	}
	got, err := c.clockValue("hour")
	if err != nil || strings.TrimSpace(got) == "" {
		t.Fatalf("clockValue: %q %v", got, err)
	}
}
