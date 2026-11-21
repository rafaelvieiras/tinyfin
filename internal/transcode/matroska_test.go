package transcode

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The fixtures of this file are written by hand, as an EBML document, so the
// reader is tested on the shapes a muxer can produce and not only on what the
// local ffmpeg happens to write: key frames on the video track, a cluster
// whose first block is the sound, a key frame carried by a BlockGroup, a file
// without cues, and a file whose clusters are far apart.

const (
	idEBML      = 0x1A45DFA3
	idSegment   = 0x18538067
	idSeekHead  = 0x114D9B74
	idSeek      = 0x4DBB
	idSeekID    = 0x53AB
	idSeekPos   = 0x53AC
	idInfo      = 0x1549A966
	idTimeScale = 0x2AD7B1
	idTracks    = 0x1654AE6B
	idTrackEnt  = 0xAE
	idTrackNum  = 0xD7
	idTrackType = 0x83
	idCodecID   = 0x86
	idCues      = 0x1C53BB6B
	idCuePoint  = 0xBB
	idCueTime   = 0xB3
	idCuePos    = 0xB7
	idCueTrack  = 0xF7
	idCueClus   = 0xF1
	idCluster   = 0x1F43B675
	idTimestamp = 0xE7
	idSimple    = 0xA3
	idBlockGrp  = 0xA0
	idBlock     = 0xA1
	idReference = 0xFB
	idVoid      = 0xEC
)

// el is one EBML element of a handwritten fixture: either a leaf (data) or a
// container (kids), never both.
type el struct {
	id   uint64
	data []byte
	kids []el
}

func ebmlID(id uint64) []byte {
	switch {
	case id > 0xFFFFFF:
		return []byte{byte(id >> 24), byte(id >> 16), byte(id >> 8), byte(id)}
	case id > 0xFFFF:
		return []byte{byte(id >> 16), byte(id >> 8), byte(id)}
	case id > 0xFF:
		return []byte{byte(id >> 8), byte(id)}
	default:
		return []byte{byte(id)}
	}
}

