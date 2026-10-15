// Package conformance runs black-box, wire-level behavior scenarios (the
// contract under docs/contract/) against any server that speaks the
// Jellyfin 12+ API: a running reference server to validate the scenarios,
// and Tinyfin to check conformance.
//
// A scenario is plain data (JSON). It never embeds reference-server code or
// identifiers: it says "given this state, this request produces that
// observable response". See docs/contract/README.md.
package conformance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Scope values classify why a scenario exists.
const (
	// ScopeModern is the current (12+) API surface.
	ScopeModern = "modern"
	// ScopeLegacyWebRequired is a route the reference server marks as legacy
	// but that the official jellyfin-web 12.x still calls, so it must work.
	ScopeLegacyWebRequired = "legacy-web-required"
	// ScopeNegative asserts that something out of scope is NOT honored.
	ScopeNegative = "scope-negative"
)

// Scenario is one self-contained behavior check.
type Scenario struct {
	ID       string   `json:"id"`
	Endpoint string   `json:"endpoint"` // "METHOD /Path" of the primary route under test
	Spec     string   `json:"spec"`     // anchor in docs/contract/specs/*.md
	Scope    string   `json:"scope"`
	Tags     []string `json:"tags,omitempty"`
	// Covers lists the spec rule IDs (e.g. "users.auth.R3") this scenario
	// exercises; the coverage report is built from it.
	Covers []string `json:"covers,omitempty"`
	// Requires lists fixture keys that must exist ("user:restricted",
	// "item:movie", ...). A missing fixture marks the scenario BLOCKED, not
	// failed.
	Requires []string `json:"requires,omitempty"`
	Steps    []Step   `json:"steps"`
	// Cleanup steps always run, after Steps, and their failures are ignored.
	Cleanup []Step `json:"cleanup,omitempty"`
	// Divergence documents an intentional difference from the reference
	// server: Tinyfin must follow Expect, the reference run is expected to
	// differ (it is skipped when validating against the reference).
	Divergence *Divergence `json:"divergence,omitempty"`
	// Pending marks a scenario that is specified but not yet verified against
	// the reference server (kept out of the pass/fail gate).
	Pending string `json:"pending,omitempty"`
}

// Divergence explains why a scenario's expectation is not the reference's.
type Divergence struct {
	Reason string `json:"reason"`
}

// Step is one HTTP exchange.
type Step struct {
	Name    string            `json:"name,omitempty"`
	As      string            `json:"as,omitempty"` // "anonymous" (default), "admin", or a fixture user key
	Request Request           `json:"request"`
	Expect  Expect            `json:"expect"`
	Capture map[string]string `json:"capture,omitempty"` // variable -> "$.json.path" or "header:Name"
	// SleepMs pauses before the step's request (time-based rules). A step may
	// consist of only a sleep, a WS action or a Config change.
	SleepMs int `json:"sleepMs,omitempty"`
	// WS drives a WebSocket client instead of an HTTP request.
	WS *WSStep `json:"ws,omitempty"`
	// Config temporarily changes the server-wide configuration; the original
	// is restored when the scenario ends, even on failure.
	Config *ConfigStep `json:"config,omitempty"`
	// ServerFile reads a file from the server's data directory (see
	// Client.ServerDataDir / Client.ReadFileCmd). Its Expect/Capture apply to
	// the file as if it were a response: status 200 with the content (JSON
	// when it parses), or 404 when the file does not exist.
	ServerFile *ServerFileStep `json:"serverFile,omitempty"`
	// FS changes the media tree (see FSStep); the scenario requires
	// "fs:write" and the files it changes are put back in its Cleanup.
	FS *FSStep `json:"fs,omitempty"`
	// RetryMs repeats the HTTP request of the step, every half second, until
	// its expectations hold or this many milliseconds passed: the way a
	// scenario waits for a background refresh to show.
	RetryMs int `json:"retryMs,omitempty"`
}

// FSStep is one change of the media tree, paths relative to the media root
// (templates are expanded). Exactly one of the operations is set.
type FSStep struct {
	// Write creates the file (and its folders) with Content.
	Write   string `json:"write,omitempty"`
	Content string `json:"content,omitempty"`
	// Mkdir creates a folder (and its parents).
	Mkdir string `json:"mkdir,omitempty"`
	// Remove deletes a file or a whole folder.
	Remove string `json:"remove,omitempty"`
	// Rename moves the path to To. With IfExists a missing source is not an
	// error (a cleanup step that must be safe to run whatever happened);
	// with Replace an existing To is removed first.
	Rename   string `json:"rename,omitempty"`
	IfExists bool   `json:"ifExists,omitempty"`
	Replace  bool   `json:"replace,omitempty"`
	// Copy copies the file Copy to To.
	Copy string `json:"copy,omitempty"`
	To   string `json:"to,omitempty"`
}

