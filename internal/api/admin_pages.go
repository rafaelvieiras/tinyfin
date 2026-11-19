package api

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"net/http"
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
	CustomName       *string               `json:"CustomName,omitempty"`
	Id               string                `json:"Id"`
	LastUserName     string                `json:"LastUserName,omitempty"`
	AppName          string                `json:"AppName,omitempty"`
	AppVersion       string                `json:"AppVersion,omitempty"`
	LastUserId       string                `json:"LastUserId,omitempty"`
	DateLastActivity string                `json:"DateLastActivity"`
	Capabilities     deviceCapabilitiesDto `json:"Capabilities"`
}

func deviceDto(d storage.Device) deviceInfoDto {
	return deviceInfoDto{
		Name:             d.Name,
		CustomName:       d.CustomName,
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
// most recently active first. userId is validated but, as on the
// reference, does not narrow the list (devices.R2).
func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	if raw := caseInsensitiveQuery(r).Get("userid"); raw != "" {
		if _, ok := guid.Normalize(raw); !ok {
			writeInvalidID(w, "userId", raw)
			return
		}
	}
	devs, err := s.store.QueryDevices(r.Context(), "")
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
	id := caseInsensitiveQuery(r).Get("id")
	d, err := s.store.GetDevice(r.Context(), storage.DeviceID(id))
	if id == "" || err != nil {
		writeProblemDoc(w, http.StatusNotFound, problemTypeNotFound, "Not Found", nil)
		return
	}
	writeJSON(w, http.StatusOK, deviceDto(d))
}

// deviceOptionsDto is the options record of a DeviceId (admin.devices.R7).
type deviceOptionsDto struct {
	ID         int64   `json:"Id"`
	DeviceID   string  `json:"DeviceId"`
	CustomName *string `json:"CustomName,omitempty"`
}

// handleDeviceOptionsGet is GET /Devices/Options?id= (administrator).
func (s *Server) handleDeviceOptionsGet(w http.ResponseWriter, r *http.Request) {
	id := caseInsensitiveQuery(r).Get("id")
	if id == "" {
		writeValidationProblem(w, "id", "The id field is required.")
		return
	}
	optID, name, found, err := s.store.GetDeviceOptions(r.Context(), storage.DeviceID(id))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if !found {
		writeProblemDoc(w, http.StatusNotFound, problemTypeNotFound, "Not Found", nil)
		return
	}
	writeJSON(w, http.StatusOK, deviceOptionsDto{ID: optID, DeviceID: id, CustomName: name})
}

// handleDeviceOptionsPost is POST /Devices/Options?id= (administrator): it
// replaces the options record of the DeviceId, creating it when needed; the
// id need not be a registered device.
func (s *Server) handleDeviceOptionsPost(w http.ResponseWriter, r *http.Request) {
	id := caseInsensitiveQuery(r).Get("id")
	if id == "" {
		writeValidationProblem(w, "id", "The id field is required.")
		return
	}
	var body struct {
		CustomName *string `json:"CustomName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w)
		return
	}
	if err := s.store.SetDeviceOptions(r.Context(), storage.DeviceID(id), body.CustomName); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeviceDelete is DELETE /Devices?id= (administrator): the device
// goes away together with the tokens issued to it, whichever the user. No
// id is a no-op; an unknown id is a 400.
func (s *Server) handleDeviceDelete(w http.ResponseWriter, r *http.Request) {
	id := caseInsensitiveQuery(r).Get("id")
	if id == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if _, err := s.store.GetDevice(r.Context(), storage.DeviceID(id)); err != nil {
		writeBadRequest(w)
		return
	}
	if err := s.store.DeleteDevice(r.Context(), storage.DeviceID(id)); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	s.sessions.dropDevice(r.Context(), id)
	w.WriteHeader(http.StatusNoContent)
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
		dto := s.libraryBaseItemDto(r.Context(), lib)
		dto.Path = "/root/default/" + lib.Name
		if len(lib.Paths) > 0 {
			dto.Path = lib.Paths[0]
		}
		dto.Etag = mediaFolderEtag(dto.ID, dto.Name)
		items = append(items, dto)
	}
	pl := s.playlistsViewDto(r.Context())
	pl.Type = "ManualPlaylistsFolder"
	pl.Path = "/data/playlists"
	pl.Etag = mediaFolderEtag(pl.ID, pl.Name)
	switch strings.ToLower(caseInsensitiveQuery(r).Get("ishidden")) {
	case "true":
		items = []baseItemDto{pl}
	case "false":
	default:
		items = append(items, pl)
	}
	// Ordered by sort name, the playlists folder included (admin.libraries.R4).
	sortLibraryItems(items)
	writeJSON(w, http.StatusOK, baseItemDtoQueryResult{Items: items, TotalRecordCount: len(items), StartIndex: 0})
}

// mediaFolderEtag is the folder's Etag: 32 lowercase hex digits, stable for
// a given folder (admin.pages.R5).
func mediaFolderEtag(id, name string) string {
	sum := md5.Sum([]byte(id + "|" + name))
	return hex.EncodeToString(sum[:])
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
