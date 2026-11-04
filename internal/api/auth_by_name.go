package api

import (
	"context"
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
		writeBadRequest(w)
		return
	}

	var req authenticateUserByName
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeInvalidBody(w)
		return
	}
	if strings.TrimSpace(req.Username) == "" {
		writeBadRequest(w)
		return
	}

	user, err := s.store.GetUserByName(ctx, req.Username)
	if err != nil {
		// Same response for "no such user" and "wrong password" (below) —
		// distinguishing them in the response would let a client enumerate
		// valid usernames. No failure counter exists for an unknown user, and
		// no activity entry is written for one (users.auth.R27).
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
		// users.auth.R14: only an otherwise permitted account counts the
		// failure; the answer is 401 either way (a lockout that this
		// attempt triggers shows from the next attempt on). The lockout
		// entry it may write comes before this attempt's own
		// AuthenticationFailed entry (users.auth.R29).
		if scheduleAllows(policy, now) && deviceAllowed {
			if err := s.recordLoginFailure(r, user, policy); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		}
		s.logAuthFailure(r, req.Username)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	s.issueLogin(w, r, user, policy, now, ac.Client, ac.Device, ac.DeviceID, ac.Version, true)
}

// issueLogin runs the checks that follow a verified credential (remote
// access, schedule, device allow-list, session limit) and, when they pass,
// issues the device's token and writes the AuthenticationResult. It is
// shared by the password login and the QuickConnect exchange so both
// apply exactly the same account rules and side effects. withAddress is
// false for the exchange: its activity entries carry no remote address
// (users.auth.R25).
func (s *Server) issueLogin(w http.ResponseWriter, r *http.Request, user storage.User, policy userPolicyDto, now time.Time, client, device, deviceID, version string, withAddress bool) {
	ctx := r.Context()
	// The account is known here (unlike the auth middleware, which sees the
	// DeviceId before any token resolves), so this is where a legacy
	// per-user suffix is dropped: the token, the entry and the session all
	// belong to the bare device (deviceid.go, devices.R11).
	deviceID = canonicalDeviceID(deviceID, string(user.ID))
	// users.auth.R21: no remote access from outside the local network.
	if !policy.EnableRemoteAccess && !s.fromLocalNetwork(r) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	// users.auth.R18: applies to administrators too.
	if !scheduleAllows(policy, now) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	// users.auth.R17.
	if !(policy.EnableAllDevices || containsString(policy.EnabledDevices, deviceID)) {
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
			if string(t.DeviceID) != deviceID {
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
		ID:               storage.DeviceID(deviceID),
		Name:             device,
		AppName:          client,
		AppVersion:       version,
		LastUserID:       user.ID,
		LastUserName:     user.Name,
		DateLastActivity: nowUTC,
	}); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// IssueSessionToken revokes the user's previous token on this DeviceId
	// (users.auth.R10); the replaced session is reported as ended with the
	// device name and address it was created with (users.auth.R31-R32).
	// The device record is read before UpsertDevice renames it.
	var replaced *storage.AuthToken
	if toks, err := s.store.QueryUserTokens(ctx, user.ID); err == nil {
		for i := range toks {
			if string(toks[i].DeviceID) == deviceID {
				replaced = &toks[i]
			}
		}
	}
	replacedName := ""
	if replaced != nil {
		replacedName = s.tokenDeviceName(ctx, *replaced)
	}
	address := ""
	if withAddress {
		address = remoteEndPoint(r)
	}
	token, err := s.store.IssueSessionToken(ctx, user.ID, storage.DeviceID(deviceID), device, address)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if replaced != nil {
		s.writeActivity(ctx, sessionEndedEntry(user.ID, user.Name, replacedName, replaced.RemoteAddress))
	}
	// A previous sign-in from this install may have left entries under the
	// legacy DeviceId that appends the account id (deviceid.go): signing in
	// again is the moment to collapse them into the one device, instead of
	// leaving a phantom entry per past sign-in (devices.R12).
	s.sweepLegacyDeviceEntries(ctx, user.ID, deviceID)

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

	session := s.sessions.loginSession(s.cfg.ServerID, deviceID, string(user.ID), user.Name, client, device, version, remoteEndPoint(r))

	s.logAuthSuccess(r, user, device, withAddress)

	writeJSON(w, http.StatusOK, authenticationResult{
		User:        s.userDto(user),
		SessionInfo: session,
		AccessToken: token.Token,
		ServerID:    s.cfg.ServerID,
	})
}

