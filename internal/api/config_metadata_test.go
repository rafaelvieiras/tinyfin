package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// readSection is a small helper for the named-section tests below: it reads a
// section as the given token and decodes it into a map of raw members, so a
// test can assert both the values present and the members absent.
func readSection(t *testing.T, base, token, section string) map[string]json.RawMessage {
	t.Helper()
	code, body := policyDo(t, base, token, http.MethodGet, "/System/Configuration/"+section, "")
	if code != http.StatusOK {
		t.Fatalf("GET /System/Configuration/%s: status %d", section, code)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding %s: %v (body %s)", section, err, body)
	}
	return got
}

// writeSection posts a section as the given token and returns the status.
func writeSection(t *testing.T, base, token, section, body string) int {
	t.Helper()
	code, _ := policyDo(t, base, token, http.MethodPost, "/System/Configuration/"+section, body)
	return code
}

// TestMetadataSectionDefaultsAndRoundTrip covers admin.config.R9: the section
// the dashboard's library Display tab loads. The member defaults to the one
// timestamp source this server has (the file's modification time), a POST
// stores what it is given, and a POST of {} puts the default back.
func TestMetadataSectionDefaultsAndRoundTrip(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "admin", "adminpass")
	admin := policyLogin(t, ts.URL, "admin", "adminpass")

	got := readSection(t, ts.URL, admin, "metadata")
	if v := string(got["UseFileCreationTimeForDateAdded"]); v != "false" {
		t.Fatalf("default UseFileCreationTimeForDateAdded = %s, want false (internal/scan uses the file's modification time)", v)
	}
	if len(got) != 1 {
		t.Errorf("section has %d members, want 1: %v", len(got), got)
	}

	if code := writeSection(t, ts.URL, admin, "metadata", `{"UseFileCreationTimeForDateAdded":true}`); code != http.StatusNoContent {
		t.Fatalf("set: status %d, want 204", code)
	}
	if v := string(readSection(t, ts.URL, admin, "metadata")["UseFileCreationTimeForDateAdded"]); v != "true" {
		t.Fatalf("after set = %s, want true", v)
	}

	// A replace puts the default back and drops a member it is not given.
	if code := writeSection(t, ts.URL, admin, "metadata", `{"NotAMember":1}`); code != http.StatusNoContent {
		t.Fatalf("replace: status %d, want 204", code)
	}
	got = readSection(t, ts.URL, admin, "metadata")
	if v := string(got["UseFileCreationTimeForDateAdded"]); v != "false" {
		t.Errorf("after replace = %s, want the default false", v)
	}
	if _, present := got["NotAMember"]; present {
		t.Errorf("an unknown member was served back: %v", got)
	}

	// A member of the wrong type is refused and the section is left alone
	// (admin.config.R11; the reference answers 500 here, the divergent scenario
	// admin.config.section-invalid).
	if code := writeSection(t, ts.URL, admin, "metadata", `{"UseFileCreationTimeForDateAdded":true}`); code != http.StatusNoContent {
		t.Fatalf("set: status %d", code)
	}
	if code := writeSection(t, ts.URL, admin, "metadata", `{"UseFileCreationTimeForDateAdded":"yes"}`); code != http.StatusBadRequest {
		t.Fatalf("wrong type: status %d, want 400", code)
	}
	if v := string(readSection(t, ts.URL, admin, "metadata")["UseFileCreationTimeForDateAdded"]); v != "true" {
		t.Errorf("after the refused POST = %s, want the stored true", v)
	}
}

