// Package coverage computes what Tinyfin does and does not do, from sources a
// machine can check, and renders it (docs/COVERAGE.md, docs/coverage/).
//
// Inputs, all under the repository root:
//
//   - docs/coverage/openapi-ops.json  every operation of the reference's
//     OpenAPI document (snapshot; refreshed with `conformance coverage
//     import-openapi`)
//   - internal/api/*.go               the routes Tinyfin registers (parsed)
//   - docs/contract/scenarios/*.json  scenarios, and docs/contract/baseline.json
//     for which of them pass
//   - docs/contract/specs/*.md        rule ids
//   - docs/coverage/overlay.json      hand-written: route groups, tag defaults
//     and per-route notes for what is partial, planned or not planned
//   - docs/coverage/features/*.json   hand-written non-route features
//
// The status of an HTTP operation is computed (registered or not, covered by
// a passing scenario or not); the overlay only adds the reasons.
package coverage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Status of a feature or route.
type Status string

const (
	Supported Status = "supported"
	Partial   Status = "partial"
	Planned   Status = "planned"
	NotPlan   Status = "not-planned"
	Divergent Status = "divergent-by-design"
)

// Statuses in display order.
var Statuses = []Status{Supported, Partial, Planned, NotPlan, Divergent}

// Icon is the emoji used in tables and bars.
func (s Status) Icon() string {
	switch s {
	case Supported:
		return "✅"
	case Partial:
		return "🟡"
	case Planned:
		return "🗓️"
	case NotPlan:
		return "⛔"
	case Divergent:
		return "↔️"
	}
	return "❓"
}

func (s Status) valid() bool {
	for _, x := range Statuses {
		if x == s {
			return true
		}
	}
	return false
}

// Op is one operation of the reference's OpenAPI document.
type Op struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Tag         string `json:"tag"`
	OperationID string `json:"operationId"`
	Policy      string `json:"policy"`
	Deprecated  bool   `json:"deprecated,omitempty"`
}

// ID is the stable route id: "GET /Items/{itemId}".
func (o Op) ID() string { return o.Method + " " + o.Path }

// OpsFile is docs/coverage/openapi-ops.json.
type OpsFile struct {
	Source     string `json:"source"`
	Version    string `json:"version"`
	Operations []Op   `json:"operations"`
}

// RouteOverlay is the hand-written part of one route.
type RouteOverlay struct {
	Status    Status `json:"status,omitempty"`
	Notes     string `json:"notes,omitempty"`
	Priority  string `json:"priority,omitempty"`
	Reference string `json:"reference,omitempty"`
	Via       string `json:"via,omitempty"`
	Web       *bool  `json:"web,omitempty"`
}

// TagDefault classifies every unregistered operation of a tag.
type TagDefault struct {
	Status   Status `json:"status"`
	Notes    string `json:"notes"`
	Priority string `json:"priority,omitempty"`
}

// Group collects OpenAPI tags into one detail page.
type Group struct {
	Title   string   `json:"title"`
	Summary string   `json:"summary"`
	Tags    []string `json:"tags"`
}

// Overlay is docs/coverage/overlay.json.
type Overlay struct {
	Groups map[string]Group        `json:"groups"`
	Tags   map[string]TagDefault   `json:"tags"`
	Routes map[string]RouteOverlay `json:"routes"`
}

// Feature is one non-route (or cross-cutting) capability.
type Feature struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Status      Status   `json:"status"`
	Description string   `json:"description"`
	Reference   string   `json:"reference,omitempty"`
	Notes       string   `json:"notes,omitempty"`
	SpecRules   []string `json:"spec_rules,omitempty"`
	Scenarios   []string `json:"scenarios,omitempty"`
	Code        []string `json:"code,omitempty"`
	Routes      []string `json:"routes,omitempty"`
	Priority    string   `json:"priority,omitempty"`
	Unverified  bool     `json:"unverified,omitempty"`
	UnverifiedR string   `json:"unverified_reason,omitempty"`
	Area        string   `json:"-"`
}

// FeatureFile is docs/coverage/features/<area>.json.
type FeatureFile struct {
	Area     string    `json:"area"`
	Title    string    `json:"title"`
	Summary  string    `json:"summary"`
	Features []Feature `json:"features"`
}

// Route is a computed route record.
type Route struct {
	Op
	Group      string
	Registered bool
	Via        string // registered pattern when not an exact match
	Status     Status
	Unverified bool // supported without a passing scenario
	Scenarios  []string
	Passing    []string
	Web        bool
	Notes      string
	Reference  string
	Priority   string
}

type scenario struct {
	ID       string   `json:"id"`
	Endpoint string   `json:"endpoint"`
	Scope    string   `json:"scope"`
	Tags     []string `json:"tags"`
	Covers   []string `json:"covers"`
	Steps    []struct {
		Request struct {
			Method string `json:"method"`
			Path   string `json:"path"`
		} `json:"request"`
	} `json:"steps"`
	file string
}

