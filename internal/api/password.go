package api

import (
	"encoding/json"
	"net/http"

	"golang.org/x/crypto/bcrypt"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// updatePasswordRequest is the body of POST /Users/Password. NewPw is a
// pointer so an explicit null reads like an omitted value (both clear the
// password). The legacy hashed CurrentPassword field is deliberately not
// decoded (users.password.R12): a request that carries only it counts as
// sending no CurrentPw.
type updatePasswordRequest struct {
	CurrentPw     string  `json:"CurrentPw"`
	NewPw         *string `json:"NewPw"`
	ResetPassword bool    `json:"ResetPassword"`
}

// handleUsersPassword is POST /Users/Password (target in the userId query)
// and its legacy alias POST /Users/{userId}/Password (users.password.R1-R14).
func (s *Server) handleUsersPassword(w http.ResponseWriter, r *http.Request) {
	rawID := r.PathValue("userId")
	if rawID == "" {
		rawID = r.URL.Query().Get("userId")
	}
	var req updatePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeInvalidBody(w)
		return
	}

	ac, _ := AuthFromContext(r.Context())
	// R1, R4, R10: target selection, 403 for a non-administrator naming
	// someone else, 404 for an unknown account.
	target, ok := s.resolveUserTarget(w, r, rawID)
	if !ok {
		return
	}
	self := target.ID == ac.User.ID
	// R13: an account without preference access cannot change its own
	// password.
	if self && !ac.User.IsAdministrator && !storedUserPolicy(target).EnableUserPreferenceAccess {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	// R9: ResetPassword clears the password with no re-authentication and
	// leaves every session alone (an administrator target is refused, R8).
	if req.ResetPassword {
		if target.IsAdministrator {
			writeBadRequest(w)
			return
		}
		s.storePassword(w, r, target, "")
		return
	}

	// R2, R6: re-authentication is required of non-administrators and of an
	// administrator who names their own id explicitly (always the case for
	// the alias, whose id comes from the path).
	if !ac.User.IsAdministrator || (self && rawID != "") {
		if !passwordMatches(target.PasswordHash, req.CurrentPw) {
			// A wrong current password is a failed login attempt for the
			// target and can trigger the configured lockout.
			if err := s.recordLoginFailure(r, target, storedUserPolicy(target)); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusForbidden)
			return
		}
	}

	newPw := ""
	if req.NewPw != nil {
		newPw = *req.NewPw
	}
	// R8: an administrator account never ends up without a password.
	if newPw == "" && target.IsAdministrator {
		writeBadRequest(w)
		return
	}

	// R7: every token of the target but the caller's own is revoked.
	var keep []storage.DeviceID
	if self {
		keep = []storage.DeviceID{ac.AuthToken.DeviceID}
	}
	if err := s.setPassword(r, target, newPw); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if err := s.store.RevokeUserTokens(r.Context(), target.ID, keep); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// storePassword sets the password and answers 204 / 500.
func (s *Server) storePassword(w http.ResponseWriter, r *http.Request, u storage.User, pw string) {
	if err := s.setPassword(r, u, pw); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// setPassword hashes pw (empty clears the password) and persists it.
func (s *Server) setPassword(r *http.Request, u storage.User, pw string) error {
	hash := ""
	if pw != "" {
		h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		hash = string(h)
	}
	u.PasswordHash = hash
	return s.store.UpdateUser(r.Context(), u)
}
