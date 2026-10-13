package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// zeroUserGUID is the empty GUID, which is never a valid account id.
const zeroUserGUID = "00000000-0000-0000-0000-000000000000"

// parseUserGUID validates s as a GUID (dashed or 32 hex digits, any case)
// and returns it in the canonical dashed lower-case form accounts are
// stored under. The empty GUID is rejected (users.get.R4, users.update.R12).
func parseUserGUID(s string) (storage.UserID, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) == 32 {
		s = s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
	}
	if len(s) != 36 {
		return "", false
	}
	for i, c := range s {
		switch {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if c != '-' {
				return "", false
			}
		case (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'):
		default:
			return "", false
		}
	}
	if s == zeroUserGUID {
		return "", false
	}
	return storage.UserID(s), true
}

// validUserName implements users.create.R5: letters (any script), marks,
// digits, underscore, space, '-', '\”, '.', '@' and '+'; no leading or
// trailing whitespace; not "." or "..".
func validUserName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	runes := []rune(name)
	if unicode.IsSpace(runes[0]) || unicode.IsSpace(runes[len(runes)-1]) {
		return false
	}
	for _, c := range runes {
		switch {
		case unicode.IsLetter(c), unicode.IsDigit(c), unicode.Is(unicode.Mn, c), unicode.Is(unicode.Pc, c):
		case c == ' ', c == '-', c == '\'', c == '.', c == '_', c == '@', c == '+':
		default:
			return false
		}
	}
	return true
}

// userNameTaken reports whether another account (not except) already uses
// name, compared case-insensitively (users.create.R6, users.update.R6).
func (s *Server) userNameTaken(ctx context.Context, name string, except storage.UserID) (bool, error) {
	users, err := s.store.QueryUsers(ctx)
	if err != nil {
		return false, err
	}
	for _, u := range users {
		if u.ID != except && strings.EqualFold(u.Name, name) {
			return true, nil
		}
	}
	return false, nil
}

// defaultUserConfiguration is the preferences set of a new account
// (spec users.md, "Default Configuration") and the base every POSTed
// configuration is decoded onto, so omitted fields reset to it.
func defaultUserConfiguration() userConfigurationDto {
	c := userConfigurationDto{
		PlayDefaultAudioTrack:      true,
		SubtitleMode:               "Default",
		HidePlayedInLatest:         true,
		RememberAudioSelections:    true,
		RememberSubtitleSelections: true,
		EnableNextEpisodeAutoPlay:  true,
	}
	c.normalize()
	return c
}

func (c *userConfigurationDto) normalize() {
	for _, l := range []*[]string{&c.GroupedFolders, &c.OrderedViews, &c.LatestItemsExcludes, &c.MyMediaExcludes} {
		if *l == nil {
			*l = []string{}
		}
	}
}

func validSubtitleMode(m string) bool {
	switch m {
	case "Default", "Always", "OnlyForced", "None", "Smart":
		return true
	}
	return false
}

// decodeUserConfiguration reads a configuration object from r onto the
// defaults. ok is false for a body that is not a JSON object of the right
// shape or that carries an invalid enum value.
func decodeUserConfiguration(r io.Reader) (userConfigurationDto, bool) {
	c := defaultUserConfiguration()
	if err := json.NewDecoder(r).Decode(&c); err != nil {
		return c, false
	}
	return validatedConfiguration(c)
}

func validatedConfiguration(c userConfigurationDto) (userConfigurationDto, bool) {
	if !validSubtitleMode(c.SubtitleMode) {
		return c, false
	}
	// CastReceiverId is only kept when it names a configured cast
	// receiver; Tinyfin has none, so it is always dropped.
	c.CastReceiverId = ""
	c.normalize()
	return c, true
}

func (s *Server) saveUserConfiguration(ctx context.Context, id storage.UserID, c userConfigurationDto) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return s.store.SetUserConfiguration(ctx, id, string(raw))
}

// resolveUserTarget applies the shared target rules of the update and
// configuration routes: the target is explicit or the caller; a
// non-administrator naming someone else is refused with 403 before the
// lookup, an unknown id answers 404. It writes the failure response itself.
func (s *Server) resolveUserTarget(w http.ResponseWriter, r *http.Request, rawID string) (storage.User, bool) {
	ac, _ := AuthFromContext(r.Context())
	target := ac.User.ID
	if rawID != "" {
		id, ok := parseUserGUID(rawID)
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			return storage.User{}, false
		}
		target = id
	}
	if target != ac.User.ID && !ac.User.IsAdministrator {
		w.WriteHeader(http.StatusForbidden)
		return storage.User{}, false
	}
	u, err := s.store.GetUser(r.Context(), target)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return storage.User{}, false
	}
	return u, true
}

