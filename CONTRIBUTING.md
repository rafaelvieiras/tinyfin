# Contributing to Tinyfin

Thanks for your interest. Tinyfin is free software under the [MIT license](LICENSE). This guide
summarizes how the project works — read it before opening a substantial PR, because the process
is deliberately decision-and-documentation-first, not just code-first.

> **Status:** `Phase 0 — foundation documentation`. Work right now is entirely about deciding and
> writing down the model before any code exists.

## How the project thinks

Six principles govern any contribution (detailed in the [README](README.md)):

1. **Idle memory is a budget, not an afterthought.** No full-catalog in-memory cache.
2. **Single binary, zero required dependencies, by default.**
3. **Scale is a mode the operator turns on, not a rewrite.**
4. **Wire compatibility is the product.** Breaking an existing client is a defect.
5. **Transcoding is delegated, not reinvented.**
6. **Decisions are recorded before code is written.**

A PR that contradicts one of these will likely be declined — open a discussion first.

## Decisions before code

Foundational decisions are tracked, not improvised inside a PR:

- [`docs/00-decision-log.md`](docs/00-decision-log.md) is the living backlog: every item starts
  `open`, moves through `in discussion`, and becomes `decided` once settled.
- A decided item gets promoted into the relevant spec document (`docs/03`, `docs/04`, ...) — the
  decision log entry then points at where it landed, it does not stay as the source of truth.

Before proposing a structural change (storage interface, API surface, transcode pipeline),
check whether it touches an already-decided item. If it does, the path is proposing a new,
explicit revision of that decision — not editing the spec document directly without discussion.

## API compatibility is non-negotiable

Any change to request/response shapes, headers, or status codes on an endpoint that mirrors a
real Jellyfin API route must be checked against actual client behavior (see
[`docs/04-api-compatibility.md`](docs/04-api-compatibility.md) for the verification approach)
before merging. "It's cleaner this way" is not a justification for diverging from the wire
format an existing client depends on.

## Contribution flow

1. **Open an issue** describing the problem or idea before writing a significant amount of code.
2. **Fork and branch** from `main` (e.g. `feat/direct-play-negotiation`).
3. **Match the surrounding stack and style** — Go on the backend (see
   [`docs/03-architecture.md`](docs/03-architecture.md)).
4. **Write in English** — code, comments, commit messages, and all versioned documentation.
5. **Commit messages** follow `type: summary` (e.g. `docs:`, `feat:`, `fix:`, `refactor:`), imperative mood.
6. **Open the PR** referencing the issue and, where applicable, the decision-log entry it resolves.

## Security

**No secrets in code, ever.** If you find a credential or key committed to history, report it
privately rather than opening a public issue, so it can be rotated first.

## License of your contribution

By contributing, you agree to license your work under the project's MIT license.
