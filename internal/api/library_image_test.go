package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// TestScanTrackerProgressIsTheMeanOfRunningScans covers the number the task
// list publishes while a batch of libraries scans: each run contributes its own
// reported percentage, so the bar moves as the libraries advance instead of
// jumping only when a whole library finishes.
func TestScanTrackerProgressIsTheMeanOfRunningScans(t *testing.T) {
	var tr scanTracker
	a := tr.begin("lib-a", func() {})
	b := tr.begin("lib-b", func() {})

	tr.setProgress(a, 40)
	if got, want := tr.progress(), 20.0; got != want {
		t.Fatalf("progress with one run at 40%% = %v, want %v", got, want)
	}
	tr.setProgress(b, 80)
	if got, want := tr.progress(), 60.0; got != want {
		t.Fatalf("progress with runs at 40%% and 80%% = %v, want %v", got, want)
	}
	// A run that reports a lower number than before never drags the mean
	// down: the scanner only publishes increases, and the tracker keeps it
	// that way even if a caller got it wrong.
	tr.setProgress(a, 10)
	if got, want := tr.progress(), 60.0; got != want {
		t.Fatalf("progress after a lower report = %v, want %v", got, want)
	}

	// A finished run counts as 100, so a fast library finishing ahead of a
	// slow one never drags the bar backwards.
	tr.end(a, nil)
	if got, want := tr.progress(), 90.0; got != want {
		t.Fatalf("progress after one run finished = %v, want %v", got, want)
	}
	if running, _, _, _ := tr.snapshot(); !running {
		t.Fatal("the batch reports idle while a run is still in flight")
	}
	tr.end(b, nil)
	if running, _, _, _ := tr.snapshot(); running {
		t.Fatal("the batch reports running after every run finished")
	}
	// The finished runs stay in the batch until a new one starts, which is
	// when the counters reset.
	next := tr.begin("lib-c", func() {})
	if got := tr.progress(); got != 0 {
		t.Fatalf("progress at the start of a new batch = %v, want 0", got)
	}
	tr.end(next, nil)
}

// TestScanTrackerProgressNeverGoesBackwards covers the shape a dashboard
// draws: a batch of libraries whose scans finish at different moments must
// report a percentage that only rises.
func TestScanTrackerProgressNeverGoesBackwards(t *testing.T) {
	var tr scanTracker
	fast := tr.begin("lib-fast", func() {})
	slow := tr.begin("lib-slow", func() {})

	seen := []float64{tr.progress()}
	for _, pct := range []float64{10, 40, 90} {
		tr.setProgress(fast, pct)
		seen = append(seen, tr.progress())
	}
	tr.setProgress(slow, 10)
	seen = append(seen, tr.progress())
	tr.end(fast, nil) // the fast library finishes first
	seen = append(seen, tr.progress())
	tr.setProgress(slow, 50)
	seen = append(seen, tr.progress())

	for i := 1; i < len(seen); i++ {
		if seen[i] < seen[i-1] {
			t.Fatalf("progress went backwards at step %d: %v -> %v (series %v)", i, seen[i-1], seen[i], seen)
		}
	}
}

// TestScanTrackerLibProgressReportsOnlyItsOwnLibrary covers
// GET /Library/VirtualFolders's per-library RefreshStatus/RefreshProgress: one
// library scanning makes that library Active with its own percentage and leaves
// its neighbours Idle.
func TestScanTrackerLibProgressReportsOnlyItsOwnLibrary(t *testing.T) {
	var tr scanTracker
	if status, pct := tr.libProgress("lib-a"); status != "Idle" || pct != 0 {
		t.Fatalf("idle library = %q/%v, want Idle/0", status, pct)
	}

	id := tr.begin("lib-a", func() {})
	tr.setProgress(id, 18.64)

	status, pct := tr.libProgress("lib-a")
	if status != "Active" || pct != 18.64 {
		t.Errorf("library a = %q/%v, want Active/18.64", status, pct)
	}
	if status, pct := tr.libProgress("lib-b"); status != "Idle" || pct != 0 {
		t.Errorf("library b = %q/%v, want Idle/0", status, pct)
	}

	tr.end(id, nil)
	if status, _ := tr.libProgress("lib-a"); status != "Idle" {
		t.Errorf("library a after its scan ended = %q, want Idle", status)
	}
}

