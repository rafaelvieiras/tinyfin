# Behavior contract (Jellyfin 12+ wire behavior)

The contract is the executable definition of "wire compatible" for Tinyfin
(`docs/04`): for each in-scope route, what an observer sees for a given
request and server state. It is checked by `cmd/conformance`, which runs the
same scenarios against a reference server (to validate the scenarios) and
against Tinyfin (to measure conformance, and to drive TDD).

## Provenance and clean-room status

The behaviors recorded here were extracted from a read of the reference
server **on the maintainer's explicit request** (phase 1 of the two-phase process in
`AGENTS.md`, decision D12), cross-checked black-box against a running 12.2
reference server and the official `jellyfin-web` 12.2 client. Nothing carries
over the reference implementation's names, files, comments or structure. That
is enforced mechanically: `go run ./cmd/conformance lint` and
`go test ./internal/conformance` fail on class/file/namespace names, source
paths and line references anywhere under `docs/contract/`.

Implementation work from these specs follows the usual rule: never with the
reference source open in the same session.

## Scope rules (what is in the contract)

1. **12+ only.** Nothing that existed only before 12 is specified.
2. **Legacy with a modern replacement is out**, with one exception below.
   Only the replacement is specified. The excluded routes are listed in
   [`scope-excluded.md`](scope-excluded.md) with their replacement, so a
   regression test can assert they are *not* required.
3. **Exception: legacy routes the official web client still calls.**
   `jellyfin-web` 12.2 (via its embedded `jellyfin-apiclient`) still calls a
   handful of routes the reference marks as legacy. The product requirement is
   that the official client works unmodified (`AGENTS.md`, principle 4), so
   these are in scope with `scope: "legacy-web-required"`, specified as thin
   aliases of the modern behavior, and isolated so they can be dropped when
   the client stops using them. Evidence: `jellyfin-api-v12/web-client/` in
   the audit workspace (static call-site inventory + live traffic capture).
4. **Mixed routes** (current route, some legacy parameters/behaviors): only the
   current parameters and behaviors are specified.
5. **Security-driven divergence.** Where the reference behaves in a way that is
   a defect (anonymous restart, ignoring download permission, ...), the
   scenario asserts the *correct* behavior and carries a `divergence` record.
   Divergent scenarios are skipped when validating against the reference.
6. **Legacy authentication is not specified.** Only `Authorization:
   MediaBrowser ...` and the `ApiKey` query parameter (WebSocket) are honored.
   `X-Emby-*` headers and `api_key` are covered by `scope-negative` scenarios
   that assert they are *not* honored (opt-in via `-scope scope-negative`).

## Layout

| Path | Content |
|---|---|
| `specs/<area>.md` | Behavior specs: rules with stable IDs (`users.auth.R3`), prose, scenario index. `specs/wire.md` holds the cross-cutting rules (id format, failure bodies) |
| `scenarios/<area>.json` | Executable scenarios (array per file) |
| `fixtures.json` | Declarative server state (users, libraries, items) applied via the public API |
| `baseline.json` | Committed per-scenario status of Tinyfin (the ratchet, see *Baseline and ratchet*); never edited by hand |
| `scope-excluded.md` | Legacy routes excluded from the contract, with their replacement |
| `../../internal/conformance` | Runner, assertions, fixtures, clean-room lint |
| `../../cmd/conformance` | CLI |
| `../../scripts/conformance-media.sh` | Generates the synthetic media tree |

## Scenario format

```json
{
  "id": "users.auth-by-name.wrong-password",
  "endpoint": "POST /Users/AuthenticateByName",
  "spec": "users.md#authenticate-by-name",
  "scope": "modern",
  "tags": ["auth", "web-client"],
  "covers": ["users.auth.R3"],
  "requires": ["user:alice"],
  "steps": [
    {
      "name": "bad password",
      "as": "anonymous",
      "request": {
        "method": "POST",
        "path": "/Users/AuthenticateByName",
        "headers": {"Authorization": "MediaBrowser Client=\"t\", Device=\"t\", DeviceId=\"t1\", Version=\"1\""},
        "body": {"Username": "{{user.alice.name}}", "Pw": "wrong"}
      },
      "expect": {"status": 401}
    }
  ],
  "cleanup": []
}
```

* `as`: `anonymous` (default), `admin`, a fixture user key, `apikey` (a
  run-specific API key that belongs to no user; the scenario `requires`
  `identity:apikey`) or `token:{{capture.x}}` (a token captured earlier in the
  scenario). The runner adds the identity's `Authorization` header unless
  `request.noAuth` is true.
