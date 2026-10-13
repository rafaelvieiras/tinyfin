package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/rafaelvieiras/tinyfin/internal/guid"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// userPolicyDto mirrors the OpenAPI schema's UserPolicy, populated with
// permissive defaults rather than the zero value: unlike the fields left
// out of userDto below (genuinely unknown data), a real jellyfin-web build
// dereferences user.Policy.IsAdministrator unconditionally on every page —
// a nil/omitted Policy crashes the library navigation drawer outright
// (found by testing against a real build), so this can't be left nullable
// in practice even though the schema marks it nullable. Tinyfin has no
// permissions system yet (users.go's handleUsersList doc comment already
// notes this for the admin-only list/create/delete routes), so every user
// gets full access here — the honest reflection of that reality, not an
// invented restriction. AuthenticationProviderId/PasswordResetProviderId
// are schema-required strings with no Tinyfin concept behind them yet;
// given placeholder values identifying this project, not copied from
// anywhere.
//
// IsHidden through SyncPlayAccess are a second batch found the same way as
// the fields above but later: reading jellyfin-sdk-kotlin's UserPolicy.kt
// directly (not just testing) while mapping the whole native playback
// flow for coverage, after GET /Users/Me kept failing inside
// PlayerViewModel.init with no visible error — caught internally by the
// SDK's ApiClientException handling (RawResponse.kt), so it never
// crashed, it just silently left autoPlayNextEpisodeEnabled at its
// default. Live-TV/channels/sync/media-conversion/transcoding-permission
// flags are honestly false (none of those features exist in Tinyfin, same
// "not built yet" reasoning as SupportsLibraryMonitor in system.go), not
// an invented restriction. Lockout/session-limit counters are 0 (no
// lockout/session-limit tracking exists) — 0 is also Jellyfin's own
// "unlimited" convention for MaxActiveSessions/RemoteClientBitrateLimit.
// SyncPlayAccess has no "not built" value other than its own "None" enum
// member, so that's what's reported.
type userPolicyDto struct {
	IsAdministrator                 bool   `json:"IsAdministrator"`
	IsHidden                        bool   `json:"IsHidden"`
	IsDisabled                      bool   `json:"IsDisabled"`
	EnableUserPreferenceAccess      bool   `json:"EnableUserPreferenceAccess"`
	EnableRemoteControlOfOtherUsers bool   `json:"EnableRemoteControlOfOtherUsers"`
	EnableSharedDeviceControl       bool   `json:"EnableSharedDeviceControl"`
	EnableMediaPlayback             bool   `json:"EnableMediaPlayback"`
	EnableAllFolders                bool   `json:"EnableAllFolders"`
	EnableAllDevices                bool   `json:"EnableAllDevices"`
	EnableRemoteAccess              bool   `json:"EnableRemoteAccess"`
	EnableContentDownloading        bool   `json:"EnableContentDownloading"`
	EnableContentDeletion           bool   `json:"EnableContentDeletion"`
	EnablePublicSharing             bool   `json:"EnablePublicSharing"`
	EnableLiveTvManagement          bool   `json:"EnableLiveTvManagement"`
	EnableLiveTvAccess              bool   `json:"EnableLiveTvAccess"`
	EnableAllChannels               bool   `json:"EnableAllChannels"`
	EnableAudioPlaybackTranscoding  bool   `json:"EnableAudioPlaybackTranscoding"`
	EnableVideoPlaybackTranscoding  bool   `json:"EnableVideoPlaybackTranscoding"`
	EnablePlaybackRemuxing          bool   `json:"EnablePlaybackRemuxing"`
	ForceRemoteSourceTranscoding    bool   `json:"ForceRemoteSourceTranscoding"`
	EnableSyncTranscoding           bool   `json:"EnableSyncTranscoding"`
	EnableMediaConversion           bool   `json:"EnableMediaConversion"`
	InvalidLoginAttemptCount        int    `json:"InvalidLoginAttemptCount"`
	LoginAttemptsBeforeLockout      int    `json:"LoginAttemptsBeforeLockout"`
	MaxActiveSessions               int    `json:"MaxActiveSessions"`
	RemoteClientBitrateLimit        int    `json:"RemoteClientBitrateLimit"`
	SyncPlayAccess                  string `json:"SyncPlayAccess"`
	AuthenticationProviderId        string `json:"AuthenticationProviderId"`
	PasswordResetProviderId         string `json:"PasswordResetProviderId"`

	EnableCollectionManagement       bool                `json:"EnableCollectionManagement"`
	EnableSubtitleManagement         bool                `json:"EnableSubtitleManagement"`
	EnableLyricManagement            bool                `json:"EnableLyricManagement"`
	BlockUnratedItems                []string            `json:"BlockUnratedItems"`
	BlockedTags                      []string            `json:"BlockedTags"`
	AllowedTags                      []string            `json:"AllowedTags"`
	EnabledFolders                   []string            `json:"EnabledFolders"`
	EnabledChannels                  []string            `json:"EnabledChannels"`
	EnabledDevices                   []string            `json:"EnabledDevices"`
	BlockedMediaFolders              []string            `json:"BlockedMediaFolders"`
	BlockedChannels                  []string            `json:"BlockedChannels"`
	EnableContentDeletionFromFolders []string            `json:"EnableContentDeletionFromFolders"`
	AccessSchedules                  []accessScheduleDto `json:"AccessSchedules"`
	MaxParentalRating                *int                `json:"MaxParentalRating,omitempty"`
	MaxParentalSubRating             *int                `json:"MaxParentalSubRating,omitempty"`
}

