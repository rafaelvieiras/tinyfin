package transcode

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
)

// This file reads the key frame positions of a Matroska/WebM file out of the
// container's own index (Cues) instead of sweeping the packet stream with
// ffprobe: the cues say where the key frames are, each of those clusters is
// opened to read the video block's exact time and key flag, and nothing else of
// the file is touched. The cost follows the number of key frames, not the size
// of the file, which is what a 3 GB copy-path playlist could not afford (the
// sweep read 156 143 packets and took 28 s on a cold array).
//
// Nothing here is a general Matroska parser: it knows the elements needed to
// answer one question, and every failure is returned as an error so the caller
// can fall back to the sweep instead of answering with a guess.

const (
	// clusterWindow is how much of a cluster is read to reach its first video
	// key frame. A cluster starts with its timestamp and its first blocks, so
	// 16 KiB covers a key frame that is a whole sound packet behind the
	// cluster's first block, with room to spare.
	clusterWindow = 16 << 10
	// maxElementBytes bounds what one element (Tracks, Cues) may be read into
	// memory as: the cues of a two hour film are a few kB, and a file claiming
	// megabytes of them is not one worth reading.
	maxElementBytes = 4 << 20
	// maxCuePoints bounds the work a single file may ask for (a two hour film
	// has a few thousand key frames; a file claiming hundreds of thousands is
	// not one this reader should spend time on).
	maxCuePoints = 20000
)

var errNotMatroska = errors.New("transcode: key frames: not a Matroska file")

// ebmlIDs of the elements this reader needs.
const (
	ebmlIDHeader      = 0x1A45DFA3
	ebmlIDSegment     = 0x18538067
	ebmlIDSeekHead    = 0x114D9B74
	ebmlIDSeek        = 0x4DBB
	ebmlIDSeekID      = 0x53AB
	ebmlIDSeekPos     = 0x53AC
	ebmlIDInfo        = 0x1549A966
	ebmlIDTimestampS  = 0x2AD7B1
	ebmlIDTracks      = 0x1654AE6B
	ebmlIDTrackEntry  = 0xAE
	ebmlIDTrackNumber = 0xD7
	ebmlIDTrackType   = 0x83
	ebmlIDCues        = 0x1C53BB6B
	ebmlIDCuePoint    = 0xBB
	ebmlIDCueTime     = 0xB3
	ebmlIDCueTrackPos = 0xB7
	ebmlIDCueTrack    = 0xF7
	ebmlIDCueCluster  = 0xF1
	ebmlIDCluster     = 0x1F43B675
	ebmlIDTimestamp   = 0xE7
	ebmlIDSimpleBlock = 0xA3
	ebmlIDBlockGroup  = 0xA0
	ebmlIDBlock       = 0xA1
	ebmlIDReference   = 0xFB
)

// matroskaKeyFrames returns the presentation times (seconds, ascending) of the
// key frames of the file's first video track, read from its Cues. A file
// without an index, or one whose clusters cannot be read, is an error: the
// caller then sweeps the packets, which is slower but never wrong.
func matroskaKeyFrames(r io.ReaderAt, size int64) ([]float64, error) {
	d := ebmlDoc{r: r, size: size}
	id, payload, length, err := d.element(0)
	if err != nil || id != ebmlIDHeader {
		return nil, errNotMatroska
	}
	// the segment is the header's sibling, at the end of the EBML header
	off := payload + max64(length, 0)
	for off < size {
		id, payload, length, err = d.element(off)
		if err != nil {
			return nil, err
		}
		if id == ebmlIDSegment {
			end := size
			if length >= 0 {
				end = payload + length
			}
			return d.segmentKeyFrames(payload, end)
		}
		if length < 0 {
			break
		}
		off = payload + length
	}
	return nil, errNotMatroska
}

// ebmlDoc is a random-access view of one EBML document.
type ebmlDoc struct {
	r    io.ReaderAt
	size int64
}

