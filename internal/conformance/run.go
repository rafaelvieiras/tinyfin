package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Result statuses.
const (
	StatusPass    = "pass"
	StatusFail    = "fail"
	StatusBlocked = "blocked" // a fixture/variable the scenario needs does not exist on this server
	StatusSkipped = "skipped"
)

// Result is the outcome of one scenario.
type Result struct {
	ID       string   `json:"id"`
	Endpoint string   `json:"endpoint"`
	Scope    string   `json:"scope"`
	Status   string   `json:"status"`
	Message  string   `json:"message,omitempty"`
	Covers   []string `json:"covers,omitempty"`
	Tags     []string `json:"tags,omitempty"`
}

// Client executes scenarios against one server.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	// Vars holds template variables ("user.alice.id", "item.movie.id", ...).
	Vars map[string]string
	// Tokens maps an identity ("admin", fixture user key) to its access token.
	Tokens map[string]string
	// Available is the set of fixture keys that exist ("user:alice", "item:movie").
	Available map[string]bool
	// DeviceSuffix is appended to every DeviceId the runner generates and is
	// exposed to scenarios as {{run.suffix}}, so concurrent runs against one
	// server do not revoke each other's tokens (a new login on a DeviceId
	// revokes the previous token of that device).
	DeviceSuffix string
	// IncludeRisky enables scenarios tagged "risky" (those that try to demote
	// or delete the last administrator); skipped by default so a buggy
	// implementation cannot lock the rest of the suite out.
	IncludeRisky bool
	// ServerDataDir is the server's data directory as seen by the runner
	// (or by ReadFileCmd). Relative names in a serverFile step resolve under
	// it. Empty together with ReadFileCmd: serverFile steps are BLOCKED and
	// the scenarios that require "serverfile:read" are blocked.
	ServerDataDir string
	// ReadFileCmd is an argv prefix (split on spaces, no shell) that prints a
	// server-side file given its path as the last argument, e.g.
	// "docker exec ref cat". When empty the runner reads the local file system.
	ReadFileCmd string

	// Now overrides the runner clock used by the clock.* templates (tests).
	Now func() time.Time

	clockCalibrated bool
	clockOffset     time.Duration // server local time minus UTC

	mediaRoot string
	lazyLibs  map[string]*lazyLibrary
	apiKeyID  string // access token of the run's API key (for Teardown)
	mu        sync.Mutex
	activeRun *runState
}

// runState is the per-scenario mutable state: captured variables, open
// sockets and the configuration to restore.
type runState struct {
	scratch    map[string]string
	sockets    map[string]*wsConn
	configOrig map[string][]byte // original configuration per section, set once
}

// NewClient returns a Client with sane defaults.
func NewClient(base string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(base, "/"),
		HTTP: &http.Client{
			Timeout:       30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		Vars:      map[string]string{},
		Tokens:    map[string]string{},
		Available: map[string]bool{},
	}
}

// resolveToken maps an identity to its access token: "admin", a fixture user
// key, "apikey" (the run's API key) or "token:<value>" for a token captured
// during the scenario.
func (c *Client) resolveToken(identity string, scratch map[string]string) (string, error) {
	if raw, ok := strings.CutPrefix(identity, "token:"); ok {
		v, err := c.expand(raw, scratch)
		if err != nil {
			return "", err
		}
		return v, nil
	}
	tok, ok := c.Tokens[identity]
	if !ok {
		return "", &blockedError{"no token for identity " + identity}
	}
	return tok, nil
}

// authHeader builds the 12+ authorization header for an identity.
func authHeader(deviceID, token string) string {
	h := fmt.Sprintf(`MediaBrowser Client="Conformance", Device="Conformance", DeviceId=%q, Version="1.0.0"`, deviceID)
	if token != "" {
		h += fmt.Sprintf(`, Token=%q`, token)
	}
	return h
}

var tmplRE = regexp.MustCompile(`\{\{\s*([^}\s]+)\s*\}\}`)

type blockedError struct{ msg string }

func (e *blockedError) Error() string { return e.msg }

