package api

import "strings"

// routePaths lists every path pattern registered in routes(), method-agnostic
// (the same path can carry more than one method). Kept as a separate,
// manually-maintained list rather than derived from http.ServeMux (which
// has no public API to enumerate its registered patterns) — add a path here
// whenever routes() gains one.
var routePaths = []string{
	"/System/Info/Public",
	"/Users/AuthenticateByName",
	"/QuickConnect/Enabled",
	"/Branding/Configuration",
	"/System/Info",
	"/Users/Me",
	"/Users",
	"/Users/New",
	// Must precede /Users/{userId} below: normalizePathCase returns on the
	// first same-length match, so the literal has to be checked before the
	// wildcard would swallow it — otherwise a lowercase "/users/public"
	// request normalizes to "/Users/public" (preserving the wildcard
	// segment's original casing) instead of the registered "/Users/Public",
	// and 401s against requireAuth instead of reaching handleUsersPublic.
	"/Users/Public",
	"/Users/{userId}",
	"/UserViews",
	"/Items",
	// Must precede /Items/{itemId} below: same reasoning as /Users/Public
	// above — normalizePathCase stops at the first same-length match, and
	// these are the same length (2 segments) as /Items/{itemId}.
	"/Items/Latest",
	"/Items/Filters",
	"/Items/Filters2",
	"/Items/{itemId}",
	"/Items/{itemId}/Ancestors",
	"/Items/{itemId}/Similar",
	"/UserItems/Resume",
	"/Studios",
	"/Users/{userId}/Items/{itemId}",
	"/Items/{itemId}/Images/{imageType}",
	"/Items/{itemId}/Images/{imageType}/{imageIndex}",
	"/Items/{itemId}/PlaybackInfo",
	// Must precede /Videos/{itemId}/{file} below: same reasoning as
	// /Items/Latest above — these are the same segment count (3) as
	// /Videos/{itemId}/{file}, and normalizePathCase stops at the first
	// same-length match.
	"/Videos/{itemId}/master.m3u8",
	"/Videos/{itemId}/main.m3u8",
	"/Videos/{itemId}/{file}",
	"/DisplayPreferences/{displayPreferencesId}",
	"/Sessions/Capabilities/Full",
	"/Sessions/Playing",
	"/Sessions/Playing/Progress",
	"/Sessions/Playing/Stopped",
	"/UserPlayedItems/{itemId}",
	"/MediaSegments/{itemId}",
}

// normalizePathCase rewrites path to the registered route's exact casing if
// it matches one case-insensitively, leaving wildcard segment values
// (item IDs, image types, ...) exactly as the client sent them. Returns
// path unchanged if nothing matches (including if it already matches
// exactly — this is a no-op on the common case).
//
// This exists because net/http.ServeMux matches path literals
// case-sensitively, but the reference server's routing (ASP.NET Core) is
// case-insensitive by default, and at least one real, currently-shipping
// client — jellyfin-web's legacy jellyfin-apiclient package, still used for
// its login flow — hardcodes a lowercase path ("Users/authenticatebyname")
// that only works against a real server because of that case-insensitivity.
// Found by testing against that actual client, not by reading the reference
// server's source (AGENTS.md's clean-room requirement) — ASP.NET Core's
// case-insensitive routing is public, well-known framework behavior, not
// something specific to Jellyfin's own code.
func normalizePathCase(path string) string {
	reqSegs := strings.Split(path, "/")
	for _, pattern := range routePaths {
		patSegs := strings.Split(pattern, "/")
		if len(patSegs) != len(reqSegs) {
			continue
		}
		match := true
		for i, p := range patSegs {
			if strings.HasPrefix(p, "{") {
				continue // wildcard: matches any value, case preserved
			}
			if !strings.EqualFold(p, reqSegs[i]) {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		out := make([]string, len(patSegs))
		for i, p := range patSegs {
			if strings.HasPrefix(p, "{") {
				out[i] = reqSegs[i]
			} else {
				out[i] = p
			}
		}
		return strings.Join(out, "/")
	}
	return path
}
