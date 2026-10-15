package scan

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// disc.go: DVD and Blu-ray folders and disc images (spec: scan.disc.*).
//
//   - a folder holding a VIDEO_TS (or BDMV) folder, or VIDEO_TS.IFO itself, is
//     one movie of VideoType Dvd (BluRay) whose Path is the disc's folder;
//   - .iso and .img files are movies of VideoType Iso, IsoType the kind of
//     disc inside when it can be told.

// discRef describes the disc a movie group stands for.
type discRef struct {
	kind string // storage.VideoTypeDvd or storage.VideoTypeBluRay
	// path is the folder that holds the disc's structure: the VIDEO_TS /
	// BDMV folder itself, or the movie folder when VIDEO_TS.IFO sits in it.
	path string
	// self is true when the movie folder is the disc (VIDEO_TS.IFO in it).
	self bool
}

// exclusive reports whether the disc takes its whole folder: a Blu-ray folder,
// or a DVD structure that holds VOB files. A DVD structure with nothing but
// IFO files is just a VIDEO_TS folder beside whatever else the folder holds.
func (d *discRef) exclusive() bool {
	if d.kind == storage.VideoTypeBluRay {
		return true
	}
	entries, err := os.ReadDir(d.path)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".vob") {
			return true
		}
	}
	return false
}

// discDirKind returns the disc kind a folder name stands for ("" when none).
func discDirKind(name string) string {
	switch strings.ToLower(name) {
	case "video_ts":
		return storage.VideoTypeDvd
	case "bdmv":
		return storage.VideoTypeBluRay
	}
	return ""
}

// IsDiscImage reports whether name is a disc image file.
func IsDiscImage(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".iso", ".img":
		return true
	}
	return false
}

var vobName = regexp.MustCompile(`(?i)^VTS_(\d+)_(\d+)\.VOB$`)

// dvdMainVobs returns the VOB files of the main title set of a DVD structure
// in play order: the title set (VTS_nn) holding the most data, without its
// menu part (VTS_nn_0). total is their combined size, largest the size of the
// biggest one, mtime the newest modification time (unix nanoseconds).
func dvdMainVobs(dir string) (files []string, total, largest, mtime int64) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, 0, 0
	}
	type vob struct {
		path string
		part int
		size int64
		mt   int64
	}
	sets := map[int][]vob{}
	sizes := map[int]int64{}
	for _, e := range entries {
		m := vobName.FindStringSubmatch(e.Name())
		if m == nil || e.IsDir() {
			continue
		}
		set, _ := strconv.Atoi(m[1])
		part, _ := strconv.Atoi(m[2])
		if part == 0 {
			continue // the title set's menu
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		sets[set] = append(sets[set], vob{filepath.Join(dir, e.Name()), part, fi.Size(), fi.ModTime().UnixNano()})
		sizes[set] += fi.Size()
	}
	best := -1
	for set := range sets {
		if best < 0 || sizes[set] > sizes[best] || (sizes[set] == sizes[best] && set < best) {
			best = set
		}
	}
	if best < 0 {
		return nil, 0, 0, 0
	}
	vobs := sets[best]
	sort.Slice(vobs, func(i, j int) bool { return vobs[i].part < vobs[j].part })
	for _, v := range vobs {
		files = append(files, v.path)
		total += v.size
		if v.size > largest {
			largest = v.size
		}
		if v.mt > mtime {
			mtime = v.mt
		}
	}
	return files, total, largest, mtime
}

// bdLargestStream returns the biggest .m2ts file of a BDMV/STREAM folder.
func bdLargestStream(bdmv string) (path string, size, mtime int64) {
	var streamDir string
	if entries, err := os.ReadDir(bdmv); err == nil {
		for _, e := range entries {
			if e.IsDir() && strings.EqualFold(e.Name(), "STREAM") {
				streamDir = filepath.Join(bdmv, e.Name())
			}
		}
	}
	if streamDir == "" {
		return "", 0, 0
	}
	entries, err := os.ReadDir(streamDir)
	if err != nil {
		return "", 0, 0
	}
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".m2ts") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		if fi.Size() > size {
			path, size, mtime = filepath.Join(streamDir, e.Name()), fi.Size(), fi.ModTime().UnixNano()
		}
	}
	return path, size, mtime
}

