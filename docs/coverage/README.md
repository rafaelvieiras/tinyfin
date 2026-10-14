# Coverage tracker

What Tinyfin does, does not do and will do, kept as data and rendered for people:

- **Overview:** [`../COVERAGE.md`](../COVERAGE.md) (totals, per controller, per subsystem, gaps).
- **Detail:** `api-<group>.md` (every HTTP operation of the reference) and `<area>.md` (non-route features).
- **Machine-readable:** [`status.json`](status.json) (every route and feature with its computed status).

## What is computed and what is written by hand

| Part | Source | Edited by hand? |
|---|---|---|
| The list of reference operations (364 for Jellyfin 12.2.0) | [`openapi-ops.json`](openapi-ops.json), a snapshot of the reference's `/api-docs/openapi.json` | No: `conformance coverage import-openapi` |
| Is a route registered | `HandleFunc("METHOD /path", ...)` calls in `internal/api/*.go` (parsed) | No |
| Is a route verified | scenarios in `docs/contract/scenarios/` (`endpoint` and step requests) that pass in `docs/contract/baseline.json` | No |
| Does the web client call it | paths quoted in backticks in `docs/11-jellyfin-web-route-coverage.md` | No (override with `web` in the overlay) |
| Reasons: partial, planned, not-planned, divergent, priority | [`overlay.json`](overlay.json) | **Yes** |
| Non-route features (library types, scanning, playback, ...) | [`features/*.json`](features/) | **Yes** |

The status of a route follows from the code: registered means `supported`; the overlay can lower it to
`partial` or mark it `divergent-by-design`. An unregistered route is `planned` or `not-planned`, taken from
its own overlay entry or from the default of its OpenAPI tag. Nothing marked `supported` can drift silently:
unregistering a route turns it into an error until it is classified.

## Statuses

`supported`, `partial`, `planned`, `not-planned`, `divergent-by-design` (legend in the overview).
Every status other than `supported` needs `notes`.

## Editing

1. **A route changed** (registered, removed, or a scenario was added): nothing to edit; regenerate.
2. **A route needs a reason or a priority:** add it under `routes` in `overlay.json`, keyed by `"METHOD /path/{param}"` exactly as in `openapi-ops.json`.
   Fields: `status`, `notes`, `priority` (`high|medium|low`), `reference` (what Jellyfin does), `via`
   (a registered route that serves it, e.g. a wildcard), `web` (override).
3. **A whole tag changed** (new default): edit `tags` in `overlay.json`.
4. **A non-route feature:** edit `features/<area>.json`. Fields: `id` (stable, prefixed by the area),
   `title`, `status`, `description` (what Tinyfin does), `reference` (what Jellyfin 12.2 does), `notes`,
   `spec_rules`, `scenarios`, `code`, `routes`, `priority`, and for a `supported` feature without a passing
   scenario `unverified: true` plus `unverified_reason`.
5. Regenerate and commit the result:

```sh
go run ./cmd/conformance coverage          # writes docs/COVERAGE.md, docs/coverage/*.md, status.json, badges, the README block
go run ./cmd/conformance coverage -check   # validates; writes nothing (this is the CI step)
```

Refreshing the reference snapshot (needs Docker; the document is public HTTP output of a stock 12.2 image):

```sh
docker run -d --rm --name ref12 -p 127.0.0.1:18197:8096 jellyfin/jellyfin:12.2
curl -s http://127.0.0.1:18197/api-docs/openapi.json -o /tmp/openapi.json
docker rm -f ref12
go run ./cmd/conformance coverage import-openapi /tmp/openapi.json
```

## What `-check` enforces (CI job "Build, vet, test", and `go test ./internal/coverage`)

- ids are unique; scenario, spec rule, code path and route references exist;
- every reference operation is classified (registered, or covered by an overlay entry or tag default);
- a registered route is never `planned`/`not-planned`; an unregistered one is never `supported`;
- `supported` features need a passing scenario in the baseline, or `unverified: true` with a reason;
- every status other than `supported` has notes;
- every generated file and the `README.md` block are up to date (diff fails).

## Notes on reading the numbers

- *Registered* counts HEAD operations as served when the GET pattern is registered (Go's mux does that).
- *Verified* means at least one non-`scope-negative` scenario that exercises the route passes in the baseline.
  The first-run wizard scenarios are `fresh-server` ones and are not in the baseline, so `/Startup/*` shows as unverified.
- *Web* is a heuristic: a path quoted in docs/11. Treat it as "the client can call it".
- Items marked *unverified* are claims backed by code and unit tests only; the reason is in the notes.
