package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// zeroUserIDHex is the all-zero user id an API key reports: a key belongs to
// no user.
const zeroUserIDHex = "00000000000000000000000000000000"

// apiKeyInfoDto is one element of the key listing (users.keys.R2). There is
// no DateRevoked member: a revoked key leaves the listing instead.
type apiKeyInfoDto struct {
	ID               int64  `json:"Id"`
	AccessToken      string `json:"AccessToken"`
	DeviceID         string `json:"DeviceId"`
	AppName          string `json:"AppName"`
	AppVersion       string `json:"AppVersion"`
	DeviceName       string `json:"DeviceName"`
	UserID           string `json:"UserId"`
	IsActive         bool   `json:"IsActive"`
	DateCreated      string `json:"DateCreated"`
	DateLastActivity string `json:"DateLastActivity"`
}

type apiKeyInfoQueryResult struct {
	Items            []apiKeyInfoDto `json:"Items"`
	TotalRecordCount int             `json:"TotalRecordCount"`
	StartIndex       int             `json:"StartIndex"`
}

func apiKeyDto(k storage.APIKey) apiKeyInfoDto {
	ts := formatWireTime(k.DateCreated)
	return apiKeyInfoDto{
		// Id is always 0 and IsActive always false: the reference fills
		// neither for a key (users.keys.R2).
		ID:          0,
		AccessToken: k.Token,
		AppName:     k.AppName,
		UserID:      zeroUserIDHex,
		IsActive:    false,
		DateCreated: ts,
		// A key is never "used" as far as this listing shows: the
		// reference keeps the zero date here.
		DateLastActivity: formatWireTime(time.Time{}),
	}
}

// handleAuthKeysList is GET /Auth/Keys (administrator only).
func (s *Server) handleAuthKeysList(w http.ResponseWriter, r *http.Request) {
	keys, err := s.store.QueryAPIKeys(r.Context())
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	items := make([]apiKeyInfoDto, 0, len(keys))
	for _, k := range keys {
		items = append(items, apiKeyDto(k))
	}
	writeJSON(w, http.StatusOK, apiKeyInfoQueryResult{Items: items, TotalRecordCount: len(items)})
}

// handleAuthKeysCreate is POST /Auth/Keys?app= (administrator only): mints
// a key for the named application; the key is read back from the listing.
func (s *Server) handleAuthKeysCreate(w http.ResponseWriter, r *http.Request) {
	// The name is stored as sent (surrounding spaces included); only a
	// missing or blank one is refused (users.keys.R4).
	app := caseInsensitiveQuery(r).Get("app")
	if strings.TrimSpace(app) == "" {
		writeValidationProblem(w, "app", "The app field is required.")
		return
	}
	if _, err := s.store.CreateAPIKey(r.Context(), app); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAuthKeysRevoke is DELETE /Auth/Keys/{key} (administrator only). A
// token that is not a key is a no-op.
func (s *Server) handleAuthKeysRevoke(w http.ResponseWriter, r *http.Request) {
	if err := s.store.RevokeAPIKey(r.Context(), r.PathValue("key")); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
