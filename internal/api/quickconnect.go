package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

const (
	// quickConnectTTL is how long a request stays visible to Connect,
	// counted from its DateAdded: ten minutes after Initiate while pending
	// and, because authorizing moves DateAdded forward by
	// quickConnectAuthorizeBump, eleven minutes after the authorization once
	// authorized. The exchange window is ten minutes from the authorization
	// itself (quickconnect.expiry.R2).
	quickConnectTTL = 10 * time.Minute
	// quickConnectAuthorizeBump is how far past the authorization time
	// DateAdded is set when a request is authorized (quickconnect.connect.R3).
	quickConnectAuthorizeBump = time.Minute
	// quickConnectMaxPending bounds the in-memory table: the Initiate
	// route is anonymous, so an unbounded table would be a memory lever.
	quickConnectMaxPending = 500
)

// quickConnectRequest is one pending device sign-in: what the device
// presented to Initiate, the six-digit code a signed-in user types in, and
// (once authorized) the account the token will be issued to.
type quickConnectRequest struct {
	Secret     string
	Code       string
	DeviceID   string
	DeviceName string
	AppName    string
	AppVersion string
	Added      time.Time

	Authorized bool
	UserID     storage.UserID
	// AuthorizedAt starts the exchange window (quickconnect.expiry.R2).
	AuthorizedAt time.Time
}

// quickConnectState holds pending requests. They are short-lived and
// bounded (quickConnectMaxPending), so a process-local table is enough;
// a restart simply invalidates the sign-ins in flight.
type quickConnectState struct {
	mu       sync.Mutex
	bySecret map[string]*quickConnectRequest
	// now is the clock; tests replace it to cross the ten-minute lifetimes.
	now func() time.Time
}

func newQuickConnectState() *quickConnectState {
	return &quickConnectState{bySecret: map[string]*quickConnectRequest{}, now: time.Now}
}

