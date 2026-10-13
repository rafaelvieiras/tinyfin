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

	"github.com/coder/websocket"
)

// toolingStub is a tiny fake server exercising the runner capabilities
// (API-key identity, WebSocket steps, configuration toggle, problem bodies).
type toolingStub struct {
	mu       sync.Mutex
	keyApp   string
	quick    bool
	wsTokens []string
	sockets  chan *websocket.Conn
	keyAuth  string
}

func newToolingStub() (*toolingStub, *httptest.Server) {
	st := &toolingStub{quick: true, sockets: make(chan *websocket.Conn, 4)}
	mux := http.NewServeMux()
	mux.HandleFunc("/Auth/Keys", func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		defer st.mu.Unlock()
		if r.Method == http.MethodPost {
			st.keyApp = r.URL.Query().Get("app")
			w.WriteHeader(204)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Items": []any{
			map[string]any{"AccessToken": "other", "AppName": "something-else"},
			map[string]any{"AccessToken": "key-token", "AppName": st.keyApp},
		}})
	})
	mux.HandleFunc("/Auth/Keys/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("/whoami", func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		st.keyAuth = r.Header.Get("Authorization")
		st.mu.Unlock()
		_, _ = w.Write([]byte(`{}`))
	})
	mux.HandleFunc("/System/Configuration", func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		defer st.mu.Unlock()
		if r.Method == http.MethodPost {
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			st.quick, _ = b["QuickConnectAvailable"].(bool)
			w.WriteHeader(204)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"QuickConnectAvailable": st.quick, "Other": 1})
	})
	mux.HandleFunc("/quick", func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		defer st.mu.Unlock()
		_, _ = w.Write([]byte(`{"v":` + map[bool]string{true: "true", false: "false"}[st.quick] + `}`))
	})
	mux.HandleFunc("/socket", func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		st.wsTokens = append(st.wsTokens, r.URL.Query().Get("ApiKey"))
		st.mu.Unlock()
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		st.sockets <- c
		<-r.Context().Done()
	})
	mux.HandleFunc("/push", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond) // let the newest socket register
		var c *websocket.Conn
		for drained := false; !drained; {
			select {
			case c = <-st.sockets:
			default:
				drained = true
			}
		}
		if c != nil {
			_ = c.Write(r.Context(), websocket.MessageText, []byte(`{"MessageType":"KeepAlive"}`))
			_ = c.Write(r.Context(), websocket.MessageText, []byte(`{"MessageType":"UserUpdated","Data":{"Name":"renamed"}}`))
			st.sockets <- c
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("/problem", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"type":"https://example.test/404","title":"Not Found","status":404}`))
	})
	mux.HandleFunc("/bare", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`"not found"`))
	})
	mux.HandleFunc("/ids", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Good":"0123456789abcdef0123456789abcdef","Dashed":"01234567-89ab-cdef-0123-456789abcdef","List":["0123456789abcdef0123456789abcdef"],"Upper":"0123456789ABCDEF0123456789ABCDEF"}`))
	})
	return st, httptest.NewServer(mux)
}

func runOne(t *testing.T, c *Client, s Scenario) Result {
	t.Helper()
	if s.Scope == "" {
		s.Scope = ScopeModern
	}
	return c.Run(context.Background(), []Scenario{s}, false)[0]
}