// iso9660 names of the folders that tell the kind of disc.
const sectorSize = 2048

// detectIsoType tells the kind of disc inside an image: it reads the root
// directory of the ISO 9660 volume and, failing that (a UDF-only image), looks
// for the folder names near the start of the file. "" when it cannot tell.
func detectIsoType(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	pvd := make([]byte, sectorSize)
	if _, err := f.ReadAt(pvd, 16*sectorSize); err == nil && pvd[0] == 1 && string(pvd[1:6]) == "CD001" {
		lba := int64(binary.LittleEndian.Uint32(pvd[156+2 : 156+6]))
		length := int64(binary.LittleEndian.Uint32(pvd[156+10 : 156+14]))
		if length > 0 && length <= 1<<20 {
			dir := make([]byte, length)
			if _, err := f.ReadAt(dir, lba*sectorSize); err == nil || err == io.EOF {
				for off := 0; off < len(dir); {
					n := int(dir[off])
					if n == 0 {
						// records do not span sectors: skip to the next one
						off = (off/sectorSize + 1) * sectorSize
						continue
					}
					if off+33 > len(dir) || off+n > len(dir) {
						break
					}
					idLen := int(dir[off+32])
					if off+33+idLen <= len(dir) {
						id := strings.ToUpper(string(dir[off+33 : off+33+idLen]))
						if k := discDirKind(strings.TrimSuffix(id, ";1")); k != "" {
							return k
						}
					}
					off += n
				}
			}
		}
	}
	// UDF (Blu-ray images): names are stored as 8-bit or UTF-16 characters
	// in the file identifiers near the start of the image.
	head := make([]byte, 4<<20)
	n, _ := f.ReadAt(head, 0)
	head = head[:n]
	for _, c := range []struct{ kind, name string }{
		{storage.VideoTypeBluRay, "BDMV"}, {storage.VideoTypeDvd, "VIDEO_TS"},
	} {
		u16 := make([]byte, 0, 2*len(c.name))
		for _, r := range []byte(c.name) {
			u16 = append(u16, 0, r)
		}
		if bytes.Contains(head, append([]byte{0x08}, c.name...)) || bytes.Contains(head, append([]byte{0x10}, u16...)) {
			return c.kind
		}
	}
	return ""
}

// discProbe is the outcome of probing a disc item.
type discProbe struct {
	probeResult
	container string
	isoType   string
	// size is the size reported for the item.
	size int64
}

