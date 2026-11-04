package api

import "strings"

// canonicalDeviceID returns the device id a client meant, without a legacy
// per-user suffix.
//
// Some official clients (the Android app, confirmed against the live
// reference server and against this server) append the id of the account
// that was signed in to their own DeviceId: the Android install whose
// DeviceId is "573c553d8a24e29f" also sends
// "573c553d8a24e29f18de42d3-c403-f617-9465-9f94fe1a3022" — the same install,
// with the account id glued on, hyphenated as the UUID it is. The client
// picks that spelling up after a sign-in and keeps using it for some calls
// while still sending the bare id for others, so one physical device arrives
// as two DeviceIds and lands in the store twice: two entries in GET /Devices,
// two sessions in GET /Sessions, and a re-login that appears to duplicate a
// device that was already there.
//
// Stripping the caller's own account id when it is the id's suffix collapses
// those two spellings into the one device they are. Only the caller's own id
// is stripped: a suffix that names some other account is a genuine device id
// and is left alone (verified on the live reference server, which keeps
// "573c553d8a24e29f18d15755-467d-e6c4-39fa-c8bd50274d6e", a suffix that
// belongs to no account). An id that is nothing but the account id is kept
// as it is — that is the whole id, not a suffix.
//
// This is a deliberate normalization, not the reference's behavior: the
// reference does store both spellings as two devices. Tinyfin keeps one, so a
// client cannot accumulate a phantom entry per sign-in (devices.R11).
func canonicalDeviceID(deviceID, userID string) string {
	if deviceID == "" || userID == "" {
		return deviceID
	}
	for _, suffix := range deviceIDSuffixes(userID) {
		if len(deviceID) > len(suffix) && strings.EqualFold(deviceID[len(deviceID)-len(suffix):], suffix) {
			return deviceID[:len(deviceID)-len(suffix)]
		}
	}
	return deviceID
}

// deviceIDSuffixes lists the spellings of a user id a client may have glued
// onto its DeviceId: the hyphenated UUID form clients use on the wire, and the
// bare 32-character form this server mints.
func deviceIDSuffixes(userID string) []string {
	bare := strings.ReplaceAll(userID, "-", "")
	if len(bare) != 32 {
		return []string{userID}
	}
	hyphenated := bare[:8] + "-" + bare[8:12] + "-" + bare[12:16] + "-" + bare[16:20] + "-" + bare[20:]
	if hyphenated == userID {
		return []string{userID}
	}
	return []string{userID, hyphenated}
}
