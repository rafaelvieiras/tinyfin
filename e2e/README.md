# Tinyfin e2e suite

Playwright tests that drive a real, unmodified [jellyfin-web](https://github.com/jellyfin/jellyfin-web)
build against a real Tinyfin server — the same method AGENTS.md's
clean-room policy prescribes for wire-compatibility verification
("observed client behavior", not the reference *server*'s source).
jellyfin-web is a separate, GPL-licensed project; cloning and running it
is not a clean-room violation, only reading `jellyfin/jellyfin` (the C#
server) would be.

## Why this exists

Every test here started as a bug found by hand: connect a real browser to
a real jellyfin-web dev server pointed at a real Tinyfin server, click
through login -> library -> playback, and see what breaks. That caught
real wire-compatibility gaps no amount of reading the OpenAPI spec would
have (case-insensitive routing, a nil `Policy`/`Configuration` crashing
the client, `MediaType` gating whether a Play button even renders,
`<img>`/`<video>` tags needing token auth via a query param instead of a
header, ...). Doing that by hand again for every future change is slow
and burns a lot of manual back-and-forth; this suite encodes the same
flows so `npm test` catches a regression in seconds instead of a
multi-step manual session.

## Setup (once)

Requires Go, Node, and ffmpeg/ffprobe (already project prerequisites —
see the repo root).

```sh
cd e2e
./setup.sh   # clones jellyfin-web into .jellyfin-web, npm installs both, installs the Playwright browser
```

`JELLYFIN_WEB_DIR` overrides where jellyfin-web is expected if you keep
it checked out somewhere else.

## Running

```sh
cd e2e
npm test
```

Playwright's `webServer` config starts both processes for you and tears
them down after: a native Tinyfin server (`run-server.sh`, seeded fresh
each run by `cmd/e2eseed` — synthetic fixtures under `testdata/e2e/`, not
any real media file, so this is reproducible on any machine including
CI) on `:8096`, and jellyfin-web's own dev server on `:8080`. Not
Docker — the read-only-mount concern that motivated Docker for the
original manual verification pass doesn't apply to a repo-owned,
throwaway test database.

## Fixtures

`cmd/e2eseed` seeds one user (`e2e` / `E2eTest123!`), one library, and
one movie item pointing at `testdata/e2e/sample.mp4` — a synthetic,
~50 KB, 3-second h264/aac clip generated with ffmpeg specifically to
match `internal/deviceprofile`'s Jellyfin-Web baseline profile, so it's
actually Direct-Play-compatible with a real browser. Manually-tested real
movie files are typically HEVC/mkv, which no mainstream browser decodes
natively — they could never exercise the Direct Play path this suite
proves.
