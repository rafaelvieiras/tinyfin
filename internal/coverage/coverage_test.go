package coverage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

// The committed tracker is valid and its generated files are current.
func TestRepositoryCoverageIsCurrent(t *testing.T) {
	root := repoRoot(t)
	m, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range Check(root, m) {
		t.Error(e)
	}
}

func TestSamePath(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"/Items/{itemId}", "/items/{id}", true},
		{"/Items/{itemId}", "/Items/Latest", false},
		{"/Users/{userId}/Items", "/Users/{x}/Items/{y}", false},
		{"/System/Info", "/system/info/", true},
	}
	for _, c := range cases {
		if got := samePath(c.a, c.b); got != c.want {
			t.Errorf("samePath(%q,%q)=%v want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestImportOpenAPI(t *testing.T) {
	doc := `{"info":{"version":"9"},"paths":{"/A/{id}":{"get":{"tags":["T"],"operationId":"G","security":[{"CustomAuthentication":["RequiresElevation"]}]},"parameters":[]},"/B":{"post":{"tags":["U"],"operationId":"P"}}}}`
	out, err := ImportOpenAPI([]byte(doc), "test")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{`"GET"`, `"RequiresElevation"`, `"Anonymous"`, `"version": "9"`} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %s:\n%s", want, s)
		}
	}
}

func TestBarHasTwentyCells(t *testing.T) {
	b := bar(counts{Supported: 1, Partial: 1, Planned: 1})
	n := 0
	for _, s := range Statuses {
		n += strings.Count(b, s.Icon())
	}
	if n != 20 {
		t.Errorf("bar has %d cells: %s", n, b)
	}
}
