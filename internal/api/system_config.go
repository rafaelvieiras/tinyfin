package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
)

const settingQuickConnect = "QuickConnectAvailable"

// quickConnectEnabled reports whether the QuickConnect flow is on: the
// runtime setting (POST /System/Configuration) wins over the command-line
// default.
func (s *Server) quickConnectEnabled() bool {
	switch s.qcOverride.Load() {
	case 1:
		return true
	case 2:
		return false
	}
	return !s.cfg.QuickConnectDisabled
}

// loadRuntimeSettings reads the persisted runtime settings at startup.
func (s *Server) loadRuntimeSettings() {
	if v, ok, err := s.store.GetSetting(context.Background(), settingQuickConnect); err == nil && ok {
		if b, err := strconv.ParseBool(v); err == nil {
			s.setQuickConnect(b)
		}
	}
}

func (s *Server) setQuickConnect(on bool) {
	if on {
		s.qcOverride.Store(1)
	} else {
		s.qcOverride.Store(2)
	}
}

type serverConfigurationDto struct {
	ServerName            string `json:"ServerName"`
	QuickConnectAvailable bool   `json:"QuickConnectAvailable"`
}

// handleSystemConfigurationGet is GET /System/Configuration (administrator
// only).
func (s *Server) handleSystemConfigurationGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, serverConfigurationDto{
		ServerName:            s.cfg.ServerName,
		QuickConnectAvailable: s.quickConnectEnabled(),
	})
}

// handleSystemConfigurationPost is POST /System/Configuration
// (administrator only). Only the members Tinyfin supports are applied;
// absent and unknown members are left alone.
func (s *Server) handleSystemConfigurationPost(w http.ResponseWriter, r *http.Request) {
	var body struct {
		QuickConnectAvailable *bool `json:"QuickConnectAvailable"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeInvalidBody(w)
		return
	}
	if body.QuickConnectAvailable != nil {
		if err := s.store.SetSetting(r.Context(), settingQuickConnect, strconv.FormatBool(*body.QuickConnectAvailable)); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		s.setQuickConnect(*body.QuickConnectAvailable)
	}
	w.WriteHeader(http.StatusNoContent)
}
