package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// First-run setup wizard (docs/contract/specs/startup.md). State lives in the
// server_settings table:
//
//   - StartupWizardCompleted ("true"): POST /Startup/Complete was accepted.
//   - StartupWizardOpened: GET /Startup/User was called (the user page was
//     opened); POST /Startup/User answers 404 before that.
//   - StartupWizardUser: id of the account the wizard created.
//
// A database that holds accounts but has no wizard account was provisioned
// out of band (an older Tinyfin, a seed tool): it counts as set up, and that
// verdict is persisted so deleting accounts later never re-opens the wizard.
const (
	settingWizardOpened = "StartupWizardOpened"
	settingWizardUser   = "StartupWizardUser"

	settingServerName     = "ServerName"
	settingUICulture      = "UICulture"
	settingMetadataCtry   = "MetadataCountryCode"
	settingMetadataLang   = "PreferredMetadataLanguage"
	settingRemoteAccess   = "EnableRemoteAccess"
	defaultUICulture      = "en-US"
	defaultMetadataCtry   = "US"
	defaultMetadataLang   = "en"
	wizardDefaultUserName = "root"
)

// setupComplete reports whether the first-run wizard is finished.
func (s *Server) setupComplete(ctx context.Context) bool {
	if s.wizardDone.Load() {
		return true
	}
	if v, ok, err := s.store.GetSetting(ctx, storage.SettingStartupWizardCompleted); err == nil && ok && v == "true" {
		s.wizardDone.Store(true)
		return true
	}
	// An account the wizard did not create: set up out of band.
	if _, ok, _ := s.store.GetSetting(ctx, settingWizardUser); ok {
		return false
	}
	users, err := s.store.QueryUsers(ctx)
	if err != nil || len(users) == 0 {
		return false
	}
	_ = s.store.SetSetting(ctx, storage.SettingStartupWizardCompleted, "true")
	s.wizardDone.Store(true)
	return true
}

// setting returns a stored runtime setting or def when it was never set.
func (s *Server) setting(ctx context.Context, key, def string) string {
	if v, ok, err := s.store.GetSetting(ctx, key); err == nil && ok {
		return v
	}
	return def
}

// effectiveServerName is the name clients see: the runtime setting when it
// is non-empty, the command-line default otherwise.
func (s *Server) effectiveServerName() string {
	if p := s.serverName.Load(); p != nil && *p != "" {
		return *p
	}
	return s.cfg.ServerName
}

// setServerName updates the runtime server name (also persisted by callers).
func (s *Server) setServerName(name string) { s.serverName.Store(&name) }

// openUntilSetUp lets anyone through while setup is not complete and applies
// requireAuth afterwards (GET /System/Info, startup.state.R2).
func (s *Server) openUntilSetUp(next http.HandlerFunc) http.HandlerFunc {
	authed := requireAuth(next)
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.setupComplete(r.Context()) {
			next(w, r)
			return
		}
		authed(w, r)
	}
}

// wizardAccess is the access rule of every /Startup route
// (startup.access.R1-R3): open to every caller until setup is complete,
// administrator-only afterwards.
func (s *Server) wizardAccess(next http.HandlerFunc) http.HandlerFunc {
	admin := requireAdmin(next)
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.setupComplete(r.Context()) {
			next(w, r)
			return
		}
		admin(w, r)
	}
}

func (s *Server) registerStartupRoutes() {
	s.mux.HandleFunc("GET /Startup/Configuration", s.wizardAccess(s.handleStartupConfigurationGet))
	s.mux.HandleFunc("POST /Startup/Configuration", s.wizardAccess(s.handleStartupConfigurationPost))
	s.mux.HandleFunc("GET /Startup/User", s.wizardAccess(s.handleStartupUserGet))
	s.mux.HandleFunc("GET /Startup/FirstUser", s.wizardAccess(s.handleStartupUserGet))
	s.mux.HandleFunc("POST /Startup/User", s.wizardAccess(s.handleStartupUserPost))
	s.mux.HandleFunc("POST /Startup/RemoteAccess", s.wizardAccess(s.handleStartupRemoteAccess))
	s.mux.HandleFunc("POST /Startup/Complete", s.wizardAccess(s.handleStartupComplete))
}

type startupConfigurationDto struct {
	ServerName                string `json:"ServerName"`
	UICulture                 string `json:"UICulture"`
	MetadataCountryCode       string `json:"MetadataCountryCode"`
	PreferredMetadataLanguage string `json:"PreferredMetadataLanguage"`
}

// startupConfiguration reads the four values (startup.config.R1 defaults).
func (s *Server) startupConfiguration(ctx context.Context) startupConfigurationDto {
	return startupConfigurationDto{
		ServerName:                s.setting(ctx, settingServerName, ""),
		UICulture:                 s.setting(ctx, settingUICulture, defaultUICulture),
		MetadataCountryCode:       s.setting(ctx, settingMetadataCtry, defaultMetadataCtry),
		PreferredMetadataLanguage: s.setting(ctx, settingMetadataLang, defaultMetadataLang),
	}
}

func (s *Server) handleStartupConfigurationGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.startupConfiguration(r.Context()))
}

