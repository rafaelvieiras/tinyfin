package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// capabilitiesSession performs the short-form call and reads back what the
// session registry holds for that device, the way a dashboard would.
func capabilitiesSession(t *testing.T, base, token, query string) (status int, caps sessionCapabilitiesDto) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/Sessions/Capabilities"+query, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", `MediaBrowser Token="`+token+`", Client="Android TV", Device="MiBox", DeviceId="cap-device", Version="0.17.0"`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	list, err := http.NewRequest(http.MethodGet, base+"/Sessions", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	list.Header.Set("Authorization", `MediaBrowser Token="`+token+`", DeviceId="cap-device"`)
	listResp, err := http.DefaultClient.Do(list)
	if err != nil {
		t.Fatalf("GET /Sessions: %v", err)
	}
	defer listResp.Body.Close()
	var sessions []sessionInfoDto
	if err := json.NewDecoder(listResp.Body).Decode(&sessions); err != nil {
		t.Fatalf("decode /Sessions: %v", err)
	}
	for _, s := range sessions {
		if s.DeviceID == "cap-device" {
			return resp.StatusCode, sessionCapabilitiesDto{
				PlayableMediaTypes:   s.PlayableMediaTypes,
				SupportedCommands:    s.SupportedCommands,
				SupportsMediaControl: s.Capabilities.SupportsMediaControl,
			}
		}
	}
	return resp.StatusCode, sessionCapabilitiesDto{}
}

// TestSessionsCapabilitiesShortFormRecordsTheQuery: the Android TV client posts
// this route instead of /Full, with the state in the query — measured on the
// reference, where a 404 here was the only missing route in a whole session.
func TestSessionsCapabilitiesShortFormRecordsTheQuery(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "alice", "hunter2")
	token := loginTokenWithDevice(t, ts.URL, "alice", "hunter2", "cap-device")

	status, caps := capabilitiesSession(t, ts.URL, token,
		"?playableMediaTypes=Video,Audio&supportedCommands=VolumeUp,Mute&supportsMediaControl=true")
	if status != http.StatusNoContent {
		t.Errorf("status = %d, want 204", status)
	}
	if len(caps.PlayableMediaTypes) != 2 || caps.PlayableMediaTypes[0] != "Video" || caps.PlayableMediaTypes[1] != "Audio" {
		t.Errorf("PlayableMediaTypes = %v, want [Video Audio]", caps.PlayableMediaTypes)
	}
	if len(caps.SupportedCommands) != 2 || caps.SupportedCommands[0] != "VolumeUp" {
		t.Errorf("SupportedCommands = %v, want [VolumeUp Mute]", caps.SupportedCommands)
	}
	if !caps.SupportsMediaControl {
		t.Error("SupportsMediaControl = false, want true")
	}
}

// TestSessionsCapabilitiesShortFormIgnoresTheBody: measured on the reference —
// query "Video" plus body ["Video","Audio"] recorded ["Video"], so the query is
// the state and the body is decoration.
func TestSessionsCapabilitiesShortFormIgnoresTheBody(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "alice", "hunter2")
	token := loginTokenWithDevice(t, ts.URL, "alice", "hunter2", "cap-device")

	body := strings.NewReader(`{"PlayableMediaTypes":["Video","Audio"],"SupportedCommands":["ToggleMute"]}`)
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/Sessions/Capabilities?playableMediaTypes=Video", body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", `MediaBrowser Token="`+token+`", DeviceId="cap-device"`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}

	_, caps := capabilitiesSession(t, ts.URL, token, "?playableMediaTypes=Video")
	if len(caps.PlayableMediaTypes) != 1 || caps.PlayableMediaTypes[0] != "Video" {
		t.Errorf("PlayableMediaTypes = %v, want [Video] (the body must not add Audio)", caps.PlayableMediaTypes)
	}
	if len(caps.SupportedCommands) != 0 {
		t.Errorf("SupportedCommands = %v, want empty (the body must not add ToggleMute)", caps.SupportedCommands)
	}
}

// TestSessionsCapabilitiesShortFormClearsWithoutAQuery: a call with no query
// clears what was recorded, measured on the reference (state Video/VolumeUp
// became empty after a bare POST).
func TestSessionsCapabilitiesShortFormClearsWithoutAQuery(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "alice", "hunter2")
	token := loginTokenWithDevice(t, ts.URL, "alice", "hunter2", "cap-device")

	if status, _ := capabilitiesSession(t, ts.URL, token, "?playableMediaTypes=Video&supportedCommands=VolumeUp"); status != http.StatusNoContent {
		t.Fatalf("first call: status = %d, want 204", status)
	}
	_, caps := capabilitiesSession(t, ts.URL, token, "")
	if len(caps.PlayableMediaTypes) != 0 || len(caps.SupportedCommands) != 0 {
		t.Errorf("after a bare POST: %v / %v, want both empty", caps.PlayableMediaTypes, caps.SupportedCommands)
	}
}

// TestSessionsCapabilitiesShortFormNeedsAToken: both forms are authenticated.
func TestSessionsCapabilitiesShortFormNeedsAToken(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, err := http.Post(ts.URL+"/Sessions/Capabilities", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}
