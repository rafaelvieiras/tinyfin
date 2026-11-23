package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
	"github.com/rafaelvieiras/tinyfin/internal/storage/sqlite"
)

// clientLogEnv serves a server whose data directory is a temp dir, and returns
// the base URL, that directory and an admin token.
func clientLogEnv(t *testing.T) (base, dataDir, token string, st storage.Store) {
	t.Helper()
	st, err := sqlite.New(filepath.Join(t.TempDir(), "tinyfin.db"))
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	dataDir = t.TempDir()
	srv := startTestServer(t, st, Config{ServerName: "TestServer", DataDir: dataDir})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	createTestAdminUser(t, st, "root", "rootpass")
	return ts.URL, dataDir, loginToken(t, ts.URL, "root", "rootpass"), st
}

// uploadLogClient posts a client log the way a crashed client does.
func uploadLogClient(t *testing.T, base, token, client, version, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/ClientLog/Document", strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "text/plain")
	tok := `Token="` + token + `"`
	if client != "" {
		tok += `, Client="` + client + `"`
	}
	if version != "" {
		tok += `, Version="` + version + `"`
	}
	req.Header.Set("Authorization", "MediaBrowser "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	return resp
}

// TestClientLogDocumentIsStored: the document reaches the disk verbatim, under
// a name that says who uploaded it and ends in .log, and the answer names the
// file — that name is what the dashboard's log list shows, so a client that
// crashes leaves something a person can open.
func TestClientLogDocumentIsStored(t *testing.T) {
	base, dataDir, token, _ := clientLogEnv(t)
	const document = "2026-11-23 03:08:21 [info] player started\n2026-11-23 03:09:02 [fatal] NullPointerException at PlayerFragment.kt:118\n"

	resp := uploadLogClient(t, base, token, "Android TV", "0.17.0", document)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct{ FileName string }
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(out.FileName, "upload_") || !strings.HasSuffix(out.FileName, ".log") {
		t.Errorf("FileName = %q, want upload_*.log", out.FileName)
	}
	// "Android TV" carries a space, which a file name cannot keep.
	if !strings.Contains(out.FileName, "Android_TV") {
		t.Errorf("FileName = %q, want the client in it (Android_TV)", out.FileName)
	}
	if !strings.Contains(out.FileName, "0.17.0") {
		t.Errorf("FileName = %q, want the version in it", out.FileName)
	}

	stored, err := os.ReadFile(filepath.Join(dataDir, "log", out.FileName))
	if err != nil {
		t.Fatalf("read stored document: %v", err)
	}
	if string(stored) != document {
		t.Errorf("stored %q, want the document verbatim", stored)
	}
}

// TestClientLogDocumentWithAnAPIKey: a key belongs to no user and carries no
// client name — the reference stores such an upload under "apikey" for both
// parts, and so does this server.
func TestClientLogDocumentWithAnAPIKey(t *testing.T) {
	base, dataDir, _, st := clientLogEnv(t)
	minted, err := st.CreateAPIKey(context.Background(), "probe")
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	key := minted.Token

	req, err := http.NewRequest(http.MethodPost, base+"/ClientLog/Document", strings.NewReader("anon-key crash\n"))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Authorization", `MediaBrowser Token="`+key+`"`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	var out struct{ FileName string }
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode (status %d): %v", resp.StatusCode, err)
	}
	if !strings.HasPrefix(out.FileName, "upload_apikey_apikey_") {
		t.Errorf("FileName = %q, want upload_apikey_apikey_…", out.FileName)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "log", out.FileName)); err != nil {
		t.Errorf("stored file: %v", err)
	}
}

// TestClientLogDocumentLimit: 1,000,000 bytes are taken and one more is
// refused, with the reference's own message and status (measured on it: the
// same boundary, a 413, and the message as a JSON string).
func TestClientLogDocumentLimit(t *testing.T) {
	base, _, token, _ := clientLogEnv(t)

	ok := uploadLogClient(t, base, token, "probe", "1.0", strings.Repeat("x", clientLogBodyLimit))
	defer ok.Body.Close()
	if ok.StatusCode != http.StatusOK {
		t.Errorf("at the limit: status = %d, want 200", ok.StatusCode)
	}

	over := uploadLogClient(t, base, token, "probe", "1.0", strings.Repeat("x", clientLogBodyLimit+1))
	defer over.Body.Close()
	if over.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("over the limit: status = %d, want 413", over.StatusCode)
	}
	body, _ := io.ReadAll(over.Body)
	var msg string
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatalf("refusal body %q is not a JSON string: %v", body, err)
	}
	if msg != clientLogTooLarge {
		t.Errorf("refusal = %q, want %q", msg, clientLogTooLarge)
	}
}

