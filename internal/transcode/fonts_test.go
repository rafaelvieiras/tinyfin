package transcode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFontEnv_WritesConfigWithoutFontconfig(t *testing.T) {
	t.Setenv("FONTCONFIG_FILE", filepath.Join(t.TempDir(), "missing.conf"))
	t.Setenv("FONTCONFIG_PATH", "")
	dir := t.TempDir()
	if fontconfigAvailable() && fileExists("/etc/fonts/fonts.conf") {
		t.Skip("a configuration is found through FONTCONFIG_FILE only when it exists; the host has /etc/fonts")
	}
	env := fontEnv(dir, "/a&b", "")
	var conf string
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "FONTCONFIG_FILE="); ok {
			conf = v
		}
	}
	if conf != filepath.Join(dir, "fonts.conf") {
		t.Fatalf("FONTCONFIG_FILE = %q", conf)
	}
	b, _ := os.ReadFile(conf)
	if !strings.Contains(string(b), "<dir>/a&amp;b</dir>") || !strings.Contains(string(b), "DejaVu Sans") {
		t.Fatalf("config = %s", b)
	}
}

func TestFontEnv_KeepsAnExistingConfiguration(t *testing.T) {
	f := filepath.Join(t.TempDir(), "fonts.conf")
	if err := os.WriteFile(f, []byte("<fontconfig/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FONTCONFIG_FILE", f)
	if env := fontEnv(t.TempDir(), "/x"); env != nil {
		t.Fatalf("env = %v, want the inherited one", env)
	}
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }
