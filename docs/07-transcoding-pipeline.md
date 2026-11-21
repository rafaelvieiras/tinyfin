# 07 — Transcoding pipeline

## Division of responsibility

`jellyfin-ffmpeg` does 100% of the encoding/decoding work, as an external subprocess. Tinyfin's
own code is the layer above it: deciding *whether* to invoke it, *how*, and managing the
lifecycle of the resulting output. This split is not an implementation detail — it's the whole
reason forking or reimplementing FFmpeg was correctly ruled out (decision **D2**,
`00-decision-log.md`): the hard, expensive-to-get-right part (codec correctness, hardware
acceleration interop, HDR tone mapping) is exactly what `jellyfin-ffmpeg` already solved and
actively maintains. Reimplementing it would be pure duplicated effort with no compatibility
upside.

## Playback modes, cheapest to most expensive

1. **Direct Play** — file served as-is. No server-side processing.
2. **Remux** — container changed, both streams copied untouched.
3. **Direct Stream** — video copied untouched; only audio is transcoded (e.g. the client can't
   decode the source audio codec).
4. **Transcode** — video (and typically audio) re-encoded, generally packaged as HLS.

## The decision

A client submits a device profile — codecs, containers, resolutions, and bitrates it can play
natively — as part of a `PlaybackInfo`-equivalent request (see `04` for the API-surface side of
this). The server compares that profile against the actual source file and selects the cheapest
mode from the list above that satisfies both. This is a rules engine over a known, enumerable set
of properties (container, video codec, audio codec, resolution, bitrate, HDR format) — not
something requiring machine learning or heuristics, but something requiring the rules to be
*correct*, because an incorrect decision either wastes server resources (unnecessary transcode) or
produces a stream the client can't actually play (incorrect Direct Play).

## Device profiles

A device profile describes what one real client is known to support — codecs, containers,
resolutions, bitrates. This is a distinct concept from `DeviceStore` (`05`): that store holds
durable *device records* (the `DeviceId`/token pairing a client authenticates with, `04`); a
device profile is about playback *capability*, keyed by client identity, not by one paired
device instance. Neither needs the other to exist.

Per the negotiation contract (`04`), a client normally submits its own device profile inline as
part of the `PlaybackInfo`-equivalent request — that inline profile is authoritative whenever
it's present, since it reflects exactly what that client build actually supports. Tinyfin's own
profile set exists only as a fallback for the client builds that don't send one, so it needs to
be looked up by *client identity*, not by request content: the `Client=` field already parsed out
of the `Authorization` header (`04`) is the natural key, since it's present on every authenticated
request regardless of whether a profile was submitted.

**Format (O2, resolved):** compiled into the binary as a Go value — `map[string]DeviceProfile`
keyed on that `Client` string — not a row in `05`'s storage interface. A profile is static
configuration data describing a piece of client software, not user data; it doesn't need to
survive a restart differently than the binary itself does, doesn't need runtime CRUD for v1, and
keeping it out of the storage interface avoids adding a migration path for something that changes
on a release cadence, not a per-deployment one. The `DeviceProfile` shape itself is the same
struct either way — built-in fallback entries and client-submitted profiles both decode into the
one Go type matching the wire-level `DeviceProfile` object (`04`), so there's no separate internal
format to keep in sync with the wire one. A `Client` string with no matching entry — and no inline
profile on the request — falls through to a single built-in conservative default profile (Direct
Play limited to a narrow baseline container/codec set, everything else forced through Transcode)
rather than failing the request outright.

**Initial coverage (O2, resolved):** the reference deployment's own clients — Jellyfin Web and the
official Jellyfin Android/iOS apps — get a built-in profile at launch. The reference
implementation ships dozens of these covering every client its maintainers have ever encountered,
which is ongoing maintenance burden with a long tail of low-value entries; Tinyfin instead starts
with only the clients actually in use and grows the set on demand as new clients get pointed at a
real deployment, relying on the conservative-default fallback above to keep an unrecognized client
working (just not optimally) in the meantime.

## Execution

The API/metadata role's playback-decision logic hands off to the transcode worker role (`03`)
with: source file reference, target mode, and the specific parameters that mode requires (target
container/codec/bitrate for Transcode; target audio codec alone for Direct Stream). The transcode
worker builds the actual `jellyfin-ffmpeg` command line — stream mapping, filters, hardware
acceleration flags for the vendor available on that worker (NVENC/VAAPI/QSV/AMF/V4L2, mirroring
the accelerators `jellyfin-ffmpeg` itself supports) — and manages the subprocess: starting it,
streaming output as HLS segments, and tearing it down when the client disconnects or finishes.

**Track selection and burn-in.** The selected audio stream is mapped by its index in the file
(`-map 0:N`; external subtitle files are listed first in a media source, so the file index is the
source index minus their count). A subtitle delivered by `Encode` needs a re-encoded picture and
is rendered with libass's `subtitles` filter (text, embedded via `si=` or a sidecar file) at the
source resolution before the scale, or with `overlay` (bitmap PGS/DVD/DVB). The cue times are
shifted around the filter when the job starts at an offset. libass needs fonts: the system's
through fontconfig (the container image must ship a font package), or a directory given with
`-fonts-dir` / `TINYFIN_FONTS_DIR`; the reference's fallback-font setting is not read. A job is
keyed by the item and the selection (codecs, tracks, subtitle method), so choosing another track in
a play session starts a new job and stops the one it replaces.

**Segment plan for a copied picture.** An encoded picture is cut on the `SegmentLength` grid, so its
playlist is arithmetic and needs nothing from the file. A copied picture in Matroska has to be cut where
the file already has key frames, so the playlist needs to know where they are — and the cost of finding
out is the first frame's delay. They are read from the container's own index (the `Cues` element, through
the `SeekHead` when the index is not in front of the media): the cues name the clusters, each of those
clusters is opened for the exact time of its video key frame, and nothing else of the file is touched, so
the cost follows the number of key frames and not the size of the file. The packet sweep with `ffprobe`
(a header of every packet in the file) stays as the fallback for a file without an index, and the answer
is kept in the catalog's `keyframe_data` table by item, size and modification time, so it survives a
restart and a container recreate. Measured on a 3.3 GB film with 988 key frames: 112 ms against 8.5 s
for the sweep, the same times to the millisecond; on a cold spinning array the scattered reads cost more
(2.4–14 s on twelve films, still three to six times the sweep on the large ones) and are paid once.

Because this role is stateless per request (`02`, `03`), nothing about scaling it out later
requires touching the decision logic above it — adding a second transcode worker is a deployment
change, not a code change, exactly per decision **D6**.

## Trickplay

Seek-preview thumbnails, pre-generated after the scan and served as sprite sheets — this is a
scan-time concern more than a playback-time one. Implemented in `internal/trickplay` (behavior in
`contract/specs/trickplay.md`): opt-in per library, one single-threaded ffmpeg at a time by default
(`-trickplay-concurrency`), incremental by file size and mtime, stored under `<data dir>/trickplay`.
Hardware acceleration is not used.
