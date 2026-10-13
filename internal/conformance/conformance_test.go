package conformance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestEvalPath(t *testing.T) {
	var doc any
	_ = json.Unmarshal([]byte(`{"a":{"b":[{"c":1},{"c":2}]},"s":"x","n":null}`), &doc)
	cases := []struct {
		path  string
		found bool
		want  string
	}{
		{"$.a.b[0].c", true, "1"},
		{"$.a.b[1].c", true, "2"},
		{"$.a.b[*].c", true, "[1,2]"},
		{"$.s", true, `"x"`},
		{"$.n", true, "null"},
		{"$.missing", false, ""},
		{"$.a.b[5]", false, ""},
		{"$.a.b[?c=2].c", true, "2"},
		{"$.a.b[?c=2]", true, `{"c":2}`},
		{"$.a.b[?c=9].c", false, ""},
		{"$.s[?c=2]", false, ""},
	}
	for _, c := range cases {
		v, found, err := evalPath(doc, c.path)
		if err != nil || found != c.found {
			t.Fatalf("%s: found=%v err=%v", c.path, found, err)
		}
		if found {
			b, _ := json.Marshal(v)
			if string(b) != c.want {
				t.Errorf("%s = %s, want %s", c.path, b, c.want)
			}
		}
	}
	if _, _, err := evalPath(doc, "a.b"); err == nil {
		t.Error("path without $ must be rejected")
	}
}

func TestRunScenarioAgainstStub(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/Users/AuthenticateByName", func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b["Pw"] != "right" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"AccessToken":"tok","User":{"Id":"u1","Name":"alice"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	scenarios := []Scenario{
		{ID: "ok", Steps: []Step{{Request: Request{Method: "POST", Path: "/Users/AuthenticateByName", Body: map[string]any{"Username": "alice", "Pw": "right"}},
			Expect:  Expect{Status: Ints{200}, JSON: []Assert{{Path: "$.User.Name", Op: "eq", Value: "alice"}, {Path: "$.AccessToken", Op: "type", Value: "string"}}},
			Capture: map[string]string{"tok": "$.AccessToken"}}}},
		{ID: "wrong", Steps: []Step{{Request: Request{Method: "POST", Path: "/Users/AuthenticateByName", Body: map[string]any{"Pw": "no"}}, Expect: Expect{Status: Ints{401}}}}},
		{ID: "mismatch", Steps: []Step{{Request: Request{Method: "POST", Path: "/Users/AuthenticateByName", Body: map[string]any{"Pw": "no"}}, Expect: Expect{Status: Ints{200}}}}},
		{ID: "blocked", Requires: []string{"user:ghost"}, Steps: nil},
		{ID: "var", Steps: []Step{{Request: Request{Method: "GET", Path: "/x/{{nope}}"}, Expect: Expect{Status: Ints{200}}}}},
		{ID: "div", Divergence: &Divergence{Reason: "r"}, Steps: nil},
	}
	c := NewClient(srv.URL)
	got := map[string]string{}
	for _, r := range c.Run(context.Background(), scenarios, true) {
		got[r.ID] = r.Status
	}
	want := map[string]string{"ok": StatusPass, "wrong": StatusPass, "mismatch": StatusFail, "blocked": StatusBlocked, "var": StatusBlocked, "div": StatusSkipped}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: %s, want %s", id, got[id], w)
		}
	}
}

func TestLintCatchesReferenceExpression(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.md")
	good := filepath.Join(dir, "good.md")
	_ = os.WriteFile(bad, []byte("See ItemsController in Jellyfin.Api/Controllers/ItemsController.cs line 120\n"), 0o644)
	_ = os.WriteFile(good, []byte("GET /Items returns a page of items. The Items array is empty when nothing matches.\n"), 0o644)
	v, err := LintDir(dir)
	if err != nil || len(v) < 3 {
		t.Fatalf("expected >=3 violations, got %d (%v)", len(v), err)
	}
	for _, s := range v {
		if filepath.Base(s[:len(bad)]) == "good.md" {
			t.Errorf("false positive: %s", s)
		}
	}
}

// TestContractIsClean fails the build if anything under docs/contract carries
// the reference server's own expression (clean-room rule, AGENTS.md).
func TestContractIsClean(t *testing.T) {
	dir := filepath.Join("..", "..", "docs", "contract")
	if _, err := os.Stat(dir); err != nil {
		t.Skip("no contract directory yet")
	}
	v, err := LintDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range v {
		t.Error(s)
	}
}

// TestScenariosLoad guards the scenario files against typos (unknown fields
// are rejected) and duplicate ids.
func TestScenariosLoad(t *testing.T) {
	dir := filepath.Join("..", "..", "docs", "contract", "scenarios")
	if _, err := os.Stat(dir); err != nil {
		t.Skip("no scenarios yet")
	}
	list, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range list {
		switch s.Scope {
		case ScopeModern, ScopeLegacyWebRequired, ScopeNegative:
		default:
			t.Errorf("%s: unknown scope %q", s.ID, s.Scope)
		}
		if s.Endpoint == "" || len(s.Steps) == 0 && s.Pending == "" {
			t.Errorf("%s: needs endpoint and steps", s.ID)
		}
	}
}

// TestRulesAndScenariosAgree enforces the two-way accounting from
// docs/contract/README.md: every spec rule is exercised by at least one
// scenario (unless its line is tagged [untestable]), and every scenario
// `covers` entry names a rule that exists.
func TestRulesAndScenariosAgree(t *testing.T) {
	specs, _ := filepath.Glob(filepath.Join("..", "..", "docs", "contract", "specs", "*.md"))
	if len(specs) == 0 {
		t.Skip("no specs yet")
	}
	ruleRE := regexp.MustCompile(`\*\*([a-z0-9-]+(?:\.[a-z0-9-]+)*\.R\d+)\*\*`)
	rules := map[string]bool{} // id -> needs a scenario
	for _, f := range specs {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "- **") {
				continue
			}
			if m := ruleRE.FindStringSubmatch(line); m != nil {
				rules[m[1]] = !strings.Contains(line, "[untestable")
			}
		}
	}
	list, err := LoadDir(filepath.Join("..", "..", "docs", "contract", "scenarios"))
	if err != nil {
		t.Fatal(err)
	}
	covered := map[string]bool{}
	for _, s := range list {
		for _, id := range s.Covers {
			covered[id] = true
			if _, ok := rules[id]; !ok {
				t.Errorf("%s covers unknown rule %q", s.ID, id)
			}
		}
	}
	for id, need := range rules {
		if need && !covered[id] {
			t.Errorf("rule %s has no scenario (add one or tag the rule line [untestable: reason])", id)
		}
	}
}