// SpecStat is the derived contract statistic of one spec file.
type SpecStat struct {
	Area      string
	Rules     int
	Covered   int // rules referenced by at least one scenario
	Scenarios int
	Passing   int
}

// Model is everything computed.
type Model struct {
	Root         string
	OpenAPI      OpsFile
	Overlay      Overlay
	Routes       []Route
	Extras       []string // registered routes that are not in the OpenAPI document
	Files        []FeatureFile
	Specs        []SpecStat
	ScenarioFile map[string]string // scenario id -> file base name
	RuleFile     map[string]string // rule id -> spec file base name
	Baseline     map[string]string
	Registered   map[string]bool
	Errors       []string
}

var (
	reHandle = regexp.MustCompile(`HandleFunc\("([A-Z]+) (/[^"]*)"`)
	reRule   = regexp.MustCompile(`\*\*([a-z][a-z0-9-]*(?:\.[a-z0-9-]+)*\.R\d+)\*\*`)
	reTick   = regexp.MustCompile("`([^`]*)`")
	rePath   = regexp.MustCompile(`/[A-Za-z][A-Za-z0-9_{}./-]*`)
)

func segs(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	parts := strings.Split(p, "/")
	for i, s := range parts {
		if strings.HasPrefix(s, "{") {
			parts[i] = "{}"
		} else {
			parts[i] = strings.ToLower(s)
		}
	}
	return parts
}

