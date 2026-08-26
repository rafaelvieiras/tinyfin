# Roadmap

> Checklist companion to [`docs/10-roadmap.md`](docs/10-roadmap.md), which explains *why* the
> phases are ordered this way and what each one's success criteria are. This file only tracks
> *what's done*. When a phase's goal is met, its box gets checked here — the reasoning stays in
> `docs/10-roadmap.md` and doesn't get duplicated.

Legend: `[x]` done · `[ ]` not started · `[~]` in progress

---

## Phase 0 — Foundation documentation

- [x] Vision and scope written (`docs/01`)
- [x] Glossary written (`docs/02`)
- [x] Architecture (roles, process layout) written (`docs/03`)
- [x] API compatibility surface and verification approach written (`docs/04`)
- [x] Storage driver design written (`docs/05`)
- [x] Session/realtime-state design written (`docs/06`)
- [x] Transcoding pipeline design written (`docs/07`)
- [x] Library scanning and metadata provider boundary written (`docs/08`)
- [x] Memory and performance goals written (`docs/09`)
- [x] Foundational decisions D1–D8 closed (`docs/00-decision-log.md`)
- [x] Repository created and pushed (private, `github.com/rafaelvieiras/tinyfin`)
- [ ] **O2** — initial device profile list written down (`docs/07`)
- [ ] **O3** — storage driver Go interface signature written down (`docs/05`)

Phase 0 isn't done until O2 and O3 are resolved — they're the two open items that block starting
Phase 1 (see `docs/10-roadmap.md`).

## Phase 1 — Compatibility core, single-node, embedded storage only

- [ ] Embedded SQLite storage driver implementing the interface from O3
- [ ] API/metadata role: library browsing, users, sessions, images, subtitles
- [ ] Direct Play only, against the small device-profile set from O2
- [ ] **Goal:** a real Jellyfin client browses a real library and plays a Direct-Play-compatible
      file end to end.

## Phase 2 — Transcoding pipeline

- [ ] Transcode worker role, `jellyfin-ffmpeg` integration
- [ ] Full Direct Play / Remux / Direct Stream / Transcode decision logic
- [ ] **Goal:** a client whose device profile forces a transcode gets a correct HLS stream.

## Phase 3 — Metadata provider interface

- [ ] Provider interface implementation
- [ ] Direct TMDB/TVDB provider (reference implementation)
- [ ] **Goal:** a freshly scanned library gets enriched metadata without Tinyfin implementing its
      own scraping.

## Phase 4 — Scale mode

- [ ] Scale-mode storage driver (write leader + streaming read replicas)
- [ ] Scale-mode session/realtime-state driver (Redis/Valkey pub-sub)
- [ ] Transcode worker role actually split onto a separate machine from the API/metadata role
- [ ] **Goal:** two or more API/metadata nodes serve reads correctly against one write leader, and
      a client's session survives being routed to a different node (or sticky routing is
      documented as the accepted alternative).

## Deliberately not scheduled

Live TV/DVR, a generic plugin-loading system, DLNA, offline mobile sync — see decision **D8** in
`docs/00-decision-log.md`. Not phased in above; revisit only if the target use case changes.
