// Command conformance runs the behavior contract (docs/contract/) against a
// running Jellyfin-12+-compatible server and reports per-scenario results.
//
//	conformance run  -base http://127.0.0.1:18096 -admin-user admin -admin-pass '...' \
//	                 -media-root /media -fixtures docs/contract/fixtures.json \
//	                 -scenarios docs/contract/scenarios [-scope modern] [-tags web-client] [-reference]
//	conformance lint [-dir docs/contract]
//	conformance seed-admin -db tinyfin.db -user admin -pass '...'
//
// -reference marks the target as the reference server: scenarios with an
// intentional divergence are skipped, so a failure there means the scenario
// (or the spec) is wrong, not the server.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

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
	case "seed-admin":
		os.Exit(seedAdmin(os.Args[2:]))
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: conformance run|lint|seed-admin [flags]")
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
	for _, s := range list {
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
	warns, err := c.Setup(ctx, *adminUser, *adminPass, *mediaRoot, fx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "setup:", err)
		return 2
	}
	for _, w := range warns {
		fmt.Println("setup warning:", w)
	}
	res := c.Run(ctx, sel, *reference)

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
	if counts[conformance.StatusFail] > 0 {
		return 1
	}
	return 0
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
