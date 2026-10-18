// subtitles.go implements the Subtitle group of docs/04: serving a
// subtitle stream (external files and text streams extracted from the
// media file) in the formats the web player asks for, the HLS playlist of
// a subtitle stream, upload and deletion of external files, and the routes
// that exist for subtitle providers and fallback fonts (an empty provider
// list: Tinyfin has no subtitle provider plugins).
//
// Behavior is specified in docs/contract/specs/subtitles.md.
package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
	"github.com/rafaelvieiras/tinyfin/internal/subtitle"
)

// writeSubtitleError is the opaque plain-text failure the reference answers
// with for a subtitle request it cannot serve.
func writeSubtitleError(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(status)
	_, _ = w.Write([]byte("Error processing request."))
}

// canManageSubtitles reports whether the caller holds the subtitle
// management policy (administrators always do).
func canManageSubtitles(ac *AuthContext) bool {
	if ac == nil || !ac.Authenticated {
		return false
	}
	return ac.User.IsAdministrator || storedUserPolicy(ac.User).EnableSubtitleManagement
}

func requireSubtitleManagement(next http.HandlerFunc) http.HandlerFunc {
	return requireAuth(func(w http.ResponseWriter, r *http.Request) {
		ac, _ := AuthFromContext(r.Context())
		if !canManageSubtitles(ac) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		next(w, r)
	})
}

// subtitleSource resolves the media source a subtitle request names: the
// item itself or one of its versions. ok is false when the id names none
// (the reference answers 500 then).
func (s *Server) subtitleSource(ctx context.Context, item storage.Item, rawSource string) (storage.Item, bool) {
	norm, ok := normalizeGUID(rawSource)
	if !ok {
		return item, false
	}
	if norm == string(item.ID) {
		return item, true
	}
	v, err := s.store.GetItem(ctx, storage.ItemID(norm))
	if err != nil || v.OwnerID == nil || *v.OwnerID != item.ID || v.ExtraType != storage.ExtraTypeVersion {
		return item, false
	}
	return v, true
}

// subtitleStreamOf returns the subtitle stream of src numbered index.
func subtitleStreamOf(src storage.Item, index int) (storage.MediaStreamRef, bool) {
	for _, st := range src.MediaStreams {
		if st.Index == index && st.Type == storage.StreamTypeSubtitle {
			return st, true
		}
	}
	return storage.MediaStreamRef{}, false
}

// subtitleKindOf maps the extension of an external subtitle file to the
// parser family that reads it.
func subtitleKindOf(path string) (subtitle.Kind, string) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	switch ext {
	case "vtt":
		return subtitle.KindVTT, ext
	case "ass":
		return subtitle.KindASS, ext
	case "ssa":
		return subtitle.KindSSA, ext
	case "smi", "sami":
		return subtitle.KindSAMI, ext
	}
	return subtitle.KindSRT, ext
}

// decodeSubtitleText returns data as UTF-8: UTF-16 (with a byte order mark)
// and single-byte (Windows-1252) files are converted.
func decodeSubtitleText(data []byte) []byte {
	if len(data) >= 2 && (data[0] == 0xFF && data[1] == 0xFE || data[0] == 0xFE && data[1] == 0xFF) {
		le := data[0] == 0xFF
		u := make([]uint16, 0, len(data)/2)
		for i := 2; i+1 < len(data); i += 2 {
			if le {
				u = append(u, binary.LittleEndian.Uint16(data[i:]))
			} else {
				u = append(u, binary.BigEndian.Uint16(data[i:]))
			}
		}
		return []byte(string(utf16.Decode(u)))
	}
	if utf8.Valid(data) {
		return data
	}
	var b bytes.Buffer
	for _, c := range data {
		b.WriteRune(rune(c))
	}
	return b.Bytes()
}

// subtitleOrdinal is the position of st among the subtitle streams that come
// from the same file (the media file for an embedded one, the subtitle file
// for an external one): what ffmpeg's 0:s:N counts.
func subtitleOrdinal(src storage.Item, st storage.MediaStreamRef) int {
	ordinal := 0
	for _, o := range src.MediaStreams {
		if o.Type == storage.StreamTypeSubtitle && o.IsExternal == st.IsExternal && o.Path == st.Path && o.Index < st.Index {
			ordinal++
		}
	}
	return ordinal
}