// TestScanTrackerProgressPushOnlyOnWholePercents keeps a very chatty scan from
// flooding the socket: the caller pushes the task list only when a whole
// percentage point has been crossed.
func TestScanTrackerProgressPushOnlyOnWholePercents(t *testing.T) {
	var tr scanTracker
	id := tr.begin("lib-a", func() {})

	if tr.setProgress(id, 0.4) {
		t.Error("a fractional first step asked for a push, want none")
	}
	if !tr.setProgress(id, 1.2) {
		t.Error("crossing 1% asked for no push, want one")
	}
	if tr.setProgress(id, 1.9) {
		t.Error("staying inside the same percentage asked for a push, want none")
	}
	if !tr.setProgress(id, 2.5) {
		t.Error("crossing 2% asked for no push, want one")
	}
	if tr.setProgress(id, 2.8) {
		t.Error("a repeated report inside the same point asked for a push, want none")
	}
	// A finished run stops being addressable at all.
	tr.end(id, nil)
	if tr.setProgress(id, 50) {
		t.Error("a finished run asked for a push, want none")
	}
}

// TestVirtualFoldersReportPrimaryImageItemId covers the field jellyfin-web's
// libraries page reads before it draws a card image: present exactly when the
// library has one, absent otherwise, and always with a RefreshStatus.
func TestVirtualFoldersReportPrimaryImageItemId(t *testing.T) {
	ts, st := newTestServer(t)
	admin := createTestAdminUser(t, st, "vf-admin", "adminpass")
	get := authedClient(t, ts.URL, loginToken(t, ts.URL, admin.Name, "adminpass"))

	withImage := createTestLibrary(t, st, "WithImage", storage.CollectionTypeMovies)
	createTestLibrary(t, st, "WithoutImage", storage.CollectionTypeMovies)

	if err := st.UpsertLibraryImage(context.Background(), storage.LibraryImage{
		LibraryID: withImage, Kind: "Primary", Path: "/images/with.png", Tag: "tag-1", Width: 960, Height: 540, Size: 12,
	}); err != nil {
		t.Fatalf("UpsertLibraryImage: %v", err)
	}

	resp := get(http.MethodGet, "/Library/VirtualFolders")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var folders []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&folders); err != nil {
		t.Fatalf("decode: %v", err)
	}

	seen := map[string]map[string]any{}
	for _, f := range folders {
		seen[f["Name"].(string)] = f
	}
	if got, want := seen["WithImage"]["PrimaryImageItemId"], string(withImage); got != want {
		t.Errorf("WithImage.PrimaryImageItemId = %v, want %q", got, want)
	}
	if _, ok := seen["WithoutImage"]["PrimaryImageItemId"]; ok {
		t.Errorf("WithoutImage.PrimaryImageItemId = %v, want the field absent", seen["WithoutImage"]["PrimaryImageItemId"])
	}
	for name, f := range seen {
		if got, want := f["RefreshStatus"], "Idle"; got != want {
			t.Errorf("%s.RefreshStatus = %v, want %q", name, got, want)
		}
		if _, ok := f["RefreshProgress"]; ok {
			t.Errorf("%s.RefreshProgress = %v, want the field absent while idle", name, f["RefreshProgress"])
		}
	}
}

// TestVirtualFoldersReportScanInProgress covers the per-library report of a
// scan actually running: RefreshStatus Active with the percentage the scanner
// last reported.
func TestVirtualFoldersReportScanInProgress(t *testing.T) {
	ts, st, srv := newTestServerWithScheduledTasks(t)
	admin := createTestAdminUser(t, st, "vf-scan-admin", "adminpass")
	token := loginToken(t, ts.URL, admin.Name, "adminpass")
	if token == "" {
		t.Fatal("loginToken returned no token")
	}

	libID := createTestLibrary(t, st, "Scanning", storage.CollectionTypeMovies)
	runID := srv.scanState.begin(libID, func() {})
	defer srv.scanState.end(runID, nil)
	srv.scanState.setProgress(runID, 42.5)

	// Called directly: this test's server exposes only the scheduled-task
	// routes, and the point here is the report, not the routing. The handler
	// reads no caller state (it lists every library an admin sees), so a bare
	// request is enough.
	rec := httptest.NewRecorder()
	srv.handleGetVirtualFolders(rec, httptest.NewRequest(http.MethodGet, "/Library/VirtualFolders", nil))
	var folders []virtualFolderInfo
	if err := json.NewDecoder(rec.Body).Decode(&folders); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, f := range folders {
		if storage.LibraryID(f.ItemId) != libID {
			continue
		}
		if f.RefreshStatus != "Active" {
			t.Errorf("RefreshStatus = %q, want %q", f.RefreshStatus, "Active")
		}
		if f.RefreshProgress == nil {
			t.Fatal("RefreshProgress = nil, want the scanner's percentage")
		}
		if got, want := *f.RefreshProgress, 42.5; got != want {
			t.Errorf("RefreshProgress = %v, want %v", got, want)
		}
		return
	}
	t.Fatalf("library %q missing from %#v", libID, folders)
}

