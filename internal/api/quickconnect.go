package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

const (
	// quickConnectTTL is how long a request stays redeemable, authorized
	// or not.
	quickConnectTTL = 10 * time.Minute
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
}

// quickConnectState holds pending requests. They are short-lived and
// bounded (quickConnectMaxPending), so a process-local table is enough;
// a restart simply invalidates the sign-ins in flight.
type quickConnectState struct {
	mu       sync.Mutex
	bySecret map[string]*quickConnectRequest
}

func newQuickConnectState() *quickConnectState {
	return &quickConnectState{bySecret: map[string]*quickConnectRequest{}}
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
		DateAdded:     r.Added.UTC().Format("2006-01-02T15:04:05.0000000Z"),
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
	now := time.Now()
	q := s.quick
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
	if !s.quickConnectEnabled() {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	secret := r.URL.Query().Get("secret")
	q := s.quick
	q.mu.Lock()
	defer q.mu.Unlock()
	q.sweepLocked(time.Now())
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
	if !s.quickConnectEnabled() {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ac, _ := AuthFromContext(r.Context())
	target := ac.User.ID
	if raw := r.URL.Query().Get("userId"); raw != "" {
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
			w.WriteHeader(http.StatusNotFound)
			return
		}
		target = id
	}
	code := r.URL.Query().Get("code")
	q := s.quick
	q.mu.Lock()
	defer q.mu.Unlock()
	q.sweepLocked(time.Now())
	req := q.byCodeLocked(code)
	if code == "" || req == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	req.Authorized = true
	req.UserID = target
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
	q.sweepLocked(time.Now())
	req, ok := q.bySecret[body.Secret]
	if !ok || !req.Authorized {
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
	s.issueLogin(w, r, user, storedUserPolicy(user), time.Now(), req.AppName, req.DeviceName, req.DeviceID, req.AppVersion)
}