// subtitleFile is the file stream st is read from.
func subtitleFile(src storage.Item, st storage.MediaStreamRef) string {
	if st.IsExternal {
		return st.Path
	}
	return src.Path
}

// extractEmbeddedSubtitle writes a text stream of a media or Matroska
// subtitle file as text of the family it is kept in: ASS stays ASS, every
// other text track (and a picture track, whose decoder yields no text but
// whatever ffmpeg makes of it) comes out as SubRip. bom is true when the
// ASS text had its "Arial" style font replaced, which the reference marks
// with a byte order mark when it serves the text as it is.
func (s *Server) extractEmbeddedSubtitle(ctx context.Context, src storage.Item, st storage.MediaStreamRef) (data []byte, kind subtitle.Kind, ext string, bom bool, err error) {
	ordinal := subtitleOrdinal(src, st)
	kind, codec, format := subtitle.KindSRT, "srt", "srt"
	if strings.EqualFold(st.Codec, "ass") || strings.EqualFold(st.Codec, "ssa") {
		kind, codec, format = subtitle.KindASS, "ass", "ass"
	}
	ffmpeg := s.cfg.TranscodeFFmpegPath
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpeg, "-v", "error", "-nostdin", "-i", subtitleFile(src, st),
		"-map", fmt.Sprintf("0:s:%d", ordinal), "-an", "-vn", "-c:s", codec, "-f", format, "-")
	out, err := cmd.Output()
	if err != nil {
		return nil, kind, "", false, err
	}
	if kind == subtitle.KindASS {
		out, bom = replaceArialStyle(out)
	}
	return out, kind, format, bom, nil
}

// replaceArialStyle swaps the font "Arial" of the Style lines of an ASS
// script for "Arial Unicode MS", as the reference does to the tracks it
// extracts, and reports whether it changed anything.
func replaceArialStyle(data []byte) ([]byte, bool) {
	lines := bytes.Split(data, []byte("\n"))
	changed := false
	for i, ln := range lines {
		if !bytes.HasPrefix(ln, []byte("Style:")) {
			continue
		}
		fields := bytes.SplitN(ln, []byte(","), 3)
		if len(fields) == 3 && string(bytes.TrimSpace(fields[1])) == "Arial" {
			lines[i] = bytes.Join([][]byte{fields[0], []byte("Arial Unicode MS"), fields[2]}, []byte(","))
			changed = true
		}
	}
	if !changed {
		return data, false
	}
	return bytes.Join(lines, []byte("\n")), true
}