// TestXbmcMetadataOptionsDefaultsAndRoundTrip covers admin.config.R10: the
// section the dashboard's library NFO tab loads. Every member is stored and
// served back; none of them changes what this server does, since it writes no
// sidecar file.
func TestXbmcMetadataOptionsDefaultsAndRoundTrip(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "admin", "adminpass")
	admin := policyLogin(t, ts.URL, "admin", "adminpass")

	got := readSection(t, ts.URL, admin, "xbmcmetadata")
	want := map[string]string{
		"ReleaseDateFormat":            `"yyyy-MM-dd"`,
		"SaveImagePathsInNfo":          "true",
		"EnablePathSubstitution":       "true",
		"EnableExtraThumbsDuplication": "false",
	}
	for k, w := range want {
		if v := string(got[k]); v != w {
			t.Errorf("default %s = %s, want %s", k, v, w)
		}
	}
	if _, present := got["UserId"]; present {
		t.Errorf("UserId is present before it was ever set: %s", got["UserId"])
	}

	body := `{"UserId":"1c9d0c0e8f0a4c0f9a1b2c3d4e5f6071","ReleaseDateFormat":"yyyy-MM-dd",` +
		`"SaveImagePathsInNfo":false,"EnablePathSubstitution":false,"EnableExtraThumbsDuplication":true}`
	if code := writeSection(t, ts.URL, admin, "xbmcmetadata", body); code != http.StatusNoContent {
		t.Fatalf("set: status %d, want 204", code)
	}
	got = readSection(t, ts.URL, admin, "xbmcmetadata")
	if v := string(got["UserId"]); v != `"1c9d0c0e8f0a4c0f9a1b2c3d4e5f6071"` {
		t.Errorf("UserId = %s", v)
	}
	if v := string(got["SaveImagePathsInNfo"]); v != "false" {
		t.Errorf("SaveImagePathsInNfo = %s, want false", v)
	}
	if v := string(got["EnableExtraThumbsDuplication"]); v != "true" {
		t.Errorf("EnableExtraThumbsDuplication = %s, want true", v)
	}

	// A replace drops the members it is not given, UserId included.
	if code := writeSection(t, ts.URL, admin, "xbmcmetadata", `{}`); code != http.StatusNoContent {
		t.Fatalf("replace: status %d", code)
	}
	got = readSection(t, ts.URL, admin, "xbmcmetadata")
	if _, present := got["UserId"]; present {
		t.Errorf("UserId survived a replace: %s", got["UserId"])
	}
	if v := string(got["SaveImagePathsInNfo"]); v != "true" {
		t.Errorf("SaveImagePathsInNfo after replace = %s, want the default true", v)
	}

	// An empty string is a value, not an absence.
	if code := writeSection(t, ts.URL, admin, "xbmcmetadata", `{"UserId":""}`); code != http.StatusNoContent {
		t.Fatalf("empty user: status %d", code)
	}
	if v, present := readSection(t, ts.URL, admin, "xbmcmetadata")["UserId"]; !present || string(v) != `""` {
		t.Errorf(`UserId after an empty set = %s (present %v), want ""`, v, present)
	}

	// A member of the wrong type is refused and the section is left alone.
	if code := writeSection(t, ts.URL, admin, "xbmcmetadata", `{"SaveImagePathsInNfo":7}`); code != http.StatusBadRequest {
		t.Fatalf("wrong type: status %d, want 400", code)
	}
	if v := string(readSection(t, ts.URL, admin, "xbmcmetadata")["SaveImagePathsInNfo"]); v != "true" {
		t.Errorf("after the refused POST = %s, want the stored true", v)
	}
}

// TestNamedSectionsAuth guards the access rule of admin.config.R1/R6 for the
// two sections added with the library settings pages: any signed-in user may
// read, only an administrator may write.
func TestNamedSectionsAuth(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "admin", "adminpass")
	createTestUser(t, st, "alice", "pw")
	alice := policyLogin(t, ts.URL, "alice", "pw")

	for _, section := range []string{"metadata", "xbmcmetadata"} {
		if code, _ := policyDo(t, ts.URL, "", http.MethodGet, "/System/Configuration/"+section, ""); code != http.StatusUnauthorized {
			t.Errorf("anonymous GET %s: status %d, want 401", section, code)
		}
		if code := writeSection(t, ts.URL, alice, section, `{}`); code != http.StatusForbidden {
			t.Errorf("non-admin POST %s: status %d, want 403", section, code)
		}
		if code, _ := policyDo(t, ts.URL, alice, http.MethodGet, "/System/Configuration/"+section, ""); code != http.StatusOK {
			t.Errorf("non-admin GET %s: status %d, want 200", section, code)
		}
	}
}
