package trickplay

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestStepsDoNotPinThreads: generating a sheet decodes the whole video, and
// pinning that decode to one thread made it about four times slower on a
// 1080p source. No ffmpeg step may pass -threads; TrickplayConcurrency, not
// the thread count, bounds how much CPU a batch takes.
func TestStepsDoNotPinThreads(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in is a shell script")
	}
	dir := t.TempDir()
	record := filepath.Join(dir, "args.txt")
	// A stand-in that records its arguments and fails, so generate stops
	// after the first step: that step is the whole-video decode, the one
	// that used to be pinned.
	stub := filepath.Join(dir, "ffmpeg")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> " + record + "; done\nexit 1\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	g := &Generator{ffmpeg: stub}
	plan := Plan{IntervalMS: 10000, Width: 320, Height: 180, TileWidth: 4, TileHeight: 4}
	if _, err := g.generate(context.Background(), filepath.Join(dir, "film.mkv"), filepath.Join(dir, "trick"), plan, "stamp"); err == nil {
		t.Fatal("generate succeeded with a stand-in that fails")
	}

	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("the stand-in recorded nothing: %v", err)
	}
	args := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(args) < 2 {
		t.Fatalf("recorded %d arguments, want the decode step", len(args))
	}
	for _, a := range args {
		if a == "-threads" {
			t.Fatalf("a step pins the thread count: %v", args)
		}
	}
	if !strings.Contains(strings.Join(args, " "), "fps=") {
		t.Errorf("recorded step = %v, want the whole-video decode (an fps filter)", args)
	}
}