// handleSubtitleStream serves GET /Videos/{itemId}/{mediaSourceId}/
// Subtitles/{index}/Stream.{format} and the same with a start position in
// ticks before the file name, and GET .../Subtitles/{index}/subtitles.m3u8.
func (s *Server) handleSubtitleStream(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("file")
	lower := strings.ToLower(file)
	rawItem, rawSource, rawIndex := r.PathValue("itemId"), r.PathValue("mediaSourceId"), r.PathValue("index")
	rawTicks := r.PathValue("startTicks")

	isPlaylist := lower == "subtitles.m3u8" && rawTicks == ""
	if !isPlaylist && (!strings.HasPrefix(lower, "stream.") || lower == "stream.") {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	itemKey, indexKey, ticksKey := "routeItemId", "routeIndex", "routeStartPositionTicks"
	if isPlaylist {
		itemKey, indexKey = "itemId", "index"
	}
	errs := map[string][]string{}
	itemID, okItem := normalizeGUID(rawItem)
	if !okItem {
		errs[itemKey] = []string{"The value '" + rawItem + "' is not valid."}
	}
	index, err := strconv.Atoi(rawIndex)
	if err != nil {
		errs[indexKey] = []string{"The value '" + rawIndex + "' is not valid."}
	}
	var startTicks int64
	if rawTicks != "" {
		if startTicks, err = strconv.ParseInt(rawTicks, 10, 64); err != nil {
			errs[ticksKey] = []string{"The value '" + rawTicks + "' is not valid."}
		}
	}
	q := caseInsensitiveQuery(r)
	if isPlaylist {
		if q.Get("segmentlength") == "" {
			errs["segmentLength"] = []string{"The segmentLength field is required."}
		}
	}
	var endTicks int64
	var copyTS, timeMap bool
	if !isPlaylist {
		bad := func(name, v string) { errs[name] = []string{"The value '" + v + "' is not valid."} }
		if v := q.Get("startpositionticks"); v != "" {
			if startTicks, err = strconv.ParseInt(v, 10, 64); err != nil {
				bad("startPositionTicks", v)
			}
		}
		if v := q.Get("endpositionticks"); v != "" {
			if endTicks, err = strconv.ParseInt(v, 10, 64); err != nil {
				bad("endPositionTicks", v)
			}
		}
		for _, f := range []struct {
			key, name string
			dst       *bool
		}{{"copytimestamps", "copyTimestamps", &copyTS}, {"addvtttimemap", "addVttTimeMap", &timeMap}} {
			switch v := q.Get(f.key); {
			case v == "":
			case strings.EqualFold(v, "true"):
				*f.dst = true
			case strings.EqualFold(v, "false"):
			default:
				bad(f.name, v)
			}
		}
	}
	if len(errs) > 0 {
		writeValidationErrors(w, errs)
		return
	}

	var format subtitle.Format
	var alias, knownFormat bool
	var rawFormat string // the format as the URL spells it
	if !isPlaylist {
		rawFormat = file[len("stream."):]
		format, alias, knownFormat = subtitle.ParseFormat(rawFormat)
	}

	item, err := s.store.GetItem(r.Context(), storage.ItemID(itemID))
	if err != nil || !s.itemReadableByCaller(r, item) || !item.HasMediaFile() {
		writeSubtitleError(w, http.StatusBadRequest)
		return
	}
	src, ok := s.subtitleSource(r.Context(), item, rawSource)
	if !ok {
		writeSubtitleError(w, http.StatusInternalServerError)
		return
	}

	if isPlaylist {
		s.writeSubtitlePlaylist(w, r, src, q.Get("segmentlength"))
		return
	}

	st, ok := subtitleStreamOf(src, index)
	if !ok {
		if !knownFormat && !bitmapCopyFormat(rawFormat) {
			writeSubtitleError(w, http.StatusBadRequest)
			return
		}
		writeSubtitleError(w, http.StatusInternalServerError)
		return
	}
	if !subtitle.SupportsExternal(st.Codec) {
		writeSubtitleError(w, http.StatusNotFound)
		return
	}
	lowerFormat := strings.ToLower(rawFormat)
	fileExt := ""
	if st.IsExternal {
		fileExt = strings.ToLower(strings.TrimPrefix(filepath.Ext(st.Path), "."))
	}

	// External files have their own rules, whatever the track inside them.
	var pre *extractedSubtitle
	switch fileExt {
	case "smi":
		if lowerFormat == "smi" {
			s.serveSubtitleFile(w, r, st.Path, true, "application/smil+xml")
			return
		}
	case "sub":
		s.serveSubtitleFile(w, r, st.Path, lowerFormat == "sub", "text/vnd.dvb.subtitle")
		return
	case "idx":
		if lowerFormat == "idx" {
			s.serveSubtitleFile(w, r, st.Path, true, "application/octet-stream")
			return
		}
	case "sami":
		// not a name the reference reads itself: whatever ffmpeg makes of
		// the file as SubRip, or a failure, before the format is looked at
		as := st
		as.Codec = "subrip"
		data, kind, ext, _, err := s.extractEmbeddedSubtitle(r.Context(), src, as)
		if err != nil || len(data) == 0 {
			writeSubtitleError(w, http.StatusInternalServerError)
			return
		}
		if lowerFormat == "smi" {
			writeSubtitleError(w, http.StatusBadRequest)
			return
		}
		pre = &extractedSubtitle{data: decodeSubtitleText(data), kind: kind, ext: ext}
	case "mks":
		if !hasEBMLHeader(st.Path) && !strings.EqualFold(st.Codec, "ass") && subtitle.NormalizeCodec(st.Codec) != "text" {
			writeSubtitleError(w, http.StatusNotFound)
			return
		}
	}

	if st.IsExternal && (nativeSubtitleText[fileExt] || fileExt == "sami") && !subtitle.IsTextCodec(st.Codec) {
		// pictures inside a text file's name: the file as it is, in the
		// format its extension names, and nothing else
		if knownFormat && !alias && (lowerFormat == fileExt || (fileExt == "ssa" && format == subtitle.FormatASS)) {
			s.serveSubtitleFile(w, r, st.Path, true, format.ContentType())
			return
		}
		writeSubtitleError(w, http.StatusBadRequest)
		return
	}
	if fileExt == "sup" && subtitle.IsTextCodec(st.Codec) {
		writeSubtitleError(w, http.StatusBadRequest)
		return
	}
	if !subtitle.IsTextCodec(st.Codec) && fileExt != "idx" {
		if !bitmapCopyFormat(rawFormat) {
			writeSubtitleError(w, http.StatusBadRequest)
			return
		}
		s.serveBitmapSubtitle(w, r, src, st, lowerFormat)
		return
	}
	if !knownFormat {
		writeSubtitleError(w, http.StatusBadRequest)
		return
	}

	var data []byte
	var kind subtitle.Kind
	var ext string
	var bom bool
	if pre != nil {
		data, kind, ext = pre.data, pre.kind, pre.ext
	} else if fileExt == "idx" {
		raw, err := os.ReadFile(st.Path)
		if err != nil {
			writeSubtitleError(w, http.StatusInternalServerError)
			return
		}
		data, kind, ext = decodeSubtitleText(raw), subtitle.KindIDX, "idx"
	} else if st.IsExternal && fileExt != "mks" {
		raw, err := os.ReadFile(st.Path)
		if err != nil {
			writeSubtitleError(w, http.StatusInternalServerError)
			return
		}
		data = decodeSubtitleText(raw)
		kind, ext = subtitleKindOf(st.Path)
	} else {
		data, kind, ext, bom, err = s.extractEmbeddedSubtitle(r.Context(), src, st)
		if err != nil {
			writeSubtitleError(w, http.StatusInternalServerError)
			return
		}
		data = decodeSubtitleText(data)
		if fileExt == "mks" && len(data) == 0 {
			writeSubtitleError(w, http.StatusNotFound)
			return
		}
	}

	var out []byte
	switch {
	case !alias && (string(format) == ext || (ext == "ssa" && format == subtitle.FormatASS)):
		out = data
		if format == subtitle.FormatVTT && timeMap {
			out = subtitle.InsertVTTTimeMap(out)
		}
		if bom && format == subtitle.FormatASS {
			out = append([]byte("\xef\xbb\xbf"), out...)
		}
	default:
		parsed := subtitle.Parse(kind, data)
		if len(parsed.Cues) == 0 && nativeSubtitleText[fileExt] {
			// a file is converted only when it holds cues of what its
			// extension says
			writeSubtitleError(w, http.StatusBadRequest)
			return
		}
		out = parsed.Window(startTicks, endTicks, copyTS).Write(format, subtitle.Options{VTTTimeMap: timeMap})
	}
	contentType := format.ContentType()
	if alias {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(out)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(out)
	}
}

// extractedSubtitle is the text ffmpeg made of a subtitle file.
type extractedSubtitle struct {
	data []byte
	kind subtitle.Kind
	ext  string
}

// nativeSubtitleText are the extensions of the text files the server reads
// itself: a request to convert one that yields no cue is refused.
var nativeSubtitleText = map[string]bool{"srt": true, "vtt": true, "ass": true, "ssa": true, "smi": true}

// hasEBMLHeader reports whether the file starts with the EBML header.
func hasEBMLHeader(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return false
	}
	return magic == [4]byte{0x1a, 0x45, 0xdf, 0xa3}
}

