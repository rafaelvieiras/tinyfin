# UI battery (`e2e/ui/`)

Browser tests that mirror the backend conformance method: every spec is written against the
**real Jellyfin 12.2 web UI talking to the reference server** (docker `jellyfin/jellyfin:12.2`),
must pass there first, and is then run **unchanged** against Tinyfin. A spec that passes on the
reference and fails on Tinyfin is a finding about the Tinyfin server, not about the test.

The existing `e2e/tests` (jellyfin-web dev server against a seeded Tinyfin) is untouched.

## Why the same web build on both sides

The `jellyfin-web` bundle is extracted from the reference image (`/jellyfin/jellyfin-web`) into
`e2e/ui/.webdist` (git-ignored, created on first run) and Tinyfin serves exactly that build via
`TINYFIN_WEB_DIR`. Any difference between the two runs is therefore a server difference, never a
client-version difference. Both are reached at `<base>/web/`.

## Running

Requirements: docker, Go, Node, ffmpeg tooling used by `scripts/conformance-media.sh`, and a
Chromium in `~/.cache/ms-playwright` (any revision; `lib/chromium.mjs` picks the newest, or set
`UI_CHROMIUM`).

```sh
cd e2e/ui
npm install                                   # once (run.sh does it if node_modules is missing)
./run.sh                                      # TARGET=both: reference, then tinyfin, then the ratchet
TARGET=ref ./run.sh                           # reference only (validate new specs here first)
TARGET=tinyfin ./run.sh -- -g "login"         # tinyfin only; args after -- go to playwright
TARGET=ref ./run.sh --update-golden           # regenerate golden/*.json (reference only)
./run.sh --keep                               # leave servers up; stop with ./down.sh <state-dir>
```

`run.sh` (via `up.sh`): builds the media tree with `scripts/conformance-media.sh` (cached in
`~/.cache/tinyfin-ui/media`, copied per server), starts the reference
(`scripts/conformance-ref-up.sh`, unique container name, free port from 18300, offline DNS so online
providers cannot rename fixtures) and Tinyfin (`scripts/conformance-tinyfin-up.sh`, free port from
18400, seeded admin), creates the **Movies** and **Shows** libraries on both through the public API
(`lib/setup-libs.mjs`) and waits for the scan. On exit `down.sh` removes only its own container
(`docker rm -f`) and kills only its own Tinyfin PID. Tinyfin's data dir
(`/tmp/tinyfin-conformance.*`) is left in place by design.

Admin credentials default to the conformance ones; override with `UI_ADMIN_USER`/`UI_ADMIN_PASS`.

### Optional TMDB key

If `UI_CREDENTIALS_FILE` points to an env file it is sourced; a non-empty `TINYFIN_TMDB_API_KEY` is passed to Tinyfin only. Nothing is
printed. The reference stays offline (`REF_DOCKER_ARGS` default `--dns 127.0.0.1`; set it
explicitly to change that). Specs are meant to pass with and without the key; so far only the
without-key configuration has been exercised.

## Layout

- `playwright.config.ts` – one project per target (`ref`, `tinyfin`), serial, host Chromium.
- `lib/fixtures.ts` – `loginAsAdmin` (works with or without public user tiles), `waitIdle`,
  `openItem`/`card`, `adminApi` (API lookups) and the `apiTrace` fixture (method + path with ids
  replaced by `:id` + status, static `/web/` assets and media bytes excluded).
- `specs/seed.spec.ts` – login page, login, home libraries, library listing, movie detail.
- `specs/trace.spec.ts` – API trace diff home -> Movies -> Alpha Movie against `golden/`.
- `golden/*.json` – reference traces; generated only with `TARGET=ref ./run.sh --update-golden`
  (the spec refuses to write them for any other target). Review the diff before committing.
- `specs/playback.spec.ts` - player specs (below); `lib/pb.ts` (mutating admin client, temporary libraries,
  playback-report recorder), `lib/extra-media.sh` (extra fixtures), `golden/playback-*.json`.
- `baseline.json`, `ratchet.mjs` – the ratchet below.

## Adding a spec

1. Write it in `specs/*.spec.ts` using the helpers; select by stable classes/ids that the stock
   web UI uses, and only assert what the reference demonstrably shows.
