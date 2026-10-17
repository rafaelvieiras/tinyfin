package scan

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// strm.go: .strm files are text files whose content is the address (or path)
// of the media. They are video files of a library; the item's Path stays the
// .strm file, an address is kept as the item's RemoteURL, and nothing is
// probed during a scan (spec: scan.strm.*).

// IsStrm reports whether name is a .strm file.
func IsStrm(name string) bool {
	return strings.EqualFold(filepath.Ext(name), ".strm")
}

var urlScheme = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9+.-]*)://`)

// readStrmURL returns the first line of a .strm file when it is an address
// with a scheme (http://, https://, rtmp://, ...); "" for an empty file or a
// local path, which stays the file itself.
func readStrmURL(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 4096), 64*1024)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\xef\xbb\xbf"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if urlScheme.MatchString(line) {
			return line
		}
		return ""
	}
	return ""
}

// ProbeMedia runs ffprobe against an address or path, with a bound on the
// time it may take, and returns what the playback layer needs from it. It is
// what a remote .strm item is described with when it is played.
func (s *Scanner) ProbeMedia(ctx context.Context, target string) (streams []storage.MediaStreamRef, ticks *int64, container string, bitRate *int64, err error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	info, err := s.probe(ctx, target)
	if err != nil {
		return nil, nil, "", nil, err
	}
	var br *int64
	if v, perr := strconv.ParseInt(info.Format.BitRate, 10, 64); perr == nil && v > 0 {
		br = &v
	}
	streams = mediaStreamsFromProbe(info)
	for i := range streams {
		// the reference lists no reference frame count for an address
		streams[i].RefFrames = nil
	}
	return streams, runtimeTicksFromProbe(info), containerFromFormat(info.Format.FormatName), br, nil
}

// containerFromFormat turns ffprobe's format_name list ("mov,mp4,m4a,3gp,3g2,mj2",
// "matroska,webm") into the container name a file with that extension would
// carry.
func containerFromFormat(name string) string {
	parts := strings.Split(name, ",")
	has := func(v string) bool {
		for _, p := range parts {
			if p == v {
				return true
			}
		}
		return false
	}
	switch {
	case has("matroska"):
		return "mkv"
	case has("mp4"):
		return "mp4"
	case has("mpegts"):
		return "ts"
	case has("mpeg"):
		return "mpg"
	}
	return parts[0]
}