// serveSubtitleFile answers with the bytes of a subtitle file as they are
// (ok), or with a 400 when the format asked for is not the file's own.
func (s *Server) serveSubtitleFile(w http.ResponseWriter, r *http.Request, path string, ok bool, contentType string) {
	if !ok {
		writeSubtitleError(w, http.StatusBadRequest)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		writeSubtitleError(w, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

// bitmapCopyFormat reports whether name is a format a picture track can be
// asked for: the names of its own codecs, which all mean "the track as it is
// stored" (the container extension of a DVD track, mks, and dvbsub, which no
// track but a DVB one answers, are checked per track).
func bitmapCopyFormat(name string) bool {
	switch strings.ToLower(name) {
	case "pgssub", "sup", "pgs", "hdmv_pgs_subtitle", "dvdsub", "vobsub", "mks", "dvbsub":
		return true
	}
	return false
}

// serveBitmapSubtitle answers a request for a picture track (PGS or DVD),
// which is not converted: a PGS track comes out as a .sup stream
// (application/octet-stream), a DVD track as a Matroska file holding only that
// track (video/x-matroska, the one format "mks" is accepted for), a sidecar
// .sup file as it is. Any other format (srt, vtt, ...) is a 400.
func (s *Server) serveBitmapSubtitle(w http.ResponseWriter, r *http.Request, src storage.Item, st storage.MediaStreamRef, format string) {
	dvd := strings.EqualFold(st.Codec, "DVDSUB")
	if format == "" || format == "dvbsub" || (format == "mks" && (!dvd || (st.IsExternal && !strings.EqualFold(filepath.Ext(st.Path), ".mks")))) {
		writeSubtitleError(w, http.StatusBadRequest)
		return
	}
	var data []byte
	if st.IsExternal && !strings.EqualFold(filepath.Ext(st.Path), ".mks") {
		raw, err := os.ReadFile(st.Path)
		if err != nil {
			writeSubtitleError(w, http.StatusInternalServerError)
			return
		}
		data = raw
	} else {
		ordinal := subtitleOrdinal(src, st)
		ffmpeg := s.cfg.TranscodeFFmpegPath
		if ffmpeg == "" {
			ffmpeg = "ffmpeg"
		}
		muxer, ext := "sup", ".sup"
		if dvd {
			muxer, ext = "matroska", ".mks"
		}
		dir, err := os.MkdirTemp("", "tinyfin-sub-")
		if err != nil {
			writeSubtitleError(w, http.StatusInternalServerError)
			return
		}
		defer os.RemoveAll(dir)
		out := filepath.Join(dir, "track"+ext)
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, ffmpeg, "-v", "error", "-nostdin", "-i", subtitleFile(src, st),
			"-map", fmt.Sprintf("0:s:%d", ordinal), "-an", "-vn", "-c:s", "copy", "-f", muxer, out)
		if err := cmd.Run(); err != nil {
			writeSubtitleError(w, http.StatusInternalServerError)
			return
		}
		if data, err = os.ReadFile(out); err != nil {
			writeSubtitleError(w, http.StatusInternalServerError)
			return
		}
	}
	contentType := "application/octet-stream"
	if dvd && (!st.IsExternal || strings.EqualFold(filepath.Ext(st.Path), ".mks")) {
		contentType = "video/x-matroska"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

// writeSubtitlePlaylist writes the VOD playlist of a subtitle stream:
// segments of segmentLength seconds covering the source's run time, each
// naming the WebVTT window of its range.
func (s *Server) writeSubtitlePlaylist(w http.ResponseWriter, r *http.Request, src storage.Item, rawLen string) {
	seg, err := strconv.Atoi(rawLen)
	if err != nil {
		writeValidationProblem(w, "segmentLength", "The value '"+rawLen+"' is not valid.")
		return
	}
	if seg <= 0 {
		writeSubtitleError(w, http.StatusBadRequest)
		return
	}
	var runtime int64
	if src.RunTimeTicks != nil {
		runtime = *src.RunTimeTicks
	}
	var token string
	if ac, ok := AuthFromContext(r.Context()); ok && ac != nil {
		token = ac.Token
	}
	segTicks := int64(seg) * 10_000_000
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", seg)
	b.WriteString("#EXT-X-VERSION:3\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n")
	for pos := int64(0); pos < runtime; pos += segTicks {
		end := pos + segTicks
		if end > runtime {
			end = runtime
		}
		fmt.Fprintf(&b, "#EXTINF:%s,\n", strconv.FormatFloat(float64(end-pos)/1e7, 'f', -1, 64))
		fmt.Fprintf(&b, "stream.vtt?CopyTimestamps=true&AddVttTimeMap=true&StartPositionTicks=%d&EndPositionTicks=%d", pos, end)
		if token != "" {
			b.WriteString("&ApiKey=" + url.QueryEscape(token))
		}
		b.WriteString("\n")
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Content-Length", strconv.Itoa(b.Len()))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(b.String()))
	}
}

// uploadSubtitleBody is the UploadSubtitleDto request body.
type uploadSubtitleBody struct {
	Language          *string `json:"Language"`
	Format            *string `json:"Format"`
	IsForced          bool    `json:"IsForced"`
	IsHearingImpaired bool    `json:"IsHearingImpaired"`
	Data              *string `json:"Data"`
}

// sanitizeFilePart drops what cannot sit in a file name.
func sanitizeFilePart(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == '\\' || r == 0 || r < 0x20:
			return '_'
		}
		return r
	}, strings.TrimSpace(s))
}

// handleSubtitleUpload serves POST /Videos/{itemId}/Subtitles: the subtitle
// is saved beside the video as <name>.<language>[.forced][.sdh].<format>
// (a numbered variant when the name is taken) and cataloged.
func (s *Server) handleSubtitleUpload(w http.ResponseWriter, r *http.Request) {
	rawItem := r.PathValue("itemId")
	errs := map[string][]string{}
	itemID, ok := normalizeGUID(rawItem)
	if !ok {
		errs["itemId"] = []string{"The value '" + rawItem + "' is not valid."}
	}
	var body uploadSubtitleBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		if len(errs) > 0 {
			writeValidationErrors(w, errs)
			return
		}
		writeInvalidBody(w)
		return
	}
	if body.Language == nil {
		errs["Language"] = []string{"The Language field is required."}
	}
	if body.Format == nil {
		errs["Format"] = []string{"The Format field is required."}
	}
	if body.Data == nil {
		errs["Data"] = []string{"The Data field is required."}
	}
	if len(errs) > 0 {
		writeValidationErrors(w, errs)
		return
	}
	item, err := s.store.GetItem(r.Context(), storage.ItemID(itemID))
	if err != nil || !item.HasMediaFile() || !s.itemReadableByCaller(r, item) {
		writeSubtitleError(w, http.StatusBadRequest)
		return
	}
	format := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(*body.Format), "."))
	if _, ok := subtitle.ExternalCodec(format); !ok {
		writeSubtitleError(w, http.StatusBadRequest)
		return
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(*body.Data))
	if err != nil || len(data) == 0 {
		writeSubtitleError(w, http.StatusBadRequest)
		return
	}

	dir := filepath.Dir(item.Path)
	base := strings.TrimSuffix(filepath.Base(item.Path), filepath.Ext(item.Path))
	name := base + "." + sanitizeFilePart(*body.Language)
	if body.IsForced {
		name += ".forced"
	}
	if body.IsHearingImpaired {
		name += ".sdh"
	}
	path := filepath.Join(dir, name+"."+format)
	for n := 0; ; n++ {
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			break
		}
		path = filepath.Join(dir, fmt.Sprintf("%s.%d.%s", name, n, format))
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		writeSubtitleError(w, http.StatusInternalServerError)
		return
	}
	if err := s.scanner.RefreshExternalSubtitles(r.Context(), item.ID); err != nil {
		writeSubtitleError(w, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSubtitleDelete serves DELETE /Videos/{itemId}/Subtitles/{index}:
// the external subtitle file of that stream is removed.
func (s *Server) handleSubtitleDelete(w http.ResponseWriter, r *http.Request) {
	errs := map[string][]string{}
	rawItem, rawIndex := r.PathValue("itemId"), r.PathValue("index")
	itemID, ok := normalizeGUID(rawItem)
	if !ok {
		errs["itemId"] = []string{"The value '" + rawItem + "' is not valid."}
	}
	index, err := strconv.Atoi(rawIndex)
	if err != nil {
		errs["index"] = []string{"The value '" + rawIndex + "' is not valid."}
	}
	if len(errs) > 0 {
		writeValidationErrors(w, errs)
		return
	}
	item, err := s.store.GetItem(r.Context(), storage.ItemID(itemID))
	if err != nil || !item.HasMediaFile() {
		writeSubtitleError(w, http.StatusBadRequest)
		return
	}
	st, ok := subtitleStreamOf(item, index)
	if !ok || !st.IsExternal {
		writeSubtitleError(w, http.StatusBadRequest)
		return
	}
	base := strings.TrimSuffix(filepath.Base(item.Path), filepath.Ext(item.Path))
	// Only a file that is a sidecar of this video, in its own folder, is
	// ever removed.
	if filepath.Dir(st.Path) != filepath.Dir(item.Path) {
		writeSubtitleError(w, http.StatusBadRequest)
		return
	}
	if _, isSidecar := subtitle.ParseSidecar(base, filepath.Base(st.Path)); !isSidecar {
		writeSubtitleError(w, http.StatusBadRequest)
		return
	}
	if err := os.Remove(st.Path); err != nil && !os.IsNotExist(err) {
		writeSubtitleError(w, http.StatusInternalServerError)
		return
	}
	if err := s.scanner.RefreshExternalSubtitles(r.Context(), item.ID); err != nil {
		writeSubtitleError(w, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRemoteSubtitleSearch serves GET /Items/{itemId}/RemoteSearch/
// Subtitles/{language}: no subtitle provider is installed, so the list is
// always empty.
func (s *Server) handleRemoteSubtitleSearch(w http.ResponseWriter, r *http.Request) {
	rawItem := r.PathValue("itemId")
	itemID, ok := normalizeGUID(rawItem)
	if !ok {
		writeInvalidID(w, "itemId", rawItem)
		return
	}
	if _, err := s.store.GetItem(r.Context(), storage.ItemID(itemID)); err != nil {
		writeProblemDoc(w, http.StatusNotFound, problemTypeNotFound, "Not Found", nil)
		return
	}
	writeJSON(w, http.StatusOK, []any{})
}

// handleRemoteSubtitleDownload serves POST /Items/{itemId}/RemoteSearch/
// Subtitles/{subtitleId}: with no provider there is nothing to fetch and
// the request is accepted without effect.
func (s *Server) handleRemoteSubtitleDownload(w http.ResponseWriter, r *http.Request) {
	rawItem := r.PathValue("itemId")
	itemID, ok := normalizeGUID(rawItem)
	if !ok {
		writeInvalidID(w, "itemId", rawItem)
		return
	}
	if _, err := s.store.GetItem(r.Context(), storage.ItemID(itemID)); err != nil {
		writeProblemDoc(w, http.StatusNotFound, problemTypeNotFound, "Not Found", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRemoteSubtitleGet serves GET /Providers/Subtitles/Subtitles/
// {subtitleId}: an id no provider issued is an unhandled error in the
// reference, reproduced.
func (s *Server) handleRemoteSubtitleGet(w http.ResponseWriter, r *http.Request) {
	writeSubtitleError(w, http.StatusInternalServerError)
}

// handleFallbackFontList serves GET /FallbackFont/Fonts: no fallback font
// folder is configured.
func (s *Server) handleFallbackFontList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []any{})
}

// handleFallbackFont serves GET /FallbackFont/Fonts/{name}: no font is
// configured, the answer is an empty 200 like the reference's.
func (s *Server) handleFallbackFont(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(http.StatusOK)
}