// ebmlSize encodes a size (or a block's track number) as a VINT in the
// shortest length that holds it; -1 is the unknown-size marker.
func ebmlSize(n int64) []byte {
	if n < 0 {
		return []byte{0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
	}
	for length := 1; length <= 8; length++ {
		if n < int64(1)<<(7*uint(length))-1 {
			out := make([]byte, length)
			v := n | int64(1)<<(7*uint(length))
			for i := length - 1; i >= 0; i-- {
				out[i] = byte(v)
				v >>= 8
			}
			return out
		}
	}
	out := make([]byte, 9)
	binary.BigEndian.PutUint64(out[1:], uint64(n))
	out[0] = 0x01
	return out
}

func (e el) marshal() []byte {
	body := e.data
	if len(e.kids) > 0 {
		body = nil
		for _, k := range e.kids {
			body = append(body, k.marshal()...)
		}
	}
	out := append(ebmlID(e.id), ebmlSize(int64(len(body)))...)
	return append(out, body...)
}

// uint32El is always four bytes, so an element that has to be patched later
// keeps its length (a cue's cluster position, a SeekHead's target).
func uint32El(id uint64, v uint32) el {
	return el{id: id, data: binary.BigEndian.AppendUint32(nil, v)}
}

// uintMinEl writes an unsigned integer element in its shortest form, which is
// how a muxer stores TrackNumber and CueTrack (a plain integer, not a VINT).
func uintMinEl(id uint64, v uint64) el {
	var b []byte
	switch {
	case v <= 0xFF:
		b = []byte{byte(v)}
	case v <= 0xFFFF:
		b = binary.BigEndian.AppendUint16(nil, uint16(v))
	default:
		b = binary.BigEndian.AppendUint32(nil, uint32(v))
	}
	return el{id: id, data: b}
}

func uint64El(id uint64, v uint64) el {
	return el{id: id, data: binary.BigEndian.AppendUint64(nil, v)}
}

func strEl(id uint64, s string) el { return el{id: id, data: []byte(s)} }

// block is one block of one track.
type block struct {
	track uint64
	relMs int16
	key   bool
}

func blockPayload(b block, withFlags bool) []byte {
	out := ebmlSize(int64(b.track))
	out = append(out, byte(b.relMs>>8), byte(b.relMs))
	if withFlags {
		var flags byte
		if b.key {
			flags = 0x80
		}
		out = append(out, flags)
	}
	return append(out, 0x00, 0x00, 0x00)
}

type clusterSpec struct {
	ms          int64 // cluster timestamp, ms
	blocks      []block
	blockGroups bool // carry the blocks in BlockGroups instead of SimpleBlocks
}

// mkvSpec is a Matroska document to write.
type mkvSpec struct {
	timeScaleNs uint64 // 0 means 1000000 (a millisecond per tick)
	clusters    []clusterSpec
	cuesFor     []int // indexes into clusters that get a cue
	withoutCues bool  // write no Cues element at all
	cueAtFront  bool  // cues before the clusters instead of after them
	padAfter    int64 // a Void element of this size after the cues
	seekHead    bool  // a SeekHead pointing at Tracks and Cues
}

// build assembles the document. The Segment has an unknown size (the fixtures
// are written to EOF); the elements that point forward are written with
// fixed-width fields and patched once the offsets are known.
func (s mkvSpec) build() []byte {
	const videoTrack = 1

	scale := s.timeScaleNs
	if scale == 0 {
		scale = 1000000
	}
	info := el{id: idInfo, kids: []el{uint64El(idTimeScale, scale)}}.marshal()
	tracks := el{id: idTracks, kids: []el{
		{id: idTrackEnt, kids: []el{
			uintMinEl(idTrackNum, videoTrack),
			{id: idTrackType, data: []byte{0x01}},
			strEl(idCodecID, "V_MPEG4/ISO/AVC"),
		}},
		{id: idTrackEnt, kids: []el{
			uintMinEl(idTrackNum, 2),
			{id: idTrackType, data: []byte{0x02}},
			strEl(idCodecID, "A_AC3"),
		}},
	}}.marshal()

	var clusters [][]byte
	for _, c := range s.clusters {
		kids := []el{{id: idTimestamp, data: binary.BigEndian.AppendUint64(nil, uint64(c.ms))}}
		for _, b := range c.blocks {
			if c.blockGroups {
				group := []el{{id: idBlock, data: blockPayload(b, false)}}
				if !b.key {
					group = append(group, el{id: idReference, data: []byte{0x00}})
				}
				kids = append(kids, el{id: idBlockGrp, kids: group})
				continue
			}
			kids = append(kids, el{id: idSimple, data: blockPayload(b, true)})
		}
		clusters = append(clusters, el{id: idCluster, kids: kids}.marshal())
	}

	// cues builds the element with the given cluster positions, which are
	// four bytes each so a patched element has the same length as a planned one.
	cues := func(posOf func(i int) uint32) []byte {
		var kids []el
		for _, idx := range s.cuesFor {
			kids = append(kids, el{id: idCuePoint, kids: []el{
				{id: idCueTime, data: binary.BigEndian.AppendUint64(nil, uint64(s.clusters[idx].ms))},
				{id: idCuePos, kids: []el{
					uintMinEl(idCueTrack, videoTrack),
					uint32El(idCueClus, posOf(idx)),
				}},
			}})
		}
		if len(kids) == 0 {
			return nil
		}
		return el{id: idCues, kids: kids}.marshal()
	}
	// the SeekHead carries two fixed four-byte positions
	seekHead := func(tracksPos, cuesPos uint32) []byte {
		return el{id: idSeekHead, kids: []el{
			{id: idSeek, kids: []el{el{id: idSeekID, data: ebmlID(idTracks)}, uint32El(idSeekPos, tracksPos)}},
			{id: idSeek, kids: []el{el{id: idSeekID, data: ebmlID(idCues)}, uint32El(idSeekPos, cuesPos)}},
		}}.marshal()
	}

	header := el{id: idEBML, kids: []el{
		{id: 0x4286, data: []byte{0x01}}, // EBMLVersion
		{id: 0x42F7, data: []byte{0x01}}, // EBMLReadVersion
		{id: 0x42F2, data: []byte{0x04}}, // EBMLMaxIDLength
		{id: 0x42F3, data: []byte{0x08}}, // EBMLMaxSizeLength
		strEl(0x4282, "matroska"),
		{id: 0x4287, data: []byte{0x04}}, // DocTypeVersion
		{id: 0x4285, data: []byte{0x02}}, // DocTypeReadVersion
	}}.marshal()

	buf := append([]byte{}, header...)
	buf = append(buf, ebmlID(idSegment)...)
	buf = append(buf, ebmlSize(-1)...)
	segStart := len(buf)

	var seekHeadIndex, tracksIndex, cuesIndex = -1, -1, -1
	if s.seekHead {
		seekHeadIndex = len(buf)
		buf = append(buf, seekHead(0, 0)...)
	}
	buf = append(buf, info...)
	tracksIndex = len(buf) - segStart
	buf = append(buf, tracks...)
	if !s.cueAtFront && !s.withoutCues {
		// written before the positions are known: same length as the final one
		cuesIndex = len(buf)
		buf = append(buf, cues(func(int) uint32 { return 0 })...)
	}
	var offsets []int64
	for _, c := range clusters {
		offsets = append(offsets, int64(len(buf)-segStart))
		buf = append(buf, c...)
	}
	if s.cueAtFront && !s.withoutCues {
		cuesIndex = len(buf)
		buf = append(buf, cues(func(i int) uint32 { return uint32(offsets[i]) })...)
	}
	if cuesIndex >= 0 {
		body := cues(func(i int) uint32 { return uint32(offsets[i]) })
		copy(buf[cuesIndex:], body)
	}
	if s.seekHead {
		copy(buf[seekHeadIndex:], seekHead(uint32(tracksIndex), uint32(cuesIndex-segStart)))
	}
	if s.padAfter > 0 {
		buf = append(buf, ebmlID(idVoid)...)
		buf = append(buf, ebmlSize(s.padAfter)...)
	}
	return buf
}

// countingReaderAt records what a reader asked for, which is the only way to
// state "this does not read the file" as a test.
type countingReaderAt struct {
	r     io.ReaderAt
	bytes int64
	reads int
}

func (c *countingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	n, err := c.r.ReadAt(p, off)
	c.bytes += int64(n)
	c.reads++
	return n, err
}

// writeFixture writes the document to disk; a padAfter hole is written as a
// real sparse Void body, so a 2 GB file costs no space and no time.
func writeFixture(t *testing.T, s mkvSpec) (path string, size int64) {
	t.Helper()
	doc := s.build()
	path = filepath.Join(t.TempDir(), "fixture.mkv")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(doc); err != nil {
		t.Fatal(err)
	}
	if s.padAfter > 0 {
		if _, err := f.Seek(s.padAfter-1, io.SeekCurrent); err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte{0}); err != nil {
			t.Fatal(err)
		}
	}
	st, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path, st.Size()
}

