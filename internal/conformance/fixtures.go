package conformance

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// Fixtures is the declarative state every scenario can rely on. It is applied
// through the server's public API only, so the same file seeds a reference
// server and Tinyfin identically.
type Fixtures struct {
	Users     []FixtureUser    `json:"users"`
	Libraries []FixtureLibrary `json:"libraries"`
	Items     []FixtureItem    `json:"items"`
}

// FixtureUser is a non-admin account. Policy keys are merged into the
// user's policy via the public policy endpoint.
type FixtureUser struct {
	Key      string         `json:"key"`
	Name     string         `json:"name"`
	Password string         `json:"password"`
	Policy   map[string]any `json:"policy,omitempty"`
}

// FixtureLibrary maps a library to a sub-directory of the media root.
type FixtureLibrary struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Type string `json:"type"` // movies | tvshows | music
	Path string `json:"path"` // relative to the media root
}

// FixtureItem resolves a library item by type and path (or name) after the
// scan. Names are what the scanning server derived from file names, which
// differ between servers; paths are the stable key.
type FixtureItem struct {
	Key         string `json:"key"`
	Type        string `json:"type"` // Movie | Series | Season | Episode | Audio ...
	Name        string `json:"name,omitempty"`
	Path        string `json:"path,omitempty"` // relative to the media root; preferred over Name
	IndexNumber *int   `json:"indexNumber,omitempty"`
	ParentIndex *int   `json:"parentIndexNumber,omitempty"`
}

// LoadFixtures reads a fixtures file.
func LoadFixtures(path string) (Fixtures, error) {
	var f Fixtures
	b, err := os.ReadFile(path)
	if err != nil {
		return f, err
	}
	return f, json.Unmarshal(b, &f)
}

