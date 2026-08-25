# 02 — Glossary

Terms used consistently across the other documents in this repository. When a term has a
specific meaning here that differs from casual usage, that meaning wins inside this repo.

**API/metadata node** — the process (or role, in a single-node deployment) that serves the
HTTP/WebSocket API, owns the storage connection, and answers library/user/session queries. Does
not itself run FFmpeg.

**Client** — any application speaking the Jellyfin wire protocol against a server: the official
web/mobile/TV apps, the Kodi add-on, Findroid, Infuse, and others. Tinyfin's compatibility target
is defined against real client behavior, not against a spec document alone — see `04`.

**Device profile** — a description, submitted by a client (or matched server-side against a known
client), of what containers/codecs/resolutions/bitrates it can play natively. Used to decide
between Direct Play, Remux, Direct Stream, and Transcode. See `07`.

**Direct Play** — serving a media file exactly as stored, no processing. Cheapest playback mode,
possible only when the client's device profile already supports the file's container and codecs.

**Direct Stream** — video stream passed through untouched; only the audio track is transcoded
(e.g. TrueHD → AAC because the client can't decode TrueHD).

**Embedded mode** (storage) — the default storage implementation: a single SQLite file, no
separate database process. See `05`.

**Provider** (metadata provider) — an external source of catalog metadata, consumed through a
fixed interface. MyMediaDB is the first intended provider; TMDB/TVDB directly are a fallback path
if no provider is configured. Not to be confused with a general-purpose plugin system — see `08`.

**Remux** — only the container is changed (e.g. MKV → MP4); both audio and video streams are
copied, not re-encoded.

**Scale mode** (storage) — the networked storage implementation an operator can turn on instead
of embedded mode: a single write leader plus one or more read replicas kept in sync by streaming
the SQLite write-ahead log, following the model Litestream uses for read replication. See `05`.

**Session/realtime state** — data that exists only for the lifetime of an active client
connection or a live event: which sessions are currently playing what, SyncPlay group membership
and playback position, live progress updates pushed over WebSocket. Deliberately modeled as a
separate concern from durable storage — see `06`.

**Transcode** — the video stream (and typically audio) is re-encoded, generally into HLS segments,
because the client's device profile doesn't support the source format closely enough for Direct
Play or Direct Stream.

**Transcode worker** — the role (process, and in scale mode potentially a separate machine) that
actually invokes `jellyfin-ffmpeg` and produces output segments. Stateless per request: given an
input file and a target profile, it produces output — nothing about it needs to be synchronized
across nodes. See `07`.

**Wire compatibility** — matching the observable HTTP/WebSocket behavior (routes, headers,
request/response JSON shapes, status codes) that real Jellyfin clients depend on, to the degree
that those clients function correctly against Tinyfin without being aware the server isn't
Jellyfin. Does not imply matching internal implementation details, database schema, or
administrative/plugin APIs that clients don't rely on. See `04`.
