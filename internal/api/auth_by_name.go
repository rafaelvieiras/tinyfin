package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
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

// authenticationResult mirrors the OpenAPI schema's AuthenticationResult.
// SessionInfo is omitted (nullable): building an honest SessionInfoDto
// requires session/realtime state (docs/06), which is out of scope for
// this slice — omitting it rather than fabricating session data a client
// might otherwise trust.
type authenticationResult struct {
	User        userDto `json:"User"`
	AccessToken string  `json:"AccessToken"`
	ServerID    string  `json:"ServerId"`
}

// handleAuthenticateByName is the login route (docs/04): no token required
// coming in, since it's how a client obtains one.
func (s *Server) handleAuthenticateByName(w http.ResponseWriter, r *http.Request) {
	var req authenticateUserByName
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	user, err := s.store.GetUserByName(r.Context(), req.Username)
	if err != nil {
		// Same response for "no such user" and "wrong password" (below) —
		// distinguishing them in the response would let a client enumerate
		// valid usernames.
		s.logAuthFailure(r, req.Username)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	if !passwordMatches(user.PasswordHash, req.Pw) {
		s.logAuthFailure(r, req.Username)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// AuthenticateByName is called before any token exists, so the
	// Client/Device/DeviceId/Version fields come off this specific
	// request's header (parsed unconditionally by withAuthContext even
	// though it carried no valid — or any — token).
	ac, _ := AuthFromContext(r.Context())
	if ac.DeviceID == "" {
		// Nothing to attach the new token to. The real header format
		// (docs/04) always includes DeviceId; a client that omits it is
		// sending a malformed request, not a "no device" edge case.
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	now := time.Now().UTC()
	if err := s.store.UpsertDevice(r.Context(), storage.Device{
		ID:               storage.DeviceID(ac.DeviceID),
		Name:             ac.Device,
		AppName:          ac.Client,
		AppVersion:       ac.Version,
		LastUserID:       user.ID,
		LastUserName:     user.Name,
		DateLastActivity: now,
	}); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	token, err := s.store.IssueToken(r.Context(), user.ID, storage.DeviceID(ac.DeviceID))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	user.LastLoginDate = &now
	user.LastActivityDate = &now
	if err := s.store.UpdateUser(r.Context(), user); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	s.logAuthSuccess(r, user, ac.Client)

	writeJSON(w, http.StatusOK, authenticationResult{
		User:        s.userDto(user),
		AccessToken: token.Token,
		ServerID:    s.cfg.ServerID,
	})
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
