package scan

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// A music library: a folder holding audio files is an album, the folders
// above it at the root are artists, tracks hang off their album. The files
// carry no tags here (and cannot be probed), so names come from the files and
// folders.
func TestScanMusicStructure(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		"Artist One/Album Alpha (2019)/01 - First.mp3",
		"Artist One/Album Alpha (2019)/02 - Second.flac",
		"Artist One/Album Alpha (2019)/cover.jpg",
		"Artist One/Album Beta/01 - Opening.ogg",
		"Artist Two/Standalone/Track.m4a",
		"Loose Song.mp3",
		"Artist One/notes.txt",
		".hidden/Skip.mp3",
	} {
		writeFile(t, filepath.Join(root, filepath.FromSlash(rel)), "x")
	}
	items, _, _ := scanAll(t, root, storage.CollectionTypeMusic)
	type want struct {
		typ    storage.ItemType
		name   string
		parent string
	}
	cases := map[string]want{
		"Artist One":         {storage.ItemTypeMusicArtist, "Artist One", ""},
		"Artist Two":         {storage.ItemTypeMusicArtist, "Artist Two", ""},
		"Album Alpha (2019)": {storage.ItemTypeMusicAlbum, "Album Alpha (2019)", "Artist One"},
		"Album Beta":         {storage.ItemTypeMusicAlbum, "Album Beta", "Artist One"},
		"Standalone":         {storage.ItemTypeMusicAlbum, "Standalone", "Artist Two"},
		"01 - First.mp3":     {storage.ItemTypeAudio, "01 - First", "Album Alpha (2019)"},
		"02 - Second.flac":   {storage.ItemTypeAudio, "02 - Second", "Album Alpha (2019)"},
		"01 - Opening.ogg":   {storage.ItemTypeAudio, "01 - Opening", "Album Beta"},
		"Track.m4a":          {storage.ItemTypeAudio, "Track", "Standalone"},
		"Loose Song.mp3":     {storage.ItemTypeAudio, "Loose Song", ""},
	}
	for key, w := range cases {
		it, ok := items[key]
		if !ok {
			t.Errorf("%s: not cataloged", key)
			continue
		}
		if it.Type != w.typ || it.Name != w.name {
			t.Errorf("%s: got %s %q, want %s %q", key, it.Type, it.Name, w.typ, w.name)
		}
		switch {
		case w.parent == "" && it.ParentID != nil:
			t.Errorf("%s: has a parent, want none", key)
		case w.parent != "":
			if it.ParentID == nil || *it.ParentID != items[w.parent].ID {
				t.Errorf("%s: parent is not %s", key, w.parent)
			}
		}
	}
	for _, key := range []string{"notes.txt", "Skip.mp3", ".hidden"} {
		if _, ok := items[key]; ok {
			t.Errorf("%s: cataloged, want left out", key)
		}
	}
}

func TestMusicTagParsing(t *testing.T) {
	tags := map[string]string{
		"TITLE": " Song ", "artist": "A; B", "ALBUM": "Alb", "album_artist": "AA",
		"track": "3/12", "DISC": "2", "date": "2019-05-06", "genre": "Rock;Pop",
	}
	m := readMusicTags(tags)
	if m.title != "Song" || m.album != "Alb" || m.albumArtist != "AA" {
		t.Fatalf("names: %+v", m)
	}
	if m.track == nil || *m.track != 3 || m.disc == nil || *m.disc != 2 {
		t.Fatalf("numbers: track=%v disc=%v", m.track, m.disc)
	}
	if m.year == nil || *m.year != 2019 || m.premiere == nil || !m.premiere.Equal(time.Date(2019, 5, 6, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("date: %v %v", m.year, m.premiere)
	}
	if got := tagList(m.artist); len(got) != 1 || got[0] != "A; B" {
		t.Fatalf("artists: %v", got)
	}
	if y, d := parseTagDate("2021"); y == nil || *y != 2021 || d == nil || d.Month() != time.January || d.Day() != 1 {
		t.Fatalf("year only: %v %v", y, d)
	}
	if y, _ := parseTagDate("n/a"); y != nil {
		t.Fatalf("junk date read as %d", *y)
	}
	// the albumartist spelling, in any case
	if m := readMusicTags(map[string]string{"ALBUMARTIST": "X", "TRACKNUMBER": "7"}); m.albumArtist != "X" || m.track == nil || *m.track != 7 {
		t.Fatalf("vorbis spelling: %+v", m)
	}
}

func TestTrackSortName(t *testing.T) {
	one, two := 1, 2
	if got := trackSortName("Song", &one, &two); got != "0002 - 0001 - Song" {
		t.Errorf("disc and track: %q", got)
	}
	if got := trackSortName("Song", &one, nil); got != "0001 - Song" {
		t.Errorf("track only: %q", got)
	}
	if got := trackSortName("Song", nil, nil); got != "Song" {
		t.Errorf("none: %q", got)
	}
}

// An Ogg file keeps its tags on the audio stream; a container tag wins.
func TestAudioTagsFromStreamAndFormat(t *testing.T) {
	var info ffprobeOutput
	raw := `{"streams":[{"codec_type":"audio","codec_name":"vorbis","tags":{"TITLE":"From Stream","ARTIST":"S"}}],
	  "format":{"tags":{"title":"From Format"}}}`
	if err := json.Unmarshal([]byte(raw), &info); err != nil {
		t.Fatal(err)
	}
	got := audioTags(info)
	if got["title"] != "From Format" || got["artist"] != "S" {
		t.Fatalf("tags: %v", got)
	}
	if info.Streams[0].Tags.Title != "From Stream" {
		t.Fatalf("stream title: %q", info.Streams[0].Tags.Title)
	}
}

func TestAttachedPictureIsNotAVideoStream(t *testing.T) {
	var info ffprobeOutput
	raw := `{"streams":[{"codec_type":"audio","codec_name":"mp3"},
	  {"codec_type":"video","codec_name":"mjpeg","disposition":{"attached_pic":1}}]}`
	if err := json.Unmarshal([]byte(raw), &info); err != nil {
		t.Fatal(err)
	}
	streams := mediaStreamsFromProbe(info)
	if len(streams) != 2 || streams[1].Type != storage.StreamTypeEmbeddedImage {
		t.Fatalf("streams: %+v", streams)
	}
	if art := attachedPicture(info); art == nil || art.index != 1 || art.codec != "mjpeg" {
		t.Fatalf("picture: %+v", art)
	}
}