// handleUsersConfiguration is POST /Users/Configuration (target in the
// userId query) and its legacy alias POST /Users/{userId}/Configuration
// (users.configuration.R1-R5): replaces the account's preferences.
func (s *Server) handleUsersConfiguration(w http.ResponseWriter, r *http.Request) {
	rawID := r.PathValue("userId")
	if rawID == "" {
		rawID = r.URL.Query().Get("userId")
	}
	cfg, ok := decodeUserConfiguration(r.Body)
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	target, ok := s.resolveUserTarget(w, r, rawID)
	if !ok {
		return
	}
	if err := s.saveUserConfiguration(r.Context(), target.ID, cfg); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// updateUserRequest is the body of POST /Users: a whole user object of
// which only Name and Configuration are applied. Policy is kept raw so an
// absent member can be told from an explicit null.
type updateUserRequest struct {
	Name          *string         `json:"Name"`
	Configuration json.RawMessage `json:"Configuration"`
	Policy        json.RawMessage `json:"Policy"`
}

func writeUpdateValidationError(w http.ResponseWriter, fields ...string) {
	errs := map[string][]string{}
	for _, f := range fields {
		errs[f] = []string{"The " + f[strings.LastIndex(f, ".")+1:] + " field is required."}
	}
	writeJSON(w, http.StatusBadRequest, bitrateTestValidationError{
		Type:   "https://tools.ietf.org/html/rfc9110#section-15.5.1",
		Title:  "One or more validation errors occurred.",
		Status: http.StatusBadRequest,
		Errors: errs,
	})
}

// handleUsersUpdate is POST /Users (target in the userId query) and its
// legacy alias POST /Users/{userId} (users.update.R1-R12): renames the
// account and replaces its preferences; every other member is ignored.
func (s *Server) handleUsersUpdate(w http.ResponseWriter, r *http.Request) {
	rawID := r.PathValue("userId")
	if rawID == "" {
		rawID = r.URL.Query().Get("userId")
	}

	var req updateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	// users.update.R12: body validation precedes lookup and authorization.
	if req.Policy == nil {
		writeUpdateValidationError(w, "Policy")
		return
	}
	if string(req.Policy) != "null" {
		var p struct {
			AuthenticationProviderId string
			PasswordResetProviderId  string
		}
		if err := json.Unmarshal(req.Policy, &p); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var missing []string
		if p.AuthenticationProviderId == "" {
			missing = append(missing, "Policy.AuthenticationProviderId")
		}
		if p.PasswordResetProviderId == "" {
			missing = append(missing, "Policy.PasswordResetProviderId")
		}
		if len(missing) > 0 {
			writeUpdateValidationError(w, missing...)
			return
		}
	}
	// An omitted Configuration resets the preferences (users.update.R9).
	cfg := defaultUserConfiguration()
	if req.Configuration != nil && string(req.Configuration) != "null" {
		var ok bool
		if cfg, ok = decodeUserConfiguration(bytes.NewReader(req.Configuration)); !ok {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	}

	ac, _ := AuthFromContext(r.Context())
	target, ok := s.resolveUserTarget(w, r, rawID)
	if !ok {
		return
	}
	// users.update.R11: a non-administrator without preference access
	// cannot edit their own account.
	if target.ID == ac.User.ID && !ac.User.IsAdministrator && !storedUserPolicy(target).EnableUserPreferenceAccess {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	// users.update.R6/R7: Name is required and follows the creation rules;
	// an identical name is a no-op, a case-only change is applied.
	if req.Name == nil || !validUserName(*req.Name) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	renamed := false
	if *req.Name != target.Name {
		taken, err := s.userNameTaken(r.Context(), *req.Name, target.ID)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if taken {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		target.Name = *req.Name
		if err := s.store.UpdateUser(r.Context(), target); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		renamed = true
	}
	if err := s.saveUserConfiguration(r.Context(), target.ID, cfg); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	// The change of name (not the preferences) is announced to the user's
	// open sessions.
	if renamed {
		s.notifyUserUpdated(r.Context(), target)
	}
	w.WriteHeader(http.StatusNoContent)
}