func (c *Client) expand(s string, scratch map[string]string) (string, error) {
	var missing []string
	out := tmplRE.ReplaceAllStringFunc(s, func(m string) string {
		name := tmplRE.FindStringSubmatch(m)[1]
		// "{{x|dashed}}" renders a 32-hex id in its 8-4-4-4-12 form.
		// Filters: |dashed (8-4-4-4-12 form), |upper, |num (see expandAny).
		var dashed, upper bool
		for {
			if n, ok := strings.CutSuffix(name, "|dashed"); ok {
				name, dashed = n, true
			} else if n, ok := strings.CutSuffix(name, "|upper"); ok {
				name, upper = n, true
			} else if n, ok := strings.CutSuffix(name, "|num"); ok {
				name = n
			} else {
				break
			}
		}
		if spec, isClock := strings.CutPrefix(name, "clock."); isClock {
			v, err := c.clockValue(spec)
			if err != nil {
				missing = append(missing, name+" ("+err.Error()+")")
				return m
			}
			return v
		}
		v, ok := scratch[name]
		if !ok {
			v, ok = c.Vars[name]
		}
		if !ok && name == "run.suffix" {
			v, ok = c.DeviceSuffix, true
		}
		if !ok {
			missing = append(missing, name)
			return m
		}
		if dashed && len(v) == 32 {
			v = v[:8] + "-" + v[8:12] + "-" + v[12:16] + "-" + v[16:20] + "-" + v[20:]
		}
		if upper {
			v = strings.ToUpper(v)
		}
		return v
	})
	if len(missing) > 0 {
		return "", &blockedError{"unresolved variable(s): " + strings.Join(missing, ", ")}
	}
	return out, nil
}

