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
		// Regression: /Users/{userId}/FavoriteItems/{itemId} and
		// /Users/{userId}/PlayedItems/{itemId} share a segment count with
		// /Users/{userId}/Items/{itemId} but must not be swallowed by it —
		// their third segment is a distinguishing literal, not a wildcard,
		// on both sides.
		{"/users/abc-123/favoriteitems/item-xyz", "/Users/abc-123/FavoriteItems/item-xyz"},
		{"/users/abc-123/playeditems/item-xyz", "/Users/abc-123/PlayedItems/item-xyz"},
		// Regression: /Users/{userId}/Items (the per-user listing form the
		// Favorites screen calls) must not be swallowed by any other
		// 4-segment pattern (e.g. /Shows/{seriesId}/Seasons).
		{"/users/abc-123/items", "/Users/abc-123/Items"},
	}
	for _, c := range cases {
		if got := normalizePathCase(c.in); got != c.want {
			t.Errorf("normalizePathCase(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
