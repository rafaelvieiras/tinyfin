package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/imagefetch"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
	"github.com/rafaelvieiras/tinyfin/internal/storage/sqlite"
)

// fakePersonImages is the download edge: it answers from memory, counts calls
// and can be told to fail.
type fakePersonImages struct {
	mu    sync.Mutex
	calls []string
	body  []byte
	err   error
	// delay makes a download slow enough for two requests to overlap.
	delay time.Duration
}

func (f *fakePersonImages) Fetch(ctx context.Context, url string) (imagefetch.Image, error) {
	f.mu.Lock()
	f.calls = append(f.calls, url)
	body, err, delay := f.body, f.err, f.delay
	f.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return imagefetch.Image{}, ctx.Err()
		}
	}
	if err != nil {
		return imagefetch.Image{}, err
	}
	return imagefetch.Image{Bytes: body, MIME: "image/png", URL: url}, nil
}

func (f *fakePersonImages) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// personImagePNG is a real image of known size, so the handler's dimension and
// blurhash work runs on something decodable.
func personImagePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 4), G: uint8(y * 4), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

// movieID is a canonical 32-hex item id; the API rejects anything else before
// it reads the store.
const movieID = "0a1b2c3d4e5f60718293a4b5c6d7e8f9"

// personImageFixture is a server with an image directory, a fake download edge
// and one cataloged movie whose first credit publishes a photo. The second
// credit has none, so both halves of the DTO rule are covered by one item.
func personImageFixture(t *testing.T) (*httptest.Server, *Server, *sqlite.Store, *fakePersonImages) {
	t.Helper()
	dir := t.TempDir()
	st, err := sqlite.New(filepath.Join(dir, "tinyfin.db"))
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	imgDir := filepath.Join(dir, "images")
	if err := os.MkdirAll(imgDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	srv := startTestServer(t, st, Config{ServerName: "TestServer", ScanImageDir: imgDir})
	fake := &fakePersonImages{body: personImagePNG(t, 40, 60)}
	srv.personImageFetcher = fake

	ctx := context.Background()
	if err := st.UpsertLibrary(ctx, storage.Library{ID: "lib", Name: "Movies", CollectionType: storage.CollectionTypeMovies}); err != nil {
		t.Fatal(err)
	}
	// A 32-hex id: the routes validate an item id before anything else.
	item := storage.Item{
		ID: movieID, Type: storage.ItemTypeMovie, LibraryID: "lib", Name: "Shot", SortName: "shot",
		People: []storage.Person{
			{Name: "Ann Actor", Role: "Lead", Type: "Actor", ImageURL: "https://images.example.com/ann.png"},
			{Name: "Bob Actor", Role: "Friend", Type: "Actor"},
		},
	}
	if err := st.UpsertItem(ctx, item); err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, srv, st, fake
}

// getItem fetches an item as an authenticated user, returning the decoded
// People list — the half of the response this feature adds to.
func getItemPeople(t *testing.T, ts *httptest.Server, st *sqlite.Store, itemID string) []struct {
	Name            string                       `json:"Name"`
	PrimaryImageTag string                       `json:"PrimaryImageTag"`
	ImageBlurHashes map[string]map[string]string `json:"ImageBlurHashes"`
} {
	t.Helper()
	createTestUser(t, st, "watcher", "watcherpass")
	token := loginToken(t, ts.URL, "watcher", "watcherpass")
	resp := authedClient(t, ts.URL, token)("GET", "/Items/"+itemID)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET item %s: status %d, want 200", itemID, resp.StatusCode)
	}
	var dto struct {
		People []struct {
			Name            string                       `json:"Name"`
			PrimaryImageTag string                       `json:"PrimaryImageTag"`
			ImageBlurHashes map[string]map[string]string `json:"ImageBlurHashes"`
		} `json:"People"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&dto); err != nil {
		t.Fatalf("decoding item: %v", err)
	}
	return dto.People
}

func getImage(t *testing.T, ts *httptest.Server, path string) (int, []byte) {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return resp.StatusCode, body
}

// TestPersonImageIsDownloadedOnFirstRequestAndServedFromDiskAfter proves the
// whole on-demand path: nothing is downloaded before a client asks, the first
// request fetches and stores, and the second is answered from the file.
func TestPersonImageIsDownloadedOnFirstRequestAndServedFromDiskAfter(t *testing.T) {
	ts, _, st, fake := personImageFixture(t)
	person := personID("Ann Actor")

	if fake.count() != 0 {
		t.Fatalf("a scan-equivalent write downloaded %d image(s); nothing may be prefetched", fake.count())
	}
	status, body := getImage(t, ts, "/Items/"+person+"/Images/Primary")
	if status != http.StatusOK {
		t.Fatalf("first request: status %d, want 200", status)
	}
	if !bytes.Equal(body, fake.body) {
		t.Fatalf("first request: body is not the downloaded picture (%d bytes vs %d)", len(body), len(fake.body))
	}
	if fake.count() != 1 {
		t.Fatalf("downloads after the first request: %d, want 1", fake.count())
	}
	// Stored, with dimensions and a blurhash, under the person id.
	img, ok, err := st.GetPersonImage(context.Background(), storage.ItemID(person))
	if err != nil {
		t.Fatal(err)
	}
	if !ok || img.Path == "" {
		t.Fatalf("person image row after the download: ok=%v row=%+v", ok, img)
	}
	if img.Width != 40 || img.Height != 60 {
		t.Errorf("stored dimensions: %dx%d, want 40x60", img.Width, img.Height)
	}
	if img.BlurHash == "" {
		t.Error("stored blurhash is empty")
	}
	if img.Tag != personImageTag(img.Source) {
		t.Errorf("stored tag %q is not the tag derived from the source", img.Tag)
	}

	status, body = getImage(t, ts, "/Items/"+person+"/Images/Primary")
	if status != http.StatusOK || !bytes.Equal(body, fake.body) {
		t.Fatalf("second request: status %d, %d bytes", status, len(body))
	}
	if fake.count() != 1 {
		t.Fatalf("downloads after the second request: %d, want 1 (served from disk)", fake.count())
	}
}

// TestPersonDTOAdvertisesThePhotoOfCreditsThatHaveOne is the load-bearing
// step: without PrimaryImageTag the client never requests the picture, so an
// on-demand download could never start.
func TestPersonDTOAdvertisesThePhotoOfCreditsThatHaveOne(t *testing.T) {
	ts, _, st, fake := personImageFixture(t)

	people := getItemPeople(t, ts, st, movieID)
	if len(people) != 2 {
		t.Fatalf("People: got %d entries, want 2", len(people))
	}
	wantTag := personImageTag("https://images.example.com/ann.png")
	if got := people[0].PrimaryImageTag; got != wantTag {
		t.Errorf("People[0].PrimaryImageTag = %q, want %q (derived from the URL)", got, wantTag)
	}
	// The credit without a URL has no tag: the field's absence is what tells
	// the client not to request an image.
	if got := people[1].PrimaryImageTag; got != "" {
		t.Errorf("People[1].PrimaryImageTag = %q, want absent", got)
	}
	// The tag is published before any download happened.
	if _, ok := people[0].ImageBlurHashes["Primary"]; ok {
		t.Error("ImageBlurHashes published before the picture was downloaded")
	}
	if fake.count() != 0 {
		t.Fatalf("reading the item downloaded %d image(s)", fake.count())
	}
}

// TestPersonDTOPublishesTheBlurHashOnceThePictureExists proves the placeholder
// appears after the download, under the same tag as the picture.
func TestPersonDTOPublishesTheBlurHashOnceThePictureExists(t *testing.T) {
	ts, _, st, _ := personImageFixture(t)
	person := personID("Ann Actor")
	if status, _ := getImage(t, ts, "/Items/"+person+"/Images/Primary"); status != http.StatusOK {
		t.Fatalf("image request: status %d", status)
	}

	people := getItemPeople(t, ts, st, movieID)
	hashes := people[0].ImageBlurHashes["Primary"]
	if len(hashes) != 1 {
		t.Fatalf("ImageBlurHashes.Primary = %v, want exactly the picture's tag", hashes)
	}
	if _, ok := hashes[people[0].PrimaryImageTag]; !ok {
		t.Errorf("blurhash is not filed under the published tag %q: %v", people[0].PrimaryImageTag, hashes)
	}
}

// TestPersonImageFailureIsA404ThatIsNotRetriedPerRequest proves a dead URL is
// not an error page and not a retry storm: one attempt, then 404s answered
// from the negative cache, and one attempt again once the retry window passes.
func TestPersonImageFailureIsA404ThatIsNotRetriedPerRequest(t *testing.T) {
	ts, srv, st, fake := personImageFixture(t)
	fake.err = errors.New("imagefetch: unexpected status: 404")
	person := personID("Ann Actor")

	now := time.Now()
	srv.personImageClock = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if status, _ := getImage(t, ts, "/Items/"+person+"/Images/Primary"); status != http.StatusNotFound {
			t.Fatalf("request %d: status %d, want 404", i+1, status)
		}
	}
	if fake.count() != 1 {
		t.Fatalf("download attempts: %d, want 1 (the rest answered from the negative cache)", fake.count())
	}
	// The DTO keeps advertising the tag — it is derived from the URL, and the
	// client's own retry is what will find the picture if the URL comes back
	// within the window.
	img, ok, err := st.GetPersonImage(context.Background(), storage.ItemID(person))
	if err != nil {
		t.Fatal(err)
	}
	if !ok || img.FailedAt.IsZero() {
		t.Fatalf("failure was not recorded: ok=%v row=%+v", ok, img)
	}

	// Past the retry window the URL is tried again.
	now = now.Add(personImageRetryAfter + time.Minute)
	fake.mu.Lock()
	fake.err = nil
	fake.mu.Unlock()
	if status, _ := getImage(t, ts, "/Items/"+person+"/Images/Primary"); status != http.StatusOK {
		t.Fatalf("after the retry window: status %d, want 200", status)
	}
	if fake.count() != 2 {
		t.Fatalf("download attempts after the window: %d, want 2", fake.count())
	}
}

// TestChangingThePhotoURLInvalidatesTheCachedPicture proves a re-scan that
// moves a credit's photo retries immediately instead of waiting out the window
// a failure had set, and that the old file is not served under the new tag.
func TestChangingThePhotoURLInvalidatesTheCachedPicture(t *testing.T) {
	ts, _, st, fake := personImageFixture(t)
	person := storage.ItemID(personID("Ann Actor"))
	ctx := context.Background()

	if status, _ := getImage(t, ts, "/Items/"+string(person)+"/Images/Primary"); status != http.StatusOK {
		t.Fatalf("first download: status %d", status)
	}
	// A re-scan finds a different URL for the same person.
	if err := st.UpsertPersonImageSource(ctx, person, "https://images.example.com/ann-v2.png"); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	fake.body = personImagePNG(t, 10, 10)
	fake.mu.Unlock()

	status, body := getImage(t, ts, "/Items/"+string(person)+"/Images/Primary")
	if status != http.StatusOK {
		t.Fatalf("after the URL changed: status %d, want 200", status)
	}
	if !bytes.Equal(body, fake.body) {
		t.Fatal("after the URL changed the old picture was served")
	}
	if fake.count() != 2 {
		t.Fatalf("downloads: %d, want 2", fake.count())
	}
}

// TestConcurrentRequestsForOnePersonDownloadOnce is the singleflight rule: a
// detail page opened twice at the same moment must not fetch the picture
// twice.
func TestConcurrentRequestsForOnePersonDownloadOnce(t *testing.T) {
	ts, _, _, fake := personImageFixture(t)
	fake.mu.Lock()
	fake.delay = 150 * time.Millisecond
	fake.mu.Unlock()
	person := personID("Ann Actor")

	var wg sync.WaitGroup
	statuses := make([]int, 4)
	for i := range statuses {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			statuses[i], _ = getImage(t, ts, "/Items/"+person+"/Images/Primary")
		}(i)
	}
	wg.Wait()
	for i, status := range statuses {
		if status != http.StatusOK {
			t.Fatalf("request %d: status %d, want 200", i+1, status)
		}
	}
	if fake.count() != 1 {
		t.Fatalf("downloads for %d concurrent requests: %d, want 1", len(statuses), fake.count())
	}
}

// TestPersonWithoutAPhotoSourceIs404WithNoDownload covers the credit that has
// no image anywhere: no tag, no request, and the route answers what it
// answered before this existed.
func TestPersonWithoutAPhotoSourceIs404WithNoDownload(t *testing.T) {
	ts, _, _, fake := personImageFixture(t)
	if status, _ := getImage(t, ts, "/Items/"+personID("Bob Actor")+"/Images/Primary"); status != http.StatusNotFound {
		t.Fatalf("status %d, want 404", status)
	}
	if fake.count() != 0 {
		t.Fatalf("downloads for a credit with no image URL: %d, want 0", fake.count())
	}
	// A kind nobody publishes for a person is a 404 too, and not a download.
	if status, _ := getImage(t, ts, "/Items/"+personID("Ann Actor")+"/Images/Backdrop"); status != http.StatusNotFound {
		t.Fatalf("backdrop: status %d, want 404", status)
	}
	if fake.count() != 0 {
		t.Fatalf("downloads for a backdrop: %d, want 0", fake.count())
	}
}

// TestPersonImageIsServedAtTheRequestedSize proves a person's picture goes
// through the same sizing path as an item's: the route was reused, so a
// maxWidth request must be answered at the requested width, not with the file.
func TestPersonImageIsServedAtTheRequestedSize(t *testing.T) {
	ts, _, _, _ := personImageFixture(t)
	person := personID("Ann Actor")
	if status, _ := getImage(t, ts, "/Items/"+person+"/Images/Primary"); status != http.StatusOK {
		t.Fatalf("download: status %d", status)
	}
	resp, err := http.Get(ts.URL + "/Items/" + person + "/Images/Primary?maxWidth=20")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sized request: status %d", resp.StatusCode)
	}
	cfg, _, err := image.DecodeConfig(resp.Body)
	if err != nil {
		t.Fatalf("decoding sized image: %v", err)
	}
	if cfg.Width != 20 || cfg.Height != 30 {
		t.Fatalf("sized image: %dx%d, want 20x30 (aspect kept)", cfg.Width, cfg.Height)
	}
}

// TestPersonImageTagIsStableAndFollowsTheURL pins the two properties the tag
// has to have: unchanged for the same URL, different for a different one.
func TestPersonImageTagIsStableAndFollowsTheURL(t *testing.T) {
	a := personImageTag("https://images.example.com/ann.png")
	if a != personImageTag("https://images.example.com/ann.png") {
		t.Error("the same URL produced two different tags")
	}
	if a == personImageTag("https://images.example.com/ann-v2.png") {
		t.Error("a different URL produced the same tag")
	}
	if len(a) != 32 || strings.Trim(a, "0123456789abcdef") != "" {
		t.Errorf("tag %q is not 32 hex characters", a)
	}
}
