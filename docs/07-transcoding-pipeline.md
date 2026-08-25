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

A device profile describes what one real client is known to support. The reference
implementation ships dozens of these, covering every client its maintainers have encountered,
which is ongoing maintenance burden with a long tail of low-value entries. Tinyfin's default
posture (open item **O2**, `00-decision-log.md`) is to start with only the clients actually used
against the reference deployment and grow the set on demand, rather than trying to pre-populate
broad coverage — an unknown client without a stored profile should degrade to a conservative
default (safe transcode) rather than fail outright.

## Execution

The API/metadata role's playback-decision logic hands off to the transcode worker role (`03`)
with: source file reference, target mode, and the specific parameters that mode requires (target
container/codec/bitrate for Transcode; target audio codec alone for Direct Stream). The transcode
worker builds the actual `jellyfin-ffmpeg` command line — stream mapping, filters, hardware
acceleration flags for the vendor available on that worker (NVENC/VAAPI/QSV/AMF/V4L2, mirroring
the accelerators `jellyfin-ffmpeg` itself supports) — and manages the subprocess: starting it,
streaming output as HLS segments, and tearing it down when the client disconnects or finishes.

Because this role is stateless per request (`02`, `03`), nothing about scaling it out later
requires touching the decision logic above it — adding a second transcode worker is a deployment
change, not a code change, exactly per decision **D6**.

## Trickplay

Seek-preview thumbnails, pre-generated at scan time (optionally hardware-accelerated) and served
as a sprite sheet — this is a scan-time concern more than a playback-time one; see `08` for where
it sits relative to the scanner.