// TestClientLogDocumentNeedsAToken: anonymous uploads are refused, so the log
// directory cannot be written by anyone who can reach the port.
func TestClientLogDocumentNeedsAToken(t *testing.T) {
	base, dataDir, _, _ := clientLogEnv(t)
	resp, err := http.Post(base+"/ClientLog/Document", "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	if entries, _ := os.ReadDir(filepath.Join(dataDir, "log")); len(entries) != 0 {
		t.Errorf("%d files written by an anonymous request", len(entries))
	}
}

// TestClientLogNameCannotLeaveTheDirectory: the client picks the name that goes
// into the file name, so a value full of path syntax must not escape the log
// directory or hide the file from the list.
func TestClientLogNameCannotLeaveTheDirectory(t *testing.T) {
	for _, in := range []string{"../../etc/passwd", "..", ".hidden", "a/b\\c", "名前", strings.Repeat("z", 100)} {
		got := clientLogNamePart(in)
		if strings.ContainsAny(got, "/\\") {
			t.Errorf("clientLogNamePart(%q) = %q, still contains a separator", in, got)
		}
		if strings.HasPrefix(got, ".") {
			t.Errorf("clientLogNamePart(%q) = %q, starts with a dot (invisible in the list)", in, got)
		}
		if len(got) > 40 {
			t.Errorf("clientLogNamePart(%q) is %d characters, want at most 40", in, len(got))
		}
	}
}

// TestClientLogKeepsTheNewest: the reference never deletes an upload, which
// lets an authenticated client fill the data directory a megabyte at a time.
// This server keeps the newest clientLogKeep documents and deletes no server
// log while doing it.
func TestClientLogKeepsTheNewest(t *testing.T) {
	base, dataDir, token, _ := clientLogEnv(t)
	logDir := filepath.Join(dataDir, "log")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	serverLog := filepath.Join(logDir, "log_20261123.log")
	if err := os.WriteFile(serverLog, []byte("server line\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	for i := 0; i < clientLogKeep+3; i++ {
		resp := uploadLogClient(t, base, token, "probe", "1.0", "crash\n")
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("upload %d: status = %d", i, resp.StatusCode)
		}
	}

	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	uploads := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "upload_") {
			uploads++
		}
	}
	if uploads != clientLogKeep {
		t.Errorf("%d uploads kept, want %d", uploads, clientLogKeep)
	}
	if _, err := os.Stat(serverLog); err != nil {
		t.Errorf("the server's own log was deleted: %v", err)
	}
}

// TestClientLogAppearsInSystemLogs: an uploaded crash report is listed by
// GET /System/Logs, which is how a person finds it in the dashboard.
func TestClientLogAppearsInSystemLogs(t *testing.T) {
	base, _, token, _ := clientLogEnv(t)
	resp := uploadLogClient(t, base, token, "Android TV", "0.17.0", "crash\n")
	var uploaded struct{ FileName string }
	if err := json.NewDecoder(resp.Body).Decode(&uploaded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	resp.Body.Close()

	req, _ := http.NewRequest(http.MethodGet, base+"/System/Logs", nil)
	req.Header.Set("Authorization", `MediaBrowser Token="`+token+`"`)
	logs, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /System/Logs: %v", err)
	}
	defer logs.Body.Close()
	var list []struct{ Name string }
	if err := json.NewDecoder(logs.Body).Decode(&list); err != nil {
		t.Fatalf("decode logs: %v", err)
	}
	for _, f := range list {
		if f.Name == uploaded.FileName {
			return
		}
	}
	t.Errorf("uploaded document %q is not listed by GET /System/Logs", uploaded.FileName)
}