// Setup applies fixtures through the public API and fills c.Vars, c.Tokens and
// c.Available. Failures to create an individual fixture are collected in the
// returned warnings (scenarios that need it become BLOCKED) rather than
// aborting the whole run. Only an admin login failure is fatal.
func (c *Client) Setup(ctx context.Context, adminUser, adminPass, mediaRoot string, f Fixtures) (warnings []string, err error) {
	warn := func(format string, a ...any) { warnings = append(warnings, fmt.Sprintf(format, a...)) }

	tok, uid, err := c.login(ctx, "admin", adminUser, adminPass)
	if err != nil {
		return nil, fmt.Errorf("admin login: %w", err)
	}
	c.Tokens["admin"] = tok
	c.Vars["user.admin.id"] = uid
	c.Vars["user.admin.name"] = adminUser
	c.Vars["user.admin.password"] = adminPass
	c.Available["user:admin"] = true
	c.Vars["server.mediaroot"] = mediaRoot
	c.Vars["device.admin"] = "conf-admin" + c.DeviceSuffix
	if err := c.SetupAPIKey(ctx); err != nil {
		warn("api key identity: %v", err)
	}

	// users
	existing := map[string]string{}
	if r, err := c.Do(ctx, "admin", "GET", "/Users", nil, nil, nil); err == nil && r.Status == 200 {
		if arr, ok := r.JSON.([]any); ok {
			for _, u := range arr {
				if m, ok := u.(map[string]any); ok {
					existing[strings.ToLower(fmt.Sprint(m["Name"]))], _ = m["Id"].(string)
				}
			}
		}
	}
	for _, u := range f.Users {
		id := existing[strings.ToLower(u.Name)]
		if id == "" {
			r, err := c.Do(ctx, "admin", "POST", "/Users/New", nil, map[string]any{"Name": u.Name, "Password": u.Password}, nil)
			if err != nil || (r.Status != 200 && r.Status != 201) {
				warn("create user %s: %v", u.Key, statusErr(r, err))
				continue
			}
			if m, ok := r.JSON.(map[string]any); ok {
				id, _ = m["Id"].(string)
			}
		}
		if id == "" {
			warn("user %s: no id", u.Key)
			continue
		}
		if len(u.Policy) > 0 {
			r, err := c.Do(ctx, "admin", "GET", "/Users/"+id, nil, nil, nil)
			if err != nil || r.Status != 200 {
				warn("read user %s: %v", u.Key, statusErr(r, err))
				continue
			}
			pol := map[string]any{}
			if m, ok := r.JSON.(map[string]any); ok {
				if p, ok := m["Policy"].(map[string]any); ok {
					pol = p
				}
			}
			for k, v := range u.Policy {
				pol[k] = v
			}
			r, err = c.Do(ctx, "admin", "POST", "/Users/"+id+"/Policy", nil, pol, nil)
			if err != nil || (r.Status != 204 && r.Status != 200) {
				warn("set policy for %s: %v", u.Key, statusErr(r, err))
				continue
			}
		}
		if dis, _ := u.Policy["IsDisabled"].(bool); !dis {
			t, _, err := c.login(ctx, u.Key, u.Name, u.Password)
			if err != nil {
				warn("login %s: %v", u.Key, err)
				continue
			}
			c.Tokens[u.Key] = t
		}
		c.Vars["user."+u.Key+".id"] = id
		c.Vars["user."+u.Key+".name"] = u.Name
		c.Vars["user."+u.Key+".password"] = u.Password
		c.Vars["device."+u.Key] = "conf-" + u.Key + c.DeviceSuffix
		c.Available["user:"+u.Key] = true
	}

	// libraries
	have := map[string]string{}
	if r, err := c.Do(ctx, "admin", "GET", "/Library/VirtualFolders", nil, nil, nil); err == nil && r.Status == 200 {
		if arr, ok := r.JSON.([]any); ok {
			for _, v := range arr {
				if m, ok := v.(map[string]any); ok {
					have[strings.ToLower(fmt.Sprint(m["Name"]))], _ = m["ItemId"].(string)
				}
			}
		}
	}
	for _, l := range f.Libraries {
		path := strings.TrimRight(mediaRoot, "/") + "/" + l.Path
		if _, ok := have[strings.ToLower(l.Name)]; !ok {
			body := map[string]any{"LibraryOptions": map[string]any{"PathInfos": []any{map[string]any{"Path": path}}}}
			r, err := c.Do(ctx, "admin", "POST", "/Library/VirtualFolders",
				map[string]string{"name": l.Name, "collectionType": l.Type, "refreshLibrary": "true"}, body, nil)
			if err != nil || (r.Status != 204 && r.Status != 200) {
				warn("create library %s: %v", l.Key, statusErr(r, err))
				continue
			}
		}
		c.Available["library:"+l.Key] = true
		c.Vars["library."+l.Key+".name"] = l.Name
	}
	_, _ = c.Do(ctx, "admin", "POST", "/Library/Refresh", nil, nil, nil)

	// items (poll while the scan runs)
	deadline := time.Now().Add(90 * time.Second)
	pending := append([]FixtureItem(nil), f.Items...)
	for len(pending) > 0 && time.Now().Before(deadline) {
		var still []FixtureItem
		for _, it := range pending {
			id, name := c.findItem(ctx, it, mediaRoot)
			if id == "" {
				still = append(still, it)
				continue
			}
			c.Vars["item."+it.Key+".id"] = id
			c.Vars["item."+it.Key+".name"] = name
			c.Available["item:"+it.Key] = true
		}
		pending = still
		if len(pending) > 0 {
			time.Sleep(2 * time.Second)
		}
	}
	for _, it := range pending {
		warn("item %s (%s %q%s) not found after scan", it.Key, it.Type, it.Name, it.Path)
	}
	return warnings, nil
}

func statusErr(r *Response, err error) string {
	if err != nil {
		return err.Error()
	}
	if r != nil {
		return fmt.Sprintf("status %d", r.Status)
	}
	return "unknown"
}