// element reads the element header at off and returns its id, the offset of
// its payload and its payload length (-1 for an element of unknown size).
func (d ebmlDoc) element(off int64) (id uint64, payload, length int64, err error) {
	if off < 0 || off >= d.size {
		return 0, 0, 0, io.ErrUnexpectedEOF
	}
	buf, err := d.readAt(off, 12)
	if err != nil {
		return 0, 0, 0, err
	}
	idLen, ok := vintLen(buf[0], 4)
	if !ok || len(buf) < idLen {
		return 0, 0, 0, fmt.Errorf("transcode: key frames: bad element id at %d", off)
	}
	for _, b := range buf[:idLen] {
		id = id<<8 | uint64(b)
	}
	if len(buf) < idLen+1 {
		return 0, 0, 0, io.ErrUnexpectedEOF
	}
	sizeLen, ok := vintLen(buf[idLen], 8)
	if !ok || len(buf) < idLen+sizeLen {
		return 0, 0, 0, fmt.Errorf("transcode: key frames: bad element size at %d", off)
	}
	value := uint64(0)
	for i, b := range buf[idLen : idLen+sizeLen] {
		v := uint64(b)
		if i == 0 { // strip the length marker
			v &^= 1 << (8 - uint(sizeLen))
		}
		value = value<<8 | v
	}
	payload = off + int64(idLen+sizeLen)
	length = int64(value)
	if value == 1<<(7*uint(sizeLen))-1 { // all ones: the size is unknown
		length = -1
	}
	if length >= 0 && payload+length > d.size {
		return 0, 0, 0, fmt.Errorf("transcode: key frames: element at %d runs past the end of the file", off)
	}
	return id, payload, length, nil
}

// readAt reads at most n bytes at off, never past the end of the document.
func (d ebmlDoc) readAt(off, n int64) ([]byte, error) {
	if off < 0 || off >= d.size {
		return nil, io.ErrUnexpectedEOF
	}
	n = min64(n, d.size-off)
	buf := make([]byte, n)
	read, err := d.r.ReadAt(buf, off)
	if read < int(n) {
		buf = buf[:read]
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
	}
	return buf, err
}

// vintLen is the length of a variable-length integer whose first byte is b.
func vintLen(b byte, max int) (int, bool) {
	if b == 0 {
		return 0, false
	}
	n := 1
	for mask := byte(0x80); mask > 0 && b&mask == 0; mask >>= 1 {
		n++
	}
	if n > max {
		return 0, false
	}
	return n, true
}

// cursor walks the children of one element.
type cursor struct {
	d        ebmlDoc
	off, end int64
}

// next returns the child element at the cursor (its offset, id, payload offset
// and payload length) and advances past it. io.EOF means the parent's children
// are over.
func (c *cursor) next() (elemOff int64, id uint64, payload, length int64, err error) {
	if c.off >= c.end {
		return 0, 0, 0, 0, io.EOF
	}
	elemOff = c.off
	id, payload, length, err = c.d.element(c.off)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	if length < 0 {
		return 0, 0, 0, 0, fmt.Errorf("transcode: key frames: element %#x of unknown size at %d", id, c.off)
	}
	c.off = payload + length
	return elemOff, id, payload, length, nil
}

// children is a cursor over the children of the element whose payload starts
// at start and ends at end.
func (d ebmlDoc) children(start, end int64) cursor {
	return cursor{d: d, off: start, end: end}
}

// segmentKeyFrames answers for the segment whose payload starts at start: the
// element positions come from the SeekHead when there is one, and from walking
// the segment's children when there is not.
func (d ebmlDoc) segmentKeyFrames(start, end int64) ([]float64, error) {
	var tracksPos, cuesPos int64 = -1, -1
	timestampScale := uint64(1000000) // Matroska's default: one millisecond

	cur := d.children(start, end)
	for {
		elemOff, id, payload, length, err := cur.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch id {
		case ebmlIDSeekHead:
			t, c, ok := d.readSeekHead(payload, payload+length)
			if ok {
				if t >= 0 {
					tracksPos = start + t
				}
				if c >= 0 {
					cuesPos = start + c
				}
			}
		case ebmlIDInfo:
			if v, ok := d.childUint(payload, payload+length, ebmlIDTimestampS); ok && v > 0 {
				timestampScale = v
			}
		case ebmlIDTracks:
			if tracksPos < 0 {
				tracksPos = elemOff
			}
		case ebmlIDCues:
			if cuesPos < 0 {
				cuesPos = elemOff
			}
		}
		// nothing else of the segment is needed: an index found in front of
		// the media spares walking a thousand clusters
		if tracksPos >= 0 && cuesPos >= 0 {
			break
		}
	}
	if tracksPos < 0 || cuesPos < 0 {
		return nil, fmt.Errorf("transcode: key frames: no cues in the file")
	}

	videoTrack, ok := d.videoTrack(tracksPos)
	if !ok {
		return nil, fmt.Errorf("transcode: key frames: no video track in the file")
	}
	cues, err := d.readCues(cuesPos, videoTrack, start)
	if err != nil {
		return nil, err
	}
	if len(cues) == 0 {
		return nil, fmt.Errorf("transcode: key frames: no cues for the video track")
	}

	out := make([]float64, 0, len(cues))
	for _, c := range cues {
		ticks, err := d.clusterKeyFrame(c, videoTrack)
		if err != nil {
			return nil, err
		}
		out = append(out, float64(ticks)*float64(timestampScale)/1e9)
	}
	sort.Float64s(out)
	deduped := out[:1]
	for _, t := range out[1:] {
		if t != deduped[len(deduped)-1] {
			deduped = append(deduped, t)
		}
	}
	return deduped, nil
}