func (c *Client) expandAny(v any, scratch map[string]string) (any, error) {
	switch t := v.(type) {
	case string:
		out, err := c.expand(t, scratch)
		if err != nil {
			return nil, err
		}
		// "{{x|num}}" as the whole value is sent as a JSON number.
		if strings.HasPrefix(t, "{{") && strings.HasSuffix(t, "|num}}") && strings.Count(t, "{{") == 1 {
			f, perr := strconv.ParseFloat(out, 64)
			if perr != nil {
				return nil, &blockedError{"template " + t + " is not a number: " + out}
			}
			return f, nil
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i := range t {
			x, err := c.expandAny(t[i], scratch)
			if err != nil {
				return nil, err
			}
			out[i] = x
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			y, err := c.expandAny(x, scratch)
			if err != nil {
				return nil, err
			}
			out[k] = y
		}
		return out, nil
	default:
		return v, nil
	}
}

// Response is what a step observed.
type Response struct {
	Status  int
	Headers http.Header
	Body    []byte
	JSON    any // decoded when the body is valid JSON, else nil
}

// Do executes one request outside of any scenario (used by fixtures).
func (c *Client) Do(ctx context.Context, identity, method, path string, query map[string]string, body any, extra map[string]string) (*Response, error) {
	return c.do(ctx, identity, Request{Method: method, Path: path, Query: query, Body: body, Headers: extra}, nil)
}

func (c *Client) do(ctx context.Context, identity string, r Request, scratch map[string]string) (*Response, error) {
	path, err := c.expand(r.Path, scratch)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	for k, v := range r.Query {
		ev, err := c.expand(v, scratch)
		if err != nil {
			return nil, err
		}
		q.Set(k, ev)
	}
	u := c.BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rdr io.Reader
	ctype := ""
	switch {
	case r.BodyRaw != "":
		raw, err := c.expand(r.BodyRaw, scratch)
		if err != nil {
			return nil, err
		}
		rdr = strings.NewReader(raw)
	case r.Body != nil:
		b, err := c.expandAny(r.Body, scratch)
		if err != nil {
			return nil, err
		}
		enc, _ := json.Marshal(b)
		rdr = bytes.NewReader(enc)
		ctype = "application/json"
	}
	method := strings.ToUpper(r.Method)
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return nil, err
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if !r.NoAuth && identity != "" && identity != "anonymous" {
		tok, err := c.resolveToken(identity, scratch)
		if err != nil {
			return nil, err
		}
		dev := "conf-" + identity
		if strings.HasPrefix(identity, "token:") {
			dev = "conf-token"
		}
		req.Header.Set("Authorization", authHeader(dev+c.DeviceSuffix, tok))
	}
	for k, v := range r.Headers {
		ev, err := c.expand(v, scratch)
		if err != nil {
			return nil, err
		}
		if strings.EqualFold(k, "Host") {
			req.Host = ev
			continue
		}
		req.Header.Set(k, ev)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	out := &Response{Status: resp.StatusCode, Headers: resp.Header, Body: b}
	if len(bytes.TrimSpace(b)) > 0 {
		var j any
		if json.Unmarshal(b, &j) == nil {
			out.JSON = j
		}
	}
	return out, nil
}

// Run executes scenarios and returns one Result per scenario, in order.
// skipDivergent controls whether scenarios with a Divergence are skipped
// (true when validating against the reference server).
func (c *Client) Run(ctx context.Context, list []Scenario, skipDivergent bool) []Result {
	// Risky scenarios run last, in their given order: whatever one of them
	// does to the server (last-admin checks, a throttled source) cannot
	// affect the others.
	ordered := make([]Scenario, 0, len(list))
	for _, s := range list {
		if !hasTag(s.Tags, "risky") {
			ordered = append(ordered, s)
		}
	}
	for _, s := range list {
		if hasTag(s.Tags, "risky") {
			ordered = append(ordered, s)
		}
	}
	var out []Result
	for _, s := range ordered {
		out = append(out, c.runOne(ctx, s, skipDivergent))
	}
	return out
}

// administratorCount counts the accounts whose policy marks them administrator.
func (c *Client) administratorCount(ctx context.Context) (int, error) {
	r, err := c.Do(ctx, "admin", "GET", "/Users", nil, nil, nil)
	if err != nil {
		return 0, err
	}
	arr, ok := r.JSON.([]any)
	if r.Status != 200 || !ok {
		return 0, fmt.Errorf("list users: status %d", r.Status)
	}
	n := 0
	for _, u := range arr {
		m, _ := u.(map[string]any)
		p, _ := m["Policy"].(map[string]any)
		if a, _ := p["IsAdministrator"].(bool); a {
			n++
		}
	}
	return n, nil
}

func (c *Client) runOne(ctx context.Context, s Scenario, skipDivergent bool) Result {
	res := Result{ID: s.ID, Endpoint: s.Endpoint, Scope: s.Scope, Covers: s.Covers, Tags: s.Tags}
	if s.Pending != "" {
		res.Status, res.Message = StatusSkipped, "pending: "+s.Pending
		return res
	}
	if skipDivergent && s.Divergence != nil {
		res.Status, res.Message = StatusSkipped, "intentional divergence: "+s.Divergence.Reason
		return res
	}
	if !c.IncludeRisky && hasTag(s.Tags, "risky") {
		res.Status, res.Message = StatusSkipped, "risky: opt-in with -risky"
		return res
	}
	// A scenario tagged last-admin acts on "the only administrator". On a
	// server that has another one it would delete or demote the runner's
	// admin for real, so it runs only when the runner's admin is the sole one.
	if hasTag(s.Tags, "last-admin") {
		n, err := c.administratorCount(ctx)
		if err != nil || n != 1 {
			res.Status = StatusBlocked
			res.Message = fmt.Sprintf("needs a server whose only administrator is the runner's (found %d: %v)", n, err)
			return res
		}
	}
	done := map[*lazyLibrary]bool{}
	for _, need := range s.Requires {
		if ll := c.lazyFor(need); ll != nil && !done[ll] {
			done[ll] = true
			undo, err := c.materialize(ctx, ll)
			if err != nil {
				res.Status, res.Message = StatusBlocked, "fixture not available: "+need+": "+err.Error()
				return res
			}
			defer undo()
		}
	}
	for _, need := range s.Requires {
		if !c.Available[need] {
			res.Status, res.Message = StatusBlocked, "fixture not available: "+need
			return res
		}
	}
	rs := &runState{scratch: map[string]string{}, sockets: map[string]*wsConn{}}
	c.setActive(rs)
	defer c.setActive(nil)
	status, msg := StatusPass, ""
	for i, st := range s.Steps {
		if err := c.runStep(ctx, st, rs); err != nil {
			var be *blockedError
			if asBlocked(err, &be) {
				status, msg = StatusBlocked, fmt.Sprintf("step %d (%s): %v", i+1, st.Name, err)
			} else {
				status, msg = StatusFail, fmt.Sprintf("step %d (%s): %v", i+1, st.Name, err)
			}
			break
		}
	}
	for _, st := range s.Cleanup {
		_ = c.runStep(ctx, st, rs)
	}
	c.finish(ctx, rs)
	res.Status, res.Message = status, msg
	return res
}

func asBlocked(err error, target **blockedError) bool {
	if b, ok := err.(*blockedError); ok {
		*target = b
		return true
	}
	return false
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

func (c *Client) setActive(rs *runState) {
	c.mu.Lock()
	c.activeRun = rs
	c.mu.Unlock()
}

// Restore undoes any server-wide change the running scenario made. The CLI
// calls it from its interrupt handler so an aborted run does not leave the
// server reconfigured.
func (c *Client) Restore(ctx context.Context) {
	c.mu.Lock()
	rs := c.activeRun
	c.mu.Unlock()
	if rs != nil {
		c.finish(ctx, rs)
	}
}

// finish closes sockets and restores the server configuration (once).
func (c *Client) finish(ctx context.Context, rs *runState) {
	for name, w := range rs.sockets {
		w.close()
		delete(rs.sockets, name)
	}
	for section, orig := range rs.configOrig {
		_, _ = c.do(ctx, "admin", Request{Method: "POST", Path: configPath(section), BodyRaw: string(orig),
			Headers: map[string]string{"Content-Type": "application/json"}}, nil)
		delete(rs.configOrig, section)
	}
}

func (c *Client) runStep(ctx context.Context, st Step, rs *runState) error {
	scratch := rs.scratch
	if st.SleepMs > 0 {
		select {
		case <-time.After(time.Duration(st.SleepMs) * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if st.Config != nil {
		if err := c.applyConfig(ctx, st.Config, rs); err != nil {
			return err
		}
	}
	var resp *Response
	switch {
	case st.WS != nil:
		msg, err := c.runWS(ctx, st.WS, rs)
		if err != nil {
			return err
		}
		if msg == nil {
			return nil
		}
		resp = msg
	case st.ServerFile != nil:
		r, err := c.readServerFile(ctx, st.ServerFile, scratch)
		if err != nil {
			return err
		}
		resp = r
	case st.Request.Path == "" && (st.SleepMs > 0 || st.Config != nil):
		return nil
	default:
		as := st.As
		if as == "" {
			as = "anonymous"
		}
		r, err := c.do(ctx, as, st.Request, scratch)
		if err != nil {
			return err
		}
		resp = r
	}
	return c.checkExpect(st, resp, scratch)
}

func configPath(section string) string {
	if section == "" {
		return "/System/Configuration"
	}
	return "/System/Configuration/" + section
}

func (c *Client) applyConfig(ctx context.Context, cs *ConfigStep, rs *runState) error {
	path := configPath(cs.Section)
	r, err := c.do(ctx, "admin", Request{Method: "GET", Path: path}, nil)
	if err != nil {
		return err
	}
	cfg, ok := r.JSON.(map[string]any)
	if r.Status != 200 || !ok {
		return fmt.Errorf("read configuration %q: status %d", path, r.Status)
	}
	if rs.configOrig == nil {
		rs.configOrig = map[string][]byte{}
	}
	if _, kept := rs.configOrig[cs.Section]; !kept {
		rs.configOrig[cs.Section] = append([]byte(nil), r.Body...)
	}
	for k, v := range cs.Set {
		cfg[k] = v
	}
	r, err = c.do(ctx, "admin", Request{Method: "POST", Path: path, Body: cfg}, nil)
	if err != nil {
		return err
	}
	if r.Status != 200 && r.Status != 204 {
		return fmt.Errorf("write configuration %q: status %d", path, r.Status)
	}
	return nil
}

func (c *Client) checkExpect(st Step, resp *Response, scratch map[string]string) error {
	if len(st.Expect.Status) > 0 {
		ok := false
		for _, want := range st.Expect.Status {
			if resp.Status == want {
				ok = true
			}
		}
		if !ok {
			return fmt.Errorf("status %d, want %v%s", resp.Status, []int(st.Expect.Status), snippet(resp))
		}
	}
	if st.Expect.Empty && len(bytes.TrimSpace(resp.Body)) != 0 {
		return fmt.Errorf("expected empty body, got %d bytes", len(resp.Body))
	}
	hk := make([]string, 0, len(st.Expect.Headers))
	for k := range st.Expect.Headers {
		hk = append(hk, k)
	}
	sort.Strings(hk)
	for _, k := range hk {
		if err := checkHeader(resp.Headers, k, st.Expect.Headers[k]); err != nil {
			return err
		}
	}
	if st.Expect.Problem != 0 {
		if err := checkProblem(resp, st.Expect.Problem); err != nil {
			return fmt.Errorf("problem document: %w%s", err, snippet(resp))
		}
	}
	for _, a := range st.Expect.JSON {
		if err := c.checkAssert(resp, a, scratch); err != nil {
			return fmt.Errorf("%s %s: %w%s", a.Path, a.Op, err, snippet(resp))
		}
	}
	for name, src := range st.Capture {
		src, err := c.expand(src, scratch)
		if err != nil {
			return fmt.Errorf("capture %s: %w", name, err)
		}
		v, err := capture(resp, src)
		if err != nil {
			return fmt.Errorf("capture %s: %w", name, err)
		}
		scratch[name] = v
		c.Vars["capture."+name] = v
	}
	return nil
}

// checkProblem asserts an RFC 9110 problem document: an object with a string
// type and title and a status member equal to want.
func checkProblem(r *Response, want int) error {
	m, ok := r.JSON.(map[string]any)
	if !ok {
		return fmt.Errorf("body is not a JSON object")
	}
	for _, k := range []string{"type", "title"} {
		if s, ok := m[k].(string); !ok || s == "" {
			return fmt.Errorf("member %q missing or not a string", k)
		}
	}
	if st, ok := toFloat(m["status"]); !ok || int(st) != want {
		return fmt.Errorf("status member %v, want %d", m["status"], want)
	}
	return nil
}

var hex32RE = regexp.MustCompile(`^[0-9a-f]{32}$`)

// checkHex32 asserts every value (a string, or each element of an array) is 32
// lowercase hexadecimal digits with no dashes.
func checkHex32(v any) error {
	vals := []any{v}
	if arr, ok := v.([]any); ok {
		if len(arr) == 0 {
			return fmt.Errorf("no values to check")
		}
		vals = arr
	}
	for _, x := range vals {
		s, ok := x.(string)
		if !ok || !hex32RE.MatchString(s) {
			return fmt.Errorf("%s is not 32 lowercase hex digits", show(x))
		}
	}
	return nil
}

func snippet(r *Response) string {
	b := r.Body
	if len(b) > 160 {
		b = b[:160]
	}
	return " [body: " + strings.ReplaceAll(string(b), "\n", " ") + "]"
}

func checkHeader(h http.Header, name, want string) error {
	got := h.Get(name)
	switch {
	case want == "present":
		if got == "" {
			return fmt.Errorf("header %s missing", name)
		}
	case want == "absent":
		if got != "" {
			return fmt.Errorf("header %s present (%q), want absent", name, got)
		}
	case len(want) > 2 && strings.HasPrefix(want, "/") && strings.HasSuffix(want, "/"):
		re, err := regexp.Compile(want[1 : len(want)-1])
		if err != nil {
			return err
		}
		if !re.MatchString(got) {
			return fmt.Errorf("header %s = %q, want match %s", name, got, want)
		}
	default:
		if !strings.EqualFold(got, want) {
			return fmt.Errorf("header %s = %q, want %q", name, got, want)
		}
	}
	return nil
}

func capture(r *Response, src string) (string, error) {
	if strings.HasPrefix(src, "header:") {
		return r.Headers.Get(strings.TrimPrefix(src, "header:")), nil
	}
	if r.JSON == nil {
		return "", fmt.Errorf("response has no JSON body")
	}
	v, found, err := evalPath(r.JSON, src)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("path %s not found", src)
	}
	switch t := v.(type) {
	case string:
		return t, nil
	case nil:
		return "", nil
	default:
		b, _ := json.Marshal(t)
		return string(b), nil
	}
}

func (c *Client) checkAssert(r *Response, a Assert, scratch map[string]string) error {
	want := a.Value
	if s, ok := want.(string); ok {
		ev, err := c.expand(s, scratch)
		if err != nil {
			return err
		}
		want = ev
	}
	if r.JSON == nil && a.Op != "absent" {
		return fmt.Errorf("response has no JSON body")
	}
	path, err := c.expand(a.Path, scratch)
	if err != nil {
		return err
	}
	got, found, err := evalPath(r.JSON, path)
	if err != nil {
		return err
	}
	switch a.Op {
	case "exists":
		if !found {
			return fmt.Errorf("not found")
		}
	case "absent":
		if found {
			return fmt.Errorf("present, want absent")
		}
	case "eq":
		if !found {
			return fmt.Errorf("not found")
		}
		if !jsonEqual(got, want) {
			return fmt.Errorf("got %s, want %s", show(got), show(want))
		}
	case "ne":
		if found && jsonEqual(got, want) {
			return fmt.Errorf("got %s, want anything else", show(got))
		}
	case "type":
		if !found {
			return fmt.Errorf("not found")
		}
		if t := typeName(got); t != fmt.Sprint(want) {
			return fmt.Errorf("type %s, want %v", t, want)
		}
	case "len":
		if !found {
			return fmt.Errorf("not found")
		}
		n, ok := length(got)
		w, _ := toFloat(want)
		if !ok || float64(n) != w {
			return fmt.Errorf("length %d, want %v", n, want)
		}
	case "gte", "lte":
		if !found {
			return fmt.Errorf("not found")
		}
		g, ok1 := toFloat(got)
		w, ok2 := toFloat(want)
		if n, isLen := length(got); !ok1 && isLen {
			g, ok1 = float64(n), true
		}
		if !ok1 || !ok2 {
			return fmt.Errorf("not numeric: %s vs %v", show(got), want)
		}
		if (a.Op == "gte" && g < w) || (a.Op == "lte" && g > w) {
			return fmt.Errorf("got %v, want %s %v", g, a.Op, w)
		}
	case "contains", "notcontains":
		has := contains(got, want)
		if a.Op == "contains" && !has {
			return fmt.Errorf("%s does not contain %s", show(got), show(want))
		}
		if a.Op == "notcontains" && has {
			return fmt.Errorf("%s unexpectedly contains %s", show(got), show(want))
		}
	case "matches":
		re, err := regexp.Compile(fmt.Sprint(want))
		if err != nil {
			return err
		}
		if !found || !re.MatchString(fmt.Sprint(got)) {
			return fmt.Errorf("%s does not match %v", show(got), want)
		}
	case "allmatch":
		// Like matches, but a wildcard path is checked value by value and an
		// empty result fails, so a list of dates can be held to one format.
		if !found {
			return fmt.Errorf("not found")
		}
		re, err := regexp.Compile(fmt.Sprint(want))
		if err != nil {
			return err
		}
		vals := []any{got}
		if arr, ok := got.([]any); ok {
			if len(arr) == 0 {
				return fmt.Errorf("no values to check")
			}
			vals = arr
		}
		for _, x := range vals {
			if str, ok := x.(string); !ok || !re.MatchString(str) {
				return fmt.Errorf("%s does not match %v", show(x), want)
			}
		}
	case "hex32":
		if !found {
			return fmt.Errorf("not found")
		}
		return checkHex32(got)
	case "in":
		arr, _ := want.([]any)
		for _, x := range arr {
			if jsonEqual(got, x) {
				return nil
			}
		}
		return fmt.Errorf("%s not in %s", show(got), show(want))
	default:
		return fmt.Errorf("unknown op %q", a.Op)
	}
	return nil
}

func jsonEqual(a, b any) bool {
	if fa, ok := toFloat(a); ok {
		if fb, ok := toFloat(b); ok {
			return fa == fb
		}
	}
	return reflect.DeepEqual(norm(a), norm(b))
}

func norm(v any) any {
	b, _ := json.Marshal(v)
	var o any
	_ = json.Unmarshal(b, &o)
	return o
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case string:
		f, err := strconv.ParseFloat(t, 64)
		return f, err == nil
	}
	return 0, false
}

func length(v any) (int, bool) {
	switch t := v.(type) {
	case []any:
		return len(t), true
	case string:
		return len(t), true
	case map[string]any:
		return len(t), true
	}
	return 0, false
}

func contains(container, item any) bool {
	switch t := container.(type) {
	case string:
		return strings.Contains(t, fmt.Sprint(item))
	case []any:
		for _, x := range t {
			if jsonEqual(x, item) {
				return true
			}
		}
	}
	return false
}

func typeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "unknown"
}

func show(v any) string {
	b, _ := json.Marshal(v)
	s := string(b)
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}
