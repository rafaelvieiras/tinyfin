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
		if v, ok := scratch[name]; ok {
			return v
		}
		if v, ok := c.Vars[name]; ok {
			return v
		}
		missing = append(missing, name)
		return m
	})
	if len(missing) > 0 {
		return "", &blockedError{"unresolved variable(s): " + strings.Join(missing, ", ")}
	}
	return out, nil
}

func (c *Client) expandAny(v any, scratch map[string]string) (any, error) {
	switch t := v.(type) {
	case string:
		return c.expand(t, scratch)
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
		tok, ok := c.Tokens[identity]
		if !ok {
			return nil, &blockedError{"no token for identity " + identity}
		}
		req.Header.Set("Authorization", authHeader("conf-"+identity, tok))
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
	var out []Result
	for _, s := range list {
		out = append(out, c.runOne(ctx, s, skipDivergent))
	}
	return out
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
	for _, need := range s.Requires {
		if !c.Available[need] {
			res.Status, res.Message = StatusBlocked, "fixture not available: "+need
			return res
		}
	}
	scratch := map[string]string{}
	status, msg := StatusPass, ""
	for i, st := range s.Steps {
		if err := c.runStep(ctx, st, scratch); err != nil {
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
		_ = c.runStep(ctx, st, scratch)
	}
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

func (c *Client) runStep(ctx context.Context, st Step, scratch map[string]string) error {
	as := st.As
	if as == "" {
		as = "anonymous"
	}
	resp, err := c.do(ctx, as, st.Request, scratch)
	if err != nil {
		return err
	}
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
	for _, a := range st.Expect.JSON {
		if err := c.checkAssert(resp, a, scratch); err != nil {
			return fmt.Errorf("%s %s: %w%s", a.Path, a.Op, err, snippet(resp))
		}
	}
	for name, src := range st.Capture {
		v, err := capture(resp, src)
		if err != nil {
			return fmt.Errorf("capture %s: %w", name, err)
		}
		scratch[name] = v
		c.Vars["capture."+name] = v
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
	got, found, err := evalPath(r.JSON, a.Path)
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