// handleStartupConfigurationPost replaces all four values: an absent or null
// member is stored as "" (startup.config.R2).
func (s *Server) handleStartupConfigurationPost(w http.ResponseWriter, r *http.Request) {
	var body startupConfigurationDto
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeInvalidBody(w)
		return
	}
	for _, kv := range [][2]string{
		{settingServerName, body.ServerName},
		{settingUICulture, body.UICulture},
		{settingMetadataCtry, body.MetadataCountryCode},
		{settingMetadataLang, body.PreferredMetadataLanguage},
	} {
		if err := s.store.SetSetting(r.Context(), kv[0], kv[1]); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}
	s.setServerName(body.ServerName)
	w.WriteHeader(http.StatusNoContent)
}

// handleStartupUserGet answers {"Name": ...}: the configured wizard account's
// name, or the offered default. Unlike the reference it creates nothing
// (startup.safety.R1); it only records that the page was opened.
func (s *Server) handleStartupUserGet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := wizardDefaultUserName
	if id, ok, err := s.store.GetSetting(ctx, settingWizardUser); err == nil && ok {
		if u, err := s.store.GetUser(ctx, storage.UserID(id)); err == nil {
			name = u.Name
		}
	} else if !s.setupComplete(ctx) {
		_ = s.store.SetSetting(ctx, settingWizardOpened, "true")
	}
	writeJSON(w, http.StatusOK, map[string]string{"Name": name})
}

// handleStartupUserPost creates the first administrator (startup.user.R2-R6).
func (s *Server) handleStartupUserPost(w http.ResponseWriter, r *http.Request) {
	s.wizardMu.Lock()
	defer s.wizardMu.Unlock()
	ctx := r.Context()

	if s.setupComplete(ctx) {
		w.WriteHeader(http.StatusForbidden) // startup.access.R4
		return
	}
	if _, ok, _ := s.store.GetSetting(ctx, settingWizardOpened); !ok {
		writeProblemDoc(w, http.StatusNotFound, problemTypeNotFound, "Not Found", nil)
		return
	}
	if _, ok, _ := s.store.GetSetting(ctx, settingWizardUser); ok {
		w.WriteHeader(http.StatusForbidden) // startup.user.R6
		return
	}
	var body struct {
		Name     *string `json:"Name"`
		Password *string `json:"Password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeInvalidBody(w)
		return
	}
	if body.Password == nil || strings.TrimSpace(*body.Password) == "" {
		writeJSON(w, http.StatusBadRequest, "Password must not be empty")
		return
	}
	name := wizardDefaultUserName
	if body.Name != nil {
		name = *body.Name
		if !validUserName(name) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("Error processing request."))
			return
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(*body.Password), bcrypt.DefaultCost)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	// The first account is an administrator that may also delete content and
	// remote-control other users (startup.user.R4).
	policy := defaultUserPolicy()
	policy.EnableContentDeletion = true
	policy.EnableRemoteControlOfOtherUsers = true
	rawPolicy, err := json.Marshal(policy)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	id, err := s.store.CreateUser(ctx, storage.User{
		Name:            name,
		PasswordHash:    string(hash),
		IsAdministrator: true,
		Policy:          string(rawPolicy),
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if err := s.store.SetSetting(ctx, settingWizardUser, string(id)); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if created, err := s.store.GetUser(ctx, id); err == nil {
		uid := created.ID
		s.writeActivity(ctx,
			storage.ActivityEntry{
				Name:     fmt.Sprintf("User %s has been created", created.Name),
				Type:     storage.ActivityTypeUserCreated,
				UserID:   &uid,
				Severity: storage.ActivitySeverityInfo,
			},
			passwordChangedEntry(created))
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleStartupRemoteAccess stores EnableRemoteAccess in the network
// configuration; EnableAutomaticPortMapping is accepted and ignored
// (startup.remote.R2).
func (s *Server) handleStartupRemoteAccess(w http.ResponseWriter, r *http.Request) {
	var body struct {
		EnableRemoteAccess         bool `json:"EnableRemoteAccess"`
		EnableAutomaticPortMapping bool `json:"EnableAutomaticPortMapping"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeInvalidBody(w)
		return
	}
	if err := s.store.SetSetting(r.Context(), settingRemoteAccess, strconv.FormatBool(body.EnableRemoteAccess)); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	s.remoteAccessOff.Store(!body.EnableRemoteAccess)
	w.WriteHeader(http.StatusNoContent)
}

// handleStartupComplete marks setup complete (startup.complete.R1). Safety
// divergence (startup.complete.R2): it is refused while no account exists,
// because completing then leaves a server nobody can sign in to.
func (s *Server) handleStartupComplete(w http.ResponseWriter, r *http.Request) {
	s.wizardMu.Lock()
	defer s.wizardMu.Unlock()
	ctx := r.Context()
	if s.setupComplete(ctx) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	users, err := s.store.QueryUsers(ctx)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if len(users) == 0 {
		writeProblemDoc(w, http.StatusBadRequest, problemTypeBadRequest, "Bad Request", nil)
		return
	}
	if err := s.store.SetSetting(ctx, storage.SettingStartupWizardCompleted, "true"); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	s.wizardDone.Store(true)
	w.WriteHeader(http.StatusNoContent)
}
