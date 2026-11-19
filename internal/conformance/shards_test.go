package conformance

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestCommittedShardPlanCoversTheSuite is the guard that keeps CI honest: the
// plan decides which machine runs which file, so a file it does not name never
// runs anywhere. Adding a scenario file therefore fails here until the plan is
// regenerated with `go run ./cmd/conformance shards -report run.json -n 4
// -write docs/contract/shards.json`.
func TestCommittedShardPlanCoversTheSuite(t *testing.T) {
	dir := filepath.Join("..", "..", "docs", "contract", "scenarios")
	list, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("load scenarios: %v", err)
	}
	plan, err := LoadShardPlan(filepath.Join("..", "..", "docs", "contract", "shards.json"))
	if err != nil {
		t.Fatalf("load shard plan: %v", err)
	}
	if plan == nil {
		t.Skip("no shard plan yet")
	}
	var files []string
	seen := map[string]bool{}
	for _, s := range list {
		if !seen[s.File] {
			seen[s.File] = true
			files = append(files, s.File)
		}
	}
	if err := plan.Check(files); err != nil {
		t.Fatal(err)
	}
	// The count lives in the workflow's matrix. Read it from there, so a change
	// to one of the two fails here instead of splitting CI unevenly in silence.
	wf, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Skipf("no workflow to compare against: %v", err)
	}
	if m := regexp.MustCompile(`shard: 1/(\d+)`).FindSubmatch(wf); m != nil {
		want, _ := strconv.Atoi(string(m[1]))
		if got := len(plan.Shards); got != want {
			t.Errorf("plan has %d shards, the workflow runs %d: run go run ./cmd/conformance shards -report run.json -n %d -write docs/contract/shards.json", got, want, want)
		}
	} else {
		t.Error("the workflow no longer names a shard as 1/N")
	}
}

func weightFixture() map[string]int64 {
	return map[string]int64{
		"big-and-slow.json":  163_000,
		"big-and-cheap.json": 181_000,
		"small.json":         60_000,
		"tiny.json":          1_000,
	}
}

// TestPlanFromWeightsBalancesByWeight: dealing by scenario count left the
// shards at 2.3, 6.1, 9.1 and 11.8 minutes on the measured suite, because a
// file's cost is not its length. Weighted by time they land together.
func TestPlanFromWeightsBalancesByWeight(t *testing.T) {
	weights := weightFixture()
	plan := PlanFromWeights(weights, 2)
	if len(plan.Shards) != 2 {
		t.Fatalf("%d shards, want 2", len(plan.Shards))
	}
	var loads []int64
	for _, shard := range plan.Shards {
		var load int64
		for _, f := range shard {
			load += weights[f]
		}
		loads = append(loads, load)
	}
	gap := loads[0] - loads[1]
	if gap < 0 {
		gap = -gap
	}
	// The largest file is 181 s, so no deal can be closer than that.
	if gap > 181_000 {
		t.Errorf("loads %v differ by %d ms, more than the largest file", loads, gap)
	}
}

func TestPlanFromWeightsIsDeterministic(t *testing.T) {
	weights := weightFixture()
	first := PlanFromWeights(weights, 3)
	for i := 0; i < 5; i++ {
		again := PlanFromWeights(weights, 3)
		for s := range first.Shards {
			if len(first.Shards[s]) != len(again.Shards[s]) {
				t.Fatalf("shard %d: %v then %v", s, first.Shards[s], again.Shards[s])
			}
			for f := range first.Shards[s] {
				if first.Shards[s][f] != again.Shards[s][f] {
					t.Fatalf("shard %d: %v then %v", s, first.Shards[s], again.Shards[s])
				}
			}
		}
	}
}

func TestPlanCheckNamesWhatIsWrong(t *testing.T) {
	plan := &ShardPlan{Shards: [][]string{
		{"a.json", "b.json"},
		{"b.json", "ghost.json"},
	}}
	err := plan.Check([]string{"a.json", "b.json", "c.json"})
	if err == nil {
		t.Fatal("want an error for a duplicate and a missing file")
	}
	if !strings.Contains(err.Error(), "b.json") || !strings.Contains(err.Error(), "c.json") {
		t.Errorf("%q should name the duplicate and the missing file", err)
	}
}

func TestPlanSelectKeepsTheFilesOrder(t *testing.T) {
	list := []Scenario{
		{ID: "a1", File: "a.json"},
		{ID: "b1", File: "b.json"},
		{ID: "a2", File: "a.json"},
	}
	plan := &ShardPlan{Shards: [][]string{{"a.json"}, {"b.json"}}}
	got := plan.Select(list, 0)
	if len(got) != 2 || got[0].ID != "a1" || got[1].ID != "a2" {
		t.Errorf("Select = %v, want a1 then a2", got)
	}
	if empty := plan.Select(list, 2); len(empty) != 0 {
		t.Errorf("out of range shard returned %v", empty)
	}
}

func TestLoadShardPlanMissingFileIsNotAnError(t *testing.T) {
	plan, err := LoadShardPlan(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil || plan != nil {
		t.Fatalf("got %v, %v; want nil, nil", plan, err)
	}
	if _, err := os.Stat(filepath.Join("..", "..", "docs", "contract", "shards.json")); err != nil {
		t.Logf("committed plan: %v", err)
	}
}
