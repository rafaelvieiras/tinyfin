package api

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests call the handlers directly (not through httptest.Server +
// Server.routes) since the Environment/* routes aren't registered in
// server.go/routing.go yet — that wiring is a separate, deliberately
// out-of-scope change here (see the task's own scope note). requireAdmin
// enforcement lives entirely in that registration, not in the handlers
// themselves, so exercising the handlers directly is a faithful test of
// their own behavior. None of the handlers touch Server's fields, so a
// zero-value &Server{} is enough — no storage layer needed.

func TestDefaultDirectoryBrowser(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/Environment/DefaultDirectoryBrowser", nil)
	s.handleDefaultDirectoryBrowser(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	// Confirmed live against the reference server: a bare {}, no Path key.
	if got := w.Body.String(); got != "{}\n" {
		t.Errorf("body = %q, want %q", got, "{}\n")
	}
}

func TestEnvironmentDrives(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/Environment/Drives", nil)
	s.handleEnvironmentDrives(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var drives []fileSystemEntryInfo
	if err := json.NewDecoder(w.Body).Decode(&drives); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(drives) != 1 || drives[0].Path != "/" || drives[0].Type != "Directory" {
		t.Errorf("drives = %+v, want single root Directory entry", drives)
	}
}

func TestDirectoryContents(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s := &Server{}

	get := func(q url.Values) *httptest.ResponseRecorder {
		q.Set("path", dir)
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/Environment/DirectoryContents?"+q.Encode(), nil)
		s.handleDirectoryContents(w, r)
		return w
	}

	t.Run("neither flag set returns empty", func(t *testing.T) {
		w := get(url.Values{})
		if w.Code != 200 {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		var got []fileSystemEntryInfo
		if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
			t.Fatalf("decoding: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("len(got) = %d, want 0", len(got))
		}
	})

	t.Run("both flags set returns dir and file", func(t *testing.T) {
		w := get(url.Values{"includeDirectories": {"true"}, "includeFiles": {"true"}})
		if w.Code != 200 {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		var got []fileSystemEntryInfo
		if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
			t.Fatalf("decoding: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("len(got) = %d, want 2: %+v", len(got), got)
		}
		byName := map[string]fileSystemEntryInfo{}
		for _, e := range got {
			byName[e.Name] = e
		}
		if e, ok := byName["subdir"]; !ok || e.Type != "Directory" || e.Path != filepath.Join(dir, "subdir") {
			t.Errorf("subdir entry = %+v, ok=%v", e, ok)
		}
		if e, ok := byName["file.txt"]; !ok || e.Type != "File" || e.Path != filepath.Join(dir, "file.txt") {
			t.Errorf("file.txt entry = %+v, ok=%v", e, ok)
		}
	})

	t.Run("includeDirectories only omits files", func(t *testing.T) {
		w := get(url.Values{"includeDirectories": {"true"}})
		var got []fileSystemEntryInfo
		if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
			t.Fatalf("decoding: %v", err)
		}
		if len(got) != 1 || got[0].Type != "Directory" {
			t.Errorf("got = %+v, want single Directory entry", got)
		}
	})

	t.Run("nonexistent path 404s", func(t *testing.T) {
		w := httptest.NewRecorder()
		q := url.Values{"path": {filepath.Join(dir, "does-not-exist")}, "includeDirectories": {"true"}}
		r := httptest.NewRequest("GET", "/Environment/DirectoryContents?"+q.Encode(), nil)
		s.handleDirectoryContents(w, r)
		if w.Code != 404 {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})

	t.Run("missing path param is a bad request", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/Environment/DirectoryContents?includeDirectories=true", nil)
		s.handleDirectoryContents(w, r)
		if w.Code != 400 {
			t.Errorf("status = %d, want 400", w.Code)
		}
	})
}

func TestParentPath(t *testing.T) {
	s := &Server{}

	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		q := url.Values{"path": {path}}
		r := httptest.NewRequest("GET", "/Environment/ParentPath?"+q.Encode(), nil)
		s.handleParentPath(w, r)
		return w
	}

	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")

	t.Run("regular path returns its parent", func(t *testing.T) {
		w := get(sub)
		if w.Code != 200 {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		var got *string
		if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
			t.Fatalf("decoding: %v", err)
		}
		if got == nil || *got != dir {
			t.Errorf("parent = %v, want %q", got, dir)
		}
	})

	t.Run("filesystem root has no parent", func(t *testing.T) {
		w := get("/")
		if w.Code != 200 {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		body := w.Body.String()
		if body != "null\n" {
			t.Errorf("body = %q, want %q", body, "null\n")
		}
	})

	t.Run("missing path param is a bad request", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/Environment/ParentPath", nil)
		s.handleParentPath(w, r)
		if w.Code != 400 {
			t.Errorf("status = %d, want 400", w.Code)
		}
	})
}

func TestValidatePath(t *testing.T) {
	s := &Server{}
	dir := t.TempDir()
	file := filepath.Join(dir, "cert.pem")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/Environment/ValidatePath", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		s.handleValidatePath(w, r)
		return w
	}

	t.Run("existing directory validates", func(t *testing.T) {
		w := post(`{"Path":"` + dir + `"}`)
		if w.Code != 204 {
			t.Errorf("status = %d, want 204", w.Code)
		}
	})

	t.Run("existing file also validates", func(t *testing.T) {
		// Confirmed live against the reference server: ValidatePath accepts
		// a plain file (e.g. a TLS cert path), not directories only.
		w := post(`{"Path":"` + file + `"}`)
		if w.Code != 204 {
			t.Errorf("status = %d, want 204", w.Code)
		}
	})

	t.Run("nonexistent path 404s", func(t *testing.T) {
		w := post(`{"Path":"` + filepath.Join(dir, "does-not-exist") + `"}`)
		if w.Code != 404 {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})

	t.Run("missing Path field is a bad request", func(t *testing.T) {
		w := post(`{}`)
		if w.Code != 400 {
			t.Errorf("status = %d, want 400", w.Code)
		}
	})

	t.Run("malformed JSON is a bad request", func(t *testing.T) {
		w := post(`not json`)
		if w.Code != 400 {
			t.Errorf("status = %d, want 400", w.Code)
		}
	})
}
