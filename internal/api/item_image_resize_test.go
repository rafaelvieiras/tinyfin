package api

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// pictureFile writes a w x h PNG or JPEG into dir and returns its path. The
// picture is a two-colour ramp so a resize has something to do.
func pictureFile(t *testing.T, dir, name string, w, h int, format string) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 0x40, A: 0xFF})
		}
	}
	var buf bytes.Buffer
	var err error
	path := filepath.Join(dir, name)
	switch format {
	case "png":
		err = png.Encode(&buf, img)
	default:
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95})
	}
	if err != nil {
		t.Fatalf("%s encode: %v", format, err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

// seededImageServer is a server with one movie item carrying a Primary picture
// of the given size and format.
func seededImageServer(t *testing.T, w, h int, format string) (baseURL, token, itemPath string, original []byte) {
	t.Helper()
	ts, st := newTestServer(t)
	admin := createTestAdminUser(t, st, "resize-admin", "adminpass")
	token = loginToken(t, ts.URL, admin.Name, "adminpass")
	libID := createTestLibrary(t, st, "ResizeLib", storage.CollectionTypeMovies)

	dir := t.TempDir()
	path := pictureFile(t, dir, "poster."+format, w, h, format)
	item := createTestItem(t, st, storage.Item{
		Type:      storage.ItemTypeMovie,
		LibraryID: libID,
		Name:      "Resizable Movie",
		Path:      filepath.Join(dir, "movie.mkv"),
	})
	if err := st.UpsertImageRef(context.Background(), storage.ImageRef{
		ItemID: item.ID,
		Kind:   "Primary",
		Path:   path,
		Tag:    "tag-resize",
		Width:  w,
		Height: h,
	}); err != nil {
		t.Fatalf("UpsertImageRef: %v", err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	return ts.URL, token, "/Items/" + string(item.ID) + "/Images/Primary", original
}

// decodeImage reads a response body as an image and returns its size.
func decodeImage(t *testing.T, body io.Reader) (int, int, string) {
	t.Helper()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return cfg.Width, cfg.Height, format
}

// TestItemImageResizeAnswersAtTheAskedSize covers the members that make a
// picture be made again: the answer comes back at the requested size, in the
// source's own format.
func TestItemImageResizeAnswersAtTheAskedSize(t *testing.T) {
	cases := []struct {
		name          string
		format        string
		query         string
		wantW, wantH  int
		wantFormat    string
		wantSameBytes bool
	}{
		{name: "jpeg maxWidth", format: "jpg", query: "?maxWidth=100", wantW: 100, wantH: 50, wantFormat: "jpeg"},
		{name: "jpeg maxHeight", format: "jpg", query: "?maxHeight=50", wantW: 100, wantH: 50, wantFormat: "jpeg"},
		{name: "png keeps its format", format: "png", query: "?maxWidth=100", wantW: 100, wantH: 50, wantFormat: "png"},
		// A fill box only shapes the size once something else asks for the
		// picture to be made again — the reference ignores one that arrives
		// alone (picture.PlanFor's doc comment), so quality rides along here.
		{name: "fill covers the box", format: "jpg", query: "?fillWidth=60&fillHeight=60&quality=80", wantW: 120, wantH: 60, wantFormat: "jpeg"},
		{name: "fill alone is the stored picture", format: "jpg", query: "?fillWidth=60&fillHeight=60", wantW: 200, wantH: 100, wantFormat: "jpeg", wantSameBytes: true},
		{name: "bigger than the picture is the picture", format: "jpg", query: "?maxWidth=5000", wantW: 200, wantH: 100, wantFormat: "jpeg", wantSameBytes: true},
		{name: "nothing asked for", format: "jpg", query: "", wantW: 200, wantH: 100, wantFormat: "jpeg", wantSameBytes: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base, token, path, original := seededImageServer(t, 200, 100, c.format)
			resp := authedRequest(t, base, token, http.MethodGet, path+c.query, "", nil)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			cfg, format, err := image.DecodeConfig(bytes.NewReader(body))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if cfg.Width != c.wantW || cfg.Height != c.wantH {
				t.Errorf("size = %dx%d, want %dx%d", cfg.Width, cfg.Height, c.wantW, c.wantH)
			}
			if format != c.wantFormat {
				t.Errorf("format = %s, want %s", format, c.wantFormat)
			}
			if c.wantSameBytes && !bytes.Equal(body, original) {
				t.Errorf("body differs from the stored picture, want the stored bytes")
			}
			if !c.wantSameBytes && bytes.Equal(body, original) {
				t.Errorf("body is the stored picture, want a picture made again")
			}
		})
	}
}

// TestItemImageResizeQualityReencodes covers quality: below the pass threshold
// it makes the picture again at its own size, at or above it the stored bytes
// are the answer.
func TestItemImageResizeQualityReencodes(t *testing.T) {
	for _, c := range []struct {
		query string
		want  bool // true: the body must differ from the stored picture
	}{
		{"?quality=60", true},
		{"?quality=90", false},
		{"?quality=100", false},
	} {
		base, token, path, original := seededImageServer(t, 200, 100, "jpg")
		resp := authedRequest(t, base, token, http.MethodGet, path+c.query, "", nil)
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", c.query, resp.StatusCode)
		}
		if got := !bytes.Equal(body, original); got != c.want {
			t.Errorf("%s: re-encoded = %v, want %v", c.query, got, c.want)
		}
		// Whatever happened, the size is unchanged.
		cfg, _, err := image.DecodeConfig(bytes.NewReader(body))
		if err != nil || cfg.Width != 200 || cfg.Height != 100 {
			t.Errorf("%s: size = %+v (err %v), want 200x100", c.query, cfg, err)
		}
	}
}

// TestItemImageResizeRejectsANonInteger pins the validation the reference
// answers with a 400.
func TestItemImageResizeRejectsANonInteger(t *testing.T) {
	base, token, path, _ := seededImageServer(t, 200, 100, "jpg")
	resp := authedRequest(t, base, token, http.MethodGet, path+"?maxWidth=abc", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := body["errors"]; !ok {
		t.Errorf("body = %#v, want a validation errors object", body)
	}
}

// TestLibraryCardImageResized covers the case that made this route matter for
// a dashboard: a library's card image is a 960x540 PNG, and a card asks for a
// smaller one — the answer must stay a PNG at the asked size.
func TestLibraryCardImageResized(t *testing.T) {
	ts, st, imgDir := newTestServerWithImageDir(t)
	admin := createTestAdminUser(t, st, "card-resize-admin", "adminpass")
	token := loginToken(t, ts.URL, admin.Name, "adminpass")
	libID := createTestLibrary(t, st, "CardResize", storage.CollectionTypeMovies)

	path := pictureFile(t, imgDir, "card.png", 960, 540, "png")
	if err := st.UpsertLibraryImage(context.Background(), storage.LibraryImage{
		LibraryID: libID, Kind: "Primary", Path: path, Tag: "tag-card",
		Width: 960, Height: 540,
	}); err != nil {
		t.Fatalf("UpsertLibraryImage: %v", err)
	}

	resp := authedRequest(t, ts.URL, token, http.MethodGet, "/Items/"+string(libID)+"/Images/Primary?maxWidth=300", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	w, h, format := decodeImage(t, resp.Body)
	if w != 300 || h != 169 || format != "png" {
		t.Errorf("answer = %dx%d %s, want 300x169 png", w, h, format)
	}
}
