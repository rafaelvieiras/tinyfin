package scan

import (
	"bytes"
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// ignore.go implements the scan-time exclusion rules (spec: scan.ignore.*):
//
//   - folders that are never cataloged by name (recycle bins, NAS metadata,
//     picture caches), in any case;
//   - hidden folders (a leading dot);
//   - `.ignore` files: an empty one (nothing but whitespace) excludes its
//     whole folder; otherwise each line is a gitignore-like pattern for the
//     entries of that folder and of every folder below it that has no
//     `.ignore` of its own (the nearest file wins, files are not merged).
//
// `.nomedia` has no effect.

// fixedIgnoredDirs are folder names (lower case) a scan never lists, whatever
// the library type. Hidden folders are covered by the leading-dot rule.
var fixedIgnoredDirs = map[string]bool{
	"@eadir": true, "#recycle": true, "@recycle": true, "$recycle.bin": true,
	"lost+found": true, "metadata": true, "extrafanart": true, "extrathumbs": true,
	"system volume information": true, "subs": true, "thumbs.db": true,
}

// fixedIgnoredDir reports whether a folder is left out by name.
func fixedIgnoredDir(name string) bool {
	return strings.HasPrefix(name, ".") || fixedIgnoredDirs[strings.ToLower(name)]
}

// ignoreRule is one parsed `.ignore` line.
type ignoreRule struct {
	pattern string // lower case, no trailing slash
	negate  bool
	dirOnly bool
}

// ignoreRules is the effective rule set of a folder.
type ignoreRules struct {
	all   bool // the folder is excluded as a whole
	rules []ignoreRule
}

// parseIgnore parses the content of a `.ignore` file.
func parseIgnore(content []byte) *ignoreRules {
	r := &ignoreRules{}
	if len(bytes.TrimSpace(bytes.TrimPrefix(content, []byte("\xef\xbb\xbf")))) == 0 {
		r.all = true
		return r
	}
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimRight(line, " \t\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var rule ignoreRule
		if strings.HasPrefix(line, "!") {
			rule.negate = true
			line = line[1:]
		}
		if strings.HasSuffix(line, "/") {
			rule.dirOnly = true
			line = strings.TrimRight(line, "/")
		}
		line = strings.TrimPrefix(line, "**/")
		if line == "" || strings.Contains(line, "/") {
			// Anchored patterns (a slash anywhere but the end) never match on
			// the reference; only the `**/` prefix makes a pattern deep, and
			// that is the default here already.
			continue
		}
		rule.pattern = strings.ToLower(line)
		r.rules = append(r.rules, rule)
	}
	return r
}

// matches reports whether name is excluded by the rules: the last matching
// rule decides, a negation re-includes.
func (r *ignoreRules) matches(name string, isDir bool) bool {
	if r == nil {
		return false
	}
	if r.all {
		return true
	}
	ignored := false
	lower := strings.ToLower(name)
	for _, rule := range r.rules {
		if rule.dirOnly && !isDir {
			continue
		}
		if globMatch(rule.pattern, lower) {
			ignored = !rule.negate
		}
	}
	return ignored
}

// globMatch matches a lower-cased gitignore name pattern: `*`, `?` and
// `[...]` classes (`[!a]` negates), backslash escapes.
func globMatch(pattern, name string) bool {
	p := strings.ReplaceAll(pattern, "[!", "[^")
	ok, err := path.Match(p, name)
	return err == nil && ok
}

// passState is the per-ScanLibrary-pass bookkeeping carried in the context:
// the `.ignore` resolution cache and the set of items the pass cataloged.
type passState struct {
	mu        sync.Mutex
	ignore    map[string]*ignoreRules
	seen      map[storage.ItemID]struct{}
	protected []string // folders that could not be read: nothing below them is forgotten
	failed    bool     // an item could not be stored: nothing is forgotten
}

type passStateKey struct{}

func passOf(ctx context.Context) *passState {
	p, _ := ctx.Value(passStateKey{}).(*passState)
	return p
}

func withPassState(ctx context.Context) context.Context {
	return context.WithValue(ctx, passStateKey{}, &passState{
		ignore: map[string]*ignoreRules{}, seen: map[storage.ItemID]struct{}{},
	})
}

// noteSeen records that the pass cataloged id.
func noteSeen(ctx context.Context, id storage.ItemID) {
	if p := passOf(ctx); p != nil {
		p.mu.Lock()
		p.seen[id] = struct{}{}
		p.mu.Unlock()
	}
}

// protect marks dir as unreadable: its items are kept by the forgetting step.
func protect(ctx context.Context, dir string) {
	if p := passOf(ctx); p != nil {
		p.mu.Lock()
		p.protected = append(p.protected, dir)
		p.mu.Unlock()
	}
}

// ignoreRulesFor resolves the rules that apply to the entries of dir: its own
// `.ignore`, else the nearest one of an ancestor (all the way up, the library
// root is not a boundary), else none.
func ignoreRulesFor(ctx context.Context, dir string) *ignoreRules {
	dir = filepath.Clean(dir)
	p := passOf(ctx)
	if p != nil {
		p.mu.Lock()
		r, ok := p.ignore[dir]
		p.mu.Unlock()
		if ok {
			return r
		}
	}
	var r *ignoreRules
	if b, err := os.ReadFile(filepath.Join(dir, ".ignore")); err == nil {
		r = parseIgnore(b)
	} else if parent := filepath.Dir(dir); parent != dir {
		r = ignoreRulesFor(ctx, parent)
	}
	if p != nil {
		p.mu.Lock()
		p.ignore[dir] = r
		p.mu.Unlock()
	}
	return r
}

// ownIgnoreAll reports whether dir holds an empty `.ignore` of its own (the
// folder is excluded as a whole).
func ownIgnoreAll(ctx context.Context, dir string) bool {
	r := ignoreRulesFor(ctx, dir)
	if r == nil || !r.all {
		return false
	}
	// The resolved rules may be an ancestor's; the folder is only excluded as
	// a whole when the file sits in the folder itself or an ancestor above.
	return true
}

// ignoredEntry reports whether the entry called name of dir is left out of a
// scan: a hidden or fixed-name folder, a name an applicable `.ignore` rule
// matches, or a folder whose own `.ignore` is empty.
func ignoredEntry(ctx context.Context, dir, name string, isDir bool) bool {
	if isDir {
		if fixedIgnoredDir(name) {
			return true
		}
		if ownIgnoreAll(ctx, filepath.Join(dir, name)) {
			return true
		}
	}
	return ignoreRulesFor(ctx, dir).matches(name, isDir)
}

// readScanDir lists dir for a scan walk: the entries ReadDir returns, minus
// the ignored ones. A directory that cannot be read is protected from the
// forgetting step (an unreadable folder is not an empty one).
func readScanDir(ctx context.Context, dir string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			protect(ctx, dir)
		}
		return nil, err
	}
	if r := ignoreRulesFor(ctx, dir); r != nil && r.all {
		return nil, nil
	}
	only, _ := ctx.Value(onlyKey{}).(onlyEntry)
	out := entries[:0:0]
	for _, e := range entries {
		if only.name != "" && filepath.Clean(dir) == only.root && e.Name() != only.name {
			continue
		}
		if ignoredEntry(ctx, dir, e.Name(), e.IsDir()) {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// upsertItem stores item and records it as cataloged by the running pass.
func (s *Scanner) upsertItem(ctx context.Context, item storage.Item) error {
	if item.Path != "" && IsStrm(item.Path) && item.RemoteURL == "" {
		item.RemoteURL = readStrmURL(item.Path)
	}
	if err := s.store.UpsertItem(ctx, item); err != nil {
		if p := passOf(ctx); p != nil {
			p.mu.Lock()
			p.failed = true
			p.mu.Unlock()
		}
		return err
	}
	noteSeen(ctx, item.ID)
	return nil
}

// onlyKey/onlyEntry narrow the listing of one folder (a library's root) to a
// single entry: a partial pass over the library walks just that part.
type onlyKey struct{}

type onlyEntry struct{ root, name string }

func withOnly(ctx context.Context, root, name string) context.Context {
	return context.WithValue(ctx, onlyKey{}, onlyEntry{root: filepath.Clean(root), name: name})
}