func samePath(a, b string) bool {
	x, y := segs(a), segs(b)
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func readJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

// Load reads every input under root and computes the model. Problems are
// collected in Model.Errors (validation) rather than returned.
func Load(root string) (*Model, error) {
	m := &Model{Root: root, ScenarioFile: map[string]string{}, RuleFile: map[string]string{}, Registered: map[string]bool{}}
	cov := filepath.Join(root, "docs", "coverage")
	if err := readJSON(filepath.Join(cov, "openapi-ops.json"), &m.OpenAPI); err != nil {
		return nil, err
	}
	if err := readJSON(filepath.Join(cov, "overlay.json"), &m.Overlay); err != nil {
		return nil, err
	}
	paths, _ := filepath.Glob(filepath.Join(cov, "features", "*.json"))
	sort.Strings(paths)
	for _, p := range paths {
		var ff FeatureFile
		if err := readJSON(p, &ff); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		if want := strings.TrimSuffix(filepath.Base(p), ".json"); ff.Area != want {
			m.errf("%s: area %q must equal the file name %q", p, ff.Area, want)
		}
		for i := range ff.Features {
			ff.Features[i].Area = ff.Area
		}
		m.Files = append(m.Files, ff)
	}
	// baseline
	var bl struct {
		Scenarios map[string]string `json:"scenarios"`
	}
	if err := readJSON(filepath.Join(root, "docs", "contract", "baseline.json"), &bl); err != nil {
		return nil, err
	}
	m.Baseline = bl.Scenarios
	// scenarios
	var scs []scenario
	sp, _ := filepath.Glob(filepath.Join(root, "docs", "contract", "scenarios", "*.json"))
	sort.Strings(sp)
	for _, p := range sp {
		var l []scenario
		if err := readJSON(p, &l); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		base := strings.TrimSuffix(filepath.Base(p), ".json")
		for i := range l {
			l[i].file = base
			m.ScenarioFile[l[i].ID] = base
		}
		scs = append(scs, l...)
	}
	// spec rules
	mdp, _ := filepath.Glob(filepath.Join(root, "docs", "contract", "specs", "*.md"))
	sort.Strings(mdp)
	covered := map[string]bool{}
	for _, s := range scs {
		for _, c := range s.Covers {
			covered[c] = true
		}
	}
	for _, p := range mdp {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		base := strings.TrimSuffix(filepath.Base(p), ".md")
		st := SpecStat{Area: base}
		seen := map[string]bool{}
		for _, mm := range reRule.FindAllStringSubmatch(string(raw), -1) {
			if seen[mm[1]] {
				continue
			}
			seen[mm[1]] = true
			m.RuleFile[mm[1]] = base
			st.Rules++
			if covered[mm[1]] {
				st.Covered++
			}
		}
		for _, s := range scs {
			if s.file == base {
				st.Scenarios++
				if m.Baseline[s.ID] == "pass" {
					st.Passing++
				}
			}
		}
		m.Specs = append(m.Specs, st)
	}
	// registered routes
	type reg struct{ method, path string }
	var regs []reg
	gp, _ := filepath.Glob(filepath.Join(root, "internal", "api", "*.go"))
	sort.Strings(gp)
	for _, p := range gp {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		for _, mm := range reHandle.FindAllStringSubmatch(string(raw), -1) {
			regs = append(regs, reg{mm[1], mm[2]})
			m.Registered[mm[1]+" "+mm[2]] = true
		}
	}
	webPaths := webClientPaths(root)

	// routes
	groupOf := map[string]string{}
	for slug, g := range m.Overlay.Groups {
		for _, t := range g.Tags {
			if prev, dup := groupOf[t]; dup {
				m.errf("tag %q is in groups %q and %q", t, prev, slug)
			}
			groupOf[t] = slug
		}
	}
	usedReg := map[string]bool{}
	opIDs := map[string]bool{}
	for _, o := range m.OpenAPI.Operations {
		r := Route{Op: o}
		id := o.ID()
		if opIDs[id] {
			m.errf("duplicate OpenAPI operation %s", id)
		}
		opIDs[id] = true
		r.Group = groupOf[o.Tag]
		if r.Group == "" {
			m.errf("tag %q of %s is in no group (overlay.json groups)", o.Tag, id)
		}
		ov, hasOv := m.Overlay.Routes[id]
		method := o.Method
		if method == "HEAD" {
			method = "GET" // a GET pattern serves HEAD
		}
		for _, g := range regs {
			if g.method == method && samePath(g.path, o.Path) {
				r.Registered = true
				usedReg[g.method+" "+g.path] = true
				break
			}
		}
		if !r.Registered && ov.Via != "" {
			if m.Registered[ov.Via] {
				r.Registered, r.Via = true, ov.Via
				usedReg[ov.Via] = true
			} else {
				m.errf("%s: via %q is not a registered route", id, ov.Via)
			}
		}
		r.Notes, r.Reference, r.Priority = ov.Notes, ov.Reference, ov.Priority
		r.Web = webPaths[strings.Join(segs(o.Path), "/")]
		if ov.Web != nil {
			r.Web = *ov.Web
		}
		switch {
		case r.Registered:
			r.Status = Supported
			if ov.Status != "" {
				if ov.Status != Supported && ov.Status != Partial && ov.Status != Divergent {
					m.errf("%s: registered route cannot be %s", id, ov.Status)
				}
				r.Status = ov.Status
			}
		default:
			td, ok := m.Overlay.Tags[o.Tag]
			switch {
			case hasOv && ov.Status != "":
				r.Status = ov.Status
			case ok:
				r.Status = td.Status
				if r.Notes == "" {
					r.Notes = td.Notes
				}
				if r.Priority == "" {
					r.Priority = td.Priority
				}
			default:
				m.errf("%s: not registered and neither the route nor tag %q is classified in overlay.json", id, o.Tag)
				r.Status = Planned
			}
			if r.Status != Planned && r.Status != NotPlan {
				m.errf("%s: not registered, so its status must be planned or not-planned, not %q", id, r.Status)
			}
		}
		if !r.Status.valid() {
			m.errf("%s: invalid status %q", id, r.Status)
		}
		if r.Status != Supported && strings.TrimSpace(r.Notes) == "" {
			m.errf("%s: status %s needs notes", id, r.Status)
		}
		m.Routes = append(m.Routes, r)
	}
	for id := range m.Overlay.Routes {
		if !opIDs[id] {
			m.errf("overlay.json routes: %q is not an OpenAPI operation", id)
		}
	}
	for t := range m.Overlay.Tags {
		if _, ok := groupOf[t]; !ok {
			m.errf("overlay.json tags: %q is in no group", t)
		}
	}
	// scenario -> route
	idx := map[string]int{}
	for i, r := range m.Routes {
		idx[r.ID()] = i
	}
	attach := func(sid, method, path string) {
		best, bestLit := []int(nil), -1
		for i, r := range m.Routes {
			if r.Method != method || len(segs(r.Path)) != len(segs(path)) {
				continue
			}
			a, b := segs(r.Path), segs(path)
			ok, lit := true, 0
			for k := range a {
				if a[k] == "{}" {
					continue
				}
				if a[k] != b[k] {
					ok = false
					break
				}
				lit++
			}
			if !ok {
				continue
			}
			if lit > bestLit {
				best, bestLit = []int{i}, lit
			} else if lit == bestLit {
				best = append(best, i)
			}
		}
		for _, i := range best {
			m.Routes[i].Scenarios = appendUniq(m.Routes[i].Scenarios, sid)
		}
	}
	for _, s := range scs {
		if s.Scope == "scope-negative" {
			continue
		}
		if f := strings.Fields(s.Endpoint); len(f) == 2 {
			if i, ok := idx[f[0]+" "+f[1]]; ok {
				m.Routes[i].Scenarios = appendUniq(m.Routes[i].Scenarios, s.ID)
			} else {
				attach(s.ID, f[0], f[1])
			}
		}
		for _, st := range s.Steps {
			p := st.Request.Path
			if q := strings.IndexByte(p, '?'); q >= 0 {
				p = p[:q]
			}
			if p == "" || st.Request.Method == "" {
				continue
			}
			p = reTmpl.ReplaceAllString(p, "{x}")
			attach(s.ID, strings.ToUpper(st.Request.Method), p)
		}
	}
	for i := range m.Routes {
		r := &m.Routes[i]
		// a HEAD operation is served by the GET handler: share its scenarios
		if r.Method == "HEAD" {
			if j, ok := idx["GET "+r.Path]; ok {
				r.Scenarios = append([]string(nil), m.Routes[j].Scenarios...)
			}
		}
		sort.Strings(r.Scenarios)
		for _, sid := range r.Scenarios {
			if m.Baseline[sid] == "pass" {
				r.Passing = append(r.Passing, sid)
			}
		}
		if r.Status == Supported && len(r.Passing) == 0 {
			r.Unverified = true
		}
	}
	// extras
	for k := range m.Registered {
		if !usedReg[k] {
			m.Extras = append(m.Extras, k)
		}
	}
	sort.Strings(m.Extras)
	sort.Slice(m.Routes, func(i, j int) bool {
		a, b := m.Routes[i], m.Routes[j]
		if a.Tag != b.Tag {
			return a.Tag < b.Tag
		}
		if strings.ToLower(a.Path) != strings.ToLower(b.Path) {
			return strings.ToLower(a.Path) < strings.ToLower(b.Path)
		}
		return a.Method < b.Method
	})
	m.validateFeatures()
	return m, nil
}

var reTmpl = regexp.MustCompile(`\{\{[^}]*\}\}`)

func appendUniq(l []string, s string) []string {
	for _, x := range l {
		if x == s {
			return l
		}
	}
	return append(l, s)
}

func (m *Model) errf(f string, a ...any) { m.Errors = append(m.Errors, fmt.Sprintf(f, a...)) }

// webClientPaths returns the normalized paths docs/11 mentions in backticks:
// the inventory of what the official web client calls.
func webClientPaths(root string) map[string]bool {
	out := map[string]bool{}
	raw, err := os.ReadFile(filepath.Join(root, "docs", "11-jellyfin-web-route-coverage.md"))
	if err != nil {
		return out
	}
	for _, t := range reTick.FindAllStringSubmatch(string(raw), -1) {
		for _, p := range rePath.FindAllString(t[1], -1) {
			out[strings.Join(segs(strings.TrimRight(p, ".")), "/")] = true
		}
	}
	return out
}

func (m *Model) validateFeatures() {
	seen := map[string]string{}
	opIDs := map[string]bool{}
	for _, r := range m.Routes {
		opIDs[r.ID()] = true
	}
	for _, ff := range m.Files {
		for _, f := range ff.Features {
			w := "feature " + f.ID
			if f.ID == "" || !strings.HasPrefix(f.ID, ff.Area+".") {
				m.errf("%s (%s): id must start with %q", w, ff.Area, ff.Area+".")
			}
			if prev, dup := seen[f.ID]; dup {
				m.errf("%s: duplicate id (also in %s)", w, prev)
			}
			seen[f.ID] = ff.Area
			if !f.Status.valid() {
				m.errf("%s: invalid status %q", w, f.Status)
			}
			if f.Title == "" || f.Description == "" {
				m.errf("%s: title and description are required", w)
			}
			if f.Status != Supported && strings.TrimSpace(f.Notes) == "" {
				m.errf("%s: status %s needs notes", w, f.Status)
			}
			pass := 0
			for _, s := range f.Scenarios {
				if _, ok := m.ScenarioFile[s]; !ok {
					m.errf("%s: unknown scenario %q", w, s)
				}
				if m.Baseline[s] == "pass" {
					pass++
				}
			}
			for _, r := range f.SpecRules {
				if _, ok := m.RuleFile[r]; !ok {
					m.errf("%s: unknown spec rule %q", w, r)
				}
			}
			for _, c := range f.Code {
				if _, err := os.Stat(filepath.Join(m.root(), c)); err != nil {
					m.errf("%s: code path %q does not exist", w, c)
				}
			}
			for _, r := range f.Routes {
				if !opIDs[r] && !m.Registered[r] {
					m.errf("%s: unknown route %q", w, r)
				}
			}
			if f.Status == Supported && pass == 0 && !(f.Unverified && strings.TrimSpace(f.UnverifiedR) != "") {
				m.errf("%s: supported needs a passing scenario in baseline.json, or unverified:true with unverified_reason", w)
			}
			if f.Unverified && pass > 0 {
				m.errf("%s: unverified:true but a scenario passes; drop the flag", w)
			}
		}
	}
}

func (m *Model) root() string { return m.Root }
