package conformance

import (
	"fmt"
	"testing"
)

func shardFixture() []Scenario {
	var list []Scenario
	for _, f := range []string{"a.json", "b.json", "c.json", "d.json"} {
		for i := 0; i < 2; i++ {
			list = append(list, Scenario{ID: f + ":" + string(rune('0'+i)), File: f})
		}
	}
	return list
}

// TestShardCoversEveryScenarioOnce is the property the CI relies on: the shards
// of a run, together, are the run.
func TestShardCoversEveryScenarioOnce(t *testing.T) {
	list := shardFixture()
	got := map[string]int{}
	for i := 0; i < 3; i++ {
		for _, s := range Shard(list, i, 3) {
			got[s.ID]++
		}
	}
	if len(got) != len(list) {
		t.Fatalf("%d scenarios covered, want %d", len(got), len(list))
	}
	for id, n := range got {
		if n != 1 {
			t.Errorf("%s ran %d times, want once", id, n)
		}
	}
}

// TestShardKeepsAFileTogether: a capture is used by a later scenario of the
// same file, so cutting inside a file would break it. The scenarios of a shard
// also keep the order their file was written in.
func TestShardKeepsAFileTogether(t *testing.T) {
	list := shardFixture()
	for i := 0; i < 4; i++ {
		var files []string
		for _, s := range Shard(list, i, 4) {
			if len(files) == 0 || files[len(files)-1] != s.File {
				files = append(files, s.File)
			}
		}
		seen := map[string]bool{}
		for _, f := range files {
			if seen[f] {
				t.Errorf("shard %d: %s appears twice, so its scenarios were split", i, f)
			}
			seen[f] = true
		}
		if len(files) != 1 {
			t.Errorf("shard %d: %v, want exactly one file's worth", i, files)
		}
	}
}

// TestShardOfOneIsTheWholeRun: CI on a single shard must behave like today.
func TestShardOfOneIsTheWholeRun(t *testing.T) {
	list := shardFixture()
	if got := Shard(list, 0, 1); len(got) != len(list) {
		t.Fatalf("%d scenarios, want %d", len(got), len(list))
	}
}

func TestShardOutOfRangeIsEmpty(t *testing.T) {
	list := shardFixture()
	if got := Shard(list, 4, 4); len(got) != 0 {
		t.Errorf("%d scenarios, want none", len(got))
	}
	if got := Shard(list, -1, 4); len(got) != 0 {
		t.Errorf("%d scenarios, want none", len(got))
	}
}

// TestShardBalancesTheLoad: the suite's files are not the same size (one holds
// 371 scenarios, another 1), so a round robin would hand one machine three
// times the work. The deal keeps the shards within one file of each other.
func TestShardBalancesTheLoad(t *testing.T) {
	sizes := []int{50, 40, 30, 20, 10, 5, 3, 2, 1, 1}
	var list []Scenario
	for i, n := range sizes {
		file := fmt.Sprintf("f%02d.json", i)
		for j := 0; j < n; j++ {
			list = append(list, Scenario{ID: fmt.Sprintf("%s:%d", file, j), File: file})
		}
	}
	const total = 3
	loads := make([]int, total)
	for i := 0; i < total; i++ {
		loads[i] = len(Shard(list, i, total))
	}
	low, high := loads[0], loads[0]
	for _, l := range loads {
		if l < low {
			low = l
		}
		if l > high {
			high = l
		}
	}
	if high-low > 50 {
		t.Errorf("loads %v differ by %d, more than the largest file (50)", loads, high-low)
	}
}