// accessScheduleDto is one entry of UserPolicy.AccessSchedules. DayOfWeek
// holds the canonical day name (users.schedule.R6-R7); an integer outside 0-9
// is kept as its decimal text and is written back as a number.
type accessScheduleDto struct {
	DayOfWeek string  `json:"DayOfWeek"`
	StartHour float64 `json:"StartHour"`
	EndHour   float64 `json:"EndHour"`
}

// scheduleDayNames are the day values by their numeric code (0 = Sunday).
var scheduleDayNames = []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday",
	"Saturday", "Everyday", "Weekday", "Weekend"}

// parseScheduleDay turns one wire DayOfWeek value (name in any case, integer,
// or integer as a string) into its canonical form.
func parseScheduleDay(raw json.RawMessage) (string, error) {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return "", errors.New("DayOfWeek must not be null")
	}
	if text[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", err
		}
		text = strings.TrimSpace(s)
		if text == "" {
			return "", errors.New("DayOfWeek must not be empty")
		}
		for _, n := range scheduleDayNames {
			if strings.EqualFold(n, text) {
				return n, nil
			}
		}
	}
	n, err := strconv.Atoi(text)
	if err != nil {
		return "", errors.New("DayOfWeek is not a day")
	}
	if n >= 0 && n < len(scheduleDayNames) {
		return scheduleDayNames[n], nil
	}
	return strconv.Itoa(n), nil
}

// parseScheduleHour accepts a JSON number or a numeric string.
func parseScheduleHour(raw json.RawMessage) (float64, error) {
	text := strings.TrimSpace(string(raw))
	if strings.HasPrefix(text, `"`) {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return 0, err
		}
		text = strings.TrimSpace(s)
	}
	return strconv.ParseFloat(text, 64)
}

func (a *accessScheduleDto) UnmarshalJSON(b []byte) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	*a = accessScheduleDto{DayOfWeek: "Sunday"}
	var err error
	if raw, ok := m["DayOfWeek"]; ok {
		if a.DayOfWeek, err = parseScheduleDay(raw); err != nil {
			return err
		}
	}
	if raw, ok := m["StartHour"]; ok {
		if a.StartHour, err = parseScheduleHour(raw); err != nil {
			return err
		}
	}
	if raw, ok := m["EndHour"]; ok {
		if a.EndHour, err = parseScheduleHour(raw); err != nil {
			return err
		}
	}
	return nil
}

func (a accessScheduleDto) MarshalJSON() ([]byte, error) {
	day := any(a.DayOfWeek)
	if n, err := strconv.Atoi(a.DayOfWeek); err == nil {
		day = n
	}
	return json.Marshal(struct {
		DayOfWeek any     `json:"DayOfWeek"`
		StartHour float64 `json:"StartHour"`
		EndHour   float64 `json:"EndHour"`
	}{day, a.StartHour, a.EndHour})
}

const defaultAuthProviderID = "Tinyfin.Auth.Default"

