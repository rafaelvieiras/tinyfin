package api

import (
	"net/http"
	"sort"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/guid"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// This file holds the small read-mostly routes the administration pages of
// the web client call on load (admin.pages.*): the device list, the media
// folder list, the authentication provider lists and the Live TV / channel
// probes. None of them carries state of its own.

// deviceCapabilitiesDto is the capability block of a device entry. The
// reference reports the same block for every device, whatever capabilities
// the client registered for its session (admin.devices.R2).
type deviceCapabilitiesDto struct {
	PlayableMediaTypes           []string `json:"PlayableMediaTypes"`
	SupportedCommands            []string `json:"SupportedCommands"`
	SupportsMediaControl         bool     `json:"SupportsMediaControl"`
	SupportsPersistentIdentifier bool     `json:"SupportsPersistentIdentifier"`
}

type deviceInfoDto struct {
	Name             string                `json:"Name"`
	Id               string                `json:"Id"`
	LastUserName     string                `json:"LastUserName,omitempty"`
	AppName          string                `json:"AppName,omitempty"`
	AppVersion       string                `json:"AppVersion,omitempty"`
	LastUserId       string                `json:"LastUserId,omitempty"`
	DateLastActivity string                `json:"DateLastActivity"`
	Capabilities     deviceCapabilitiesDto `json:"Capabilities"`
}

func deviceDto(d storage.Device) deviceInfoDto {
	name := d.Name
	if d.CustomName != "" {
		name = d.CustomName
	}
	return deviceInfoDto{
		Name:             name,
		Id:               string(d.ID),
		LastUserName:     d.LastUserName,
		AppName:          d.AppName,
		AppVersion:       d.AppVersion,
		LastUserId:       string(d.LastUserID),
		DateLastActivity: formatWireTime(d.DateLastActivity),
		Capabilities: deviceCapabilitiesDto{
			PlayableMediaTypes:           []string{},
			SupportedCommands:            []string{},
			SupportsPersistentIdentifier: true,
		},
	}
}

// handleDevices is GET /Devices (administrator): the registered devices,
// most recently active first; userId keeps the devices a user used last.
func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	var userID storage.UserID
	if raw := r.URL.Query().Get("userId"); raw != "" {
		n, ok := guid.Normalize(raw)
		if !ok {
			writeInvalidID(w, "userId", raw)
			return
		}
		userID = storage.UserID(n)
	}
	devs, err := s.store.QueryDevices(r.Context(), userID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	items := make([]deviceInfoDto, 0, len(devs))
	for _, d := range devs {
		items = append(items, deviceDto(d))
	}
	writeJSON(w, http.StatusOK, map[string]any{"Items": items, "TotalRecordCount": len(items), "StartIndex": 0})
}

// handleDeviceInfo is GET /Devices/Info?id= (administrator).
func (s *Server) handleDeviceInfo(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	d, err := s.store.GetDevice(r.Context(), storage.DeviceID(id))
	if id == "" || err != nil {
		writeProblemDoc(w, http.StatusNotFound, problemTypeNotFound, "Not Found", nil)
		return
	}
	writeJSON(w, http.StatusOK, deviceDto(d))
}

// handleMediaFolders is GET /Library/MediaFolders (administrator): every
// library root, plus the playlists folder, which is the hidden one.
// isHidden narrows the list either way.
func (s *Server) handleMediaFolders(w http.ResponseWriter, r *http.Request) {
	libs, err := s.store.QueryLibraries(r.Context())
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	items := make([]baseItemDto, 0, len(libs)+1)
	for _, lib := range libs {
		if lib.CollectionType == storage.CollectionTypePlaylists || lib.CollectionType == storage.CollectionTypeBoxSets {
			continue
		}
		items = append(items, s.libraryBaseItemDto(r.Context(), lib))
	}
	pl := s.playlistsViewDto(r.Context())
	switch strings.ToLower(r.URL.Query().Get("isHidden")) {
	case "true":
		items = []baseItemDto{pl}
	case "false":
	default:
		items = append(items, pl)
	}
	sort.SliceStable(items, func(i, j int) bool { return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name) })
	writeJSON(w, http.StatusOK, baseItemDtoQueryResult{Items: items, TotalRecordCount: len(items), StartIndex: 0})
}

// authProviderDto is one entry of the authentication / password reset
// provider lists. The Ids are opaque to clients, which only echo them back
// in the user policy; they are the ones the stored policies carry.
type authProviderDto struct {
	Name string `json:"Name"`
	Id   string `json:"Id"`
}

func (s *Server) handleAuthProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []authProviderDto{{Name: "Default", Id: defaultAuthProviderID}})
}

func (s *Server) handlePasswordResetProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []authProviderDto{{Name: "Default Password Reset Provider", Id: defaultAuthProviderID}})
}

// handleLiveTvInfo is GET /LiveTv/Info: Live TV is not implemented, so the
// report is the one a server without tuners gives: its single built-in
// service, invisible, with no tuner and no enabled user.
func (s *Server) handleLiveTvInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"Services": []map[string]any{{
			"Name": "Emby", "Status": "Ok", "HasUpdateAvailable": false, "IsVisible": false, "Tuners": []string{},
		}},
		"IsEnabled":    true,
		"EnabledUsers": []string{},
	})
}

// handleChannels is GET /Channels: there are no channel plugins, so the
// list is always empty.
func (s *Server) handleChannels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, baseItemDtoQueryResult{Items: []baseItemDto{}, TotalRecordCount: 0, StartIndex: 0})
}