// TestLibraryPrimaryImageServedFromItsOwnTable covers the dashboard asking for
// a library's card image exactly the way it asks for an item's:
// /Items/{itemId}/Images/Primary with the library's own ItemId.
func TestLibraryPrimaryImageServedFromItsOwnTable(t *testing.T) {
	ts, st, imgDir := newTestServerWithImageDir(t)
	admin := createTestAdminUser(t, st, "libimg-admin", "adminpass")
	token := loginToken(t, ts.URL, admin.Name, "adminpass")

	libID := createTestLibrary(t, st, "CardImage", storage.CollectionTypeMovies)
	path := filepath.Join(imgDir, "card.png")
	want := []byte("not really a png, but bytes to serve")
	if err := os.WriteFile(path, want, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := st.UpsertLibraryImage(context.Background(), storage.LibraryImage{
		LibraryID: libID, Kind: "Primary", Path: path, Tag: "tag-card", Width: 960, Height: 540, Size: int64(len(want)),
	}); err != nil {
		t.Fatalf("UpsertLibraryImage: %v", err)
	}

	resp := authedRequest(t, ts.URL, token, http.MethodGet, "/Items/"+string(libID)+"/Images/Primary", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("body = %q, want %q", got, want)
	}
	// The list route the dashboard's "Edit images" dialog uses must show it.
	listResp := authedRequest(t, ts.URL, token, http.MethodGet, "/Items/"+string(libID)+"/Images", "", nil)
	defer listResp.Body.Close()
	var infos []imageInfo
	if err := json.NewDecoder(listResp.Body).Decode(&infos); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(infos) != 1 || infos[0].ImageType != "Primary" || infos[0].Path != path {
		t.Fatalf("images = %#v, want one Primary entry pointing at %q", infos, path)
	}
}

// TestLibraryImageTagPublishedOnTheLibraryItem covers the half of a library
// card image that makes jellyfin-web draw it: GET /UserViews and
// GET /Items/{libraryId} must carry ImageTags.Primary (plus the aspect ratio
// and blurhash the client paints while loading), exactly as the reference
// server's CollectionFolder does. Serving the bytes is not enough — a library
// whose DTO has no tag renders the collection-type icon and never asks for the
// picture, which is what a fresh deployment looked like before this.
func TestLibraryImageTagPublishedOnTheLibraryItem(t *testing.T) {
	ts, st := newTestServer(t)
	admin := createTestAdminUser(t, st, "libtag-admin", "adminpass")
	token := loginToken(t, ts.URL, admin.Name, "adminpass")

	libID := createTestLibrary(t, st, "TaggedCard", storage.CollectionTypeMovies)

	// No image yet: the library reports no tag at all, so the client keeps
	// drawing its fallback icon.
	var views struct {
		Items []baseItemDto `json:"Items"`
	}
	resp := authedRequest(t, ts.URL, token, http.MethodGet, "/UserViews", "", nil)
	if err := json.NewDecoder(resp.Body).Decode(&views); err != nil {
		t.Fatalf("decode: %v", err)
	}
	resp.Body.Close()
	if len(views.Items) != 1 {
		t.Fatalf("UserViews has %d items, want 1", len(views.Items))
	}
	if tag := views.Items[0].ImageTags["Primary"]; tag != "" {
		t.Errorf("ImageTags.Primary with no image = %q, want none", tag)
	}

	if err := st.UpsertLibraryImage(context.Background(), storage.LibraryImage{
		LibraryID: libID, Kind: "Primary", Path: "/nowhere/tagged.png", Tag: "tag-tagged",
		Width: 960, Height: 540, BlurHash: "L6PZfSi_.AyE", Size: 5,
	}); err != nil {
		t.Fatalf("UpsertLibraryImage: %v", err)
	}

	views.Items = nil
	resp = authedRequest(t, ts.URL, token, http.MethodGet, "/UserViews", "", nil)
	if err := json.NewDecoder(resp.Body).Decode(&views); err != nil {
		t.Fatalf("decode: %v", err)
	}
	resp.Body.Close()
	if len(views.Items) != 1 {
		t.Fatalf("UserViews has %d items, want 1", len(views.Items))
	}
	got := views.Items[0]
	if tag := got.ImageTags["Primary"]; tag != "tag-tagged" {
		t.Fatalf("UserViews ImageTags.Primary = %q, want tag-tagged", tag)
	}
	if got.PrimaryImageAspectRatio == nil || *got.PrimaryImageAspectRatio < 1.77 || *got.PrimaryImageAspectRatio > 1.78 {
		t.Errorf("PrimaryImageAspectRatio = %v, want 960/540", got.PrimaryImageAspectRatio)
	}
	if hash := got.ImageBlurHashes["Primary"]["tag-tagged"]; hash != "L6PZfSi_.AyE" {
		t.Errorf("ImageBlurHashes = %#v, want the stored hash under its tag", got.ImageBlurHashes)
	}

	// The same library fetched as an item carries the same fields: the
	// dashboard's library page uses this route, not /UserViews.
	resp = authedRequest(t, ts.URL, token, http.MethodGet, "/Items/"+string(libID), "", nil)
	var item baseItemDto
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		t.Fatalf("decode: %v", err)
	}
	resp.Body.Close()
	if tag := item.ImageTags["Primary"]; tag != "tag-tagged" {
		t.Errorf("GET /Items/{libraryId} ImageTags.Primary = %q, want tag-tagged", tag)
	}
}

