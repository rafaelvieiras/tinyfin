package api

import (
	"encoding/json"
	"net/http"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// displayPreferencesDto mirrors the OpenAPI DisplayPreferencesDto — Id is
// the display-preferences-set id (the reference server accepts an
// arbitrary caller-chosen id here, jellyfin-web always sends "usersettings"
// for its own settings), Client is carried on the DTO too even though it's
// also a query param on the request.
//
// ScrollDirection and SortOrder are NOT omitempty, unlike the other string
// fields here: found by testing against a real jellyfin-android build —
// its jellyfin-sdk-kotlin client deserializes this response with
// kotlinx.serialization into a DisplayPreferencesDto that declares those
// two fields required (no default), so a fresh user with no preferences
// ever set (both are "" here) throws MissingFieldException the moment the
// key is absent from the JSON entirely, right when the app's native player
// fetches display preferences before starting playback — jellyfin-web
// never noticed because its JS reads them as simply undefined.
//
// That first fix was incomplete, found while mapping the whole native
// playback flow for coverage: an empty string isn't just "missing", it's
// also not a valid member of either field's enum (ScrollDirection.kt only
// has "Horizontal"/"Vertical", SortOrder.kt only "Ascending"/"Descending",
// neither declares a fallback), so kotlinx.serialization still throws —
// a different SerializationException (invalid enum value, not a missing
// key) but the same caught-and-degrade outcome in PlayerViewModel. This
// hits any (userId, client) pair that has never had preferences saved
// under it before, which includes the native player's own
// client="emby"/id="usersettings" pair the first time a device plays
// anything, even for a user who has already used jellyfin-web (a
// different client value). defaultScrollDirection/defaultSortOrder below
// are applied wherever the stored or zero value would otherwise be empty.
type displayPreferencesDto struct {
	ID                 string            `json:"Id"`
	ViewType           string            `json:"ViewType,omitempty"`
	SortBy             string            `json:"SortBy,omitempty"`
	IndexBy            string            `json:"IndexBy,omitempty"`
	RememberIndexing   bool              `json:"RememberIndexing"`
	PrimaryImageWidth  int               `json:"PrimaryImageWidth"`
	PrimaryImageHeight int               `json:"PrimaryImageHeight"`
	CustomPrefs        map[string]string `json:"CustomPrefs"`
	ScrollDirection    string            `json:"ScrollDirection"`
	ShowBackdrop       bool              `json:"ShowBackdrop"`
	RememberSorting    bool              `json:"RememberSorting"`
	SortOrder          string            `json:"SortOrder"`
	ShowSidebar        bool              `json:"ShowSidebar"`
	Client             string            `json:"Client,omitempty"`
}

// defaultScrollDirection/defaultSortOrder return v unchanged unless it's
// empty, in which case they substitute the reference server's own default
// enum member — see the doc comment above displayPreferencesDto for why an
// empty string can't just pass through here the way the other string
// fields on this DTO do.
func defaultScrollDirection(v string) string {
	if v == "" {
		return "Vertical"
	}
	return v
}

func defaultSortOrder(v string) string {
	if v == "" {
		return "Ascending"
	}
	return v
}

// handleDisplayPreferencesGet is GET /DisplayPreferences/{displayPreferencesId}
// (docs/04's "Users, sessions, devices" group). The reference server keys
// display preferences by (userId, client) — the id path segment itself is
// caller-chosen and not otherwise meaningful (storage.UserStore.
// GetDisplayPreferences never sees it), matching how jellyfin-web always
// sends the literal "usersettings" regardless of what's actually stored.
func (s *Server) handleDisplayPreferencesGet(w http.ResponseWriter, r *http.Request) {
	client := r.URL.Query().Get("client")
	userID := storage.UserID(r.URL.Query().Get("userId"))
	if userID == "" {
		ac, _ := AuthFromContext(r.Context())
		userID = ac.User.ID
	}

	prefs, err := s.store.GetDisplayPreferences(r.Context(), userID, client)
	if err != nil {
		writeJSON(w, http.StatusOK, displayPreferencesDto{
			ID:              r.PathValue("displayPreferencesId"),
			Client:          client,
			CustomPrefs:     map[string]string{},
			ScrollDirection: defaultScrollDirection(""),
			SortOrder:       defaultSortOrder(""),
		})
		return
	}
	if prefs.CustomPrefs == nil {
		prefs.CustomPrefs = map[string]string{}
	}
	writeJSON(w, http.StatusOK, displayPreferencesDto{
		ID:                 r.PathValue("displayPreferencesId"),
		ViewType:           prefs.ViewType,
		SortBy:             prefs.SortBy,
		IndexBy:            prefs.IndexBy,
		RememberIndexing:   prefs.RememberIndexing,
		PrimaryImageWidth:  prefs.PrimaryImageWidth,
		PrimaryImageHeight: prefs.PrimaryImageHeight,
		CustomPrefs:        prefs.CustomPrefs,
		ScrollDirection:    defaultScrollDirection(prefs.ScrollDirection),
		ShowBackdrop:       prefs.ShowBackdrop,
		RememberSorting:    prefs.RememberSorting,
		SortOrder:          defaultSortOrder(prefs.SortOrder),
		ShowSidebar:        prefs.ShowSidebar,
		Client:             client,
	})
}

// handleDisplayPreferencesUpdate is POST /DisplayPreferences/{displayPreferencesId}.
func (s *Server) handleDisplayPreferencesUpdate(w http.ResponseWriter, r *http.Request) {
	client := r.URL.Query().Get("client")
	userID := storage.UserID(r.URL.Query().Get("userId"))
	if userID == "" {
		ac, _ := AuthFromContext(r.Context())
		userID = ac.User.ID
	}

	var dto displayPreferencesDto
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	err := s.store.SetDisplayPreferences(r.Context(), userID, client, storage.DisplayPreferences{
		ViewType:           dto.ViewType,
		SortBy:             dto.SortBy,
		IndexBy:            dto.IndexBy,
		RememberIndexing:   dto.RememberIndexing,
		PrimaryImageWidth:  dto.PrimaryImageWidth,
		PrimaryImageHeight: dto.PrimaryImageHeight,
		CustomPrefs:        dto.CustomPrefs,
		ScrollDirection:    dto.ScrollDirection,
		ShowBackdrop:       dto.ShowBackdrop,
		RememberSorting:    dto.RememberSorting,
		SortOrder:          dto.SortOrder,
		ShowSidebar:        dto.ShowSidebar,
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