// defaultUserPolicy is the policy of a newly created account (users.create.R1)
// and the base every POSTed policy is decoded onto, so omitted fields take
// these values (users.policy.R4). IsHidden is true: new accounts stay out
// of the public list until an administrator clears it.
func defaultUserPolicy() userPolicyDto {
	return userPolicyDto{
		IsHidden:                       true,
		EnableUserPreferenceAccess:     true,
		EnableSharedDeviceControl:      true,
		EnableMediaPlayback:            true,
		EnableAllFolders:               true,
		EnableAllDevices:               true,
		EnableAllChannels:              true,
		EnableRemoteAccess:             true,
		EnableContentDownloading:       true,
		EnableLiveTvAccess:             true,
		EnableAudioPlaybackTranscoding: true,
		EnableVideoPlaybackTranscoding: true,
		EnablePlaybackRemuxing:         true,
		LoginAttemptsBeforeLockout:     -1,
		SyncPlayAccess:                 "CreateAndJoinGroups",
		AuthenticationProviderId:       defaultAuthProviderID,
		PasswordResetProviderId:        defaultAuthProviderID,
	}
}

// normalize replaces nil list members with empty slices so the wire value
// is [] (clients declare these as non-nullable lists), never null.
func (p *userPolicyDto) normalize() {
	for _, l := range []*[]string{&p.BlockUnratedItems, &p.BlockedTags, &p.AllowedTags, &p.EnabledFolders,
		&p.EnabledChannels, &p.EnabledDevices, &p.BlockedMediaFolders, &p.BlockedChannels, &p.EnableContentDeletionFromFolders} {
		if *l == nil {
			*l = []string{}
		}
	}
	if p.AccessSchedules == nil {
		p.AccessSchedules = []accessScheduleDto{}
	}
	// users.lockout.R1: zero is stored as the default threshold.
	if p.LoginAttemptsBeforeLockout == 0 {
		p.LoginAttemptsBeforeLockout = defaultLockoutAttempts
	}
	// Folder and channel ids are identifiers: always written canonically
	// (wire.id.R1), whichever spelling the client posted or an older
	// database row holds.
	for _, l := range []*[]string{&p.EnabledFolders, &p.EnabledChannels, &p.BlockedMediaFolders,
		&p.BlockedChannels, &p.EnableContentDeletionFromFolders} {
		*l = canonicalIDs(*l)
	}
}

// canonicalIDs returns ids with every GUID-shaped member folded to the
// canonical form; other members are left untouched.
func canonicalIDs(ids []string) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		if n, ok := guid.Normalize(id); ok {
			id = n
		}
		out[i] = id
	}
	return out
}

// storedUserPolicy decodes the policy persisted for u. A user without a
// stored policy (created before policies were persisted, or by the
// bootstrap path) gets the defaults but is not hidden, so existing
// accounts stay visible on the login screen. IsAdministrator and
// IsDisabled always come from the account columns, which authentication
// reads on every request.
func storedUserPolicy(u storage.User) userPolicyDto {
	p := defaultUserPolicy()
	if u.Policy == "" {
		p.IsHidden = false
	} else if err := json.Unmarshal([]byte(u.Policy), &p); err != nil {
		p = defaultUserPolicy()
		p.IsHidden = false
	}
	p.IsAdministrator = u.IsAdministrator
	p.IsDisabled = u.IsDisabled
	p.normalize()
	return p
}

