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
type displayPreferencesDto struct {
	ID                 string            `json:"Id"`
	ViewType           string            `json:"ViewType,omitempty"`
	SortBy             string            `json:"SortBy,omitempty"`
	IndexBy            string            `json:"IndexBy,omitempty"`
	RememberIndexing   bool              `json:"RememberIndexing"`
	PrimaryImageWidth  int               `json:"PrimaryImageWidth"`
	PrimaryImageHeight int               `json:"PrimaryImageHeight"`
	CustomPrefs        map[string]string `json:"CustomPrefs"`
	ScrollDirection    string            `json:"ScrollDirection,omitempty"`
	ShowBackdrop       bool              `json:"ShowBackdrop"`
	RememberSorting    bool              `json:"RememberSorting"`
	SortOrder          string            `json:"SortOrder,omitempty"`
	ShowSidebar        bool              `json:"ShowSidebar"`
	Client             string            `json:"Client,omitempty"`
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
		writeJSON(w, http.StatusOK, displayPreferencesDto{ID: r.PathValue("displayPreferencesId"), Client: client, CustomPrefs: map[string]string{}})
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
		ScrollDirection:    prefs.ScrollDirection,
		ShowBackdrop:       prefs.ShowBackdrop,
		RememberSorting:    prefs.RememberSorting,
		SortOrder:          prefs.SortOrder,
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
