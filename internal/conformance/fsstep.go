package conformance

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// fsPath resolves a media-root-relative path of an fs step, refusing anything
// that would leave the media root.
func (c *Client) fsPath(rel string, scratch map[string]string) (string, error) {
	rel, err := c.expand(rel, scratch)
	if err != nil {
		return "", err
	}
	if rel == "" || filepath.IsAbs(rel) {
		return "", fmt.Errorf("fs step path %q must be relative to the media root", rel)
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == "." {
		return "", fmt.Errorf("fs step path %q leaves the media root", rel)
	}
	return filepath.Join(c.MediaLocalRoot, clean), nil
}

// applyFS runs one change of the media tree. Without a MediaLocalRoot the
// step is BLOCKED (the runner does not see the files the server scans).
func (c *Client) applyFS(fs *FSStep, scratch map[string]string) error {
	if c.MediaLocalRoot == "" {
		return &blockedError{"no local media root configured (-media-local-root)"}
	}
	switch {
	case fs.Write != "":
		p, err := c.fsPath(fs.Write, scratch)
		if err != nil {
			return err
		}
		content, err := c.expand(fs.Content, scratch)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		return os.WriteFile(p, []byte(content), 0o644)
	case fs.Mkdir != "":
		p, err := c.fsPath(fs.Mkdir, scratch)
		if err != nil {
			return err
		}
		return os.MkdirAll(p, 0o755)
	case fs.Remove != "":
		p, err := c.fsPath(fs.Remove, scratch)
		if err != nil {
			return err
		}
		return os.RemoveAll(p)
	case fs.Rename != "":
		from, err := c.fsPath(fs.Rename, scratch)
		if err != nil {
			return err
		}
		to, err := c.fsPath(fs.To, scratch)
		if err != nil {
			return err
		}
		if _, err := os.Stat(from); err != nil && fs.IfExists {
			return nil
		}
		if fs.Replace {
			if err := os.RemoveAll(to); err != nil {
				return err
			}
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		return os.Rename(from, to)
	case fs.Copy != "":
		from, err := c.fsPath(fs.Copy, scratch)
		if err != nil {
			return err
		}
		to, err := c.fsPath(fs.To, scratch)
		if err != nil {
			return err
		}
		in, err := os.Open(from)
		if err != nil {
			return err
		}
		defer in.Close()
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		out, err := os.Create(to)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	}
	return fmt.Errorf("fs step names no operation")
}
