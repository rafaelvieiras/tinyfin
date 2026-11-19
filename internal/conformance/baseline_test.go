package conformance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCoverageFixture lays out a scenario directory and a baseline file, so
// the pair can be compared without the real docs tree.
func writeCoverageFixture(t *testing.T, scenarioIDs, baselineIDs []string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	scenDir := filepath.Join(dir, "scenarios")
	if err := os.MkdirAll(scenDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	list := make([]map[string]any, 0, len(scenarioIDs))
	for _, id := range scenarioIDs {
		list = append(list, map[string]any{"id": id, "steps": []any{}})
	}
	raw, err := json.Marshal(list)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scenDir, "a.json"), raw, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	b := Baseline{Version: 1, Counts: map[string]int{}, Scenarios: map[string]string{}}
	for _, id := range baselineIDs {
		b.Scenarios[id] = StatusPass
		b.Counts[StatusPass]++
	}
	out, err := b.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	basePath := filepath.Join(dir, "baseline.json")
	if err := os.WriteFile(basePath, out, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return scenDir, basePath
}

// The two directions the check exists for, and the case where it says nothing.
func TestCheckBaselineCoversTheSuite(t *testing.T) {
	cases := []struct {
		name     string
		suite    []string
		baseline []string
		missing  []string
		stale    []string
	}{
		{
			name:     "a baseline that matches the suite",
			suite:    []string{"a.one", "b.two"},
			baseline: []string{"a.one", "b.two"},
		},
		{
			name:     "a scenario with no entry is a blind spot",
			suite:    []string{"a.one", "b.two", "c.three"},
			baseline: []string{"a.one", "b.two"},
			missing:  []string{"c.three"},
		},
		{
			name:     "an entry with no scenario is stale",
			suite:    []string{"a.one"},
			baseline: []string{"a.one", "z.gone"},
			stale:    []string{"z.gone"},
		},
		{
			name:     "both at once, sorted",
			suite:    []string{"m.new2", "m.new1"},
			baseline: []string{"z.stale2", "a.stale1"},
			missing:  []string{"m.new1", "m.new2"},
			stale:    []string{"a.stale1", "z.stale2"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			scenDir, basePath := writeCoverageFixture(t, c.suite, c.baseline)
			cov, err := CheckBaselineCoversTheSuite(scenDir, basePath)
			if err != nil {
				t.Fatalf("CheckBaselineCoversTheSuite: %v", err)
			}
			if got, want := strings.Join(cov.Missing, ","), strings.Join(c.missing, ","); got != want {
				t.Errorf("missing = %q, want %q", got, want)
			}
			if got, want := strings.Join(cov.Stale, ","), strings.Join(c.stale, ","); got != want {
				t.Errorf("stale = %q, want %q", got, want)
			}
			if c.missing == nil && c.stale == nil && !cov.Empty() {
				t.Errorf("Empty() = false, want true for %v/%v", cov.Missing, cov.Stale)
			}
		})
	}
}

// Fresh-server scenarios are outside the ratchet by design: no baselined run
// selects them, so they cannot have an entry, and an entry someone once wrote
// for one is not stale either.
func TestCheckBaselineExemptsFreshServerScenarios(t *testing.T) {
	dir := t.TempDir()
	scenDir := filepath.Join(dir, "scenarios")
	if err := os.MkdirAll(scenDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	body := `[
	  {"id": "a.one", "steps": []},
	  {"id": "wizard.setup", "tags": ["fresh-server"], "steps": []}
	]`
	if err := os.WriteFile(filepath.Join(scenDir, "a.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	basePath := filepath.Join(dir, "baseline.json")
	write := func(b Baseline) {
		t.Helper()
		out, err := b.Marshal()
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		if err := os.WriteFile(basePath, out, 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	b := Baseline{Version: 1, Counts: map[string]int{StatusPass: 1}, Scenarios: map[string]string{"a.one": StatusPass}}
	write(b)

	cov, err := CheckBaselineCoversTheSuite(scenDir, basePath)
	if err != nil {
		t.Fatalf("CheckBaselineCoversTheSuite: %v", err)
	}
	if !cov.Empty() {
		t.Fatalf("missing = %v, stale = %v; want neither", cov.Missing, cov.Stale)
	}

	b.Scenarios["wizard.setup"] = StatusPass
	write(b)
	cov, err = CheckBaselineCoversTheSuite(scenDir, basePath)
	if err != nil {
		t.Fatalf("CheckBaselineCoversTheSuite: %v", err)
	}
	if !cov.Empty() {
		t.Fatalf("with an entry for the fresh-server scenario: missing = %v, stale = %v; want neither", cov.Missing, cov.Stale)
	}
}

// A baseline that is not a baseline is an error, not an empty coverage: a
// wrong -baseline path must not read as "nothing is missing".
func TestCheckBaselineRejectsAForeignFile(t *testing.T) {
	dir := t.TempDir()
	scenDir := filepath.Join(dir, "scenarios")
	if err := os.MkdirAll(scenDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scenDir, "a.json"), []byte(`[{"id":"a.one","steps":[]}]`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	basePath := filepath.Join(dir, "baseline.json")
	if err := os.WriteFile(basePath, []byte(`{"scenarios":{}}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := CheckBaselineCoversTheSuite(scenDir, basePath); err == nil {
		t.Fatal("CheckBaselineCoversTheSuite accepted a document that is not a version 1 baseline")
	}
}

// The real tree must pass, and the failure has to say what to do: a scenario
// added without a baseline entry would otherwise be untested by the ratchet
// for as long as nobody looks. This is the same check `conformance lint` runs
// in CI, kept here so a contributor who only runs `go test ./...` still sees
// it.
func TestRepoBaselineCoversTheSuite(t *testing.T) {
	baselinePath := filepath.Join("..", "..", "docs", "contract", "baseline.json")
	cov, err := CheckBaselineCoversTheSuite(filepath.Join("..", "..", "docs", "contract", "scenarios"), baselinePath)
	if err != nil {
		t.Fatalf("CheckBaselineCoversTheSuite: %v", err)
	}
	if cov.Empty() {
		return
	}
	var msg []string
	for _, id := range cov.Missing {
		msg = append(msg, fmt.Sprintf("%s: scenario has no baseline entry, so Compare reports it NEW and its failure does not gate", id))
	}
	for _, id := range cov.Stale {
		msg = append(msg, fmt.Sprintf("%s: baseline entry names no scenario in docs/contract/scenarios", id))
	}
	t.Fatalf("%s\n\nrefresh %s from a full-suite run (a shard, -only or -reference run cannot write a whole baseline); see \"Baseline and ratchet\" in docs/contract/README.md",
		strings.Join(msg, "\n"), baselinePath)
}