* Templates: `{{user.<key>.id|name|password}}`, `{{item.<key>.id|name}}`,
  `{{library.<key>.name}}`, `{{capture.<name>}}`, `{{server.mediaroot}}`.
* `{{x|dashed}}` renders a 32-hex id in its 8-4-4-4-12 form;
  `{{device.<identity>}}` is the DeviceId the runner uses for an identity and
  `{{run.suffix}}` the per-run DeviceId suffix (see *Running*).
* `expect.status` is a number or an array (any of); `expect.headers` values are
  `present`, `absent`, `/regex/` or a literal (case-insensitive); `expect.json`
  asserts on `$.path` with ops `eq ne exists absent type len gte lte contains
  notcontains matches in hex32` (`hex32`: every value at the path is 32
  lowercase hex digits without dashes; an empty wildcard result fails).
  `expect.problem: <status>` asserts an RFC 9110 problem document (`type`,
  `title`, `status` equal to the number).
* `capture` stores a value (`$.json.path` or `header:Name`) for later steps.
* Non-HTTP steps: `{"sleepMs": 1500}` pauses (time-based rules);
  `{"config": {"set": {"QuickConnectAvailable": false}}}` patches the
  server-wide configuration and the original is **always restored** when the
  scenario ends (also when it fails, and on Ctrl-C); a `ws` step drives a
  WebSocket client: `{"ws": {"open": "a", "as": "admin"}}` connects
  `/socket?ApiKey=<token>` (optionally `refuseStatus` to expect a refused
  handshake, `param` to send the token under another name),
  `{"ws": {"wait": "a", "type": "UserUpdated", "timeoutMs": 5000}}` blocks for
  a message of that type and applies the step's `expect.json`/`capture` to it
  (`absent: true` asserts that none arrives), `{"ws": {"close": "a"}}`
  disconnects (sockets also close when the scenario ends).
* `requires` lists fixture keys; a missing fixture makes the scenario
  **BLOCKED**, not failed (the server cannot even set the state up yet).
* `divergence.reason` marks an intentional difference from the reference.
* `pending` parks a scenario that is specified but not yet verified.
* Mutating scenarios must restore state in `cleanup` (it always runs).
* Scenarios tagged `risky` can leave the server unusable for the runner (they
  try to demote or delete the last administrator, or leave the runner's
  source address throttled). They are skipped unless the runner is given
  `-risky`, and when it is they always run **after** every other selected
  scenario, so nothing else can be affected. Run them against a dedicated
  throwaway server (`-tags risky -risky`), never mixed into the main run.
* The `last-admin` tag (on the two last-administrator scenarios) additionally
  makes the runner count the administrators first: unless the runner's admin
  is the only one the scenario is BLOCKED without sending anything, so it can
  never delete or demote the runner's admin on a server that has another one.
* Non-HTTP step `{"serverFile": {"read": "<name or absolute path>", "under":
  "passwordreset"}}` reads a file from the server's filesystem (the runner
  never writes there). Its `expect`/`capture` apply as if it were a response:
  `200` with the content (JSON-decoded when it parses) or `404` when the file
  does not exist. A bare name resolves to `<-server-data-dir>/<under>/<name>`,
  an absolute path is used as is (the reference answers with one). Without
  `-server-data-dir`/`-read-file-cmd` the step, and every scenario that
  `requires` `serverfile:read`, is BLOCKED.
* `{"config": {"section": "network", "set": {...}}}` patches a named
  configuration section (`GET`/`POST /System/Configuration/<section>`); every
  section touched is restored when the scenario ends, like the main one.
* `requires` also takes runner-detected capabilities: `serverfile:read`
  (above) and `origin:non-loopback`. The runner decides the latter at setup
  from its own admin login: both servers write `IP address: <peer>` into the
  login's activity entries, so it knows the address the server sees it from. A
  scenario that needs a *remote* caller requires it, then makes that address
  remote by listing subnets that exclude it in the network section
  (`LocalNetworkSubnets`, restored afterwards). Loopback is always local on
  every server, so a run whose base URL is reached from `127.0.0.1` BLOCKS
  these scenarios instead of failing them.
* A fixture library marked `"lazy": true` (with items carrying
  `"library": "<key>"`, e.g. the 330 s `item:long` movie that exercises the
  resume-position save branch) is created, scanned and removed around each
  scenario that `requires` it, so its media never shows up in the listings
  other scenarios count.

## Running

```sh
scripts/conformance-media.sh /tmp/conf-media          # once
# reference server (12.2) with /tmp/conf-media mounted at /media, admin already created:
go run ./cmd/conformance run -base http://127.0.0.1:18096 -admin-user admin \
    -admin-pass '...' -media-root /media -reference
# tinyfin with the same files on its own filesystem path:
go run ./cmd/conformance run -base http://127.0.0.1:18098 -admin-user admin \
    -admin-pass '...' -media-root /tmp/conf-media
```

