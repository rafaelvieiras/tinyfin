package api

import (
	"encoding/json"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"

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

// accessScheduleDto is one entry of UserPolicy.AccessSchedules.
type accessScheduleDto struct {
	DayOfWeek string  `json:"DayOfWeek"`
	StartHour float64 `json:"StartHour"`
	EndHour   float64 `json:"EndHour"`
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
	SubtitleLanguagePreference string   `json:"SubtitleLanguagePreference,omitempty"`
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
	CastReceiverId             string   `json:"CastReceiverId,omitempty"`
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
	Name                  string               `json:"Name"`
	ServerID              string               `json:"ServerId"`
	ServerName            string               `json:"ServerName"`
	ID                    string               `json:"Id"`
	HasConfiguredPassword bool                 `json:"HasConfiguredPassword"`
	EnableAutoLogin       bool                 `json:"EnableAutoLogin"`
	LastLoginDate         *time.Time           `json:"LastLoginDate,omitempty"`
	LastActivityDate      *time.Time           `json:"LastActivityDate,omitempty"`
	Configuration         userConfigurationDto `json:"Configuration"`
	Policy                userPolicyDto        `json:"Policy"`
}

func (s *Server) userDto(u storage.User) userDto {
	return userDto{
		Name:                  u.Name,
		ServerID:              s.cfg.ServerID,
		ServerName:            s.cfg.ServerName,
		ID:                    string(u.ID),
		HasConfiguredPassword: u.PasswordHash != "",
		EnableAutoLogin:       u.EnableAutoLogin,
		LastLoginDate:         u.LastLoginDate,
		LastActivityDate:      u.LastActivityDate,
		Configuration: userConfigurationDto{
			RememberAudioSelections:    true,
			RememberSubtitleSelections: true,
			SubtitleMode:               "Default",
			GroupedFolders:             []string{},
			OrderedViews:               []string{},
			LatestItemsExcludes:        []string{},
			MyMediaExcludes:            []string{},
		},
		Policy: storedUserPolicy(u),
	}
}

func (s *Server) handleUsersMe(w http.ResponseWriter, r *http.Request) {
	ac, _ := AuthFromContext(r.Context())
	writeJSON(w, http.StatusOK, s.userDto(ac.User))
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
		dtos = append(dtos, s.userDto(u))
	}
	writeJSON(w, http.StatusOK, dtos)
}

func (s *Server) handleUsersList(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.QueryUsers(r.Context())
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	dtos := make([]userDto, 0, len(users))
	for _, u := range users {
		dtos = append(dtos, s.userDto(u))
	}
	writeJSON(w, http.StatusOK, dtos)
}

// createUserByName mirrors the OpenAPI schema's CreateUserByName request
// body (used by the real POST /Users/New route — see the comment on that
// route's registration in server.go for why creation lives there, not on
// plain POST /Users).
type createUserByName struct {
	Name     string `json:"Name"`
	Password string `json:"Password"`
}

func (s *Server) handleUsersCreate(w http.ResponseWriter, r *http.Request) {
	var req createUserByName
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	var hash string
	if req.Password != "" {
		h, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
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
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	created, err := s.store.GetUser(r.Context(), id)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, s.userDto(created))
}

// handleUsersGet is GET /Users/{userId} — distinct from handleUsersMe
// (GET /Users/Me): jellyfin-web fetches the logged-in user by explicit ID
// rather than the Me shortcut once it's past the login screen, found by
// testing against a real jellyfin-web build.
func (s *Server) handleUsersGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("userId")
	user, err := s.store.GetUser(r.Context(), storage.UserID(id))
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, s.userDto(user))
}

func (s *Server) handleUsersDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("userId")
	if id == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	target, err := s.store.GetUser(ctx, storage.UserID(id))
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
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	}
	// users.delete.R4: tokens of the deleted account stop working.
	if err := s.store.RevokeUserTokens(ctx, target.ID, nil); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if err := s.store.DeleteUser(ctx, target.ID); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleUsersPolicy is POST /Users/{userId}/Policy (users.policy.R1-R12):
// administrator only, full replacement of the account's policy.
func (s *Server) handleUsersPolicy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Decode onto the new-user defaults so omitted fields take them (R4).
	policy := defaultUserPolicy()
	policy.AuthenticationProviderId = ""
	policy.PasswordResetProviderId = ""
	if err := json.NewDecoder(r.Body).Decode(&policy); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	target, err := s.store.GetUser(ctx, storage.UserID(r.PathValue("userId")))
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if policy.AuthenticationProviderId == "" || policy.PasswordResetProviderId == "" {
		w.WriteHeader(http.StatusBadRequest)
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
		if err := s.store.RevokeUserTokens(ctx, target.ID, nil); err != nil {
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
		if err := s.store.RevokeUserTokens(ctx, target.ID, keep); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