// ServerFileStep names a file on the server's filesystem.
type ServerFileStep struct {
	// Read is an absolute path (used as is) or a bare file name resolved
	// under the server data directory, in the Under sub-directory.
	Read  string `json:"read"`
	Under string `json:"under,omitempty"`
}

// WSStep is one WebSocket client action. Exactly one of Open, Wait and Close
// is set. Message assertions use the step's Expect.JSON and Capture, evaluated
// against the received message object ({"MessageType": ..., "Data": ...}).
type WSStep struct {
	// Open connects a named socket as the identity in As (admin, apikey, a
	// fixture user key or "token:{{capture.x}}").
	Open string `json:"open,omitempty"`
	As   string `json:"as,omitempty"`
	// Param is the query parameter carrying the token (default "ApiKey").
	Param string `json:"param,omitempty"`
	// RefuseStatus makes Open expect the handshake to be refused with this
	// HTTP status instead of upgrading.
	RefuseStatus int `json:"refuseStatus,omitempty"`
	// Wait blocks until the named socket receives a message of type Type (any
	// type when empty), within TimeoutMs (default 5000).
	Wait string `json:"wait,omitempty"`
	Type string `json:"type,omitempty"`
	// Absent inverts Wait: no such message may arrive within TimeoutMs.
	Absent    bool `json:"absent,omitempty"`
	TimeoutMs int  `json:"timeoutMs,omitempty"`
	// Close disconnects the named socket (sockets are also closed when the
	// scenario ends).
	Close string `json:"close,omitempty"`
}

// ConfigStep patches top-level keys of a server configuration section: the
// main one (GET/POST /System/Configuration) or, with Section, a named one
// (GET/POST /System/Configuration/<section>, e.g. "network").
type ConfigStep struct {
	Section string         `json:"section,omitempty"`
	Set     map[string]any `json:"set"`
}

// Request describes the HTTP request. Strings may contain {{var}} templates.
type Request struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Query   map[string]string `json:"query,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    any               `json:"body,omitempty"`
	// BodyRaw sends the string verbatim (for non-JSON or malformed bodies).
	BodyRaw string `json:"bodyRaw,omitempty"`
	// NoAuth suppresses the identity's Authorization header even when As is set.
	NoAuth bool `json:"noAuth,omitempty"`
}

// Expect is the assertion set for a step.
type Expect struct {
	Status  Ints              `json:"status,omitempty"`
	Headers map[string]string `json:"headers,omitempty"` // value: "present" | "absent" | /regex/ | literal
	JSON    []Assert          `json:"json,omitempty"`
	// Text asserts on the body as text (for responses that are not JSON:
	// subtitle documents, playlists).
	Text []TextAssert `json:"text,omitempty"`
	// Empty asserts a zero-length body.
	Empty bool `json:"empty,omitempty"`
	// Problem asserts the body is an RFC 9110 problem document for this status
	// (type, title and status members); 0 disables the check.
	Problem int `json:"problem,omitempty"`
}

// TextAssert is one assertion on the raw response body. Op is eq (the whole
// body, exactly), prefix, contains, notcontains, matches (a regular
// expression, unanchored; (?s) lets "." cross lines) or lines (the number of
// non-empty lines, Value a number). String values are template-expanded.
type TextAssert struct {
	Op    string `json:"op"`
	Value any    `json:"value"`
}

// Assert is one JSON-path assertion.
type Assert struct {
	Path  string `json:"path"`
	Op    string `json:"op"` // eq ne exists absent type len gte lte contains notcontains matches allmatch in hex32
	Value any    `json:"value,omitempty"`
}

// Ints unmarshals either a number or an array of numbers.
type Ints []int

// UnmarshalJSON implements json.Unmarshaler.
func (i *Ints) UnmarshalJSON(b []byte) error {
	var one int
	if err := json.Unmarshal(b, &one); err == nil {
		*i = Ints{one}
		return nil
	}
	var many []int
	if err := json.Unmarshal(b, &many); err != nil {
		return fmt.Errorf("status must be a number or an array of numbers: %w", err)
	}
	*i = many
	return nil
}

// LoadDir loads every *.json scenario file (an array of scenarios per file)
// under dir, sorted by file name then position.
func LoadDir(dir string) ([]Scenario, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var all []Scenario
	seen := map[string]string{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var list []Scenario
		dec := json.NewDecoder(strings.NewReader(string(data)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&list); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		for _, s := range list {
			if s.ID == "" {
				return nil, fmt.Errorf("%s: scenario without id", f)
			}
			if prev, dup := seen[s.ID]; dup {
				return nil, fmt.Errorf("%s: duplicate scenario id %q (also in %s)", f, s.ID, prev)
			}
			seen[s.ID] = f
			if s.Scope == "" {
				s.Scope = ScopeModern
			}
			all = append(all, s)
		}
	}
	return all, nil
}
