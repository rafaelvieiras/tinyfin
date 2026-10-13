package api

import "testing"

// TestParseEmbyAuthPercentDecodesFields covers a real bug found while
// implementing GET /Sessions (sessions.go): a real jellyfin-web build sends
// this header's Client field percent-encoded — literally
// `Client="Jellyfin%20Web"` — and a real Jellyfin server decodes it before
// reporting it back. Confirmed live via Playwright against a real
// jellyfin-web build's own POST /Sessions/Capabilities/Full request headers.
func TestParseEmbyAuthPercentDecodesFields(t *testing.T) {
	header := `MediaBrowser Client="Jellyfin%20Web", Device="Chrome", DeviceId="device-1", Version="12.0.0", Token="abc123"`
	client, device, deviceID, version, token := parseEmbyAuth(header)

	if client != "Jellyfin Web" {
		t.Errorf("Client = %q, want %q", client, "Jellyfin Web")
	}
	if device != "Chrome" {
		t.Errorf("Device = %q, want %q", device, "Chrome")
	}
	if deviceID != "device-1" {
		t.Errorf("DeviceId = %q, want %q", deviceID, "device-1")
	}
	if version != "12.0.0" {
		t.Errorf("Version = %q, want %q", version, "12.0.0")
	}
	if token != "abc123" {
		t.Errorf("Token = %q, want %q", token, "abc123")
	}
}

// TestParseEmbyAuthPlainValuesUnaffected covers the common case (no
// percent-escaping at all) to guard against the decode step corrupting an
// already-plain value.
func TestParseEmbyAuthPlainValuesUnaffected(t *testing.T) {
	header := `MediaBrowser Client="Kodi", Device="Living Room", DeviceId="abc/123=", Version="1.0"`
	client, device, deviceID, _, _ := parseEmbyAuth(header)

	if client != "Kodi" {
		t.Errorf("Client = %q, want %q", client, "Kodi")
	}
	if device != "Living Room" {
		t.Errorf("Device = %q, want %q", device, "Living Room")
	}
	if deviceID != "abc/123=" {
		t.Errorf("DeviceId = %q, want %q", deviceID, "abc/123=")
	}
}

// TestParseEmbyAuthPlusIsSpace covers users.auth.R11: "+" decodes as a
// space, "%2B" as a literal plus, "%20" as a space.
func TestParseEmbyAuthPlusIsSpace(t *testing.T) {
	header := `MediaBrowser Client="My+Client", Device="Living%20Room", DeviceId="a%2Bb+c", Version="1.0"`
	client, device, deviceID, _, _ := parseEmbyAuth(header)
	if client != "My Client" {
		t.Errorf("Client = %q, want %q", client, "My Client")
	}
	if device != "Living Room" {
		t.Errorf("Device = %q, want %q", device, "Living Room")
	}
	if deviceID != "a+b c" {
		t.Errorf("DeviceId = %q, want %q", deviceID, "a+b c")
	}
}
