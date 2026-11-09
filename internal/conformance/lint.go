package conformance

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Clean-room lint. The contract under docs/contract/ must describe observable
// behavior only. It must never carry the reference server's own expression of
// that behavior: class/file/function names, source paths, line references.
// This mirrors AGENTS.md's clean-room rule and runs in `go test`.
var forbidden = []struct {
	name string
	re   *regexp.Regexp
}{
	{"C# source file reference", regexp.MustCompile(`\b[\w./-]+\.cs\b`)},
	{"reference-server namespace", regexp.MustCompile(`\b(Jellyfin\.(Api|Server|Data|Model|Common|Controller)|Emby\.Server|MediaBrowser\.(Controller|Model|Common|Providers|MediaEncoding|LocalMetadata|XbmcMetadata))\b`)},
	{"reference-server class name", regexp.MustCompile(`\b[A-Z][A-Za-z]{2,}(Controller|Manager|Helper|Handler|Repository|Middleware|Dto|Provider|Extensions|Converter|Binder)\b`)},
	{"interface naming of the reference server", regexp.MustCompile(`\bI[A-Z][a-z]+(Manager|Service|Repository|Provider|Handler)\b`)},
	{"source line reference", regexp.MustCompile(`(?i)\b(line|lines|L)\s?\d{2,}\b|\.cs:\d+`)},
	{"method-style reference", regexp.MustCompile(`\b[A-Z][A-Za-z]+\.[A-Z][A-Za-z]+Async\b`)},
}

// allowedWords are tokens that match a pattern but are public wire-level
// vocabulary (JSON field names, route segments) and therefore fine.
var allowedWords = map[string]bool{
	"UserDto": true, // wire field family name appears in public OpenAPI; kept only if needed
}

// LintFile returns the clean-room violations found in one file.
func LintFile(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []string
	for i, line := range strings.Split(string(b), "\n") {
		for _, f := range forbidden {
			for _, m := range f.re.FindAllString(line, -1) {
				if allowedWords[m] {
					continue
				}
				out = append(out, fmt.Sprintf("%s:%d: %s: %q", path, i+1, f.name, m))
			}
		}
	}
	return out, nil
}

// LintDir lints every .md and .json file under dir.
func LintDir(dir string) ([]string, error) {
	var all []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if ext := filepath.Ext(p); ext != ".md" && ext != ".json" {
			return nil
		}
		v, err := LintFile(p)
		if err != nil {
			return err
		}
		all = append(all, v...)
		return nil
	})
	return all, err
}

// CheckDivergenceRegistry verifies that every scenario carrying a `divergence`
// record — a behavior where the reference server is defective and the scenario
// asserts the correct one instead — is listed in docs/13-divergences.md.
//
// That file is the project's one index of deliberate differences (AGENTS.md:
// "a change that introduces, changes or removes a divergence updates it in the
// same commit"). The scenarios are where the difference is actually enforced,
// so checking one against the other is what stops a new divergence from
// landing with only its `divergence` record and no entry in the index: the
// record is invisible unless someone already knows the scenario exists.
func CheckDivergenceRegistry(scenarioDir, registryPath string) ([]string, error) {
	registry, err := os.ReadFile(registryPath)
	if err != nil {
		return nil, err
	}
	listed := string(registry)

	scenarios, err := LoadDir(scenarioDir)
	if err != nil {
		return nil, err
	}

	var missing []string
	for _, sc := range scenarios {
		if sc.Divergence == nil {
			continue
		}
		if !strings.Contains(listed, "`"+sc.ID+"`") {
			missing = append(missing, fmt.Sprintf("scenario %q carries a divergence record but %s does not list it", sc.ID, registryPath))
		}
	}
	return missing, nil
}
