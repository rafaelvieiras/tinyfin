package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

type activityEntry struct {
	Id     int64
	Name   string
	Type   string
	ItemId string
	UserId string
}

// activityEntries reads the whole activity log through the public route,
// newest first.
func (e *psEnv) activityEntries() []activityEntry {
	e.t.Helper()
	code, b := e.do(e.adminTok, http.MethodGet, "/System/ActivityLog/Entries?limit=500", "")
	if code != http.StatusOK {
		e.t.Fatalf("GET /System/ActivityLog/Entries = %d: %s", code, b)
	}
	var res struct{ Items []activityEntry }
	if err := json.Unmarshal(b, &res); err != nil {
		e.t.Fatal(err)
	}
	return res.Items
}

// playbackEntries keeps only the entries a playback report can create.
func playbackEntries(all []activityEntry) []activityEntry {
	var out []activityEntry
	for _, a := range all {
		switch a.Type {
		case "VideoPlayback", "AudioPlayback", "Playback",
			"VideoPlaybackStopped", "AudioPlaybackStopped":
			out = append(out, a)
		}
	}
	return out
}

// TestPlaybackReportsWriteActivityEntries covers playstate.start.R10: a
// start creates a playback entry whose type follows the media type and a
// stop creates the matching "stopped" entry (a failed stop included); an
// unknown item, progress reports and non-playable items create none.
func TestPlaybackReportsWriteActivityEntries(t *testing.T) {
	e := newPSEnv(t)
	ctx := context.Background()
	if err := e.st.UpsertItem(ctx, storage.Item{Type: storage.ItemTypeAudio, LibraryID: e.libID, Name: "Song", Path: "/media/s.mp3"}); err != nil {
		t.Fatal(err)
	}
	song := mustFindItemByName(t, e.st, e.libID, "Song")
	body := func(id storage.ItemID, extra string) string {
		return `{"ItemId":"` + string(id) + `"` + extra + `}`
	}

	want := func(step string, n int, typ string, id storage.ItemID) {
		t.Helper()
		got := playbackEntries(e.activityEntries())
		if len(got) != n {
			t.Fatalf("%s: %d playback entries, want %d: %+v", step, len(got), n, got)
		}
		if typ == "" {
			return
		}
		top := got[0]
		if top.Type != typ || top.ItemId != string(id) || top.UserId != string(e.alice.ID) {
			t.Fatalf("%s: newest entry = %+v, want type %s item %s user %s", step, top, typ, id, e.alice.ID)
		}
		if top.Name == "" {
			t.Fatalf("%s: entry has no name", step)
		}
	}

	want("before", 0, "", "")
	e.report(e.aliceTok, "", body(e.movie, ""))
	want("start video", 1, "VideoPlayback", e.movie)
	e.report(e.aliceTok, "/Progress", body(e.movie, `,"PositionTicks":25000000`))
	want("progress", 1, "VideoPlayback", e.movie)
	e.report(e.aliceTok, "/Stopped", body(e.movie, `,"PositionTicks":25000000`))
	want("stop video", 2, "VideoPlaybackStopped", e.movie)
	e.report(e.aliceTok, "/Stopped", body(e.movie, `,"Failed":true`))
	want("failed stop", 3, "VideoPlaybackStopped", e.movie)
	e.report(e.aliceTok, "", body(song, ""))
	want("start audio", 4, "AudioPlayback", song)
	e.report(e.aliceTok, "/Stopped", body(song, ""))
	want("stop audio", 5, "AudioPlaybackStopped", song)

	// Nothing is written for an unknown or missing item.
	e.report(e.aliceTok, "", body("ffffffffffffffffffffffffffffffff", ""))
	e.report(e.aliceTok, "/Stopped", body("ffffffffffffffffffffffffffffffff", ""))
	e.report(e.aliceTok, "", `{}`)
	want("unknown item", 5, "AudioPlaybackStopped", song)

	// Another media type starts as a plain "Playback" and stops silently.
	e.report(e.aliceTok, "", body(e.show, ""))
	want("start other", 6, "Playback", e.show)
	e.report(e.aliceTok, "/Stopped", body(e.show, ""))
	want("stop other", 6, "Playback", e.show)
}
