package transcode

import (
	"html"
	"os"
	"path/filepath"
)

// systemFontDirs are the places a host keeps fonts, in the order a fallback
// font is looked for when no fonts directory is configured.
var systemFontDirs = []string{
	"/usr/share/fonts/truetype/dejavu",
	"/usr/share/fonts/dejavu",
	"/usr/share/fonts/TTF",
	"/usr/share/fonts/truetype",
	"/usr/share/fonts",
	"/usr/local/share/fonts",
}

// defaultFontsDir is the first directory of systemFontDirs that holds fonts, ""
// when the host has none.
func defaultFontsDir() string {
	for _, d := range systemFontDirs {
		if entries, err := os.ReadDir(d); err == nil && len(entries) > 0 {
			return d
		}
	}
	return ""
}

// fontconfigAvailable tells whether fontconfig has a configuration to read:
// libass starts no font provider at all without one, and the text of a burn-in
// is not drawn ("Failed to load fontconfig fonts"), whatever fontsdir says.
func fontconfigAvailable() bool {
	if f := os.Getenv("FONTCONFIG_FILE"); f != "" {
		_, err := os.Stat(f)
		return err == nil
	}
	if p := os.Getenv("FONTCONFIG_PATH"); p != "" {
		if _, err := os.Stat(filepath.Join(p, "fonts.conf")); err == nil {
			return true
		}
	}
	_, err := os.Stat("/etc/fonts/fonts.conf")
	return err == nil
}

// fontEnv is the environment ffmpeg needs to draw a burned-in text: when the
// host has no fontconfig configuration a minimal one is written to dir, naming
// the fonts directory and making DejaVu Sans the sans-serif face, which is what
// the reference's fontconfig answers for a font that is not installed. nil
// leaves the environment as it is.
func fontEnv(dir string, fontsDirs ...string) []string {
	if fontconfigAvailable() {
		return nil
	}
	conf := `<?xml version="1.0"?>
<!DOCTYPE fontconfig SYSTEM "fonts.dtd">
<fontconfig>
`
	for _, d := range fontsDirs {
		if d != "" {
			conf += "  <dir>" + html.EscapeString(d) + "</dir>\n"
		}
	}
	conf += `  <cachedir>` + html.EscapeString(filepath.Join(dir, "fontconfig-cache")) + `</cachedir>
  <alias><family>sans-serif</family><prefer><family>DejaVu Sans</family></prefer></alias>
</fontconfig>
`
	path := filepath.Join(dir, "fonts.conf")
	if os.WriteFile(path, []byte(conf), 0o644) != nil {
		return nil
	}
	return append(os.Environ(), "FONTCONFIG_FILE="+path)
}
