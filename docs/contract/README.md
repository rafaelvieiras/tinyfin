# Behavior contract (Jellyfin 12+ wire behavior)

The contract is the executable definition of "wire compatible" for Tinyfin
(`docs/04`): for each in-scope route, what an observer sees for a given
request and server state. It is checked by `cmd/conformance`, which runs the
same scenarios against a reference server (to validate the scenarios) and
against Tinyfin (to measure conformance, and to drive TDD).

## Provenance and clean-room status

The behaviors recorded here were extracted from a read of the reference
server **on Rafael's explicit request** (phase 1 of the two-phase process in
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
| `specs/<area>.md` | Behavior specs: rules with stable IDs (`users.auth.R3`), prose, scenario index |
| `scenarios/<area>.json` | Executable scenarios (array per file) |
| `fixtures.json` | Declarative server state (users, libraries, items) applied via the public API |
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

* `as`: `anonymous` (default), `admin`, or a fixture user key. The runner adds
  the identity's `Authorization` header unless `request.noAuth` is true.
* Templates: `{{user.<key>.id|name|password}}`, `{{item.<key>.id|name}}`,
  `{{library.<key>.name}}`, `{{capture.<name>}}`, `{{server.mediaroot}}`.
* `expect.status` is a number or an array (any of); `expect.headers` values are
  `present`, `absent`, `/regex/` or a literal (case-insensitive); `expect.json`
  asserts on `$.path` with ops `eq ne exists absent type len gte lte contains
  notcontains matches in`.
* `capture` stores a value (`$.json.path` or `header:Name`) for later steps.
* `requires` lists fixture keys; a missing fixture makes the scenario
  **BLOCKED**, not failed (the server cannot even set the state up yet).
* `divergence.reason` marks an intentional difference from the reference.
* `pending` parks a scenario that is specified but not yet verified.
* Mutating scenarios must restore state in `cleanup` (it always runs).

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
machine-readable report; `-scope`, `-tags`, `-only` narrow the run.

## Coverage accounting

Every spec rule has an ID; scenarios list the rules they exercise in
`covers`. A rule with no scenario is a hole in the contract, and a scenario
with no rule is unexplained. `go test ./internal/conformance` checks both
directions for the specs that exist.