// readSeekHead returns the positions (relative to the segment's payload) of the
// tracks and cues elements a SeekHead names, -1 when it names neither.
func (d ebmlDoc) readSeekHead(start, end int64) (tracks, cues int64, ok bool) {
	tracks, cues = -1, -1
	cur := d.children(start, end)
	for {
		_, id, payload, length, err := cur.next()
		if err == io.EOF {
			return tracks, cues, tracks >= 0 || cues >= 0
		}
		if err != nil {
			return -1, -1, false
		}
		if id != ebmlIDSeek {
			continue
		}
		var target uint64
		var pos int64 = -1
		inner := d.children(payload, payload+length)
		for {
			_, id, payload, length, err := inner.next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return -1, -1, false
			}
			switch id {
			case ebmlIDSeekID:
				if length > 0 && length <= 4 {
					if buf, _ := d.readAt(payload, length); len(buf) == int(length) {
						for _, b := range buf {
							target = target<<8 | uint64(b)
						}
					}
				}
			case ebmlIDSeekPos:
				if v, err := d.uintAt(payload, length); err == nil {
					pos = int64(v)
				}
			}
		}
		if pos < 0 {
			continue
		}
		switch target {
		case ebmlIDTracks:
			tracks = pos
		case ebmlIDCues:
			cues = pos
		}
	}
}

// videoTrack is the number of the file's first video track (ffprobe's v:0).
func (d ebmlDoc) videoTrack(start int64) (uint64, bool) {
	buf, err := d.elementBytes(start)
	if err != nil {
		return 0, false
	}
	for cur := 0; cur < len(buf); {
		id, data, length, err := bufElement(buf, cur)
		if err != nil || length < 0 {
			return 0, false
		}
		if id == ebmlIDTrackEntry {
			var number, kind uint64
			var hasNumber, hasKind bool
			for inner := data; inner < data+length && inner < len(buf); {
				id, data2, length2, err := bufElement(buf, inner)
				if err != nil || length2 < 0 {
					return 0, false
				}
				end := minInt(data2+length2, len(buf))
				switch id {
				case ebmlIDTrackNumber:
					if v, err := bufUint(buf[data2:end], length2); err == nil {
						number, hasNumber = v, true
					}
				case ebmlIDTrackType:
					if v, err := bufUint(buf[data2:end], length2); err == nil {
						kind, hasKind = v, true
					}
				}
				inner = data2 + length2
			}
			if hasNumber && hasKind && kind == 1 {
				return number, true
			}
		}
		cur = data + length
	}
	return 0, false
}

// elementBytes reads one element's payload into memory. The Tracks and Cues
// elements are small, and parsing them in memory turns a read per child into a
// read per element (which is what makes the difference on a real film: a
// thousand cues parsed one 12-byte read at a time cost more than the clusters).
func (d ebmlDoc) elementBytes(start int64) ([]byte, error) {
	_, payload, length, err := d.element(start)
	if err != nil {
		return nil, err
	}
	if length < 0 {
		return nil, fmt.Errorf("transcode: key frames: element at %d of unknown size", start)
	}
	if length > maxElementBytes {
		return nil, fmt.Errorf("transcode: key frames: element at %d is %d bytes", start, length)
	}
	return d.readAt(payload, length)
}

