# AGENTS.md — guidance for agents (human and LLM)

> Read this before writing code. This is the entry point: the principles that don't bend, the
> operational practices that avoid rework, and where to go next. This file **points to**
> authoritative sources — it does not duplicate them. When something here disagrees with a
> doc/decision-log entry, the specific source wins and this file should be corrected.

## What the project is, in one sentence

Tinyfin is a **wire-compatible reimplementation of the Jellyfin server API** in Go, built so that
existing Jellyfin clients keep working unmodified while idle memory usage stays near-zero and a
path to multi-node scale exists without being mandatory. Module:
`github.com/rafaelvieiras/tinyfin`. Phases 0–3 are implemented (single-node compatibility core,
transcoding pipeline, metadata provider interface — see [`ROADMAP.md`](ROADMAP.md) for what's
actually done); Phase 4 (scale mode) hasn't started.

## Repository conventions (open source)

This is an open-source project. These rules apply to every contributor, human
or LLM, and to every artifact in the repo:

- **English only.** Code, comments, identifiers, UI strings, log/error
  messages, test names, docs, commit messages, issues and PRs are all written
  in English. The only exception is test data or captured wire values that
  deliberately exercise non-English input (e.g. a localized display title the
  reference server produced), and it must be explained by an English comment.
- **Commits are human-owned.** Every commit is authored by, and is the
  responsibility of, one or more humans. Never add AI/LLM/tool co-authorship
  or attribution to a commit or PR — no `Co-Authored-By:` trailer naming a
  tool or model, no "Generated with ..." footer — even if your tooling adds
  one by default. A `Co-Authored-By:` trailer for another human is fine.
  Messages are short and imperative, in English, with a conventional prefix
  (`feat:`, `fix:`, `docs:`, `test:`, `refactor:`, `chore:`).
- **No personal or infrastructure data.** Nothing in the repo (files, tests,
  fixtures, comments, commit messages, docs) may reveal a developer's or
  operator's personal details or setup: real names or e-mail addresses (git
  authorship and `LICENSE` are the only places), absolute home directories,
  hostnames, IPs, ports or paths of someone's machine, the names of someone's
  personal media library or titles, hardware/OS anecdotes stated as a local
  fact ("on my machine ..."), private notes or vault references, credentials,
  API keys, tokens or `.env*` contents. Use placeholders (`example.com`,
  `<path>`) and synthetic fixtures. Refer to people by role ("the maintainer",
  "a reporter"), never by name. The rule covers everything published about the
  project, not only files: issues, issue and PR comments, PR descriptions,
  release notes, and their edit history. It also covers inference: never say or
  imply which hardware, operating system, hosting or deployment the
  maintainers or any user runs; describe a platform only as a supported target.
  Write issues from the project's point of view, not from a person's setup.
- **Evidence is described, not narrated.** Docs record what the server does and
  why (behavior, root cause, decision), not the story of who noticed it or in
  which session. Say "a client sent X", not "<person> reported X".
- **The release tooling is tested like code.** `scripts/release-notes-check.mjs`
  renders release notes through the real plugin with the options from
  `.releaserc.json`, and CI runs it on every pull request. It exists because a
  changelog preset and `@semantic-release/release-notes-generator` are versioned
  apart: a mismatch between them renders nothing, but only when `generateNotes`
  runs, which never happens before a push to `main`. A dependency bump that
  touches any of those packages has to leave that check green.
- Quick self-check before committing:
  `git grep -nIP '[áéíóúâêôãõçÁÉÍÓÚÂÊÔÃÕÇ]'` and
  `git grep -nIE '/home/|/Users/'` should print nothing except deliberate,
  explained multilingual data.

## Principles that don't bend

All six are in the [README](README.md) and [CONTRIBUTING.md](CONTRIBUTING.md):

