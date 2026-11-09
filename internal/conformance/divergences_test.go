package conformance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRegistryScenario lays out a scenario directory plus a registry file with
// the given bodies, so the check can be exercised without the real docs tree.
func writeRegistryScenario(t *testing.T, scenarioJSON, registry string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	scenDir := filepath.Join(dir, "scenarios")
	if err := os.MkdirAll(scenDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scenDir, "a.json"), []byte(scenarioJSON), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	registryPath := filepath.Join(dir, "13-divergences.md")
	if err := os.WriteFile(registryPath, []byte(registry), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return scenDir, registryPath
}

// A scenario whose divergence record is not listed in the registry is exactly
// the case the check exists for: the difference is enforced but invisible.
func TestCheckDivergenceRegistryCatchesUnlisted(t *testing.T) {
	scenDir, registryPath := writeRegistryScenario(t, `[
	  {"id": "x.diverges", "divergence": {"reason": "the reference leaks a path"}, "steps": []},
	  {"id": "x.agrees", "steps": []}
	]`, "| Scenario (ID) | What differs |\n|---|---|\n| `x.other` | something |\n")

	missing, err := CheckDivergenceRegistry(scenDir, registryPath)
	if err != nil {
		t.Fatalf("CheckDivergenceRegistry: %v", err)
	}
	if len(missing) != 1 {
		t.Fatalf("missing = %v, want exactly the one unlisted divergence", missing)
	}
	if !strings.Contains(missing[0], "x.diverges") {
		t.Errorf("missing = %q, want it to name x.diverges", missing[0])
	}
}

func TestCheckDivergenceRegistryAcceptsListed(t *testing.T) {
	scenDir, registryPath := writeRegistryScenario(t, `[
	  {"id": "x.diverges", "divergence": {"reason": "the reference leaks a path"}, "steps": []}
	]`, "| Scenario (ID) | What differs |\n|---|---|\n| `x.diverges` | no path leak |\n")

	missing, err := CheckDivergenceRegistry(scenDir, registryPath)
	if err != nil {
		t.Fatalf("CheckDivergenceRegistry: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing = %v, want none", missing)
	}
}

// The real tree must pass: every divergence the contract asserts is in the
// registry. This is the same check `conformance lint` runs in CI, kept here so
// a contributor who only runs `go test ./...` still sees it.
func TestRepoDivergenceRegistryIsComplete(t *testing.T) {
	missing, err := CheckDivergenceRegistry(
		filepath.Join("..", "..", "docs", "contract", "scenarios"),
		filepath.Join("..", "..", "docs", "13-divergences.md"),
	)
	if err != nil {
		t.Fatalf("CheckDivergenceRegistry: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("divergences missing from docs/13-divergences.md:\n%s", strings.Join(missing, "\n"))
	}
}