// probeDisc probes a disc structure or image, reusing the stored media info
// when what was probed last time is unchanged.
func (s *Scanner) probeDisc(ctx context.Context, pool *jobPool, itemID storage.ItemID, videoType, path string, self bool) discProbe {
	var (
		target   string
		size     int64
		sigSize  int64
		mtime    int64
		added    time.Time
		fallbk   string
		iso      string
		firstVob string
	)
	switch videoType {
	case storage.VideoTypeDvd:
		dir := path
		files, total, largest, mt := dvdMainVobs(dir)
		if len(files) == 0 {
			// the VOBs may be in a VIDEO_TS folder below a movie folder
			sub := filepath.Join(dir, "VIDEO_TS")
			if entries, err := os.ReadDir(dir); err == nil {
				for _, e := range entries {
					if e.IsDir() && discDirKind(e.Name()) == storage.VideoTypeDvd {
						sub = filepath.Join(dir, e.Name())
					}
				}
			}
			files, total, largest, mt = dvdMainVobs(sub)
		}
		if len(files) > 0 {
			firstVob = files[0]
		}
		switch len(files) {
		case 0:
		case 1:
			target = files[0]
		default:
			target = "concat:" + strings.Join(files, "|")
		}
		size, sigSize, mtime = largest, total, mt
	case storage.VideoTypeBluRay:
		// path is the disc's root (it holds BDMV) or the BDMV folder itself.
		root, bd := path, path
		if discDirKind(filepath.Base(path)) == storage.VideoTypeBluRay {
			root = filepath.Dir(path)
		} else if entries, err := os.ReadDir(path); err == nil {
			for _, e := range entries {
				if e.IsDir() && discDirKind(e.Name()) == storage.VideoTypeBluRay {
					bd = filepath.Join(path, e.Name())
				}
			}
		}
		target = "bluray:" + root
		fallbk, sigSize, mtime = bdLargestStream(bd)
	case storage.VideoTypeIso:
		fi, err := os.Stat(path)
		if err == nil {
			size, sigSize, mtime = fi.Size(), fi.Size(), fi.ModTime().UnixNano()
		}
		iso = detectIsoType(path)
		target = path
		if iso == storage.VideoTypeBluRay {
			target = "bluray:" + path
			fallbk = path
		}
	}
	added = time.Unix(0, mtime).UTC()
	if mtime == 0 {
		added = fileDateAdded(path)
	}
	out := discProbe{isoType: iso, size: size}
	out.size = size
	out.added = added
	out.mtimeNS = mtime
	if target == "" {
		return out
	}
	// Reuse the stored media info of an unchanged disc.
	sigs, hasSigs := s.store.(signatureStore)
	if hasSigs && !s.forceProbe && mtime != 0 {
		if sSize, sMtime, ok, err := sigs.GetScanSignature(ctx, itemID); err == nil && ok && sSize == sigSize && sMtime == mtime {
			if old, err := s.store.GetItem(ctx, itemID); err == nil && len(old.MediaStreams) > 0 && old.RunTimeTicks != nil {
				out.streams, out.ticks, out.container = old.MediaStreams, old.RunTimeTicks, old.Container
				if videoType != storage.VideoTypeBluRay {
					out.size = old.Size
				}
				pool.countSkipped()
				return out
			}
		}
	}
	var info ffprobeOutput
	var err error
	var sumTicks *int64
	if strings.HasPrefix(target, "concat:") {
		// The title set is several files: the streams are the first one's and
		// the duration is the sum of them all.
		var total int64
		for i, f := range strings.Split(strings.TrimPrefix(target, "concat:"), "|") {
			part, perr := s.probe(ctx, f)
			if perr != nil {
				err = perr
				break
			}
			if i == 0 {
				info = part
			}
			if t := runtimeTicksFromProbe(part); t != nil {
				total += *t
			}
		}
		if err == nil && total > 0 {
			sumTicks = &total
		}
	} else {
		info, err = s.probe(ctx, target)
	}
	if err != nil && fallbk != "" && target != fallbk {
		// A build of ffprobe without libbluray, or a folder without the
		// index files: the main feature's stream still describes the disc.
		info, err = s.probe(ctx, fallbk)
	}
	pool.countProbed()
	out.err = err
	out.streams = mediaStreamsFromProbe(info)
	out.ticks = runtimeTicksFromProbe(info)
	if sumTicks != nil {
		out.ticks = sumTicks
	}
	if err == nil && videoType != storage.VideoTypeBluRay {
		out.container = info.Format.FormatName
	}
	if out.container == "" && videoType == storage.VideoTypeIso {
		out.container = strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	}
	out.record = hasSigs && err == nil && mtime != 0
	out.mtimeNS = mtime
	if videoType == storage.VideoTypeDvd {
		// reported only when the probe could read the VOBs: the size of the
		// first one of the title set
		out.size = 0
		if err == nil && firstVob != "" {
			if fi, serr := os.Stat(firstVob); serr == nil {
				out.size = fi.Size()
			}
		}
	}
	// the signature size is the combined size of what was probed
	if out.record {
		out.sigSize = sigSize
	}
	return out
}
