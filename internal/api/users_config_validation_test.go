package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// configMembers reads the raw Configuration object of a user.
func configMembers(t *testing.T, baseURL, tok, id string) map[string]any {
	t.Helper()
	code, body := policyDo(t, baseURL, tok, http.MethodGet, "/Users/"+id, "")
	if code != http.StatusOK {
		t.Fatalf("GET user: %d", code)
	}
	var u struct{ Configuration map[string]any }
	if err := json.Unmarshal(body, &u); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return u.Configuration
}

// TestUserConfigurationValidation covers users.configuration.R7-R11 on both
// routes: strict booleans, GUID lists, the lenient enum and string members.
func TestUserConfigurationValidation(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "admin", "adminpass")
	alice := createTestUser(t, st, "alice", "pw")
	tok := policyLogin(t, ts.URL, "admin", "adminpass")
	aid := string(alice.ID)
	const g = "11111111-2222-3333-4444-555555555555"
	const g32 = "11111111222233334444555555555555"

	routes := map[string]string{
		"query": "/Users/Configuration?userId=" + aid,
		"alias": "/Users/" + aid + "/Configuration",
	}
	for rn, path := range routes {
		type tc struct {
			name, body string
			want       int
			member     string
			read       any
		}
		cases := []tc{
			{"bool null", `{"PlayDefaultAudioTrack":null}`, 400, "", nil},
			{"bool string", `{"HidePlayedInLatest":"true"}`, 400, "", nil},
			{"bool number", `{"EnableLocalPassword":0}`, 400, "", nil},
			{"list null", `{"OrderedViews":null}`, 400, "", nil},
			{"list not array", `{"GroupedFolders":"abc"}`, 400, "", nil},
			{"list non-guid", `{"MyMediaExcludes":["x"]}`, 400, "", nil},
			{"list empty string", `{"MyMediaExcludes":[""]}`, 400, "", nil},
			{"list number entry", `{"LatestItemsExcludes":[1]}`, 400, "", nil},
			{"list null entry", `{"OrderedViews":[null]}`, 204, "OrderedViews", []any{"00000000000000000000000000000000"}},
			{"list spellings", `{"OrderedViews":["` + strings.ToUpper(g) + `","{` + g + `}","(` + g + `)"," ` + g32 + ` "]}`, 204, "OrderedViews", []any{g32, g32, g32, g32}},
			{"list unbalanced brace", `{"OrderedViews":["{` + g + `"]}`, 400, "", nil},
			{"mode null", `{"SubtitleMode":null}`, 400, "", nil},
			{"mode bad name", `{"SubtitleMode":"nope"}`, 400, "", nil},
			{"mode empty", `{"SubtitleMode":""}`, 400, "", nil},
			{"mode bool", `{"SubtitleMode":true}`, 400, "", nil},
			{"mode fraction", `{"SubtitleMode":1.0}`, 400, "", nil},
			{"mode overflow", `{"SubtitleMode":2147483648}`, 400, "", nil},
			{"mode number", `{"SubtitleMode":2}`, 204, "SubtitleMode", "OnlyForced"},
			{"mode number out of range", `{"SubtitleMode":9}`, 204, "SubtitleMode", float64(9)},
			{"mode negative", `{"SubtitleMode":-1}`, 204, "SubtitleMode", float64(-1)},
			{"mode case", `{"SubtitleMode":"ALWAYS"}`, 204, "SubtitleMode", "Always"},
			{"mode numeric string", `{"SubtitleMode":" +3 "}`, 204, "SubtitleMode", "None"},
			{"mode flags", `{"SubtitleMode":"Always, None"}`, 204, "SubtitleMode", "None"},
			{"mode flags out of range", `{"SubtitleMode":"always,smart"}`, 204, "SubtitleMode", float64(5)},
			{"mode flags trailing comma", `{"SubtitleMode":"smart,"}`, 204, "SubtitleMode", "Smart"},
			{"mode flags empty member", `{"SubtitleMode":"Always,,None"}`, 400, "", nil},
			{"mode flags number member", `{"SubtitleMode":"Always,1"}`, 400, "", nil},
			{"string number", `{"AudioLanguagePreference":5}`, 204, "AudioLanguagePreference", "5"},
			{"string bool", `{"SubtitleLanguagePreference":true}`, 204, "SubtitleLanguagePreference", "true"},
			{"string literal kept", `{"AudioLanguagePreference":1.50}`, 204, "AudioLanguagePreference", "1.50"},
			{"string array", `{"AudioLanguagePreference":["a"]}`, 400, "", nil},
			{"string object", `{"CastReceiverId":{}}`, 400, "", nil},
			{"string null", `{"SubtitleLanguagePreference":null}`, 204, "SubtitleLanguagePreference", ""},
			{"cast number ignored", `{"CastReceiverId":5}`, 204, "CastReceiverId", "F007D354"},
			{"member name case", `{"playdefaultaudiotrack":false}`, 204, "PlayDefaultAudioTrack", false},
			{"unknown member", `{"Unknown":null}`, 204, "", nil},
			{"duplicate member", `{"SubtitleMode":"Always","SubtitleMode":"None"}`, 204, "SubtitleMode", "None"},
			{"null body", `null`, 400, "", nil},
			{"array body", `[]`, 400, "", nil},
			{"number body", `1`, 400, "", nil},
			{"trailing garbage", `{} x`, 400, "", nil},
			{"trailing comma", `{"SubtitleMode":"Always",}`, 400, "", nil},
			{"byte order mark", "\xef\xbb\xbf{}", 204, "", nil},
		}
		for _, c := range cases {
			code, _ := policyDo(t, ts.URL, tok, http.MethodPost, "/Users/Configuration?userId="+aid, `{}`)
			if code != http.StatusNoContent {
				t.Fatalf("reset: %d", code)
			}
			code, _ = policyDo(t, ts.URL, tok, http.MethodPost, path, c.body)
			if code != c.want {
				t.Errorf("%s %s: status %d, want %d", rn, c.name, code, c.want)
				continue
			}
			if c.member == "" {
				continue
			}
			got := configMembers(t, ts.URL, tok, aid)[c.member]
			if want, _ := json.Marshal(c.read); func() string { g, _ := json.Marshal(got); return string(g) }() != string(want) {
				t.Errorf("%s %s: %s = %v, want %v", rn, c.name, c.member, got, c.read)
			}
		}
	}

	// A rejected request leaves the stored configuration unchanged.
	post := func(body string) {
		if code, _ := policyDo(t, ts.URL, tok, http.MethodPost, "/Users/Configuration?userId="+aid, body); code != 204 {
			t.Fatalf("post: %d", code)
		}
	}
	post(`{"SubtitleMode":"Smart"}`)
	if code, _ := policyDo(t, ts.URL, tok, http.MethodPost, "/Users/Configuration?userId="+aid, `{"SubtitleMode":"Always","DisplayCollectionsView":null}`); code != 400 {
		t.Fatalf("mixed: %d", code)
	}
	if m := configMembers(t, ts.URL, tok, aid)["SubtitleMode"]; m != "Smart" {
		t.Errorf("rejected request changed SubtitleMode to %v", m)
	}

	// The same validation applies to the Configuration member of POST /Users.
	for _, c := range []struct {
		name, cfg string
		want      int
	}{
		{"bool null", `{"PlayDefaultAudioTrack":null}`, 400},
		{"list bad", `{"OrderedViews":["x"]}`, 400},
		{"mode number", `{"SubtitleMode":2}`, 204},
	} {
		if code, _ := policyDo(t, ts.URL, tok, http.MethodPost, "/Users?userId="+aid, updateBody("alice", `"Configuration":`+c.cfg)); code != c.want {
			t.Errorf("POST /Users %s: status %d, want %d", c.name, code, c.want)
		}
	}
}