Exit status is non-zero when any scenario fails. `-json out.json` writes a
machine-readable report; `-scope`, `-tags`, `-skip-tags`, `-only` narrow the run.

Server-side capabilities (all optional; the scenarios that need them are
BLOCKED, not failed, without them):

```sh
# Tinyfin: the data dir is the directory of its database file. The runner must
# also reach it through a non-loopback address of the host (for users.auth.R21):
BIND=<host-address> scripts/conformance-tinyfin-up.sh 18098     # prints data-dir=...
go run ./cmd/conformance run -base http://<host-address>:18098 ... -server-data-dir <data-dir>
# reference in Docker: the PIN file lives inside the container, read it with a command
# (argv prefix, no shell; the path is appended). A published port already reaches
# the server from the container gateway, a non-loopback address.
go run ./cmd/conformance run -base http://127.0.0.1:18096 ... -reference \
    -read-file-cmd "docker exec tinyfin-conformance-ref cat"
# the risky scenarios, alone, against a dedicated throwaway server (fresh admin only):
go run ./cmd/conformance run ... -tags risky -risky -strict    # -strict: BLOCKED fails too
```

`-device-suffix auto` appends a random suffix to every DeviceId the runner and
the scenarios use, so concurrent runs against one server do not revoke each
other's tokens (a login on a DeviceId revokes that device's previous token).
It is off by default because a few scenarios address a device by its literal
runner-generated id; those use `{{device.<identity>}}` instead.
`scripts/conformance-ref-up.sh` honors `REF_NAME` (parallel containers) and
`REF_DOCKER_ARGS="--dns 127.0.0.1"` (keeps the reference offline so online
metadata providers cannot rename the fixture titles).

## Baseline and ratchet

`baseline.json` records the per-scenario status (`pass`/`fail`/`blocked`/
`skipped`) of Tinyfin and, once, the counts of the manual reference-server run
the scenarios were validated against. It turns the suite into a ratchet:
scenarios may only move from not-passing to passing.

```sh
# compare (what CI runs): exit 1 when a scenario that passed in the baseline no longer passes
go run ./cmd/conformance run -base ... -admin-user admin -admin-pass '...' -media-root ... \
    -baseline docs/contract/baseline.json
# refresh after an intentional improvement (never edit baseline.json by hand)
go run ./cmd/conformance run -base ... -admin-user admin -admin-pass '...' -media-root ... \
    -write-baseline docs/contract/baseline.json [-ref-report ref.json]
# per-area progress table
go run ./cmd/conformance progress
```

* Regressions fail the run; `IMPROVED` and `NEW` scenarios are listed so the
  baseline can be refreshed in the same change that fixed (or added) them.
* Refresh when a change flips scenarios to passing or adds scenarios; commit
  the new file with the change. A refresh that lowers a count needs a reason
  in the commit message.
* `-ref-report` takes the `-json` report of a `-reference` run to record its
  counts. The reference validation itself stays manual (it needs the 12.x
  image); a scenario must pass there before it enters the baseline as a goal.
* CI (`.github/workflows/ci.yml`, job `conformance`) builds Tinyfin, generates
  the media tree, starts a throwaway server with
  `scripts/conformance-tinyfin-up.sh` and runs the ratchet with
  `-skip-tags time-dependent` (those scenarios assume they do not run in the
  03:xx local hour). The server listens on the runner's own (non-loopback)
  address and the run passes `-server-data-dir`, so the remote-access and PIN
  scenarios run; a second job step starts a fresh throwaway server and runs
  only the `risky` scenarios with `-risky -strict`.

## Coverage accounting

Every spec rule has an ID; scenarios list the rules they exercise in
`covers`. A rule with no scenario is a hole in the contract, and a scenario
with no rule is unexplained. `go test ./internal/conformance` checks both
directions for the specs that exist.

## Progress

Per-area status of Tinyfin at the committed baseline (generated with
`go run ./cmd/conformance progress`; the reference server passes every
scenario that is not pending or divergent; `risky` ones run only with
`-risky`, see *Scenario format*):

| Area | Scenarios | Pass | Fail | Blocked | Skipped |
|---|---|---|---|---|---|
| items | 113 | 113 | 0 | 0 | 0 |
| playstate | 103 | 103 | 0 | 0 | 0 |
| quickconnect | 13 | 13 | 0 | 0 | 0 |
| users | 154 | 149 | 0 | 0 | 5 |
| wire | 6 | 6 | 0 | 0 | 0 |
| **Total** | 389 | 384 | 0 | 0 | 5 |
