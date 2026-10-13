package conformance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestRiskyScenariosRunLastAndLastAdminIsGuarded(t *testing.T) {
	admins := 2
	mux := http.NewServeMux()
	mux.HandleFunc("/Users", func(w http.ResponseWriter, r *http.Request) {
		var us []any
		for i := 0; i < admins; i++ {
			us = append(us, map[string]any{"Policy": map[string]any{"IsAdministrator": true}})
		}
		us = append(us, map[string]any{"Policy": map[string]any{"IsAdministrator": false}})
		_ = json.NewEncoder(w).Encode(us)
	})
	var mu sync.Mutex
	var order []string
	mux.HandleFunc("/mark/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		order = append(order, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(204)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := NewClient(srv.URL)
	c.Tokens["admin"] = "t"
	c.IncludeRisky = true
	mk := func(id string, tags ...string) Scenario {
		return Scenario{ID: id, Scope: ScopeModern, Tags: tags, Steps: []Step{{Request: Request{Path: "/mark/" + id}, Expect: Expect{Status: Ints{204}}}}}
	}
	res := c.Run(context.Background(), []Scenario{mk("risky1", "risky"), mk("plain1"), mk("lastadmin", "risky", "last-admin"), mk("plain2")}, false)
	var ids []string
	for _, r := range res {
		ids = append(ids, r.ID)
	}
	want := []string{"plain1", "plain2", "risky1", "lastadmin"}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("order %v, want %v", ids, want)
		}
	}
	if res[3].Status != StatusBlocked {
		t.Errorf("last-admin with 2 administrators: %+v, want blocked", res[3])
	}
	mu.Lock()
	for _, p := range order {
		if p == "/mark/lastadmin" {
			t.Error("a blocked last-admin scenario sent its requests")
		}
	}
	mu.Unlock()
	admins = 1
	if r := c.Run(context.Background(), []Scenario{mk("lastadmin", "risky", "last-admin")}, false)[0]; r.Status != StatusPass {
		t.Errorf("sole administrator: %+v", r)
	}
}

func TestServerFileStep(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "passwordreset"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "passwordreset", "p.json"), []byte(`{"Pin":"AA-BB"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c := NewClient("http://127.0.0.1:1")
	read := func(name string, exp int) Scenario {
		sc := Scenario{ID: "f", Steps: []Step{{
			ServerFile: &ServerFileStep{Read: name, Under: "passwordreset"},
			Expect:     Expect{Status: Ints{exp}},
		}}}
		if exp == 200 {
			sc.Steps[0].Capture = map[string]string{"pin": "$.Pin"}
		}
		return sc
	}
	// Not configured: blocked, never a failure.
	if r := runOne(t, c, read("p.json", 200)); r.Status != StatusBlocked {
		t.Errorf("unconfigured: %+v", r)
	}
	c.ServerDataDir = dir
	if r := runOne(t, c, read("p.json", 200)); r.Status != StatusPass {
		t.Errorf("bare name: %+v", r)
	}
	if r := runOne(t, c, read(filepath.Join(dir, "passwordreset", "p.json"), 200)); r.Status != StatusPass {
		t.Errorf("absolute path: %+v", r)
	}
	if r := runOne(t, c, read("gone.json", 404)); r.Status != StatusPass {
		t.Errorf("missing file: %+v", r)
	}
	for _, bad := range []string{"../x", "a/b", "-rf"} {
		if r := runOne(t, c, read(bad, 200)); r.Status != StatusFail {
			t.Errorf("%q must be refused: %+v", bad, r)
		}
	}
	// Reader command.
	c.ReadFileCmd = "cat"
	if r := runOne(t, c, read("p.json", 200)); r.Status != StatusPass {
		t.Errorf("reader command: %+v", r)
	}
	if r := runOne(t, c, read("gone.json", 404)); r.Status != StatusPass {
		t.Errorf("reader command, missing: %+v", r)
	}
}

func TestConfigSectionIsRestored(t *testing.T) {
	var mu sync.Mutex
	subnets := []any{}
	mux := http.NewServeMux()
	mux.HandleFunc("/System/Configuration/network", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodPost {
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			subnets, _ = b["LocalNetworkSubnets"].([]any)
			w.WriteHeader(204)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"LocalNetworkSubnets": subnets, "Keep": 1})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := NewClient(srv.URL)
	c.Tokens["admin"] = "t"
	r := runOne(t, c, Scenario{ID: "n", Steps: []Step{
		{Config: &ConfigStep{Section: "network", Set: map[string]any{"LocalNetworkSubnets": []any{"192.0.2.0/24"}}}},
		{Config: &ConfigStep{Section: "network", Set: map[string]any{"LocalNetworkSubnets": []any{"10.0.0.0/8"}}}},
		{Request: Request{Path: "/nope"}, Expect: Expect{Status: Ints{200}}},
	}})
	if r.Status != StatusFail {
		t.Fatalf("%+v", r)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(subnets) != 0 {
		t.Errorf("network section not restored to its original: %v", subnets)
	}
}

func TestProbeOrigin(t *testing.T) {
	for addr, want := range map[string]bool{"172.17.0.1": true, "::ffff:192.168.1.5": true, "127.0.0.1": false, "::1": false, "": false} {
		mux := http.NewServeMux()
		mux.HandleFunc("/System/ActivityLog/Entries", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"Items": []any{
				map[string]any{"ShortOverview": "IP address: " + addr}}})
		})
		srv := httptest.NewServer(mux)
		c := NewClient(srv.URL)
		c.Tokens["admin"] = "t"
		c.probeOrigin(context.Background())
		srv.Close()
		if c.Available["origin:non-loopback"] != want {
			t.Errorf("origin %q: non-loopback=%v, want %v", addr, c.Available["origin:non-loopback"], want)
		}
	}
}
