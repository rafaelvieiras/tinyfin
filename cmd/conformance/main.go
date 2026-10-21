// Command conformance runs the behavior contract (docs/contract/) against a
// running Jellyfin-12+-compatible server and reports per-scenario results.
//
//	conformance run  -base http://127.0.0.1:18096 -admin-user admin -admin-pass '...' \
//	                 -media-root /media -fixtures docs/contract/fixtures.json \
//	                 -scenarios docs/contract/scenarios [-scope modern] [-tags web-client] [-reference]
//	                 [-risky] [-device-suffix auto|<text>] [-skip-tags a,b] [-strict]
//	                 [-server-data-dir <dir>] [-read-file-cmd "<argv prefix>"]
//	                 [-baseline docs/contract/baseline.json] [-write-baseline <file>] [-ref-report <json>]
//	conformance progress [-baseline docs/contract/baseline.json]
//	conformance lint [-dir docs/contract]
//	conformance seed-admin -db tinyfin.db -user admin -pass '...'
//	conformance coverage [-root .] [-check]            regenerate docs/COVERAGE.md and docs/coverage/ (or verify them)
//	conformance coverage import-openapi <openapi.json> [-root .] [-source text]
//
// -reference marks the target as the reference server: scenarios with an
// intentional divergence are skipped, so a failure there means the scenario
// (or the spec) is wrong, not the server.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"

	"golang.org/x/crypto/bcrypt"

	"github.com/rafaelvieiras/tinyfin/internal/conformance"
	"github.com/rafaelvieiras/tinyfin/internal/coverage"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
	"github.com/rafaelvieiras/tinyfin/internal/storage/sqlite"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "lint":
		os.Exit(lint(os.Args[2:]))
	case "run":
		os.Exit(run(os.Args[2:]))
	case "progress":
		os.Exit(progress(os.Args[2:]))
	case "seed-admin":
		os.Exit(seedAdmin(os.Args[2:]))
	case "coverage":
		os.Exit(coverageCmd(os.Args[2:]))
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: conformance run|lint|progress|coverage|seed-admin [flags]")
	os.Exit(2)
}