2. `TARGET=ref ./run.sh` until green. Never weaken a spec to make Tinyfin pass.
3. `TARGET=tinyfin ./run.sh -- -g "<title>"`; a failure is a finding to record.
4. Spec titles are the baseline keys (`<file> > <title>`): do not rename lightly.

## Baseline ratchet

`baseline.json` lists the specs (`"<file> > <title>"`) that must pass on Tinyfin. After a run that
included Tinyfin, `ratchet.mjs` exits 1 if a baselined spec did not pass; failing specs that are
not baselined are printed as **known divergences**; newly passing specs are listed as candidates.
The file is never generated or rewritten by tooling. To raise the ratchet by hand:

```sh
node ratchet.mjs results.json --print-union   # prints baseline plus newly passing, to stdout
# review it, paste the union into baseline.json, commit
```

Removing an entry (accepting a regression) is a deliberate, reviewed edit.

## Admin dashboard specs (`specs/admin-*.spec.ts`)

Browser coverage of the admin dashboard: home, users (create, policy toggles, password set/reset,
delete), libraries (wizard with a media path, options, rename, remove), devices, activity log,
scheduled tasks (list, trigger, stop), API keys, transcoding / networking / General / Branding
settings, plugins and logs pages. Every mutation is undone (also on failure), so the specs are
order independent. Each flow also records its API trace and compares it with
`golden/admin-<flow>.json` through `checkTrace` (`lib/fixtures.ts`): no 5xx anywhere, and on
Tinyfin no endpoint the reference called may be missing (artwork requests excepted) and no 4xx/5xx
may appear that the reference did not produce. Regenerate with `TARGET=ref ./run.sh --update-golden`.
Proposed ratchet additions live in `baseline.ui3.proposed.json` (merged into `baseline.json` by hand).

## Playback specs (`specs/playback.spec.ts`)

Drive the real web player in Chromium: Play, `<video>` currentTime advancing with no error dialog,
the `POST /Sessions/Playing`, `/Progress` and `/Stopped` reports, and the PlaybackInfo decision
(direct play vs HLS) read from the exchanges the client makes. Covered: direct play, pause/seek/resume
with the resume position persisting (API and details page), the Quality menu, a forced HLS transcode
(playlists and segments fetched with 200), audio-track switching, embedded subtitle selection and Off,
burned-in vs external subtitle under transcode, the Up Next popup and Start Now, chapters and trickplay
thumbnails, plus two golden API traces (`golden/playback-direct.json`, `playback-hls.json`).

How it is set up (so a failure can be read correctly):

- Fixture folders that the default Movies/Shows libraries do not cover are added as temporary `PB ...`
  libraries in `beforeAll` through the public API and removed in `afterAll`. A library created while
  another scan runs is sometimes never scanned, so the helper re-requests a refresh while it waits.
- Every item a spec plays is reset afterwards (played flag, play count, resume position).
- Browser codecs: the Playwright Chromium plays the h264/aac MP4 and the MKV fixtures, so no codec
  limitation affected the reference. The web client's own limits shaped the media choices:
  - the Quality menu bottoms out at 420 kbps, above every fixture (about 110-215 kbps), so choosing
    from the menu never forces a transcode; a transcode is forced with the same per-browser
    localStorage setting the menu writes (`maxbitrate-Video-*`, 60 kbps) and the menu itself is
    asserted to send 420000 and stay direct;
  - the client shows an ASS track through its own renderer (no stream restart), so burn-in is exercised
    by setting `alwaysBurnInSubtitleWhenTranscoding` together with the bitrate limit;
  - the Up Next popup requires an episode of 10 minutes or more with 20-30 s left and a hidden OSD, which
    no conformance clip satisfies: `lib/extra-media.sh` (called by `up.sh`) adds a 700 s episode;
  - chapter stills and trickplay sheets are made by the reference only for the 30 s `ChapterImg`
    fixture (none for the 6 s chapter clips; the 1500 s trickplay clip only delays the scan), so that
    one item serves both specs;
  - the web UI hides chapters without an extracted still, hence the wait on `Chapters[].ImageTag`.
- A spec that checked the details page flipping to "Resume" without a reload was dropped: it was flaky on
  the reference itself (view reuse); the persisted position is asserted via the API and a fresh load.