// sweepLocked drops expired requests. Callers hold q.mu.
func (q *quickConnectState) sweepLocked(now time.Time) {
	for k, v := range q.bySecret {
		if now.Sub(v.Added) > quickConnectTTL {
			delete(q.bySecret, k)
		}
	}
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func randomDigits(n int) (string, error) {
	max := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
	v, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*d", n, v), nil
}

// quickConnectResult mirrors the OpenAPI schema's QuickConnectResult.
type quickConnectResult struct {
	Authenticated bool   `json:"Authenticated"`
	Secret        string `json:"Secret"`
	Code          string `json:"Code"`
	DeviceId      string `json:"DeviceId"`
	DeviceName    string `json:"DeviceName"`
	AppName       string `json:"AppName"`
	AppVersion    string `json:"AppVersion"`
	DateAdded     string `json:"DateAdded"`
}

func (r *quickConnectRequest) result() quickConnectResult {
	return quickConnectResult{
		Authenticated: r.Authorized,
		Secret:        r.Secret,
		Code:          r.Code,
		DeviceId:      r.DeviceID,
		DeviceName:    r.DeviceName,
		AppName:       r.AppName,
		AppVersion:    r.AppVersion,
		DateAdded:     formatWireTime(r.Added),
	}
}

// handleQuickConnectInitiate is POST /QuickConnect/Initiate: anonymous; the
// device identity comes from the Authorization header and is what the
// issued token will be bound to (users.quickconnect.R1).
func (s *Server) handleQuickConnectInitiate(w http.ResponseWriter, r *http.Request) {
	if !s.quickConnectEnabled() {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ac, _ := AuthFromContext(r.Context())
	if ac == nil || ac.Client == "" || ac.Device == "" || ac.DeviceID == "" || ac.Version == "" {
		writeBadRequest(w)
		return
	}
	secret, err := randomHex(16)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	q := s.quick
	now := q.now()
	q.mu.Lock()
	defer q.mu.Unlock()
	q.sweepLocked(now)
	if len(q.bySecret) >= quickConnectMaxPending {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	var code string
	for {
		if code, err = randomDigits(6); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if q.byCodeLocked(code) == nil {
			break
		}
	}
	req := &quickConnectRequest{
		Secret: secret, Code: code,
		DeviceID: ac.DeviceID, DeviceName: ac.Device, AppName: ac.Client, AppVersion: ac.Version,
		Added: now,
	}
	q.bySecret[secret] = req
	writeJSON(w, http.StatusOK, req.result())
}

func (q *quickConnectState) byCodeLocked(code string) *quickConnectRequest {
	for _, v := range q.bySecret {
		if v.Code == code {
			return v
		}
	}
	return nil
}

// handleQuickConnectConnect is GET /QuickConnect/Connect?secret=: the poll
// the device runs while it waits to be authorized.
func (s *Server) handleQuickConnectConnect(w http.ResponseWriter, r *http.Request) {
	// quickconnect.disabled.R1: a missing parameter is a validation error
	// before the availability check.
	secret := r.URL.Query().Get("secret")
	if secret == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if !s.quickConnectEnabled() {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	q := s.quick
	q.mu.Lock()
	defer q.mu.Unlock()
	q.sweepLocked(q.now())
	req, ok := q.bySecret[secret]
	if !ok || secret == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, req.result())
}

// handleQuickConnectAuthorize is POST /QuickConnect/Authorize?code=
// [&userId=]: a signed-in user approves the pending request. An
// administrator may name another account; a non-administrator naming
// someone else is refused (users.quickconnect.R5).
func (s *Server) handleQuickConnectAuthorize(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if !s.quickConnectEnabled() {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ac, _ := AuthFromContext(r.Context())
	target := ac.User.ID
	// quickconnect.authorize.R6-R7: the empty id counts as "not given", any
	// other id must be well formed and name an account (both 400).
	if raw := r.URL.Query().Get("userId"); raw != "" && !isEmptyGUID(raw) {
		id, ok := parseUserGUID(raw)
		if !ok {
			writeInvalidID(w, "userId", raw)
			return
		}
		if id != ac.User.ID && !ac.User.IsAdministrator {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if _, err := s.store.GetUser(r.Context(), id); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		target = id
	}
	q := s.quick
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.now()
	q.sweepLocked(now)
	req := q.byCodeLocked(code)
	if req == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if req.Authorized {
		// quickconnect.authorize.R8: an already authorized code is refused
		// with the reference's opaque 500; the first authorization stands.
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Error processing request."))
		return
	}
	req.Authorized = true
	req.UserID = target
	req.AuthorizedAt = now
	req.Added = now.Add(quickConnectAuthorizeBump)
	writeJSON(w, http.StatusOK, true)
}

// handleAuthenticateWithQuickConnect is POST
// /Users/AuthenticateWithQuickConnect: the device trades its authorized
// secret for a normal login result. The secret is consumed by the first
// attempt that finds it authorized (users.quickconnect.R7, a deliberate
// divergence from the reference, which lets it be replayed).
func (s *Server) handleAuthenticateWithQuickConnect(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Secret string `json:"Secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Secret == "" {
		writeInvalidBody(w)
		return
	}
	if !s.quickConnectEnabled() {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	q := s.quick
	q.mu.Lock()
	now := q.now()
	q.sweepLocked(now)
	req, ok := q.bySecret[body.Secret]
	if !ok || !req.Authorized || now.Sub(req.AuthorizedAt) > quickConnectTTL {
		q.mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
		return
	}
	delete(q.bySecret, body.Secret)
	q.mu.Unlock()

	user, err := s.store.GetUser(r.Context(), req.UserID)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	// users.auth.R8: a disabled account is refused with 403.
	if user.IsDisabled {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	s.issueLogin(w, r, user, storedUserPolicy(user), time.Now(), req.AppName, req.DeviceName, req.DeviceID, req.AppVersion, false)
}

// isEmptyGUID reports whether s is the all-zero id, in dashed or plain form.
func isEmptyGUID(s string) bool {
	s = strings.ReplaceAll(strings.TrimSpace(s), "-", "")
	return len(s) == 32 && strings.Trim(s, "0") == ""
}
