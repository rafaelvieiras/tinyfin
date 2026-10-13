package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// authenticateUserByName mirrors the OpenAPI schema's AuthenticateUserByName
// request body — field names (Username, Pw) are exactly as the schema
// defines them, not renamed for Go style, since wire compatibility depends
// on the JSON keys a client sends matching what this decodes.
type authenticateUserByName struct {
	Username string `json:"Username"`
	Pw       string `json:"Pw"`
}

// authenticationResult mirrors the OpenAPI schema's AuthenticationResult
// (users.auth.R1).
type authenticationResult struct {
	User        userDto        `json:"User"`
	SessionInfo sessionInfoDto `json:"SessionInfo"`
	AccessToken string         `json:"AccessToken"`
	ServerID    string         `json:"ServerId"`
}

// defaultLockoutAttempts is what LoginAttemptsBeforeLockout=0 stands for
// (users.auth.R14).
const defaultLockoutAttempts = 3

// handleAuthenticateByName is the login route (docs/04): no token required
// coming in, since it's how a client obtains one. Order of checks
// (users.md): header fields (400) -> username (400) -> account and
// password (401) -> disabled / remote access / schedule (403) -> device
// allow-list and session limit (403) -> token issued.
func (s *Server) handleAuthenticateByName(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// users.auth.R6: the header must carry all four client fields, even
	// when the credentials are wrong; checked before the body is read.
	ac, _ := AuthFromContext(ctx)
	if ac == nil || ac.Client == "" || ac.Device == "" || ac.DeviceID == "" || ac.Version == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	var req authenticateUserByName
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Username) == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	user, err := s.store.GetUserByName(ctx, req.Username)
	if err != nil {
		// Same response for "no such user" and "wrong password" (below) —
		// distinguishing them in the response would let a client enumerate
		// valid usernames. No failure counter exists for an unknown user.
		s.logAuthFailure(r, req.Username)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	policy := storedUserPolicy(user)
	now := time.Now()

	// users.auth.R8: a disabled account is refused with 403 whether or not
	// the password is correct, and the refusal is not a failed attempt.
	if user.IsDisabled {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	deviceAllowed := policy.EnableAllDevices || containsString(policy.EnabledDevices, ac.DeviceID)
	if !passwordMatches(user.PasswordHash, req.Pw) {
		s.logAuthFailure(r, req.Username)
		// users.auth.R14: only an otherwise permitted account counts the
		// failure; the answer is 401 either way (a lockout that this
		// attempt triggers shows from the next attempt on).
		if scheduleAllows(policy, now) && deviceAllowed {
			if err := s.recordLoginFailure(r, user, policy); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		}
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// users.auth.R21: no remote access from outside the local network.
	if !policy.EnableRemoteAccess && !fromLocalNetwork(r) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	// users.auth.R18: applies to administrators too.
	if !scheduleAllows(policy, now) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	// users.auth.R17.
	if !deviceAllowed {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	// users.auth.R16: a login that replaces the same device's token does
	// not add a session.
	if policy.MaxActiveSessions > 0 {
		toks, err := s.store.QueryUserTokens(ctx, user.ID)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		active := 0
		for _, t := range toks {
			if string(t.DeviceID) != ac.DeviceID {
				active++
			}
		}
		if active >= policy.MaxActiveSessions {
			w.WriteHeader(http.StatusForbidden)
			return
		}
	}

	nowUTC := now.UTC()
	if err := s.store.UpsertDevice(ctx, storage.Device{
		ID:               storage.DeviceID(ac.DeviceID),
		Name:             ac.Device,
		AppName:          ac.Client,
		AppVersion:       ac.Version,
		LastUserID:       user.ID,
		LastUserName:     user.Name,
		DateLastActivity: nowUTC,
	}); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// IssueToken revokes the user's previous token on this DeviceId
	// (users.auth.R10).
	token, err := s.store.IssueToken(ctx, user.ID, storage.DeviceID(ac.DeviceID))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// users.auth.R15, R22: a success resets the failure counter and
	// records the login time.
	user.LastLoginDate = &nowUTC
	user.LastActivityDate = &nowUTC
	if policy.InvalidLoginAttemptCount != 0 {
		policy.InvalidLoginAttemptCount = 0
		raw, err := json.Marshal(policy)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		user.Policy = string(raw)
	}
	if err := s.store.UpdateUser(ctx, user); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	session := s.sessions.loginSession(s.cfg.ServerID, ac.DeviceID, string(user.ID), user.Name, ac.Client, ac.Device, ac.Version, remoteEndPoint(r))

	s.logAuthSuccess(r, user, ac.Client)

	writeJSON(w, http.StatusOK, authenticationResult{
		User:        s.userDto(user),
		SessionInfo: session,
		AccessToken: token.Token,
		ServerID:    s.cfg.ServerID,
	})
}

// recordLoginFailure counts one failed password attempt on a permitted
// account (users.auth.R13-R14): no lockout when LoginAttemptsBeforeLockout
// is negative, 0 behaves as 3, and reaching the limit disables the
// account and revokes its tokens.
func (s *Server) recordLoginFailure(r *http.Request, user storage.User, policy userPolicyDto) error {
	limit := policy.LoginAttemptsBeforeLockout
	if limit < 0 {
		return nil
	}
	if limit == 0 {
		limit = defaultLockoutAttempts
	}
	policy.InvalidLoginAttemptCount++
	locked := policy.InvalidLoginAttemptCount >= limit
	if locked {
		policy.IsDisabled = true
		user.IsDisabled = true
	}
	raw, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	user.Policy = string(raw)
	if err := s.store.UpdateUser(r.Context(), user); err != nil {
		return err
	}
	if locked {
		return s.store.RevokeUserTokens(r.Context(), user.ID, nil)
	}
	return nil
}

// fromLocalNetwork reports whether the request came from the loopback or a
// private/link-local address (same notion as system.go's
// system-info IsInNetwork); an unparseable peer address counts as local.
func fromLocalNetwork(r *http.Request) bool {
	ip := remoteIP(r)
	if ip == nil {
		return true
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// logAuthFailure appends an AuthenticationFailed activity log entry
// (docs/05's ActivityLogStore, internal/storage/activity.go) for a
// rejected login attempt — username unknown or password wrong, the two
// call sites above deliberately can't be told apart on the wire (this
// handler's own doc comment), and aren't told apart in the log either.
// UserID is left nil: no user was ever authenticated for this attempt,
// only a (possibly nonexistent) attempted username, which Name mentions
// instead. Failing to write this entry must never change the auth response
// the client sees — the error is logged and dropped, not propagated
// (docs/05's ActivityLogStore doc comment).
func (s *Server) logAuthFailure(r *http.Request, username string) {
	entry := storage.ActivityEntry{
		Name:     fmt.Sprintf("Failed login attempt by %s", username),
		Type:     storage.ActivityTypeAuthenticationFailed,
		Date:     time.Now().UTC(),
		Severity: storage.ActivitySeverityError,
	}
	if _, err := s.store.AppendActivityEntry(r.Context(), entry); err != nil {
		log.Printf("api: appending activity log entry for failed login by %q: %v", username, err)
		return
	}
	s.notifyActivityLog(r.Context())
}

// logAuthSuccess appends a SessionStarted activity log entry for a
// successful login, right before the AccessToken it protects goes out on
// the wire. ShortOverview carries the remote IP the way the reference
// server's own confirmed-live example does; client is ac.Client (the
// AuthContext parsed off this same request, above) rather than a device
// name, matching that example's "<user> is online in <client>" phrasing.
// Same log-and-continue error handling as logAuthFailure.
func (s *Server) logAuthSuccess(r *http.Request, user storage.User, client string) {
	ipStr := "unknown"
	if ip := remoteIP(r); ip != nil {
		ipStr = ip.String()
	}
	uid := user.ID
	entry := storage.ActivityEntry{
		Name:          fmt.Sprintf("%s is online from %s", user.Name, client),
		ShortOverview: "Remote IP: " + ipStr,
		Type:          storage.ActivityTypeSessionStarted,
		Date:          time.Now().UTC(),
		UserID:        &uid,
		Severity:      storage.ActivitySeverityInfo,
	}
	if _, err := s.store.AppendActivityEntry(r.Context(), entry); err != nil {
		log.Printf("api: appending activity log entry for successful login by %q: %v", user.Name, err)
		return
	}
	s.notifyActivityLog(r.Context())
}

// passwordMatches verifies a submitted plaintext password against a stored
// bcrypt hash. A user with no password configured (PasswordHash == "", per
// storage/user.go's doc comment) only matches an empty submitted password —
// it does not match everything, which would turn "no password set" into
// "no password required".
func passwordMatches(hash, submitted string) bool {
	if hash == "" {
		return submitted == ""
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(submitted)) == nil
}