// sweepLegacyDeviceEntries removes the user's own entries whose DeviceId is
// this device's id with the user id appended — the spelling an official
// client keeps after a sign-in (deviceid.go, devices.R11) — together with
// their tokens and sessions, so a re-login cannot leave the device listed
// twice (devices.R12). Entries of other users are left alone: the sweep is
// scoped to the account that just signed in.
func (s *Server) sweepLegacyDeviceEntries(ctx context.Context, userID storage.UserID, deviceID string) {
	if deviceID == "" {
		return
	}
	devices, err := s.store.QueryDevices(ctx, userID)
	if err != nil {
		return
	}
	for _, d := range devices {
		id := string(d.ID)
		if id == "" || id == deviceID {
			continue
		}
		if canonicalDeviceID(id, string(userID)) != deviceID {
			continue
		}
		if err := s.store.DeleteDevice(ctx, storage.DeviceID(id)); err != nil {
			continue
		}
		s.sessions.dropDevice(ctx, id)
	}
}

// recordLoginFailure counts one failed password attempt on a permitted
// account (users.auth.R13-R14, R28, R30): no lockout when
// LoginAttemptsBeforeLockout is negative, 0 behaves as 3, and reaching the
// limit disables the account (its tokens are kept).
func (s *Server) recordLoginFailure(r *http.Request, user storage.User, policy userPolicyDto) error {
	// users.lockout.R2-R3: the persisted counter counts every failure, also when
	// no lockout is configured, and the failure that reaches the threshold
	// counts twice.
	limit := policy.LoginAttemptsBeforeLockout
	if limit == 0 {
		limit = defaultLockoutAttempts
	}
	policy.InvalidLoginAttemptCount++
	locked := limit > 0 && policy.InvalidLoginAttemptCount >= limit
	if locked {
		policy.InvalidLoginAttemptCount++
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
		// users.auth.R28, R30: the lockout is logged but revokes no token;
		// they are refused while the account is disabled.
		uid := user.ID
		s.writeActivity(r.Context(), storage.ActivityEntry{
			Name:     fmt.Sprintf("User %s has been locked out", user.Name),
			Type:     storage.ActivityTypeUserLockedOut,
			UserID:   &uid,
			Severity: storage.ActivitySeverityError,
		})
	}
	return nil
}

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// activityAddress is the ShortOverview of the login activity entries:
// "IP address: <remote address>" (users.auth.R25). An empty address is
// written for the QuickConnect exchange, as the reference does.
func activityAddress(r *http.Request, withAddress bool) string {
	if !withAddress {
		return "IP address: "
	}
	if ip := remoteIP(r); ip != nil {
		return "IP address: " + ip.String()
	}
	return "IP address: " + r.RemoteAddr
}

// logAuthFailure appends an AuthenticationFailed activity log entry
// (internal/storage/activity.go) for a wrong password on an existing,
// enabled account (users.auth.R26). It is not called for an unknown
// username or a disabled account (users.auth.R27). UserID is left nil
// (stored as NULL, so hasUserId=false finds it; on the wire it is the
// all-zero id, activityEntryToDto), and Name quotes the username exactly
// as sent. Failing to write this entry must never change the auth
// response the client sees: the error is logged and dropped.
func (s *Server) logAuthFailure(r *http.Request, username string) {
	entry := storage.ActivityEntry{
		Name:          fmt.Sprintf("Failed login attempt from %s", username),
		ShortOverview: activityAddress(r, true),
		Type:          storage.ActivityTypeAuthenticationFailed,
		Date:          time.Now().UTC(),
		Severity:      storage.ActivitySeverityError,
	}
	if _, err := s.store.AppendActivityEntry(r.Context(), entry); err != nil {
		log.Printf("api: appending activity log entry for failed login by %q: %v", username, err)
		return
	}
	s.notifyActivityLog(r.Context())
}

// logAuthSuccess appends the two activity log entries of a successful
// login, in order: SessionStarted ("<user> is online from <device>") and
// AuthenticationSucceeded ("<user> successfully authenticated")
// (users.auth.R23-R25), right before the AccessToken they accompany goes
// out on the wire. device is the percent-decoded Device of the login.
// Same log-and-continue error handling as logAuthFailure.
func (s *Server) logAuthSuccess(r *http.Request, user storage.User, device string, withAddress bool) {
	overview := activityAddress(r, withAddress)
	uid := user.ID
	entries := []storage.ActivityEntry{
		{
			Name:          fmt.Sprintf("%s is online from %s", user.Name, device),
			ShortOverview: overview,
			Type:          storage.ActivityTypeSessionStarted,
			UserID:        &uid,
			Severity:      storage.ActivitySeverityInfo,
		},
		{
			Name:          fmt.Sprintf("%s successfully authenticated", user.Name),
			ShortOverview: overview,
			Type:          storage.ActivityTypeAuthenticationSucceeded,
			UserID:        &uid,
			Severity:      storage.ActivitySeverityInfo,
		},
	}
	for _, entry := range entries {
		entry.Date = time.Now().UTC()
		if _, err := s.store.AppendActivityEntry(r.Context(), entry); err != nil {
			log.Printf("api: appending activity log entry for successful login by %q: %v", user.Name, err)
			return
		}
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
