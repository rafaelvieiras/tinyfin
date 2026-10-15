package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// lyricsFileFor finds the lyrics file that sits beside an audio file: the
// same name with an .lrc (timed) or .txt (plain) extension, in that order.
// It returns the path and whether the file is timed; "" when there is none.
func lyricsFileFor(audioPath string) (string, bool) {
	if audioPath == "" {
		return "", false
	}
	base := strings.TrimSuffix(audioPath, filepath.Ext(audioPath))
	for _, c := range []struct {
		ext   string
		timed bool
	}{{".lrc", true}, {".txt", false}} {
		if fi, err := os.Stat(base + c.ext); err == nil && !fi.IsDir() {
			return base + c.ext, c.timed
		}
	}
	return "", false
}

// lyricLineDto is one line of a lyrics document. A timed line carries Start
// (in ticks) and an empty Cues list; a plain one only its text.
type lyricLineDto struct {
	Text  string    `json:"Text"`
	Start *int64    `json:"Start,omitempty"`
	Cues  *[]string `json:"Cues,omitempty"`
}

type lyricsDto struct {
	Metadata map[string]string `json:"Metadata"`
	Lyrics   []lyricLineDto    `json:"Lyrics"`
}

var lrcStampRe = regexp.MustCompile(`\[(\d+):(\d{1,2})(?:[.:](\d{1,3}))?\]`)

// parseLRC reads a timed lyrics file: every "[mm:ss.xx]" stamp of a line
// makes one lyric line with the text that follows the stamps; lines with no
// stamp (tags like "[ar:...]", untimed text) are left out; lines are in
// time order. The offset tag is not applied.
func parseLRC(text string) []lyricLineDto {
	out := []lyricLineDto{}
	for _, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		stamps := lrcStampRe.FindAllStringSubmatchIndex(line, -1)
		if len(stamps) == 0 {
			continue
		}
		// Only leading stamps count.
		end, n := 0, 0
		for _, m := range stamps {
			if m[0] != end {
				break
			}
			end = m[1]
			n++
		}
		if n == 0 {
			continue
		}
		body := strings.TrimSpace(line[end:])
		for _, m := range stamps[:n] {
			min, _ := strconv.Atoi(line[m[2]:m[3]])
			sec, _ := strconv.Atoi(line[m[4]:m[5]])
			var frac int64
			if m[6] >= 0 {
				f := line[m[6]:m[7]]
				v, _ := strconv.Atoi(f)
				scale := int64(10_000_000)
				for range f {
					scale /= 10
				}
				frac = int64(v) * scale
			}
			start := (int64(min)*60+int64(sec))*10_000_000 + frac
			cues := []string{}
			out = append(out, lyricLineDto{Text: body, Start: &start, Cues: &cues})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return *out[i].Start < *out[j].Start })
	return out
}

// parsePlainLyrics reads an untimed lyrics file: one line per text line.
func parsePlainLyrics(text string) []lyricLineDto {
	out := []lyricLineDto{}
	for _, l := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		out = append(out, lyricLineDto{Text: l})
	}
	return out
}

// handleAudioLyrics serves GET /Audio/{itemId}/Lyrics: the lyrics file that
// sits beside the track, parsed. A track with none is a 404.
func (s *Server) handleAudioLyrics(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.GetItem(r.Context(), storage.ItemID(r.PathValue("itemId")))
	if err != nil || !s.itemReadableByCaller(r, item) {
		writeItemsProblem(w, http.StatusNotFound, "Not Found")
		return
	}
	lp, timed := "", false
	if item.Type == storage.ItemTypeAudio {
		lp, timed = lyricsFileFor(item.Path)
	}
	data, err := os.ReadFile(lp)
	if lp == "" || err != nil {
		writeItemsProblem(w, http.StatusNotFound, "Not Found")
		return
	}
	text := strings.TrimPrefix(string(data), "\ufeff")
	doc := lyricsDto{Metadata: map[string]string{}}
	if timed {
		doc.Lyrics = parseLRC(text)
	} else {
		doc.Lyrics = parsePlainLyrics(text)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(doc)
}

// handleArtistByName is GET /Artists/{name}: the artist item that bears the
// name (an artist folder before a name taken from tags), as /Items/{id}.
func (s *Server) handleArtistByName(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := r.PathValue("name")
	user, ok := s.itemsContextUser(w, r, caseInsensitiveQuery(r).Get("userid"))
	if !ok {
		return
	}
	sc, err := s.itemsScopeFor(ctx, user)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	var f storage.ItemFilter
	sc.apply(&f)
	f.Recursive, f.IncludeExtras, f.NameContains, f.Limit = true, true, name, 100
	f.ItemTypes = []storage.ItemType{storage.ItemTypeMusicArtist}
	page, err := s.store.QueryItems(ctx, f)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	var found *storage.Item
	for i, it := range page.Items {
		if !strings.EqualFold(it.Name, name) {
			continue
		}
		if found == nil || (found.ExtraType != "" && it.ExtraType == "") {
			found = &page.Items[i]
		}
	}
	if found == nil {
		writeItemsProblem(w, http.StatusNotFound, "Not Found")
		return
	}
	r.SetPathValue("itemId", string(found.ID))
	s.handleItemGet(w, r)
}
