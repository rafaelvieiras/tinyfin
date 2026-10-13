package conformance

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Baseline is the committed per-scenario status of a Tinyfin run (the ratchet
// of docs/contract/README.md). A scenario that passes in the baseline must not
// stop passing; improvements are reported so the baseline can be refreshed.
type Baseline struct {
	Version int `json:"version"`
	// Reference records the reference-server run the scenarios were validated
	// against (counts only; that run is manual).
	Reference *BaselineReference `json:"reference,omitempty"`
	Counts    map[string]int     `json:"counts"`
	Scenarios map[string]string  `json:"scenarios"`
}

// BaselineReference describes the manual reference validation.
type BaselineReference struct {
	Image  string         `json:"image"`
	Counts map[string]int `json:"counts"`
}

// Regression is a baseline-passing scenario that no longer passes.
type Regression struct {
	ID      string
	Now     string
	Message string
}

// Comparison is the result of checking a run against a baseline.
type Comparison struct {
	Regressions  []Regression
	Improvements []string // not passing in the baseline, passing now
	New          []string // not in the baseline
	Missing      []string // in the baseline, not part of this run
}

// NewBaseline builds a baseline from a run.
func NewBaseline(res []Result) Baseline {
	b := Baseline{Version: 1, Counts: map[string]int{}, Scenarios: map[string]string{}}
	for _, r := range res {
		b.Scenarios[r.ID] = r.Status
		b.Counts[r.Status]++
	}
	return b
}

// Marshal renders the baseline deterministically (sorted keys, trailing
// newline) so refreshes produce minimal diffs.
func (b Baseline) Marshal() ([]byte, error) {
	out, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// ParseBaseline decodes a baseline file.
func ParseBaseline(data []byte) (Baseline, error) {
	var b Baseline
	if err := json.Unmarshal(data, &b); err != nil {
		return b, err
	}
	if b.Version != 1 || b.Scenarios == nil {
		return b, fmt.Errorf("not a version 1 baseline")
	}
	return b, nil
}

// Compare checks a run against the baseline.
func (b Baseline) Compare(res []Result) Comparison {
	var c Comparison
	seen := map[string]bool{}
	for _, r := range res {
		seen[r.ID] = true
		was, ok := b.Scenarios[r.ID]
		switch {
		case !ok:
			c.New = append(c.New, r.ID)
		case was == StatusPass && r.Status != StatusPass:
			c.Regressions = append(c.Regressions, Regression{ID: r.ID, Now: r.Status, Message: r.Message})
		case was != StatusPass && r.Status == StatusPass:
			c.Improvements = append(c.Improvements, r.ID)
		}
	}
	for id := range b.Scenarios {
		if !seen[id] {
			c.Missing = append(c.Missing, id)
		}
	}
	sort.Strings(c.Missing)
	sort.Strings(c.New)
	sort.Strings(c.Improvements)
	sort.Slice(c.Regressions, func(i, j int) bool { return c.Regressions[i].ID < c.Regressions[j].ID })
	return c
}

// ProgressTable renders a markdown table of statuses per area (the first
// dot-separated segment of the scenario id).
func (b Baseline) ProgressTable() string {
	type row struct{ pass, fail, blocked, skipped, total int }
	areas := map[string]*row{}
	var total row
	for id, st := range b.Scenarios {
		area, _, _ := strings.Cut(id, ".")
		r := areas[area]
		if r == nil {
			r = &row{}
			areas[area] = r
		}
		for _, x := range []*row{r, &total} {
			x.total++
			switch st {
			case StatusPass:
				x.pass++
			case StatusFail:
				x.fail++
			case StatusBlocked:
				x.blocked++
			default:
				x.skipped++
			}
		}
	}
	names := make([]string, 0, len(areas))
	for a := range areas {
		names = append(names, a)
	}
	sort.Strings(names)
	var sb strings.Builder
	sb.WriteString("| Area | Scenarios | Pass | Fail | Blocked | Skipped |\n|---|---|---|---|---|---|\n")
	for _, a := range names {
		r := areas[a]
		fmt.Fprintf(&sb, "| %s | %d | %d | %d | %d | %d |\n", a, r.total, r.pass, r.fail, r.blocked, r.skipped)
	}
	fmt.Fprintf(&sb, "| **Total** | %d | %d | %d | %d | %d |\n", total.total, total.pass, total.fail, total.blocked, total.skipped)
	return sb.String()
}