// readCues returns the cluster positions (absolute offsets) that carry a key
// frame of the given track, in the order the cues list them.
func (d ebmlDoc) readCues(start int64, videoTrack uint64, segmentStart int64) ([]int64, error) {
	buf, err := d.elementBytes(start)
	if err != nil {
		return nil, err
	}
	var out []int64
	for cur := 0; cur < len(buf); {
		id, data, length, err := bufElement(buf, cur)
		if err != nil || length < 0 {
			return nil, fmt.Errorf("transcode: key frames: broken cues")
		}
		if id != ebmlIDCuePoint {
			cur = data + length
			continue
		}
		if len(out) >= maxCuePoints {
			return nil, fmt.Errorf("transcode: key frames: more than %d cues, not reading them", maxCuePoints)
		}
		var cluster int64 = -1
		for inner := data; inner < data+length && inner < len(buf); {
			id, data2, length2, err := bufElement(buf, inner)
			if err != nil || length2 < 0 {
				return nil, fmt.Errorf("transcode: key frames: broken cue point")
			}
			if id == ebmlIDCueTrackPos {
				var track, pos uint64
				var hasTrack, hasPos bool
				for k := data2; k < data2+length2 && k < len(buf); {
					id3, data3, length3, err := bufElement(buf, k)
					if err != nil || length3 < 0 {
						return nil, fmt.Errorf("transcode: key frames: broken cue position")
					}
					stop := minInt(data3+length3, len(buf))
					switch id3 {
					case ebmlIDCueTrack:
						if v, err := bufUint(buf[data3:stop], length3); err == nil {
							track, hasTrack = v, true
						}
					case ebmlIDCueCluster:
						if v, err := bufUint(buf[data3:stop], length3); err == nil {
							pos, hasPos = v, true
						}
					}
					k = data3 + length3
				}
				if hasTrack && hasPos && track == videoTrack {
					cluster = segmentStart + int64(pos)
				}
			}
			inner = data2 + length2
		}
		if cluster >= 0 {
			out = append(out, cluster)
		}
		cur = data + length
	}
	return out, nil
}

// clusterKeyFrame reads the cluster at off and returns the time (in the file's
// timestamp scale) of its first video key frame block. The block is needed and
// not only the cluster's timestamp: a cluster's time is the time of the first
// block written into it, which for a file whose sound leads the picture is the
// sound's, a few milliseconds before the key frame.
func (d ebmlDoc) clusterKeyFrame(off int64, videoTrack uint64) (int64, error) {
	buf, err := d.readAt(off, clusterWindow)
	if len(buf) == 0 {
		return 0, fmt.Errorf("transcode: key frames: cluster at %d: %w", off, err)
	}
	id, data, length, err := bufElement(buf, 0)
	if err != nil || id != ebmlIDCluster {
		return 0, fmt.Errorf("transcode: key frames: no cluster at %d", off)
	}
	end := data + length
	if end > len(buf) || length < 0 {
		end = len(buf)
	}
	var clusterTs int64
	var haveTs bool
	for cur := data; cur < end; {
		id, data, length, err := bufElement(buf, cur)
		if err != nil {
			return 0, err
		}
		if id == ebmlIDTimestamp && length >= 0 {
			v, err := bufUint(buf[data:], length)
			if err != nil {
				return 0, err
			}
			clusterTs, haveTs = int64(v), true
			break
		}
		if length < 0 || data+length > end {
			return 0, fmt.Errorf("transcode: key frames: cluster at %d runs past the window", off)
		}
		cur = data + length
	}
	if !haveTs {
		return 0, fmt.Errorf("transcode: key frames: cluster at %d has no timestamp", off)
	}
	for cur := data; cur < end; {
		id, data, length, err := bufElement(buf, cur)
		if err != nil {
			return 0, err
		}
		if length < 0 {
			return 0, fmt.Errorf("transcode: key frames: element of unknown size in the cluster at %d", off)
		}
		switch id {
		case ebmlIDSimpleBlock:
			// only the header is needed, so a frame larger than the window is
			// fine: the block is read as far as it was read at all
			track, rel, key, err := bufBlock(buf[data:minInt(data+length, len(buf))], true)
			if err != nil {
				return 0, err
			}
			if track == videoTrack && key {
				return clusterTs + int64(rel), nil
			}
		case ebmlIDBlockGroup:
			track, rel, key, err := bufGroup(buf[data:minInt(data+length, len(buf))])
			if err != nil {
				return 0, err
			}
			if track == videoTrack && key {
				return clusterTs + int64(rel), nil
			}
		}
		cur = data + length
		if cur > len(buf) {
			break
		}
	}
	return 0, fmt.Errorf("transcode: key frames: no video key frame block in the cluster at %d", off)
}

