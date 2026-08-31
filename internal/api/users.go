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
type userPolicyDto struct {
	IsAdministrator          bool   `json:"IsAdministrator"`
	IsDisabled               bool   `json:"IsDisabled"`
	EnableMediaPlayback      bool   `json:"EnableMediaPlayback"`
	EnableAllFolders         bool   `json:"EnableAllFolders"`
	EnableAllDevices         bool   `json:"EnableAllDevices"`
	EnableRemoteAccess       bool   `json:"EnableRemoteAccess"`
	EnableContentDownloading bool   `json:"EnableContentDownloading"`
	AuthenticationProviderId string `json:"AuthenticationProviderId"`
	PasswordResetProviderId  string `json:"PasswordResetProviderId"`
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
// every other field is left at its natural zero value.
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
		},
		Policy: userPolicyDto{
			IsAdministrator:          u.IsAdministrator,
			IsDisabled:               u.IsDisabled,
			EnableMediaPlayback:      true,
			EnableAllFolders:         true,
			EnableAllDevices:         true,
			EnableRemoteAccess:       true,
			EnableContentDownloading: true,
			AuthenticationProviderId: "Tinyfin.Auth.Default",
			PasswordResetProviderId:  "Tinyfin.Auth.Default",
		},
	}
}

func (s *Server) handleUsersMe(w http.ResponseWriter, r *http.Request) {
	ac, _ := AuthFromContext(r.Context())
	writeJSON(w, http.StatusOK, s.userDto(ac.User))
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

	id, err := s.store.CreateUser(r.Context(), storage.User{
		Name:         req.Name,
		PasswordHash: hash,
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
	if err := s.store.DeleteUser(r.Context(), storage.UserID(id)); err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