// videoOnly is the usual fixture: one video key frame per cluster at the given
// seconds, each cluster starting with the video block.
func videoOnly(seconds ...int64) mkvSpec {
	spec := mkvSpec{}
	for i, s := range seconds {
		spec.clusters = append(spec.clusters, clusterSpec{ms: s * 1000, blocks: []block{
			{track: 1, relMs: 0, key: true},
			{track: 1, relMs: 1000, key: false},
			{track: 2, relMs: 0, key: true},
		}})
		spec.cuesFor = append(spec.cuesFor, i)
	}
	return spec
}

func readFixture(t *testing.T, s mkvSpec) ([]float64, *countingReaderAt) {
	t.Helper()
	path, size := writeFixture(t, s)
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	counting := &countingReaderAt{r: f}
	got, err := matroskaKeyFrames(counting, size)
	if err != nil {
		t.Fatal(err)
	}
	return got, counting
}

func assertFrames(t *testing.T, got []float64, want ...float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("key frames: got %v, want %v", got, want)
	}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-9 {
			t.Fatalf("key frames: got %v, want %v", got, want)
		}
	}
}

func TestMatroskaKeyFramesReadsTheCues(t *testing.T) {
	assertFrames(t, mustRead(t, videoOnly(0, 4, 10, 20)), 0, 4, 10, 20)
}

func mustRead(t *testing.T, s mkvSpec) []float64 {
	t.Helper()
	got, _ := readFixture(t, s)
	return got
}

// The cues of a file whose index sits in front of the media (what mkvmerge
// writes) are found through the SeekHead, and without one by walking the
// segment's elements.
func TestMatroskaKeyFramesFindTheCuesAtTheFront(t *testing.T) {
	spec := videoOnly(0, 7, 15)
	spec.cueAtFront = true
	assertFrames(t, mustRead(t, spec), 0, 7, 15)

	spec.seekHead = true
	assertFrames(t, mustRead(t, spec), 0, 7, 15)
}