func TestAPIKeyIdentity(t *testing.T) {
	st, srv := newToolingStub()
	defer srv.Close()
	c := NewClient(srv.URL)
	c.Tokens["admin"] = "adm"
	if err := c.SetupAPIKey(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.Tokens["apikey"] != "key-token" || !c.Available["identity:apikey"] {
		t.Fatalf("api key not wired: %v %v", c.Tokens, c.Available)
	}
	if !strings.HasPrefix(st.keyApp, "conformance-") {
		t.Errorf("key app name %q must be run-specific", st.keyApp)
	}
	r := runOne(t, c, Scenario{ID: "k", Requires: []string{"identity:apikey"}, Steps: []Step{
		{As: "apikey", Request: Request{Path: "/whoami"}, Expect: Expect{Status: Ints{200}}}}})
	if r.Status != StatusPass {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(st.keyAuth, `Token="key-token"`) {
		t.Errorf("authorization header %q lacks the key", st.keyAuth)
	}
	if err := c.Teardown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDeviceSuffix(t *testing.T) {
	st, srv := newToolingStub()
	defer srv.Close()
	c := NewClient(srv.URL)
	c.DeviceSuffix = "-r1"
	c.Tokens["admin"] = "t"
	r := runOne(t, c, Scenario{ID: "d", Steps: []Step{{As: "admin", Request: Request{Path: "/whoami"}, Expect: Expect{Status: Ints{200}}}}})
	if r.Status != StatusPass || !strings.Contains(st.keyAuth, `DeviceId="conf-admin-r1"`) {
		t.Fatalf("%+v %q", r, st.keyAuth)
	}
	if got, _ := c.expand("{{run.suffix}}", nil); got != "-r1" {
		t.Errorf("run.suffix = %q", got)
	}
}

func TestWebSocketSteps(t *testing.T) {
	st, srv := newToolingStub()
	defer srv.Close()
	c := NewClient(srv.URL)
	c.Tokens["admin"] = "adm-token"
	step := func(ws WSStep, e Expect) Step { return Step{WS: &ws, Expect: e} }
	push := Step{Request: Request{Path: "/push"}, Expect: Expect{Status: Ints{204}}}
	ok := runOne(t, c, Scenario{ID: "ws", Steps: []Step{
		step(WSStep{Open: "a", As: "admin"}, Expect{}),
		push,
		{WS: &WSStep{Wait: "a", Type: "UserUpdated"}, Expect: Expect{JSON: []Assert{{Path: "$.Data.Name", Op: "eq", Value: "renamed"}}},
			Capture: map[string]string{"n": "$.Data.Name"}},
		{Request: Request{Path: "/ids", Query: map[string]string{"x": "{{capture.n}}"}}, Expect: Expect{Status: Ints{200}}},
		step(WSStep{Wait: "a", Type: "UserUpdated", Absent: true, TimeoutMs: 150}, Expect{}),
	}})
	if ok.Status != StatusPass {
		t.Fatalf("%+v", ok)
	}
	if len(st.wsTokens) != 1 || st.wsTokens[0] != "adm-token" {
		t.Errorf("socket must authenticate with ApiKey=<token>: %v", st.wsTokens)
	}
	// a message that never arrives fails the wait
	miss := runOne(t, c, Scenario{ID: "ws2", Steps: []Step{
		step(WSStep{Open: "a", As: "admin"}, Expect{}),
		step(WSStep{Wait: "a", Type: "Nope", TimeoutMs: 100}, Expect{}),
	}})
	if miss.Status != StatusFail {
		t.Fatalf("want fail, got %+v", miss)
	}
	// absent fails when the message does arrive
	present := runOne(t, c, Scenario{ID: "ws3", Steps: []Step{
		step(WSStep{Open: "a", As: "admin"}, Expect{}),
		push,
		step(WSStep{Wait: "a", Type: "UserUpdated", Absent: true, TimeoutMs: 500}, Expect{}),
	}})
	if present.Status != StatusFail {
		t.Fatalf("want fail, got %+v", present)
	}
	// a refused handshake can be asserted; an accepted one fails the step
	refused := runOne(t, c, Scenario{ID: "ws5", Steps: []Step{
		step(WSStep{Open: "x", As: "admin", RefuseStatus: 403, Param: "api_key"}, Expect{})}})
	if refused.Status != StatusFail {
		t.Fatalf("stub accepts every handshake, so a refusal expectation must fail: %+v", refused)
	}
	noTok := runOne(t, c, Scenario{ID: "ws4", Steps: []Step{step(WSStep{Open: "a", As: "ghost"}, Expect{})}})
	if noTok.Status != StatusBlocked {
		t.Fatalf("want blocked, got %+v", noTok)
	}
}

func TestSleepStep(t *testing.T) {
	_, srv := newToolingStub()
	defer srv.Close()
	c := NewClient(srv.URL)
	start := time.Now()
	r := runOne(t, c, Scenario{ID: "s", Steps: []Step{{SleepMs: 120}, {Request: Request{Path: "/whoami"}, Expect: Expect{Status: Ints{200}}}}})
	if r.Status != StatusPass || time.Since(start) < 120*time.Millisecond {
		t.Fatalf("%+v after %v", r, time.Since(start))
	}
}

func TestConfigToggleIsRestored(t *testing.T) {
	st, srv := newToolingStub()
	defer srv.Close()
	c := NewClient(srv.URL)
	c.Tokens["admin"] = "t"
	r := runOne(t, c, Scenario{ID: "cfg", Steps: []Step{
		{Config: &ConfigStep{Set: map[string]any{"QuickConnectAvailable": false}}},
		{Request: Request{Path: "/quick"}, Expect: Expect{Status: Ints{200}, JSON: []Assert{{Path: "$.v", Op: "eq", Value: false}}}},
		{Request: Request{Path: "/bare"}, Expect: Expect{Status: Ints{200}}}, // fails: restore must still happen
	}})
	if r.Status != StatusFail {
		t.Fatalf("%+v", r)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.quick {
		t.Error("configuration was not restored after a failing scenario")
	}
}

func TestProblemAndHex32(t *testing.T) {
	_, srv := newToolingStub()
	defer srv.Close()
	c := NewClient(srv.URL)
	good := runOne(t, c, Scenario{ID: "p", Steps: []Step{
		{Request: Request{Path: "/problem"}, Expect: Expect{Status: Ints{404}, Problem: 404}},
		{Request: Request{Path: "/ids"}, Expect: Expect{JSON: []Assert{
			{Path: "$.Good", Op: "hex32"}, {Path: "$.List[*]", Op: "hex32"}}}},
	}})
	if good.Status != StatusPass {
		t.Fatalf("%+v", good)
	}
	for name, st := range map[string]Step{
		"bare":   {Request: Request{Path: "/bare"}, Expect: Expect{Problem: 404}},
		"status": {Request: Request{Path: "/problem"}, Expect: Expect{Problem: 400}},
		"dashed": {Request: Request{Path: "/ids"}, Expect: Expect{JSON: []Assert{{Path: "$.Dashed", Op: "hex32"}}}},
		"upper":  {Request: Request{Path: "/ids"}, Expect: Expect{JSON: []Assert{{Path: "$.Upper", Op: "hex32"}}}},
		"empty":  {Request: Request{Path: "/ids"}, Expect: Expect{JSON: []Assert{{Path: "$.Missing[*]", Op: "hex32"}}}},
	} {
		if r := runOne(t, c, Scenario{ID: name, Steps: []Step{st}}); r.Status != StatusFail {
			t.Errorf("%s: want fail, got %+v", name, r)
		}
	}
}

func TestAllMatch(t *testing.T) {
	_, srv := newToolingStub()
	defer srv.Close()
	c := NewClient(srv.URL)
	re := "^[0-9a-f]{32}$"
	good := runOne(t, c, Scenario{ID: "m", Steps: []Step{
		{Request: Request{Path: "/ids"}, Expect: Expect{JSON: []Assert{
			{Path: "$.Good", Op: "allmatch", Value: re}, {Path: "$.List[*]", Op: "allmatch", Value: re}}}},
	}})
	if good.Status != StatusPass {
		t.Fatalf("%+v", good)
	}
	for name, a := range map[string]Assert{
		"single":  {Path: "$.Dashed", Op: "allmatch", Value: re},
		"missing": {Path: "$.Missing", Op: "allmatch", Value: re},
		"empty":   {Path: "$.Missing[*]", Op: "allmatch", Value: re},
		"notstr":  {Path: "$", Op: "allmatch", Value: re},
	} {
		st := Step{Request: Request{Path: "/ids"}, Expect: Expect{JSON: []Assert{a}}}
		if r := runOne(t, c, Scenario{ID: name, Steps: []Step{st}}); r.Status != StatusFail {
			t.Errorf("%s: want fail, got %+v", name, r)
		}
	}
}

func TestRiskyIsOptIn(t *testing.T) {
	_, srv := newToolingStub()
	defer srv.Close()
	s := Scenario{ID: "r", Tags: []string{"risky"}, Steps: []Step{{Request: Request{Path: "/whoami"}, Expect: Expect{Status: Ints{200}}}}}
	c := NewClient(srv.URL)
	if r := runOne(t, c, s); r.Status != StatusSkipped {
		t.Fatalf("risky must be skipped by default: %+v", r)
	}
	c.IncludeRisky = true
	if r := runOne(t, c, s); r.Status != StatusPass {
		t.Fatalf("%+v", r)
	}
}

func TestBaselineCompare(t *testing.T) {
	base := NewBaseline([]Result{
		{ID: "a", Status: StatusPass}, {ID: "b", Status: StatusPass}, {ID: "c", Status: StatusFail},
		{ID: "d", Status: StatusBlocked}, {ID: "gone", Status: StatusPass},
	})
	cmp := base.Compare([]Result{
		{ID: "a", Status: StatusPass},
		{ID: "b", Status: StatusFail}, // regression
		{ID: "c", Status: StatusPass}, // improvement
		{ID: "d", Status: StatusBlocked},
		{ID: "n", Status: StatusPass}, // new scenario
	})
	if len(cmp.Regressions) != 1 || cmp.Regressions[0].ID != "b" {
		t.Errorf("regressions: %+v", cmp.Regressions)
	}
	if len(cmp.Improvements) != 1 || cmp.Improvements[0] != "c" {
		t.Errorf("improvements: %v", cmp.Improvements)
	}
	if len(cmp.New) != 1 || cmp.New[0] != "n" {
		t.Errorf("new: %v", cmp.New)
	}
	if len(cmp.Missing) != 1 || cmp.Missing[0] != "gone" {
		t.Errorf("missing: %v", cmp.Missing)
	}
	// round trip is deterministic
	b1, _ := base.Marshal()
	back, err := ParseBaseline(b1)
	if err != nil {
		t.Fatal(err)
	}
	b2, _ := back.Marshal()
	if string(b1) != string(b2) {
		t.Error("baseline serialization is not stable")
	}
	if tbl := base.ProgressTable(); !strings.Contains(tbl, "| **Total** | 5 | 3 | 1 | 1 | 0 |") {
		t.Errorf("progress table: %s", tbl)
	}
}

func TestLazyFixtureIsMaterializedAroundScenario(t *testing.T) {
	var mu sync.Mutex
	libs := map[string]bool{}
	mux := http.NewServeMux()
	mux.HandleFunc("/Users/AuthenticateByName", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"AccessToken":"adm","User":{"Id":"u1"}}`))
	})
	mux.HandleFunc("/Users", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	mux.HandleFunc("/Auth/Keys", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) })
	mux.HandleFunc("/Library/Refresh", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("/Library/VirtualFolders", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		name := r.URL.Query().Get("name")
		switch r.Method {
		case http.MethodPost:
			libs[name] = true
			w.WriteHeader(204)
		case http.MethodDelete:
			delete(libs, name)
			w.WriteHeader(204)
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	})
	mux.HandleFunc("/Items", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if libs["Long"] {
			_, _ = w.Write([]byte(`{"Items":[{"Id":"abc","Name":"Long Movie","Path":"/m/Long/x.mp4"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"Items":[]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient(srv.URL)
	fx := Fixtures{
		Libraries: []FixtureLibrary{{Key: "long", Name: "Long", Type: "movies", Path: "Long", Lazy: true}},
		Items:     []FixtureItem{{Key: "long", Type: "Movie", Library: "long", Path: "Long/x.mp4"}},
	}
	if _, err := c.Setup(context.Background(), "admin", "pw", "/m", fx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	created := len(libs)
	mu.Unlock()
	if created != 0 || c.Available["item:long"] {
		t.Fatalf("lazy library must not exist after setup (libs=%d avail=%v)", created, c.Available["item:long"])
	}
	probe := Scenario{ID: "l", Requires: []string{"item:long"}, Steps: []Step{
		{As: "admin", Request: Request{Path: "/Items", Query: map[string]string{"x": "{{item.long.id}}"}}, Expect: Expect{Status: Ints{200}}}}}
	if r := runOne(t, c, probe); r.Status != StatusPass {
		t.Fatalf("%+v", r)
	}
	mu.Lock()
	left := len(libs)
	mu.Unlock()
	if left != 0 || c.Available["item:long"] {
		t.Errorf("lazy library must be removed after the scenario (libs=%d)", left)
	}
}

func TestDashedTemplateModifier(t *testing.T) {
	c := NewClient("http://x")
	c.Vars["user.a.id"] = "0123456789abcdef0123456789abcdef"
	got, err := c.expand("{{user.a.id|dashed}} {{user.a.id}}", nil)
	if err != nil || got != "01234567-89ab-cdef-0123-456789abcdef 0123456789abcdef0123456789abcdef" {
		t.Fatalf("%q %v", got, err)
	}
}
