package api

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

// TestContentDownloadAndDeletePolicy covers the download route and the
// delete/download permission fields (specs/policy.md).
func TestContentDownloadAndDeletePolicy(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "root", "rootpass")
	createTestUser(t, st, "plain", "plainpass")
	hash, err := bcrypt.GenerateFromPassword([]byte("nodlpass"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUser(context.Background(), storage.User{Name: "nodl", PasswordHash: string(hash),
		Policy: `{"EnableContentDownloading":false}`}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "Clip (2020).mp4")
	if err := os.WriteFile(file, []byte("0123456789abcdef"), 0o644); err != nil {
		t.Fatal(err)
	}
	libID := createTestLibrary(t, st, "DlLib", storage.CollectionTypeMovies)
	if err := st.UpsertItem(context.Background(), storage.Item{
		Type: storage.ItemTypeMovie, LibraryID: libID, Name: "Clip", Path: file, Container: "mp4",
	}); err != nil {
		t.Fatal(err)
	}
	page, err := st.QueryItems(context.Background(), storage.ItemFilter{Limit: 10})
	if err != nil || len(page.Items) == 0 {
		t.Fatalf("QueryItems: %v (%d)", err, len(page.Items))
	}
	id := string(page.Items[0].ID)

	do := func(user, pass, method, path string) (*http.Response, string) {
		tok := loginToken(t, ts.URL, user, pass)
		resp := authedClient(t, ts.URL, tok)(method, path)
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}
	if resp, body := do("plain", "plainpass", http.MethodGet, "/Items/"+id+"/Download"); resp.StatusCode != 200 || body != "0123456789abcdef" ||
		!strings.HasPrefix(resp.Header.Get("Content-Disposition"), `attachment; filename="Clip (2020).mp4"`) {
		t.Errorf("download = %d %q %q", resp.StatusCode, body, resp.Header.Get("Content-Disposition"))
	}
	if resp, _ := do("nodl", "nodlpass", http.MethodGet, "/Items/"+id+"/Download"); resp.StatusCode != 403 {
		t.Errorf("download without permission = %d, want 403", resp.StatusCode)
	}
	if resp, _ := do("plain", "plainpass", http.MethodDelete, "/Items/"+id); resp.StatusCode != 401 {
		t.Errorf("delete without permission = %d, want 401", resp.StatusCode)
	}
	if resp, _ := do("root", "rootpass", http.MethodDelete, "/Items/"+id); resp.StatusCode != 403 {
		t.Errorf("delete with permission = %d, want 403 (files are never removed)", resp.StatusCode)
	}
	if _, err := os.Stat(file); err != nil {
		t.Errorf("file removed: %v", err)
	}
}