1. **Idle memory is a budget, not an afterthought.** No full-catalog in-memory cache, ever. Every
   read is a query against a properly indexed embedded store (SQLite, FTS5 + generated columns).
   This directly targets a documented, root-caused failure
   mode in the reference implementation (`jellyfin/jellyfin` discussion #10444) — an
   aggressive non-critical in-memory cache is why a reference server grows from ~400MB to several
   GB over normal use. Do not reintroduce that pattern here for "performance."
2. **Single binary, zero required dependencies, by default.** `tinyfin` with an embedded SQLite
   file must be a complete, working deployment. Anything that requires a second process
   (Postgres, Redis/Valkey, a message broker) is opt-in scale-mode configuration, never a
   default requirement.
3. **Scale is a mode, not a rewrite.** Storage ([`docs/05`](docs/05-storage-drivers.md)) and
   session/realtime state ([`docs/06`](docs/06-session-and-realtime-state.md)) are each behind an
   interface with an embedded default and a networked implementation. These are **two separate
   interfaces** — do not conflate them. Swapping the database engine alone does not make the
   application horizontally scalable; PocketBase's own maintainer made this exact point when
   explaining why Postgres support wouldn't fix PocketBase's single-node ceiling (no
   synchronization mechanism for in-process event/session state). Tinyfin exists specifically to
   not repeat that gap.
4. **Wire compatibility is the product.** The only reason this project has value over writing a
   from-scratch media server is that unmodified Jellyfin clients (web, mobile, TV apps, the Kodi
   add-on, Findroid, Infuse) work against it. See [`docs/04-api-compatibility.md`](docs/04-api-compatibility.md)
   for what "compatible" means precisely and how it's checked. A change that improves internal
   code cleanliness at the cost of diverging from observed client-facing behavior is a bug, full
   stop — file it as one.
5. **Transcoding is delegated, not reinvented.** [`jellyfin-ffmpeg`](https://github.com/jellyfin/jellyfin-ffmpeg)
   is an external, independently-maintained subprocess dependency — invoked, never forked, never
   vendored, never linked. Tinyfin owns exactly one thing here: the decision layer (Direct
   Play/Remux/Direct Stream/Transcode selection against a device profile) and the command/session
   lifecycle around it. See [`docs/07-transcoding-pipeline.md`](docs/07-transcoding-pipeline.md).
6. **No cataloging logic here.** Scanning, metadata resolution, artwork curation belong to an
   external cataloging tool, consumed through a provider interface. Do not grow Tinyfin's own
   scanner into a competing cataloging system — see
   [`docs/08-library-scanning-and-metadata.md`](docs/08-library-scanning-and-metadata.md) for the
   boundary.

## Divergences are recorded, always

Wire compatibility is the product (principle 4), which makes every deliberate
difference from the reference server a decision that has to be written down —
otherwise nobody can tell a decision from a bug, and the next contributor
"fixes" it back.

[`docs/13-divergences.md`](docs/13-divergences.md) is the one index of them. A
change that **introduces, changes or removes** a divergence updates that file in
the **same commit**, and the code comment, spec or CHANGELOG entry describing it
names its ID there:

- a difference where the reference server is **defective** and Tinyfin does the
  correct thing (a path leak, a 500 on a bad request, a token that never
  expires) belongs in section A of that file, and the scenario that asserts the
  correct behavior carries a `divergence` record — `go run ./cmd/conformance
  lint` (also `go test ./internal/conformance`) fails when a scenario's
  `divergence` record is not listed, so this half is machine-enforced;
- a difference **no scenario asserts** (a heuristic that cannot be compared,
  an order the reference derives from something the files do not show) belongs
  in section B with a `DIV-<n>` ID — nothing fails if it drifts, so it needs the
  entry and a human reading it;
- something the reference has **no equivalent for** at all (a generated library
  picture, an extra field on a push) belongs in section C, with the decision-log
  entry or spec note it came from.

Choosing the reference's own defective behavior "for compatibility" is not an
option either: assert the correct behavior, record the divergence, and move on.
Not implemented is a different thing from divergent — gaps live in
[`docs/11`](docs/11-jellyfin-web-route-coverage.md)'s route table and the
contract specs' "Known differences" sections, not here.

## Clean-room requirement

Tinyfin implements the Jellyfin *wire protocol* (HTTP routes, headers, JSON shapes). What's
protected here, and what isn't: a protocol and the ideas/algorithms behind it are not
copyrightable (17 U.S.C. §102(b); CJEU, *SAS Institute v. World Programming*, C-406/10 —
studying a program's functioning to build a compatible one is lawful). What's off-limits is the
reference server's *expression* of it — its code, comments, identifiers, file/class structure.
**Never** copy any of that from the `jellyfin/jellyfin` C# source tree into this repository, not
even a fragment kept "as a reference" in a comment. This boundary is what keeps the MIT license
clean; do not blur it for convenience. See D12 in
[`docs/00-decision-log.md`](docs/00-decision-log.md) for the full reasoning.

Prefer black-box observation — real client traffic, a running reference server, published API
docs (`docs/04`'s verification approach) — over reading the C# source by default. It's usually
enough for wire compatibility and needs nothing further. Reach for the C# source when black-box
behavior alone can't resolve something, **or whenever the maintainer explicitly asks for it** — an
explicit request is its own sufficient reason, not a fallback to second-guess or quietly
downgrade back to black-box-only. When you do, follow a strict two-phase process:

1. **Extraction.** Reading the source is allowed, but only to understand *behavior* — inputs,
   outputs, data flow, algorithm, state transitions. Write that understanding down as a
   functional spec in `docs/` before touching Go code. The spec describes what happens, never how
   the reference server's own code happens to be organized: no file/class/function names, no
   comments, no variable names, no code structure carried over.
2. **Implementation.** Go code is written only from that spec, plus black-box verification
   against a running reference server. Never implement with the C# source open in the same
   session — that's side-by-side reimplementation, which this process exists to prevent
   regardless of how the resulting code reads.

## Reading jellyfin-web (the client)

The clean-room requirement above is about the `jellyfin/jellyfin` C# **server** only. jellyfin-web
(github.com/jellyfin/jellyfin-web, GPL-2.0) is a separate, standalone client project this server
has to satisfy, not the reference implementation being reimplemented — reading its source carries
none of that restriction, and doing so is not "reading the reference server's source" in AGENTS.md's
sense above. Never copy its code into this repository either (it's GPL-2.0, this project is MIT —
same D9/D12 boundary as always), but reading it to understand what it does is unrestricted and
should be routine, not a last resort.

Do this proactively whenever verifying a client interaction — a button, a conditional render, an
action that fires (or doesn't fire) a request — not only after black-box observation alone fails to
explain something. Two failure modes black-box network capture cannot see through on its own:

- **A client-side guard can silently swallow a click.** If the client decides not to call anything
  (a lookup that came back empty/undefined, a condition that's never true), there is no request to
  capture, no 404 to notice — just silence that looks identical to "nothing happens on this page."
  Only reading the component's code shows the precondition being checked.
- **A `200` response's field *values*, not just its shape, can drive later client behavior.** Status
  code and field-presence diffing against the reference server won't catch a field whose exact
  *value* (an ID, a key, an enum string) is a hardcoded lookup target somewhere else in the client.

Found live: the Dashboard's "Scan All Libraries" button spun forever because `GET /ScheduledTasks`
returned `200` with the right shape but the wrong `Key`/`Id` *value* — jellyfin-web locates that
task by filtering on the literal string `"RefreshLibrary"`, a value nothing in this project's own
route-coverage passes had verified since the field looked auxiliary from Tinyfin's side. See
`docs/12`'s note for the same task and `docs/11`'s "Method" section for how this is now folded into
the standard route-coverage process.

One caveat, already on record in `docs/11`: different jellyfin-web builds can diverge in what they
actually send (the modern `@jellyfin/sdk` client vs. the legacy `jellyfin-apiclient` package
embedded in the same bundle disagree on some URL shapes). Reading source is a way to find *what to
verify*, not a substitute for confirming it live against the actual embedded bundle this project
ships (a Tinyfin deployment's own `/web`) — treat it as generating a hypothesis, not the final answer.

## Decisions-first workflow

- Open questions live in [`docs/00-decision-log.md`](docs/00-decision-log.md) until resolved.
- A resolved decision gets promoted into the relevant spec doc (`03` through `09`); the log entry
  then references where it landed instead of restating it.
- Don't make a structural call (storage interface shape, API route behavior, transcode pipeline
  contract) inside a PR without it first passing through the log — see
  [`CONTRIBUTING.md`](CONTRIBUTING.md).

## Where to go next

| Question | Doc |
|---|---|
| What's actually in scope for v1, what's deliberately deferred? | [`docs/01-vision-and-scope.md`](docs/01-vision-and-scope.md) |
| What does a given term mean in this project specifically? | [`docs/02-glossary.md`](docs/02-glossary.md) |
| How do the processes/roles fit together? | [`docs/03-architecture.md`](docs/03-architecture.md) |
| What API surface, and how is compatibility verified? | [`docs/04-api-compatibility.md`](docs/04-api-compatibility.md) |
| SQLite mode vs. scale mode — the actual interface | [`docs/05-storage-drivers.md`](docs/05-storage-drivers.md) |
| Session/realtime state — why it's separate from storage | [`docs/06-session-and-realtime-state.md`](docs/06-session-and-realtime-state.md) |
| Direct Play/Remux/Direct Stream/Transcode, device profiles, worker separation | [`docs/07-transcoding-pipeline.md`](docs/07-transcoding-pipeline.md) |
| Scanner scope and the metadata provider boundary | [`docs/08-library-scanning-and-metadata.md`](docs/08-library-scanning-and-metadata.md) |
| Concrete memory/performance targets and why | [`docs/09-memory-and-performance-goals.md`](docs/09-memory-and-performance-goals.md) |
| Delivery order | [`docs/10-roadmap.md`](docs/10-roadmap.md) |
| Where does Tinyfin deliberately differ from the reference, and why? | [`docs/13-divergences.md`](docs/13-divergences.md) |
| What's actually done so far | [`ROADMAP.md`](ROADMAP.md) |
