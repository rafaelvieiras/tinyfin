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
	// Same segment count (3, including the leading empty split element) as
	// "/System/Info" above, but no collision risk: normalizePathCase checks
	// every non-wildcard segment, and these two differ at the second
	// segment ("Info" vs. "Endpoint", both literals, no wildcard on either
	// side) — same reasoning as the FavoriteItems/PlayedItems comment
	// further down this list.
	"/System/Endpoint",
	// 4 segments (including the leading empty split element) — no length
	// collision with "/System/Info" (3) or "/System/Endpoint" (3) above.
	"/System/Info/Storage",
	// 2 segments (including the leading empty split element) — shorter than
	// every other entry in this list, no collision possible.
	"/ScheduledTasks",
	// Fully literal, no wildcard segment at all — no collision is possible
	// regardless of any other pattern in this list.
	"/System/ActivityLog/Entries",
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
	// No length collision with the several other 4-segment patterns below
	// (/Items/{itemId}/Ancestors, /Shows/{seriesId}/Seasons, /Videos/{itemId}/
	// {file}, /Sessions/Capabilities/Full, ...) despite matching segment count
	// (4, including the leading empty split element): normalizePathCase
	// checks every non-wildcard segment, and this pattern's second segment
	// ("Users", a literal) differs from every one of theirs — no wildcard
	// sits at that position on either side, so no pattern can swallow another
	// regardless of list order here. Same reasoning as the
	// FavoriteItems/PlayedItems comment below.
	"/Users/{userId}/Items",
	// Must precede /Items/{itemId} below: same reasoning as /Users/Public
	// above — normalizePathCase stops at the first same-length match, and
	// these are the same length (2 segments) as /Items/{itemId}.
	"/Items/Latest",
	"/Items/Filters",
	"/Items/Filters2",
	"/Items/Counts",
	"/Items/{itemId}",
	"/Items/{itemId}/Ancestors",
	"/Items/{itemId}/Similar",
	// Same segment count (4, including the leading empty split element) as
	// the two entries above and /Items/{itemId}/PlaybackInfo below, but no
	// collision risk: normalizePathCase checks every non-wildcard segment,
	// and this pattern's last segment ("ThemeMedia") differs from all of
	// theirs — same reasoning as the FavoriteItems/PlayedItems comment
	// further down this list.
	"/Items/{itemId}/ThemeMedia",
	"/UserItems/Resume",
	"/Shows/{seriesId}/Seasons",
	"/Shows/{seriesId}/Episodes",
	// No length collision with the two entries above (3 segments here vs.
	// 4 for those, including the leading empty split element) — see
	// server.go's registration comment for this route.
	"/Shows/NextUp",
	"/Studios",
	// 3 segments (including the leading empty split element), same count as
	// "/Playback/BitrateTest"/"/Items/Latest"/"/UserItems/Resume"/
	// "/System/Endpoint" above — no collision risk: this pattern's second
	// segment ("LiveTv", a literal) differs from all of theirs, same
	// reasoning as every other entry in this list keyed off segment count.
	"/LiveTv/Programs",
	"/Users/{userId}/Items/{itemId}",
	"/Items/{itemId}/Images/{imageType}",
	"/Items/{itemId}/Images/{imageType}/{imageIndex}",
	// Same segment count (4) as /Items/{itemId}/PlaybackInfo below and
	// /Items/{itemId}/Ancestors, /Items/{itemId}/Similar, /Items/{itemId}/
	// ThemeMedia above — no collision, same reasoning: differs from all of
	// them at the final literal segment ("Images"/"Refresh").
	"/Items/{itemId}/Images",
	"/Items/{itemId}/Refresh",
	"/Items/{itemId}/PlaybackInfo",
	// Must precede /Videos/{itemId}/{file} below: same reasoning as
	// /Items/Latest above — these are the same segment count (3) as
	// /Videos/{itemId}/{file}, and normalizePathCase stops at the first
	// same-length match.
	"/Videos/{itemId}/master.m3u8",
	"/Videos/{itemId}/main.m3u8",
	"/Videos/{itemId}/{file}",
	// 3 segments (including the leading empty split element), same count
	// as several entries above ("/Items/Latest", "/UserItems/Resume",
	// "/System/Endpoint", ...) — no collision risk with any of them:
	// they all differ from this one at the second segment ("Playback", a
	// literal, matched against another literal on every one of those — no
	// wildcard sits at that position on either side), same reasoning as
	// every other entry in this list keyed off segment count.
	"/Playback/BitrateTest",
	"/DisplayPreferences/{displayPreferencesId}",
	"/Sessions/Capabilities/Full",
	// 2 segments (including the leading empty split element), unlike every
	// other "/Sessions/..." entry here (3+) — no collision risk possible at
	// a different segment count.
	"/Sessions",
	"/Sessions/Playing",
	"/Sessions/Playing/Progress",
	// Single segment, same shape as "/Studios"/"/Users" elsewhere in this
	// list — no collision risk at that segment count.
	"/socket",
	"/Sessions/Playing/Stopped",
	"/UserPlayedItems/{itemId}",
	"/MediaSegments/{itemId}",
	// No length collision with /Users/{userId}/Items/{itemId} above despite
	// matching segment count (5, including the leading empty split
	// element): normalizePathCase checks every non-wildcard segment, and
	// these two differ from it at the third segment ("FavoriteItems"/
	// "PlayedItems" vs "Items", all three literals) — no wildcard sits at
	// that position on either side, so no pattern can swallow another
	// regardless of list order here.
	"/Users/{userId}/FavoriteItems/{itemId}",
	"/Users/{userId}/PlayedItems/{itemId}",
	"/Library/VirtualFolders",
	// Fully literal, no wildcard segment at all — no collision is possible
	// regardless of any other pattern in this list.
	"/Library/VirtualFolders/LibraryOptions",
	"/Library/VirtualFolders/Name",
	"/Library/Refresh",
	"/Environment/DefaultDirectoryBrowser",
	"/Environment/Drives",
	"/Environment/DirectoryContents",
	"/Environment/ParentPath",
	"/Environment/ValidatePath",
	"/Localization/cultures",
	"/Localization/countries",
	"/Libraries/AvailableOptions",
	// 3 segments (including the leading empty split element), same count as
	// "/System/Info/Public"/"/Items/Latest"/"/Playback/BitrateTest" above —
	// no collision risk: this pattern's second segment ("web", a literal)
	// differs from all of theirs, same reasoning as every other entry in
	// this list keyed off segment count. Also note: this path lives under
	// cmd/tinyfin/webui.go's static-file-serving prefix when a web bundle
	// is mounted — that wrapper special-cases this exact literal path
	// before falling through to the file server, so case normalization
	// here and the static-file shadow risk there are independent concerns
	// that both had to be handled.
	"/web/ConfigurationPages",
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