// userConfigurationDto mirrors the OpenAPI schema's UserConfiguration.
// Every field is a plain scalar/array with an unambiguous zero value in
// the reference server's own schema (no nested required object like
// userPolicyDto had to fake sub-fields for), but it still can't be left
// nil: confirmed by testing against a real jellyfin-web build that both
// the playback pipeline (playbackmanager.js dereferences
// user.Configuration.RememberAudioSelections unconditionally when
// starting playback) and the Chromecast plugin (reads
// user.Configuration.CastReceiverId) crash outright on a nil
// Configuration, the same failure mode userPolicyDto's doc comment
// already documented for Policy.IsAdministrator. Values below are
// permissive/off defaults, not data this project actually tracks yet —
// Tinyfin has no per-user configuration store (docs/05 has no such
// store), so RememberAudioSelections/RememberSubtitleSelections are
// simply always true (no other project behavior depends on them) and
// every other field is left at its natural zero value — except the four
// []string fields, which userDto's constructor below must initialize to
// non-nil empty slices, not leave nil: Go's encoding/json marshals a nil
// slice as JSON null even without omitempty, but jellyfin-sdk-kotlin
// declares GroupedFolders (and, by the same schema pattern, the other
// three) as a non-nullable List<String> with no default, so a null
// throws a JsonDecodingException ("Expected start of the array '['").
// Found by reading a real jellyfin-android build's logcat: it surfaced as
// "Failed to load auto play preference" inside PlayerViewModel, caught
// (not crashing) but silently breaking that one preference, right
// alongside the actual "Source error" 404 the user was reproducing to
// diagnose — a second, unrelated bug that just happened to log at the
// same moment, found by reading the trace rather than assumed.
type userConfigurationDto struct {
	AudioLanguagePreference    string   `json:"AudioLanguagePreference,omitempty"`
	PlayDefaultAudioTrack      bool     `json:"PlayDefaultAudioTrack"`
	SubtitleLanguagePreference string   `json:"SubtitleLanguagePreference"`
	DisplayMissingEpisodes     bool     `json:"DisplayMissingEpisodes"`
	GroupedFolders             []string `json:"GroupedFolders"`
	SubtitleMode               string   `json:"SubtitleMode"`
	DisplayCollectionsView     bool     `json:"DisplayCollectionsView"`
	EnableLocalPassword        bool     `json:"EnableLocalPassword"`
	OrderedViews               []string `json:"OrderedViews"`
	LatestItemsExcludes        []string `json:"LatestItemsExcludes"`
	MyMediaExcludes            []string `json:"MyMediaExcludes"`
	HidePlayedInLatest         bool     `json:"HidePlayedInLatest"`
	RememberAudioSelections    bool     `json:"RememberAudioSelections"`
	RememberSubtitleSelections bool     `json:"RememberSubtitleSelections"`
	EnableNextEpisodeAutoPlay  bool     `json:"EnableNextEpisodeAutoPlay"`
	CastReceiverId             string   `json:"CastReceiverId"`
}

// userDto mirrors the OpenAPI schema's UserDto, trimmed to fields
// storage.User actually carries (plus ServerID/ServerName, which are
// server-level configuration, not per-user data — see server.go's Config).
//
// Deliberately omitted, rather than fabricated:
//   - PrimaryImageTag, PrimaryImageAspectRatio: no user-image support in
//     this slice (that's the "images and branding" group in docs/04, out
//     of scope here).
//   - HasPassword, HasConfiguredEasyPassword: HasPassword is a deprecated
//     duplicate of HasConfiguredPassword (kept below); "easy password"
//     (PIN-based login) isn't a concept storage.User has at all.
type userDto struct {
	Name                  string `json:"Name"`
	ServerID              string `json:"ServerId"`
	ServerName            string `json:"ServerName"`
	ID                    string `json:"Id"`
	HasPassword           bool   `json:"HasPassword"`
	HasConfiguredPassword bool   `json:"HasConfiguredPassword"`
	// HasConfiguredEasyPassword is always false: there is no PIN login.
	HasConfiguredEasyPassword bool                 `json:"HasConfiguredEasyPassword"`
	EnableAutoLogin           bool                 `json:"EnableAutoLogin"`
	LastLoginDate             *time.Time           `json:"LastLoginDate,omitempty"`
	LastActivityDate          *time.Time           `json:"LastActivityDate,omitempty"`
	Configuration             userConfigurationDto `json:"Configuration"`
	Policy                    userPolicyDto        `json:"Policy"`
}

// userDto builds the wire object of u, loading its stored preferences. It
// is the context-free form used by callers that have no request context at
// hand; handlers in this file use userDtoCtx.
func (s *Server) userDto(u storage.User) userDto {
	return s.userDtoCtx(context.Background(), u)
}

func (s *Server) userDtoCtx(ctx context.Context, u storage.User) userDto {
	cfg := defaultUserConfiguration()
	if raw, err := s.store.GetUserConfiguration(ctx, u.ID); err == nil && raw != "" {
		if json.Unmarshal([]byte(raw), &cfg) != nil {
			cfg = defaultUserConfiguration()
		}
		cfg.normalize()
	}
	return userDto{
		Name:                  u.Name,
		ServerID:              s.cfg.ServerID,
		ServerName:            s.cfg.ServerName,
		ID:                    string(u.ID),
		HasPassword:           u.PasswordHash != "",
		HasConfiguredPassword: u.PasswordHash != "",
		EnableAutoLogin:       u.EnableAutoLogin,
		LastLoginDate:         u.LastLoginDate,
		LastActivityDate:      u.LastActivityDate,
		Configuration:         cfg,
		Policy:                storedUserPolicy(u),
	}
}