func (c *Client) login(ctx context.Context, key, user, pass string) (token, userID string, err error) {
	req := Request{Method: "POST", Path: "/Users/AuthenticateByName", Body: map[string]any{"Username": user, "Pw": pass},
		Headers: map[string]string{"Authorization": authHeader("conf-"+key, "")}}
	r, err := c.do(ctx, "anonymous", req, nil)
	if err != nil {
		return "", "", err
	}
	if r.Status != 200 {
		return "", "", fmt.Errorf("status %d", r.Status)
	}
	m, _ := r.JSON.(map[string]any)
	token, _ = m["AccessToken"].(string)
	if u, ok := m["User"].(map[string]any); ok {
		userID, _ = u["Id"].(string)
	}
	if token == "" {
		return "", "", fmt.Errorf("no AccessToken in response")
	}
	return token, userID, nil
}

func (c *Client) findItem(ctx context.Context, it FixtureItem, mediaRoot string) (id, name string) {
	r, err := c.Do(ctx, "admin", "GET", "/Items",
		map[string]string{"Recursive": "true", "IncludeItemTypes": it.Type, "Fields": "Path", "userId": c.Vars["user.admin.id"]}, nil, nil)
	if err != nil || r.Status != 200 {
		return "", ""
	}
	wantPath := ""
	if it.Path != "" {
		wantPath = strings.TrimRight(mediaRoot, "/") + "/" + it.Path
	}
	m, _ := r.JSON.(map[string]any)
	arr, _ := m["Items"].([]any)
	for _, x := range arr {
		o, _ := x.(map[string]any)
		if wantPath != "" {
			if fmt.Sprint(o["Path"]) != wantPath {
				continue
			}
		} else if !strings.EqualFold(fmt.Sprint(o["Name"]), it.Name) {
			continue
		}
		if it.IndexNumber != nil && toInt(o["IndexNumber"]) != *it.IndexNumber {
			continue
		}
		if it.ParentIndex != nil && toInt(o["ParentIndexNumber"]) != *it.ParentIndex {
			continue
		}
		if id, ok := o["Id"].(string); ok {
			return id, fmt.Sprint(o["Name"])
		}
	}
	return "", ""
}

func toInt(v any) int {
	f, _ := toFloat(v)
	return int(f)
}

// SetupAPIKey creates a run-specific API key and registers it as the "apikey"
// identity (a credential that belongs to no user). The key's app name carries
// the run's device suffix and a random part so concurrent runs find their own
// key regardless of the order of the listing.
func (c *Client) SetupAPIKey(ctx context.Context) error {
	var rnd [6]byte
	_, _ = rand.Read(rnd[:])
	app := "conformance-" + hex.EncodeToString(rnd[:])
	r, err := c.Do(ctx, "admin", "POST", "/Auth/Keys", map[string]string{"app": app}, nil, nil)
	if err != nil || (r.Status != 204 && r.Status != 200) {
		return fmt.Errorf("create key: %s", statusErr(r, err))
	}
	r, err = c.Do(ctx, "admin", "GET", "/Auth/Keys", nil, nil, nil)
	if err != nil || r.Status != 200 {
		return fmt.Errorf("list keys: %s", statusErr(r, err))
	}
	m, _ := r.JSON.(map[string]any)
	items, _ := m["Items"].([]any)
	for _, it := range items {
		o, _ := it.(map[string]any)
		if o["AppName"] == app {
			if tok, _ := o["AccessToken"].(string); tok != "" {
				c.Tokens["apikey"] = tok
				c.apiKeyID = tok
				c.Available["identity:apikey"] = true
				return nil
			}
		}
	}
	return fmt.Errorf("created key %q not found in the listing", app)
}

// Teardown revokes the run's API key.
func (c *Client) Teardown(ctx context.Context) error {
	if c.apiKeyID == "" {
		return nil
	}
	r, err := c.Do(ctx, "admin", "DELETE", "/Auth/Keys/"+c.apiKeyID, nil, nil, nil)
	c.apiKeyID = ""
	delete(c.Tokens, "apikey")
	if err != nil || (r.Status != 204 && r.Status != 200) {
		return fmt.Errorf("revoke key: %s", statusErr(r, err))
	}
	return nil
}