// bufElement reads an element out of a window already in memory.
func bufElement(buf []byte, off int) (id uint64, data, length int, err error) {
	if off < 0 || off >= len(buf) {
		return 0, 0, 0, io.ErrUnexpectedEOF
	}
	idLen, ok := vintLen(buf[off], 4)
	if !ok || off+idLen > len(buf) {
		return 0, 0, 0, fmt.Errorf("transcode: key frames: bad element id")
	}
	for _, b := range buf[off : off+idLen] {
		id = id<<8 | uint64(b)
	}
	pos := off + idLen
	if pos >= len(buf) {
		return 0, 0, 0, io.ErrUnexpectedEOF
	}
	sizeLen, ok := vintLen(buf[pos], 8)
	if !ok || pos+sizeLen > len(buf) {
		return 0, 0, 0, fmt.Errorf("transcode: key frames: bad element size")
	}
	value := uint64(0)
	for i, b := range buf[pos : pos+sizeLen] {
		v := uint64(b)
		if i == 0 {
			v &^= 1 << (8 - uint(sizeLen))
		}
		value = value<<8 | v
	}
	length = int(value)
	if value == 1<<(7*uint(sizeLen))-1 {
		length = -1
	}
	return id, pos + sizeLen, length, nil
}

func bufUint(buf []byte, length int) (uint64, error) {
	if length <= 0 || length > 8 || len(buf) < length {
		return 0, fmt.Errorf("transcode: key frames: integer of %d bytes", length)
	}
	var v uint64
	for _, b := range buf[:length] {
		v = v<<8 | uint64(b)
	}
	return v, nil
}

// bufBlock reads the track, the time relative to the cluster and the key flag
// of a SimpleBlock (withFlags) or of the Block inside a BlockGroup. buf is as
// much of the block as was read: the frame itself is never needed.
func bufBlock(buf []byte, withFlags bool) (track uint64, rel int16, key bool, err error) {
	if len(buf) < 3 {
		return 0, 0, false, fmt.Errorf("transcode: key frames: block header past the window")
	}
	n, ok := vintLen(buf[0], 8)
	if !ok || len(buf) <= n+2 {
		return 0, 0, false, fmt.Errorf("transcode: key frames: bad block")
	}
	v := uint64(0)
	for i, b := range buf[:n] {
		u := uint64(b)
		if i == 0 {
			u &^= 1 << (8 - uint(n))
		}
		v = v<<8 | u
	}
	track = v
	rel = int16(binary.BigEndian.Uint16(buf[n : n+2]))
	if withFlags {
		key = buf[n+2]&0x80 != 0
	}
	return track, rel, key, nil
}

// bufGroup reads a BlockGroup: its block, and whether it carries no
// ReferenceBlock (a key frame).
func bufGroup(buf []byte) (track uint64, rel int16, key bool, err error) {
	key = true
	found := false
	for cur := 0; cur < len(buf); {
		id, data, size, err := bufElement(buf, cur)
		if err != nil {
			return 0, 0, false, err
		}
		switch id {
		case ebmlIDBlock:
			t, r, _, err := bufBlock(buf[data:minInt(data+maxInt(size, 0), len(buf))], false)
			if err != nil {
				return 0, 0, false, err
			}
			track, rel, found = t, r, true
		case ebmlIDReference:
			key = false
		}
		if size < 0 {
			break
		}
		cur = data + size
	}
	if !found {
		return 0, 0, false, fmt.Errorf("transcode: key frames: block group without a block")
	}
	return track, rel, key, nil
}

// uintAt reads an unsigned integer element payload.
func (d ebmlDoc) uintAt(off, length int64) (uint64, error) {
	if length <= 0 || length > 8 {
		return 0, fmt.Errorf("transcode: key frames: integer of %d bytes", length)
	}
	buf, err := d.readAt(off, length)
	if err != nil {
		return 0, err
	}
	var v uint64
	for _, b := range buf {
		v = v<<8 | uint64(b)
	}
	return v, nil
}

// childUint returns the first child of the given id as an unsigned integer.
func (d ebmlDoc) childUint(start, end int64, want uint64) (uint64, bool) {
	cur := d.children(start, end)
	for {
		_, id, payload, length, err := cur.next()
		if err != nil {
			return 0, false
		}
		if id == want {
			v, err := d.uintAt(payload, length)
			return v, err == nil
		}
	}
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
