package api

import "testing"

func TestNormalizePathCase(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/Users/authenticatebyname", "/Users/AuthenticateByName"},
		{"/USERS/AUTHENTICATEBYNAME", "/Users/AuthenticateByName"},
		{"/Users/AuthenticateByName", "/Users/AuthenticateByName"},
		{"/items/item-abc-123", "/Items/item-abc-123"},
		{"/ITEMS/item-ABC-123/images/PRIMARY", "/Items/item-ABC-123/Images/PRIMARY"},
		{"/nonexistent/path", "/nonexistent/path"},
		// Regression: the literal /Users/Public route must win over the
		// /Users/{userId} wildcard, not be swallowed by it.
		{"/users/public", "/Users/Public"},
		{"/quickconnect/enabled", "/QuickConnect/Enabled"},
		{"/branding/configuration", "/Branding/Configuration"},
		{"/sessions/capabilities/full", "/Sessions/Capabilities/Full"},
	}
	for _, c := range cases {
		if got := normalizePathCase(c.in); got != c.want {
			t.Errorf("normalizePathCase(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
