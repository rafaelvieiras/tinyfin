package api

import (
	"encoding/json"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// userDto mirrors the OpenAPI schema's UserDto, trimmed to fields
// storage.User actually carries (plus ServerID/ServerName, which are
// server-level configuration, not per-user data — see server.go's Config).
//
// Deliberately omitted, rather than fabricated:
//   - Configuration, Policy: these correspond to the reference server's
//     UserConfiguration/UserPolicy objects. storage.User flattens only
//     IsAdministrator/IsDisabled onto itself (see storage/user.go's doc
//     comment); it doesn't carry the rest of UserPolicy (e.g.
//     AuthenticationProviderId, PasswordResetProviderId, which the schema
//     marks required on UserPolicy) or any of UserConfiguration. Building
//     a partial object with invented values for the required-but-unknown
//     fields would be fabrication, so both are left nil (both nullable).
//   - PrimaryImageTag, PrimaryImageAspectRatio: no user-image support in
//     this slice (that's the "images and branding" group in docs/04, out
//     of scope here).
//   - HasPassword, HasConfiguredEasyPassword: HasPassword is a deprecated
//     duplicate of HasConfiguredPassword (kept below); "easy password"
//     (PIN-based login) isn't a concept storage.User has at all.
type userDto struct {
	Name                  string     `json:"Name"`
	ServerID              string     `json:"ServerId"`
	ServerName            string     `json:"ServerName"`
	ID                    string     `json:"Id"`
	HasConfiguredPassword bool       `json:"HasConfiguredPassword"`
	EnableAutoLogin       bool       `json:"EnableAutoLogin"`
	LastLoginDate         *time.Time `json:"LastLoginDate,omitempty"`
	LastActivityDate      *time.Time `json:"LastActivityDate,omitempty"`
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