// A cluster whose first block is the sound: the cue time is the cluster's, and
// the key frame is the video block that follows, a few milliseconds later.
// Reading the cue time alone would report a position the picture is not at.
func TestMatroskaKeyFramesUseTheVideoBlockNotTheClusterTime(t *testing.T) {
	spec := mkvSpec{
		clusters: []clusterSpec{
			{ms: 0, blocks: []block{{track: 2, relMs: 0, key: true}, {track: 1, relMs: 24, key: true}}},
			{ms: 8000, blocks: []block{{track: 2, relMs: 0, key: true}, {track: 1, relMs: 24, key: true}}},
		},
		cuesFor: []int{0, 1},
	}
	assertFrames(t, mustRead(t, spec), 0.024, 8.024)
}

// A key frame carried by a BlockGroup (the older muxing shape) is read the
// same way as one in a SimpleBlock.
func TestMatroskaKeyFramesReadBlockGroups(t *testing.T) {
	spec := videoOnly(0, 6, 12)
	for i := range spec.clusters {
		spec.clusters[i].blockGroups = true
	}
	assertFrames(t, mustRead(t, spec), 0, 6, 12)
}

// A block that is not a key frame is not one, whatever the cue says.
func TestMatroskaKeyFramesSkipNonKeyVideoBlocks(t *testing.T) {
	spec := mkvSpec{
		clusters: []clusterSpec{{ms: 0, blocks: []block{
			{track: 1, relMs: 0, key: false}, // a cluster that does not start on one
			{track: 2, relMs: 10, key: true},
			{track: 1, relMs: 30, key: true},
		}}},
		cuesFor: []int{0},
	}
	assertFrames(t, mustRead(t, spec), 0.03)
}

// A file without cues cannot be answered from the container: the caller falls
// back to the packet scan, so the reader must say so instead of guessing.
func TestMatroskaKeyFramesWithoutCuesIsAnError(t *testing.T) {
	spec := videoOnly(0, 5)
	spec.withoutCues = true
	path, size := writeFixture(t, spec)
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := matroskaKeyFrames(f, size); err == nil {
		t.Fatal("a file without cues must not answer")
	}
}

// A file that is not Matroska at all must not be mistaken for one.
func TestMatroskaKeyFramesRejectsOtherDocuments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not.mkv")
	if err := os.WriteFile(path, bytes.Repeat([]byte{0x00, 0x01}, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := matroskaKeyFrames(f, 8192); err == nil {
		t.Fatal("a document that is not EBML must not answer")
	}
}

// The cost of the read follows the number of key frames, not the size of the
// file: a 2 GB hole after the cues is never read.
func TestMatroskaKeyFramesDoNotReadTheFile(t *testing.T) {
	spec := videoOnly(0, 10, 20)
	spec.padAfter = 2 << 30
	got, counting := readFixture(t, spec)
	assertFrames(t, got, 0, 10, 20)
	if counting.bytes > 256<<10 {
		t.Fatalf("read %d bytes of a %d byte file in %d reads: the read must not follow the file size",
			counting.bytes, 2<<30, counting.reads)
	}
}

// The hand-written shape and what a real muxer writes are the same thing: the
// reader agrees with the packet scan on a file ffmpeg produced, to the tick,
// including its irregular key frames and its sound track.
func TestMatroskaKeyFramesAgreeWithThePacketScan(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg on $PATH")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("no ffprobe on $PATH")
	}
	src := filepath.Join(t.TempDir(), "irregular.mkv")
	gen := exec.Command(ffmpeg, "-v", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=40:size=320x240:rate=24",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=40",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-g", "1000", "-sc_threshold", "0",
		"-force_key_frames", "0,4,11,19,25,33", "-c:a", "ac3", "-shortest", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate: %v\n%s", err, out)
	}
	m := New(Config{FFmpegPath: ffmpeg, FFprobePath: ffprobe, WorkDir: t.TempDir()})
	scanned, err := m.scanKeyFrames(src)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	indexed, err := matroskaKeyFrames(f, st.Size())
	if err != nil {
		t.Fatal(err)
	}
	if len(indexed) != len(scanned) {
		t.Fatalf("index read %v, scan read %v", indexed, scanned)
	}
	for i := range scanned {
		if math.Abs(indexed[i]-scanned[i]) > 1e-6 {
			t.Fatalf("index read %v, scan read %v", indexed, scanned)
		}
	}
}
