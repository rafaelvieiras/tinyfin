// Package webicons answers the upstream web client's icon-only pictures with
// the Tinyfin mascot.
//
// Why this lives in the server: jellyfin-web stays the unmodified upstream
// build (docs/00-decision-log.md, D9), so the only place Tinyfin can change what
// a client shows is its own answer. Of the pictures the bundle ships, the ones
// below carry no wordmark — the favicon, the apple-touch and web-app icons, the
// splash mark, the notification icon — and a server that serves a mascot there
// changes nothing about the client's code.
//
// The wordmark pictures are deliberately NOT here: banner-light and banner-dark
// pair the mark with the "Jellyfin" lettering, and replacing them would leave a
// mascot with no name next to it (docs/13-divergences.md, DIV-39).
//
// The paths are fingerprinted by the client's build (favicon.bc8d5140….ico,
// icon-transparent.baba78f2….png), and the fingerprint changes every time the
// bundle is rebuilt. Lookup therefore matches on the stem, never the hash, or
// every upstream release would silently bring the Jellyfin icons back.
package webicons

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"image"
	"image/png"
	"path"
	"strconv"
	"strings"
	"sync"

	xdraw "golang.org/x/image/draw"
)

// The two masters, both 512x512 and cut out of the mascot artwork with a
// transparent background.
//
//	mascot.png        the whole character, limbs included
//	mascot-tight.png  cropped to the cube, so the face still reads at 16px
//
// The master is 512 because the largest size the client asks for is 512
// (favicons/touchicon512.png); rendering bigger would be inventing detail.
//
//go:embed mascot.png
var mascotPNG []byte

//go:embed mascot-tight.png
var mascotTightPNG []byte

// sizes the client requests, and the ones Lookup will answer.
const (
	sizeFavicon       = 48 // the largest frame a .ico carries; smaller ones are scaled down
	sizeTouchIcon     = 180
	sizeSplash        = 512
	sizeNotification  = 440
	sizeMaster        = 512
	maxRenderableSize = sizeMaster
)

// Asset is one finished picture, ready to be written to a response.
type Asset struct {
	// Name is the file name the client asked for, for logging.
	Name string
	// ContentType is image/png or image/x-icon.
	ContentType string
	// Data is the encoded picture. Never mutate it.
	Data []byte
	// ETag is the strong validator for Data, quoted as HTTP wants it.
	ETag string
}

// Lookup maps a request path under /web/ to a mascot picture. The bool is false
// for every path this package does not own, which is the vast majority of the
// bundle — including the two wordmark banners.
func Lookup(urlPath string) (Asset, bool) {
	name := path.Base(strings.ToLower(strings.TrimSpace(urlPath)))

	ext := ""
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		name, ext = name[:i], name[i:]
	}
	if ext != ".png" && ext != ".ico" {
		return Asset{}, false
	}
	// The build fingerprints the file name: touchicon.f5bbb798cb2c65908633.png.
	// A dot in a stem is only ever that fingerprint, so dropping it is safe.
	if i := strings.LastIndexByte(name, '.'); i >= 0 && looksLikeFingerprint(name[i+1:]) {
		name = name[:i]
	}

	if ext == ".ico" {
		if name != "favicon" {
			return Asset{}, false
		}
		return cached(name, "image/x-icon", func() ([]byte, error) { return faviconICO() })
	}

	size, ok := pictureSize(name)
	if !ok {
		return Asset{}, false
	}
	return cached(name, "image/png", func() ([]byte, error) { return renderPNG(size) })
}

// pictureSize reads the target size out of the stem the bundle uses.
func pictureSize(stem string) (int, bool) {
	switch {
	case stem == "icon-transparent":
		// The splash mark: a CSS background inside a 30%-of-viewport box.
		return sizeSplash, true
	case stem == "notificationicon":
		// The icon of a desktop notification.
		return sizeNotification, true
	case stem == "touchicon":
		// favicons/touchicon.png and the 180x180 apple-touch-icon.
		return sizeTouchIcon, true
	case strings.HasPrefix(stem, "touchicon"):
		// touchicon72.png, touchicon114, touchicon144, touchicon512.
		n, err := strconv.Atoi(strings.TrimPrefix(stem, "touchicon"))
		if err != nil || n < 16 {
			return 0, false
		}
		if n > maxRenderableSize {
			n = maxRenderableSize
		}
		return n, true
	}
	return 0, false
}

