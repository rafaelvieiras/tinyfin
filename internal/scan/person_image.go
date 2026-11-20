package scan

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// actorsFolder is the folder the media conventions keep credit photos in,
// next to the video: .actors/<Name>.jpg, or .actors/<Name>/folder.jpg.
const actorsFolder = ".actors"

// personImageExtensions are the files a credit photo can be in.
var personImageExtensions = []string{".jpg", ".jpeg", ".png", ".webp", ".gif"}

// resolvePersonImageSource decides where a credit's photo is fetched from, or
// returns "" when this library offers none.
//
// The <thumb> an NFO carries is whatever tool wrote it. Two shapes are usable:
//
//   - an http(s) URL, which the image fetcher downloads behind its guard;
//   - a file that is here and inside the item's own folder, which is read as
//     it is.
//
// A Jellyfin install writes the second as an absolute path into its own config
// directory ("/config/data/metadata/People/B/Brad Pitt/folder.jpg"). On the
// server that wrote it that path is real; anywhere else it names nothing —
// measured on production, 20,657 credits carried paths like it and exactly one
// had a photo that could be read, so every cast card on every details page
// answered 404. A thumb that points at nothing here is therefore dropped
// instead of stored, and the local conventions below get their turn.
func resolvePersonImageSource(itemPath, name, thumb string) string {
	thumb = strings.TrimSpace(thumb)
	if isWebURL(thumb) {
		return thumb
	}
	folder := itemFolder(itemPath)
	if folder == "" {
		return ""
	}
	// An absolute path that is really here, inside this item's own folder: the
	// NFO was written by a tool that kept its artwork beside the media.
	if thumb != "" && filepath.IsAbs(thumb) && insideFolder(folder, thumb) && isImageFile(thumb) {
		return thumb
	}
	if name == "" {
		return ""
	}
	// .actors/<Name>.jpg and .actors/<Name>/<anything>.jpg — the convention
	// the reference server's docs describe for a per-credit photo.
	base := filepath.Join(folder, actorsFolder)
	for _, candidate := range actorImageCandidates(base, name) {
		if isImageFile(candidate) {
			return candidate
		}
	}
	return ""
}

// actorImageCandidates lists where a credit's photo may sit under .actors,
// most specific first. The names are matched case-insensitively by the caller's
// file system, so both "Brad Pitt.jpg" and "brad pitt/folder.jpg" are found.
func actorImageCandidates(actorsDir, name string) []string {
	out := make([]string, 0, len(personImageExtensions)*2+2)
	for _, ext := range personImageExtensions {
		out = append(out, filepath.Join(actorsDir, name+ext))
	}
	personDir := filepath.Join(actorsDir, name)
	for _, name := range []string{"folder", "poster", "image"} {
		for _, ext := range personImageExtensions {
			out = append(out, filepath.Join(personDir, name+ext))
		}
	}
	return out
}

// itemFolder is the directory a credit photo is looked up in: the file's own
// directory for a movie or an episode, or the folder itself for anything whose
// path is a directory.
func itemFolder(itemPath string) string {
	if itemPath == "" {
		return ""
	}
	if fi, err := os.Stat(itemPath); err == nil && fi.IsDir() {
		return itemPath
	}
	return filepath.Dir(itemPath)
}

// insideFolder reports whether path sits inside folder. Both are made absolute
// first, so a relative library root still compares correctly.
func insideFolder(folder, path string) bool {
	absFolder, err := filepath.Abs(folder)
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absFolder, absPath)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// isImageFile reports whether path is a regular file this scanner treats as a
// picture: it must exist and carry an image extension.
func isImageFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	ok := false
	for _, want := range personImageExtensions {
		if ext == want {
			ok = true
			break
		}
	}
	if !ok {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// isWebURL reports whether a credit photo is something the image fetcher can
// download.
func isWebURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// resolvePersonImageSources applies resolvePersonImageSource to every credit of
// an item, in place. A credit whose photo cannot be located keeps no source at
// all, so the DTO stops advertising a picture that can never be served.
func resolvePersonImageSources(item *storage.Item) {
	if len(item.People) == 0 {
		return
	}
	for i := range item.People {
		item.People[i].ImageURL = resolvePersonImageSource(item.Path, item.People[i].Name, item.People[i].ImageURL)
	}
}
