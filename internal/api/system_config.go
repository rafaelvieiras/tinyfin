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
	if v, ok, err := s.store.GetSetting(context.Background(), settingServerName); err == nil && ok {
		s.setServerName(v)
	}
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
	ServerName                string `json:"ServerName"`
	QuickConnectAvailable     bool   `json:"QuickConnectAvailable"`
	UICulture                 string `json:"UICulture"`
	MetadataCountryCode       string `json:"MetadataCountryCode"`
	PreferredMetadataLanguage string `json:"PreferredMetadataLanguage"`
}

// handleSystemConfigurationGet is GET /System/Configuration (administrator
// only).
func (s *Server) handleSystemConfigurationGet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// startup.config.R3: the wizard's values are the server configuration;
	// without a stored name the command-line default is reported.
	writeJSON(w, http.StatusOK, serverConfigurationDto{
		ServerName:                s.setting(ctx, settingServerName, s.cfg.ServerName),
		QuickConnectAvailable:     s.quickConnectEnabled(),
		UICulture:                 s.setting(ctx, settingUICulture, defaultUICulture),
		MetadataCountryCode:       s.setting(ctx, settingMetadataCtry, defaultMetadataCtry),
		PreferredMetadataLanguage: s.setting(ctx, settingMetadataLang, defaultMetadataLang),
	})
}

// handleSystemConfigurationPost is POST /System/Configuration
// (administrator only). Only the members Tinyfin supports are applied;
// absent and unknown members are left alone.
func (s *Server) handleSystemConfigurationPost(w http.ResponseWriter, r *http.Request) {
	var body struct {
		QuickConnectAvailable     *bool   `json:"QuickConnectAvailable"`
		ServerName                *string `json:"ServerName"`
		UICulture                 *string `json:"UICulture"`
		MetadataCountryCode       *string `json:"MetadataCountryCode"`
		PreferredMetadataLanguage *string `json:"PreferredMetadataLanguage"`
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
	for _, kv := range []struct {
		key string
		val *string
	}{
		{settingServerName, body.ServerName},
		{settingUICulture, body.UICulture},
		{settingMetadataCtry, body.MetadataCountryCode},
		{settingMetadataLang, body.PreferredMetadataLanguage},
	} {
		if kv.val == nil {
			continue
		}
		if err := s.store.SetSetting(r.Context(), kv.key, *kv.val); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if kv.key == settingServerName {
			s.setServerName(*kv.val)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
