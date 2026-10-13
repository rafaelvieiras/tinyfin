package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// serverFileAvailable reports whether serverFile steps can work at all.
func (c *Client) serverFileAvailable() bool {
	return c.ServerDataDir != "" || c.ReadFileCmd != ""
}

// readServerFile resolves and reads a file of the server's filesystem. The
// answer is shaped like a Response so a step's expect/capture apply: 200 with
// the content, 404 when the file does not exist. The runner never writes to
// the server's files.
func (c *Client) readServerFile(ctx context.Context, sf *ServerFileStep, scratch map[string]string) (*Response, error) {
	if !c.serverFileAvailable() {
		return nil, &blockedError{"no server data directory configured (-server-data-dir / -read-file-cmd)"}
	}
	name, err := c.expand(sf.Read, scratch)
	if err != nil {
		return nil, err
	}
	if name == "" || strings.HasPrefix(name, "-") {
		return nil, fmt.Errorf("refusing server file path %q", name)
	}
	path := name
	if !filepath.IsAbs(name) {
		if strings.ContainsAny(name, `/\\`) || name == ".." || name == "." {
			return nil, fmt.Errorf("server file name %q must be bare or an absolute path", name)
		}
		if c.ServerDataDir == "" {
			return nil, &blockedError{"relative server file name but no -server-data-dir"}
		}
		path = filepath.Join(c.ServerDataDir, sf.Under, name)
	}
	var data []byte
	if c.ReadFileCmd != "" {
		argv := append(strings.Fields(c.ReadFileCmd), path)
		var stderr bytes.Buffer
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			// A reader command cannot tell "missing" from other failures
			// portably; any non-zero exit reads as "no such file".
			return &Response{Status: http.StatusNotFound}, nil
		}
		data = out
	} else {
		data, err = os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			return &Response{Status: http.StatusNotFound}, nil
		}
		if err != nil {
			return nil, err
		}
	}
	out := &Response{Status: http.StatusOK, Body: data, Headers: http.Header{}}
	if len(bytes.TrimSpace(data)) > 0 {
		var j any
		if json.Unmarshal(data, &j) == nil {
			out.JSON = j
		}
	}
	return out, nil
}

// probeOrigin records whether the server sees the runner at a non-loopback
// address. Both the reference and Tinyfin write "IP address: <peer>" into the
// activity log entries of a login, so the runner's own admin login tells it.
// A non-loopback peer can be made "remote" by configuring the local network
// (the network configuration section), which loopback never can.
func (c *Client) probeOrigin(ctx context.Context) {
	r, err := c.Do(ctx, "admin", "GET", "/System/ActivityLog/Entries", map[string]string{"limit": "10"}, nil, nil)
	if err != nil || r.Status != 200 {
		return
	}
	m, _ := r.JSON.(map[string]any)
	items, _ := m["Items"].([]any)
	for _, it := range items {
		e, _ := it.(map[string]any)
		s, _ := e["ShortOverview"].(string)
		addr, ok := strings.CutPrefix(s, "IP address: ")
		if !ok {
			continue
		}
		ip := net.ParseIP(strings.TrimPrefix(strings.TrimSpace(addr), "::ffff:"))
		if ip == nil {
			continue
		}
		c.Vars["run.origin"] = ip.String()
		if !ip.IsLoopback() {
			c.Available["origin:non-loopback"] = true
		}
		return
	}
}