// looksLikeFingerprint reports whether s is the hex digest the build puts
// between the stem and the extension.
func looksLikeFingerprint(s string) bool {
	if len(s) < 8 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// cache keeps the encoded pictures for the life of the process: the set of
// sizes is fixed by the bundle, so the work is done once.
var cache struct {
	sync.Mutex
	assets map[string]Asset
}

// cached renders one picture on first use and memoizes it.
func cached(name, contentType string, render func() ([]byte, error)) (Asset, bool) {
	cache.Lock()
	defer cache.Unlock()
	if cache.assets == nil {
		cache.assets = make(map[string]Asset)
	}
	key := contentType + " " + name
	if a, ok := cache.assets[key]; ok {
		return a, true
	}
	data, err := render()
	if err != nil || len(data) == 0 {
		return Asset{}, false
	}
	sum := sha256.Sum256(data)
	a := Asset{
		Name:        name,
		ContentType: contentType,
		Data:        data,
		ETag:        `"` + hex.EncodeToString(sum[:8]) + `"`,
	}
	cache.assets[key] = a
	return a, true
}

// renderPNG scales the mascot to size. Anything up to 48 pixels takes the
// tighter crop, because at 16 the limbs cost more than they show.
func renderPNG(size int) ([]byte, error) {
	src := mascotPNG
	if size <= sizeFavicon {
		src = mascotTightPNG
	}
	img, err := png.Decode(bytes.NewReader(src))
	if err != nil {
		return nil, fmt.Errorf("decode mascot: %w", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, scale(img, size)); err != nil {
		return nil, fmt.Errorf("encode %dx%d: %w", size, size, err)
	}
	return buf.Bytes(), nil
}

// scale resamples img to size x size, keeping the alpha channel.
func scale(src image.Image, size int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Over, nil)
	return dst
}

// faviconICO builds a real .ico holding 16, 32 and 48 pixel PNG frames. PNG
// frames inside an icon container are what every current browser reads; the
// older uncompressed-DIB form buys nothing here.
func faviconICO() ([]byte, error) {
	sizes := []int{16, 32, 48}

	frames := make([][]byte, len(sizes))
	for i, size := range sizes {
		frame, err := renderPNG(size)
		if err != nil {
			return nil, err
		}
		frames[i] = frame
	}

	// ICONDIR: reserved, type 1 (icon), image count.
	var buf bytes.Buffer
	buf.Write([]byte{0, 0, 1, 0})
	if err := binary.Write(&buf, binary.LittleEndian, uint16(len(frames))); err != nil {
		return nil, err
	}

	// ICONDIRENTRY per frame: width, height (0 means 256), palette size,
	// reserved, color planes, bits per pixel, byte size, byte offset.
	offset := 6 + 16*len(frames)
	for i, size := range sizes {
		dim := byte(size)
		if size == 256 {
			dim = 0
		}
		for _, v := range []byte{dim, dim, 0, 0} {
			buf.WriteByte(v)
		}
		if err := binary.Write(&buf, binary.LittleEndian, uint16(1)); err != nil { // color planes
			return nil, err
		}
		if err := binary.Write(&buf, binary.LittleEndian, uint16(32)); err != nil { // bits per pixel
			return nil, err
		}
		if err := binary.Write(&buf, binary.LittleEndian, uint32(len(frames[i]))); err != nil {
			return nil, err
		}
		if err := binary.Write(&buf, binary.LittleEndian, uint32(offset)); err != nil {
			return nil, err
		}
		offset += len(frames[i])
	}
	for _, frame := range frames {
		buf.Write(frame)
	}
	return buf.Bytes(), nil
}
