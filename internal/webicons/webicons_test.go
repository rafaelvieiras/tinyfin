package webicons

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"testing"
)

// The name the production bundle asks for, fingerprint and all.
const prodFavicon = "/web/favicon.bc8d51405ec040305a87.ico"

func TestFaviconIsARealIconWithThreeFrames(t *testing.T) {
	asset, ok := Lookup(prodFavicon)
	if !ok {
		t.Fatalf("Lookup(%q) = not ours", prodFavicon)
	}
	if asset.ContentType != "image/x-icon" {
		t.Errorf("content type = %q, want image/x-icon", asset.ContentType)
	}
	if len(asset.Data) < 6 {
		t.Fatalf("icon is %d bytes, too short for a header", len(asset.Data))
	}
	if got := binary.LittleEndian.Uint16(asset.Data[0:2]); got != 0 {
		t.Errorf("ICONDIR reserved = %d, want 0", got)
	}
	if got := binary.LittleEndian.Uint16(asset.Data[2:4]); got != 1 {
		t.Errorf("ICONDIR type = %d, want 1 (icon)", got)
	}
	count := int(binary.LittleEndian.Uint16(asset.Data[4:6]))
	if count != 3 {
		t.Fatalf("frame count = %d, want 3 (16, 32, 48)", count)
	}

	// Each directory entry says the frame is a PNG of that many bytes, and
	// the bytes at the stated offset really are a PNG of that width.
	want := []int{16, 32, 48}
	for i := 0; i < count; i++ {
		entry := asset.Data[6+16*i : 6+16*(i+1)]
		width := int(entry[0])
		if width != want[i] {
			t.Errorf("frame %d width = %d, want %d", i, width, want[i])
		}
		size := binary.LittleEndian.Uint32(entry[8:12])
		offset := binary.LittleEndian.Uint32(entry[12:16])
		if offset+size > uint32(len(asset.Data)) {
			t.Fatalf("frame %d runs past the end: offset %d size %d", i, offset, size)
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(asset.Data[offset : offset+size]))
		if err != nil {
			t.Fatalf("frame %d is not a PNG: %v", i, err)
		}
		if cfg.Width != want[i] || cfg.Height != want[i] {
			t.Errorf("frame %d is %dx%d, want %dx%d", i, cfg.Width, cfg.Height, want[i], want[i])
		}
	}
}

func TestTouchIconsAreTheSizeTheyClaim(t *testing.T) {
	for _, tc := range []struct {
		path string
		size int
	}{
		{"/web/touchicon.f5bbb798cb2c65908633.png", 180}, // the apple-touch-icon
		{"/web/touchicon144.39209dd2362c0db7c673.png", 144},
		{"/web/favicons/touchicon.png", 180},
		{"/web/favicons/touchicon72.png", 72},
		{"/web/favicons/touchicon114.png", 114},
		{"/web/favicons/touchicon144.png", 144},
		{"/web/favicons/touchicon512.png", 512},
	} {
		asset, ok := Lookup(tc.path)
		if !ok {
			t.Errorf("Lookup(%q) = not ours", tc.path)
			continue
		}
		if asset.ContentType != "image/png" {
			t.Errorf("%s: content type = %q, want image/png", tc.path, asset.ContentType)
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(asset.Data))
		if err != nil {
			t.Errorf("%s: not a PNG: %v", tc.path, err)
			continue
		}
		if cfg.Width != tc.size || cfg.Height != tc.size {
			t.Errorf("%s is %dx%d, want %dx%d", tc.path, cfg.Width, cfg.Height, tc.size, tc.size)
		}
	}
}

func TestSplashAndNotificationIcons(t *testing.T) {
	for _, tc := range []struct {
		path string
		size int
	}{
		{"/web/icon-transparent.baba78f2a106d9baee83.png", 512},
		{"/web/notificationicon.d6ecf2254db85ff3b545.png", 440},
	} {
		asset, ok := Lookup(tc.path)
		if !ok {
			t.Fatalf("Lookup(%q) = not ours", tc.path)
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(asset.Data))
		if err != nil {
			t.Fatalf("%s: not a PNG: %v", tc.path, err)
		}
		if cfg.Width != tc.size || cfg.Height != tc.size {
			t.Errorf("%s is %dx%d, want %dx%d", tc.path, cfg.Width, cfg.Height, tc.size, tc.size)
		}
	}
}

// The pictures that carry the "Jellyfin" wordmark keep the upstream bytes: a
// mascot on its own would leave the name off the login page and the drawer.
func TestWordmarkPicturesAreNotOurs(t *testing.T) {
	for _, p := range []string{
		"/web/banner-light.b113d4d1c6c07fcb73f0.png",
		"/web/banner-dark.31011bc0c5e6acb07879.png",
		"/web/logoScreensaver-plugin.a66c07ecf602b1ce7356.chunk.js",
		"/web/main.jellyfin.bundle.js",
		"/web/manifest.json",
		"/web/index.html",
		"/web/favicons/",
		"/web/favicon.png",
	} {
		if asset, ok := Lookup(p); ok {
			t.Errorf("Lookup(%q) = %q, want not ours (the wordmark must stay)", p, asset.Name)
		}
	}
}

// The fingerprint changes with every upstream build, so the match cannot depend
// on it: any hex digest, or none at all, has to resolve to the same picture.
func TestAnyFingerprintResolves(t *testing.T) {
	base, ok := Lookup("/web/favicons/touchicon144.png")
	if !ok {
		t.Fatal("unfingerprinted path is not ours")
	}
	for _, p := range []string{
		"/web/favicons/touchicon144.0123456789abcdef.png",
		"/WEB/FAVICONS/TOUCHICON144.PNG",
		"/web//favicons/touchicon144.png",
	} {
		asset, ok := Lookup(p)
		if !ok {
			t.Errorf("Lookup(%q) = not ours", p)
			continue
		}
		if asset.ETag != base.ETag {
			t.Errorf("Lookup(%q) has ETag %s, want %s", p, asset.ETag, base.ETag)
		}
	}
}

// The mascot is cut out of a white background, so its corners are transparent.
// A master that lost its alpha channel would paint a white box on the dark
// splash screen, which no size assertion above would catch.
func TestMascotKeepsItsTransparentBackground(t *testing.T) {
	asset, ok := Lookup("/web/icon-transparent.baba78f2a106d9baee83.png")
	if !ok {
		t.Fatal("splash icon is not ours")
	}
	img, err := png.Decode(bytes.NewReader(asset.Data))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
		t.Errorf("top-left alpha = %d, want fully transparent", a)
	}
}

func TestUnknownNamesAreNotOurs(t *testing.T) {
	for _, p := range []string{
		"/web/touchiconabc.png",
		"/web/touchicon8.png",
		"/web/icon-transparent.svg",
		"/web/othericon.png",
		"/System/Info/Public",
		"/web/favicon.ico/extra",
	} {
		if asset, ok := Lookup(p); ok {
			t.Errorf("Lookup(%q) = %q, want not ours", p, asset.Name)
		}
	}
}