func (s *Server) handleUsersMe(w http.ResponseWriter, r *http.Request) {
	ac, _ := AuthFromContext(r.Context())
	if ac.IsAPIKey {
		// users.me.R4: an API key belongs to no user.
		writeBadRequest(w)
		return
	}
	writeJSON(w, http.StatusOK, s.userDtoCtx(r.Context(), ac.User))
}

// handleUsersPublic is GET /Users/Public — unauthenticated, unlike every
// other /Users route: jellyfin-web's login screen calls it before any
// token exists, to render the "select user" list. Tinyfin has no
// IsHidden/user-visibility concept yet, so every user is public — same
// documented simplification as handleUsersList's admin-only note above.
// Must be registered as its own literal route in server.go: without it,
// GET /Users/{userId} swallows this path as userId="Public" and
// requireAuth rejects it with 401 instead of the 200 the login screen
// expects, found by testing against a real jellyfin-web build.
func (s *Server) handleUsersPublic(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.QueryUsers(r.Context())
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	dtos := make([]userDto, 0, len(users))
	for _, u := range users {
		// users.public.R2/R3: hidden and disabled accounts are not listed.
		if p := storedUserPolicy(u); p.IsHidden || p.IsDisabled {
			continue
		}
		dtos = append(dtos, s.userDtoCtx(r.Context(), u))
	}
	writeJSON(w, http.StatusOK, dtos)
}

// handleUsersList is GET /Users (users.list.R1-R6): every account, ordered
// by name, optionally narrowed by the isHidden / isDisabled filters.
func (s *Server) handleUsersList(w http.ResponseWriter, r *http.Request) {
	var hidden, disabled *bool
	for key, dst := range map[string]**bool{"isHidden": &hidden, "isDisabled": &disabled} {
		v := r.URL.Query().Get(key)
		if v == "" {
			continue
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			writeInvalidID(w, key, v)
			return
		}
		*dst = &b
	}

	users, err := s.store.QueryUsers(r.Context())
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	dtos := make([]userDto, 0, len(users))
	for _, u := range users {
		p := storedUserPolicy(u)
		if hidden != nil && p.IsHidden != *hidden {
			continue
		}
		if disabled != nil && p.IsDisabled != *disabled {
			continue
		}
		dtos = append(dtos, s.userDtoCtx(r.Context(), u))
	}
	writeJSON(w, http.StatusOK, dtos)
}

// createUserByName mirrors the OpenAPI schema's CreateUserByName request
// body (used by the real POST /Users/New route — see the comment on that
// route's registration in server.go for why creation lives there, not on
// plain POST /Users).
type createUserByName struct {
	Name string `json:"Name"`
	// Password is a pointer: a body that carries the member (even empty)
	// writes a UserPasswordChanged entry, one that omits it does not
	// (users.create.R11).
	Password *string `json:"Password"`
}

func (s *Server) handleUsersCreate(w http.ResponseWriter, r *http.Request) {
	var req createUserByName
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeInvalidBody(w)
		return
	}
	// users.create.R5/R6: valid, unique (case-insensitive) name.
	if !validUserName(req.Name) {
		writeBadRequest(w)
		return
	}
	taken, err := s.userNameTaken(r.Context(), req.Name, "")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if taken {
		writeBadRequest(w)
		return
	}

	var hash string
	if req.Password != nil && *req.Password != "" {
		h, err := bcrypt.GenerateFromPassword([]byte(*req.Password), bcrypt.DefaultCost)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		hash = string(h)
	}

	policy, err := json.Marshal(defaultUserPolicy())
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	id, err := s.store.CreateUser(r.Context(), storage.User{
		Name:         req.Name,
		PasswordHash: hash,
		Policy:       string(policy),
	})
	if err != nil {
		writeBadRequest(w)
		return
	}

	created, err := s.store.GetUser(r.Context(), id)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	// users.create.R10-R11.
	entries := []storage.ActivityEntry{{
		Name:     fmt.Sprintf("User %s has been created", created.Name),
		Type:     storage.ActivityTypeUserCreated,
		UserID:   &created.ID,
		Severity: storage.ActivitySeverityInfo,
	}}
	if req.Password != nil {
		entries = append(entries, passwordChangedEntry(created))
	}
	s.writeActivity(r.Context(), entries...)
	writeJSON(w, http.StatusOK, s.userDtoCtx(r.Context(), created))
}

