package api

import (
	"encoding/json"
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
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	if !passwordMatches(user.PasswordHash, req.Pw) {
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

	writeJSON(w, http.StatusOK, authenticationResult{
		User:        s.userDto(user),
		AccessToken: token.Token,
		ServerID:    s.cfg.ServerID,
	})
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
