// Command conformance runs the behavior contract (docs/contract/) against a
// running Jellyfin-12+-compatible server and reports per-scenario results.
//
//	conformance run  -base http://127.0.0.1:18096 -admin-user admin -admin-pass '...' \
//	                 -media-root /media -fixtures docs/contract/fixtures.json \
//	                 -scenarios docs/contract/scenarios [-scope modern] [-tags web-client] [-reference]
//	                 [-risky] [-device-suffix auto|<text>] [-skip-tags a,b]
//	                 [-baseline docs/contract/baseline.json] [-write-baseline <file>] [-ref-report <json>]
//	conformance progress [-baseline docs/contract/baseline.json]
//	conformance lint [-dir docs/contract]
//	conformance seed-admin -db tinyfin.db -user admin -pass '...'
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
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: conformance run|lint|progress|seed-admin [flags]")
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
	fixtures := fs.String("fixtures", "docs/contract/fixtures.json", "fixtures file")
	scenarios := fs.String("scenarios", "docs/contract/scenarios", "scenarios directory")
	scope := fs.String("scope", "", "only run scenarios with this scope (modern|legacy-web-required|scope-negative)")
	tags := fs.String("tags", "", "comma-separated tags; scenario must have all")
	only := fs.String("only", "", "substring filter on scenario id")
	reference := fs.Bool("reference", false, "target is the reference server (skip intentional divergences)")
	skipTags := fs.String("skip-tags", "", "comma-separated tags; scenarios with any of them are not run")
	risky := fs.Bool("risky", false, "also run scenarios tagged risky (demote/delete the last administrator)")
	suffix := fs.String("device-suffix", "", "suffix for generated DeviceIds ({{run.suffix}}); \"auto\" picks a random one so concurrent runs do not revoke each other's tokens")
	baselinePath := fs.String("baseline", "", "compare against this baseline file; exit non-zero when a passing scenario regresses")
	writeBaseline := fs.String("write-baseline", "", "write the run's per-scenario statuses to this baseline file (refresh)")
	refReport := fs.String("ref-report", "", "with -write-baseline: a -json report of a -reference run, recorded in the baseline")
	refImage := fs.String("ref-image", "jellyfin/jellyfin:12.2", "with -ref-report: reference image name recorded in the baseline")
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
	warns, err := c.Setup(ctx, *adminUser, *adminPass, *mediaRoot, fx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "setup:", err)
		return 2
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
	if counts[conformance.StatusFail] > 0 {
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
// Tinyfin has no first-run wizard, and POST /Users/New needs an existing
// admin, so conformance runs bootstrap one directly through the storage
// layer (the reference server is bootstrapped through its own wizard).
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
	id, err := st.CreateUser(context.Background(), storage.User{Name: *user, PasswordHash: string(hash), IsAdministrator: true})
	if err != nil {
		fmt.Fprintln(os.Stderr, "seed-admin:", err)
		return 2
	}
	fmt.Println("seeded administrator", *user, id)
	return 0
}