// handleUsersGet is GET /Users/{userId} — distinct from handleUsersMe
// (GET /Users/Me): jellyfin-web fetches the logged-in user by explicit ID
// rather than the Me shortcut once it's past the login screen, found by
// testing against a real jellyfin-web build.
func (s *Server) handleUsersGet(w http.ResponseWriter, r *http.Request) {
	// users.get.R4/R5: the empty GUID and non-GUIDs are 400, unknown ids 404.
	id, ok := parseUserGUID(r.PathValue("userId"))
	if !ok {
		writeInvalidID(w, "userId", r.PathValue("userId"))
		return
	}
	user, err := s.store.GetUser(r.Context(), id)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, s.userDtoCtx(r.Context(), user))
}

func (s *Server) handleUsersDelete(w http.ResponseWriter, r *http.Request) {
	// users.ids.R1: a non-GUID or all-zero id is a 400, not a 404.
	id, ok := parseUserGUID(r.PathValue("userId"))
	if !ok {
		writeInvalidID(w, "userId", r.PathValue("userId"))
		return
	}
	ctx := r.Context()
	target, err := s.store.GetUser(ctx, id)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	// users.delete.R5: validate before any side effect. The only
	// administrator (disabled ones count) cannot be deleted.
	if target.IsAdministrator {
		users, err := s.store.QueryUsers(ctx)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		others := 0
		for _, u := range users {
			if u.ID != target.ID && u.IsAdministrator {
				others++
			}
		}
		if others == 0 {
			writeBadRequest(w)
			return
		}
	}
	// users.delete.R4: tokens of the deleted account stop working.
	// users.delete.R8: one SessionEnded per token, then UserDeleted (which
	// belongs to no user, so it is found by hasUserId=false).
	if err := s.revokeUserSessions(ctx, target, nil); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if err := s.store.DeleteUser(ctx, target.ID); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	s.writeActivity(ctx, storage.ActivityEntry{
		Name:     fmt.Sprintf("User %s has been deleted", target.Name),
		Type:     storage.ActivityTypeUserDeleted,
		Severity: storage.ActivitySeverityInfo,
	})
	w.WriteHeader(http.StatusNoContent)
}

// handleUsersPolicy is POST /Users/{userId}/Policy (users.policy.R1-R12):
// administrator only, full replacement of the account's policy.
func (s *Server) handleUsersPolicy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// users.ids.R1: a non-GUID or all-zero id is a 400, not a 404.
	targetID, ok := parseUserGUID(r.PathValue("userId"))
	if !ok {
		writeInvalidID(w, "userId", r.PathValue("userId"))
		return
	}

	// Decode onto the new-user defaults so omitted fields take them (R4).
	policy := defaultUserPolicy()
	policy.AuthenticationProviderId = ""
	policy.PasswordResetProviderId = ""
	if err := json.NewDecoder(r.Body).Decode(&policy); err != nil {
		writeInvalidBody(w)
		return
	}

	target, err := s.store.GetUser(ctx, targetID)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if policy.AuthenticationProviderId == "" || policy.PasswordResetProviderId == "" {
		writeBadRequest(w)
		return
	}

	if target.IsAdministrator {
		// R7: an administrator cannot be disabled, even while being demoted.
		if policy.IsDisabled {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		// R6: the last administrator (disabled ones count) keeps its rights.
		if !policy.IsAdministrator {
			users, err := s.store.QueryUsers(ctx)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			others := 0
			for _, u := range users {
				if u.ID != target.ID && u.IsAdministrator {
					others++
				}
			}
			if others == 0 {
				w.WriteHeader(http.StatusForbidden)
				return
			}
		}
	}

	wasDisabled := target.IsDisabled
	target.IsAdministrator = policy.IsAdministrator
	target.IsDisabled = policy.IsDisabled
	policy.normalize()
	raw, err := json.Marshal(policy)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	target.Policy = string(raw)
	if err := s.store.UpdateUser(ctx, target); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// R8: disabling revokes every token of the account.
	if policy.IsDisabled && !wasDisabled {
		if err := s.revokeUserSessions(ctx, target, nil); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}
	// users.auth.R17: tokens of devices that are no longer allowed go away.
	if !policy.EnableAllDevices {
		keep := make([]storage.DeviceID, 0, len(policy.EnabledDevices))
		for _, d := range policy.EnabledDevices {
			keep = append(keep, storage.DeviceID(d))
		}
		if err := s.revokeUserSessions(ctx, target, keep); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}
	// users.policy.R13: the target's own open sockets are told.
	s.notifyUserUpdated(ctx, target)
	w.WriteHeader(http.StatusNoContent)
}