// TestUploadAndDeleteLibraryImage covers an admin replacing the generated
// picture with their own and then removing it.
func TestUploadAndDeleteLibraryImage(t *testing.T) {
	ts, st, _ := newTestServerWithImageDir(t)
	admin := createTestAdminUser(t, st, "libimg-admin-2", "adminpass")
	token := loginToken(t, ts.URL, admin.Name, "adminpass")

	libID := createTestLibrary(t, st, "UploadedCard", storage.CollectionTypeMovies)

	up := authedRequest(t, ts.URL, token, http.MethodPost, "/Items/"+string(libID)+"/Images/Primary", "image/png", testPNGBase64(t))
	up.Body.Close()
	if up.StatusCode != http.StatusNoContent {
		t.Fatalf("upload status = %d, want 204", up.StatusCode)
	}
	stored, ok, err := st.GetLibraryImage(context.Background(), libID, "Primary")
	if err != nil || !ok {
		t.Fatalf("GetLibraryImage after upload = (%v, %v), want the uploaded image", ok, err)
	}
	if stored.Width != 1 || stored.Height != 1 || stored.Tag == "" {
		t.Errorf("stored image = %#v, want the uploaded 1x1 PNG with a tag", stored)
	}
	if _, err := os.Stat(stored.Path); err != nil {
		t.Fatalf("uploaded file missing at %q: %v", stored.Path, err)
	}

	del := authedRequest(t, ts.URL, token, http.MethodDelete, "/Items/"+string(libID)+"/Images/Primary", "", nil)
	del.Body.Close()
	if del.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", del.StatusCode)
	}
	if _, ok, _ := st.GetLibraryImage(context.Background(), libID, "Primary"); ok {
		t.Error("the image is still stored after the delete")
	}
	if _, err := os.Stat(stored.Path); !os.IsNotExist(err) {
		t.Errorf("the file at %q survived the delete (err = %v)", stored.Path, err)
	}

	// Deleting again is idempotent, like an item's own image delete.
	again := authedRequest(t, ts.URL, token, http.MethodDelete, "/Items/"+string(libID)+"/Images/Primary", "", nil)
	again.Body.Close()
	if again.StatusCode != http.StatusNoContent {
		t.Errorf("second delete status = %d, want 204", again.StatusCode)
	}
}