func lint(args []string) int {
	fs := flag.NewFlagSet("lint", flag.ExitOnError)
	dir := fs.String("dir", "docs/contract", "directory to lint")
	_ = fs.Parse(args)
	v, err := conformance.LintDir(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	for _, x := range v {
		fmt.Println(x)
	}
	if len(v) > 0 {
		fmt.Printf("%d clean-room violation(s)\n", len(v))
		return 1
	}
	fmt.Println("clean-room lint: ok")
	return 0
}

func run(args []string) int {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	base := fs.String("base", "http://127.0.0.1:8096", "server base URL")
	adminUser := fs.String("admin-user", "admin", "administrator user name")
	adminPass := fs.String("admin-pass", "", "administrator password")
	mediaRoot := fs.String("media-root", "/media", "media root AS SEEN BY THE SERVER")
	mediaLocal := fs.String("media-local-root", "", "the same media root as the RUNNER sees it; lets fs steps change files (scenarios that require fs:write are BLOCKED without it); never defaulted, the runner must not guess where to write")
	ffmpeg := fs.String("ffmpeg", os.Getenv("TINYFIN_FFMPEG_PATH"), "the ffmpeg the server under test uses, as the runner can run it (default $TINYFIN_FFMPEG_PATH); scenarios that require ffmpeg:noise-bsf are BLOCKED when it lacks that filter (the reference always has it)")
	fixtures := fs.String("fixtures", "docs/contract/fixtures.json", "fixtures file")
	scenarios := fs.String("scenarios", "docs/contract/scenarios", "scenarios directory")
	scope := fs.String("scope", "", "only run scenarios with this scope (modern|legacy-web-required|scope-negative)")
	tags := fs.String("tags", "", "comma-separated tags; scenario must have all")
	only := fs.String("only", "", "substring filter on scenario id")
	reference := fs.Bool("reference", false, "target is the reference server (skip intentional divergences)")
	skipTags := fs.String("skip-tags", "", "comma-separated tags; scenarios with any of them are not run")
	risky := fs.Bool("risky", false, "also run scenarios tagged risky (demote/delete the last administrator)")
	dataDir := fs.String("server-data-dir", "", "the server's data directory as seen by the runner (or by -read-file-cmd); lets scenarios read server-side files such as password-reset PIN files")
	readCmd := fs.String("read-file-cmd", "", "argv prefix (split on spaces, no shell) that prints a server-side file given its path, e.g. \"docker exec ref cat\"; default: read the local file system")
	strict := fs.Bool("strict", false, "treat BLOCKED scenarios as failures (for dedicated runs that must exercise everything they select)")
	suffix := fs.String("device-suffix", "", "suffix for generated DeviceIds ({{run.suffix}}); \"auto\" picks a random one so concurrent runs do not revoke each other's tokens")
	baselinePath := fs.String("baseline", "", "compare against this baseline file; exit non-zero when a passing scenario regresses")
	writeBaseline := fs.String("write-baseline", "", "write the run's per-scenario statuses to this baseline file (refresh)")
	refReport := fs.String("ref-report", "", "with -write-baseline: a -json report of a -reference run, recorded in the baseline")
	refImage := fs.String("ref-image", "jellyfin/jellyfin:12.2", "with -ref-report: reference image name recorded in the baseline")
	fresh := fs.Bool("fresh-server", false, "the target is a server that never completed setup: run only the scenarios tagged fresh-server (they drive the first-run wizard, in file order, and consume it); no administrator login or fixtures are needed")
	jsonOut := fs.String("json", "", "write results as JSON to this path")
	verbose := fs.Bool("v", false, "print passing scenarios too")
	_ = fs.Parse(args)

	list, err := conformance.LoadDir(*scenarios)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load scenarios:", err)
		return 2
	}
	var sel []conformance.Scenario
	want := splitCSV(*tags)
	skip := splitCSV(*skipTags)
	for _, s := range list {
		// fresh-server scenarios need a never-set-up server and consume it:
		// they run only in -fresh-server mode, and then they are all that runs.
		if hasAny(s.Tags, []string{"fresh-server"}) != *fresh {
			continue
		}
		if hasAny(s.Tags, skip) {
			continue
		}
		if *scope != "" && s.Scope != *scope {
			continue
		}
		if *only != "" && !strings.Contains(s.ID, *only) {
			continue
		}
		if !hasAll(s.Tags, want) {
			continue
		}
		sel = append(sel, s)
	}
	fx, err := conformance.LoadFixtures(*fixtures)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load fixtures:", err)
		return 2
	}
	ctx := context.Background()
	c := conformance.NewClient(*base)
	c.IncludeRisky = *risky
	c.ServerDataDir = *dataDir
	c.ReadFileCmd = *readCmd
	c.MediaLocalRoot = *mediaLocal
	c.FFmpegPath, c.Reference = *ffmpeg, *reference
	switch *suffix {
	case "":
	case "auto":
		var b [3]byte
		_, _ = rand.Read(b[:])
		c.DeviceSuffix = "-" + hex.EncodeToString(b[:])
	default:
		c.DeviceSuffix = *suffix
	}
	// An interrupted run must not leave the server reconfigured.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		c.Restore(context.Background())
		_ = c.Teardown(context.Background())
		os.Exit(130)
	}()
	var warns []string
	if *fresh {
		c.SetupFresh(*mediaRoot)
	} else {
		warns, err = c.Setup(ctx, *adminUser, *adminPass, *mediaRoot, fx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "setup:", err)
			return 2
		}
	}
	for _, w := range warns {
		fmt.Println("setup warning:", w)
	}
	res := c.Run(ctx, sel, *reference)
	if err := c.Teardown(ctx); err != nil {
		fmt.Println("teardown warning:", err)
	}

	counts := map[string]int{}
	for _, r := range res {
		counts[r.Status]++
		if r.Status != conformance.StatusPass || *verbose {
			fmt.Printf("%-8s %-58s %s\n", strings.ToUpper(r.Status), r.ID, r.Message)
		}
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Printf("\n%d scenarios:", len(res))
	for _, k := range keys {
		fmt.Printf(" %s=%d", k, counts[k])
	}
	fmt.Println()
	if *jsonOut != "" {
		b, _ := json.MarshalIndent(map[string]any{"target": *base, "reference": *reference, "counts": counts, "results": res}, "", "  ")
		if err := os.WriteFile(*jsonOut, b, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}
	code := 0
	if counts[conformance.StatusFail] > 0 || (*strict && counts[conformance.StatusBlocked] > 0) {
		code = 1
	}
	if *baselinePath != "" {
		raw, err := os.ReadFile(*baselinePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "baseline:", err)
			return 2
		}
		bl, err := conformance.ParseBaseline(raw)
		if err != nil {
			fmt.Fprintln(os.Stderr, "baseline:", err)
			return 2
		}
		cmp := bl.Compare(res)
		for _, r := range cmp.Regressions {
			fmt.Printf("REGRESSION %-54s now %s: %s\n", r.ID, r.Now, r.Message)
		}
		for _, id := range cmp.Improvements {
			fmt.Printf("IMPROVED   %s\n", id)
		}
		for _, id := range cmp.New {
			fmt.Printf("NEW        %s\n", id)
		}
		fmt.Printf("baseline: %d regression(s), %d improvement(s), %d new, %d not run\n",
			len(cmp.Regressions), len(cmp.Improvements), len(cmp.New), len(cmp.Missing))
		if len(cmp.Improvements)+len(cmp.New) > 0 {
			fmt.Println("refresh the baseline (see docs/contract/README.md) to record the progress")
		}
		if len(cmp.Regressions) > 0 {
			// The ratchet gates on regressions only: known failures stay failures.
			return 1
		}
		// Known failures are not an error under the ratchet.
		code = 0
	}
	if *writeBaseline != "" {
		nb := conformance.NewBaseline(res)
		if *refReport != "" {
			rb, err := os.ReadFile(*refReport)
			if err != nil {
				fmt.Fprintln(os.Stderr, "ref-report:", err)
				return 2
			}
			var rep struct {
				Counts map[string]int `json:"counts"`
			}
			if err := json.Unmarshal(rb, &rep); err != nil {
				fmt.Fprintln(os.Stderr, "ref-report:", err)
				return 2
			}
			nb.Reference = &conformance.BaselineReference{Image: *refImage, Counts: rep.Counts}
		}
		out, _ := nb.Marshal()
		if err := os.WriteFile(*writeBaseline, out, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		fmt.Println("baseline written:", *writeBaseline)
	}
	return code
}

func progress(args []string) int {
	fs := flag.NewFlagSet("progress", flag.ExitOnError)
	path := fs.String("baseline", "docs/contract/baseline.json", "baseline file")
	_ = fs.Parse(args)
	raw, err := os.ReadFile(*path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	bl, err := conformance.ParseBaseline(raw)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	fmt.Print(bl.ProgressTable())
	return 0
}

func hasAny(have, any []string) bool {
	for _, a := range any {
		for _, h := range have {
			if h == a {
				return true
			}
		}
	}
	return false
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func hasAll(have, want []string) bool {
	set := map[string]bool{}
	for _, h := range have {
		set[h] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}

// seedAdmin creates the initial administrator in a fresh Tinyfin database.
// POST /Users/New needs an existing admin, so conformance runs bootstrap one
// directly through the storage layer (the reference server is bootstrapped
// through its first-run wizard; Tinyfin's own wizard is exercised by the
// fresh-server scenarios, see docs/contract/README.md).
func seedAdmin(args []string) int {
	fs := flag.NewFlagSet("seed-admin", flag.ExitOnError)
	db := fs.String("db", "tinyfin.db", "SQLite database path")
	user := fs.String("user", "admin", "administrator name")
	pass := fs.String("pass", "", "administrator password")
	_ = fs.Parse(args)
	if *pass == "" {
		fmt.Fprintln(os.Stderr, "seed-admin: -pass is required")
		return 2
	}
	st, err := sqlite.New(*db)
	if err != nil {
		fmt.Fprintln(os.Stderr, "seed-admin:", err)
		return 2
	}
	defer st.Close()
	hash, err := bcrypt.GenerateFromPassword([]byte(*pass), bcrypt.DefaultCost)
	if err != nil {
		fmt.Fprintln(os.Stderr, "seed-admin:", err)
		return 2
	}
	id, err := st.CreateUser(context.Background(), storage.User{Name: *user, PasswordHash: string(hash), IsAdministrator: true,
		Policy: `{"IsHidden":true,"EnableContentDeletion":true,"EnableRemoteControlOfOtherUsers":true}`})
	if err != nil {
		fmt.Fprintln(os.Stderr, "seed-admin:", err)
		return 2
	}
	// A seeded administrator means the server is set up: keep the anonymous
	// first-run wizard closed.
	if err := st.SetSetting(context.Background(), storage.SettingStartupWizardCompleted, "true"); err != nil {
		fmt.Fprintln(os.Stderr, "seed-admin:", err)
		return 2
	}
	fmt.Println("seeded administrator", *user, id)
	return 0
}

// coverageCmd regenerates (or, with -check, verifies) the coverage tracker:
// docs/COVERAGE.md, docs/coverage/*.md and docs/coverage/status.json. See
// docs/coverage/README.md. "import-openapi <file>" refreshes the snapshot of
// the reference's OpenAPI operations.
func coverageCmd(args []string) int {
	fs := flag.NewFlagSet("coverage", flag.ExitOnError)
	root := fs.String("root", ".", "repository root")
	check := fs.Bool("check", false, "validate the sources and fail when a generated file is out of date; write nothing")
	source := fs.String("source", "jellyfin/jellyfin:12.2 /api-docs/openapi.json", "with import-openapi: where the document came from")
	if len(args) > 0 && args[0] == "import-openapi" {
		_ = fs.Parse(args[1:])
		if fs.NArg() != 1 {
			fmt.Fprintln(os.Stderr, "usage: conformance coverage import-openapi <openapi.json> [-root .] [-source text]")
			return 2
		}
		doc, err := os.ReadFile(fs.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		out, err := coverage.ImportOpenAPI(doc, *source)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		dst := *root + "/docs/coverage/openapi-ops.json"
		if err := os.WriteFile(dst, out, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		fmt.Println("wrote", dst)
		return 0
	}
	_ = fs.Parse(args)
	m, err := coverage.Load(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if *check {
		errs := coverage.Check(*root, m)
		for _, e := range errs {
			fmt.Println(e)
		}
		if len(errs) > 0 {
			fmt.Printf("%d coverage problem(s)\n", len(errs))
			return 1
		}
		t := m.Totals()
		fmt.Printf("coverage: ok (%d operations, %d registered, %d verified; %d features)\n", t.Routes, t.Registered, t.Verified, t.Features.Total())
		return 0
	}
	if len(m.Errors) > 0 {
		for _, e := range m.Errors {
			fmt.Println(e)
		}
		fmt.Printf("%d coverage problem(s); nothing written\n", len(m.Errors))
		return 1
	}
	if err := coverage.Write(*root, m.Render()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	t := m.Totals()
	fmt.Printf("coverage: wrote docs/COVERAGE.md and docs/coverage/ (%d operations, %d registered, %d verified)\n", t.Routes, t.Registered, t.Verified)
	return 0
}
