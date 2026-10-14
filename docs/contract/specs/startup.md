# Spec: startup (first-run setup wizard)

Wire-level behavior of the `/Startup/*` routes of a Jellyfin 12+ server and of the first-run state around them. Every rule has a stable ID (`startup.<topic>.R<n>`) that scenarios in `../scenarios/startup.json` reference through `covers`. Observed black-box against a 12.2 reference server started from an empty configuration; each observation consumes the wizard, so the probes ran on many fresh servers.

Scenarios of this area are tagged `fresh-server`: they need a server that has **never completed setup** and no accounts (the wizard can run once), so the runner executes them only with `-fresh-server` (see `../README.md`), in file order, against one server that is consumed by the run. They are excluded from every normal run.

Index of routes:

| Route | Scope | Section |
|---|---|---|
| `GET /Startup/Configuration`, `POST /Startup/Configuration` | modern | [configuration](#configuration) |
| `GET /Startup/User`, `GET /Startup/FirstUser`, `POST /Startup/User` | modern | [user](#user) |
| `POST /Startup/RemoteAccess` | modern | [remote-access](#remote-access) |
| `POST /Startup/Complete` | modern | [complete](#complete) |

Routes outside `/Startup` whose answer depends on the wizard state are covered under [state](#state).

---

<a id="state"></a>
## Wizard state

A server is *set up* once `POST /Startup/Complete` has been accepted. A server that never completed setup is *fresh*; it has no accounts.

- **startup.state.R1** `StartupWizardCompleted` in `GET /System/Info/Public` and `GET /System/Info` is `false` on a fresh server and `true` once setup is complete. Nothing but `POST /Startup/Complete` changes it: creating the account, saving the configuration or logging in does not.
- **startup.state.R2** `GET /System/Info` is answered to anonymous callers (`200`) while setup is not complete, and requires authentication (`401` anonymous) afterwards.
- **startup.state.R3** On a fresh server, before the first account exists: `GET /Users/Public` is `200` with `[]`; `GET /Branding/Configuration` is `200` with `{"SplashscreenEnabled":false}`; `POST /Users/AuthenticateByName` for any name is `401`; `GET /Users`, `POST /Users/New` and `GET /System/Configuration` are `401` for anonymous callers.
- **startup.state.R4** `POST /Users/New` is **not** reachable anonymously at any time: before and after completion it needs an administrator. Accounts are created through `POST /Startup/User` (see [user](#user)) while the wizard is open.

---

<a id="access"></a>
## Access rules

- **startup.access.R1** While setup is not complete, every `/Startup` route accepts every caller: no credential, an unknown token, or a valid one.
- **startup.access.R2** Once setup is complete, every `/Startup` route answers `401` to a caller without a valid credential (no token, unknown or revoked token), whatever the method and body.
- **startup.access.R3** Once setup is complete, a caller authenticated as a non-administrator account gets `403` on every `/Startup` route.
- **startup.access.R4** Once setup is complete, an administrator (account token or API key) may still use the routes: `GET` answers `200`, `POST /Startup/Configuration`, `POST /Startup/RemoteAccess` and `POST /Startup/Complete` answer `204`, and `POST /Startup/User` answers `403` (the first account is already configured, see `startup.user.R6`).
- **startup.access.R5** Only the documented methods exist: `GET /Startup/RemoteAccess` and `POST /Startup/FirstUser` answer `405` (also before completion).

---

<a id="configuration"></a>
## GET and POST /Startup/Configuration

**Purpose.** The first wizard page: server name, UI language and metadata defaults.

- **startup.config.R1** `GET /Startup/Configuration` is `200` with an object of exactly four string members: `ServerName`, `UICulture`, `MetadataCountryCode`, `PreferredMetadataLanguage`. On a fresh server they are `""`, `"en-US"`, `"US"` and `"en"`.
- **startup.config.R2** `POST /Startup/Configuration` answers `204` and **replaces** all four values: a member that is absent or `null` is stored as `""` (it is not left alone). Values are not validated: `"xx-ZZ"` is accepted. Posting the same body twice is idempotent. A body that is not valid JSON answers `400` (a problem document, `wire.problem.R2`) and changes nothing.
- **startup.config.R3** The stored values are the server's configuration: `GET /System/Configuration` (administrator) reports the same `ServerName`, `UICulture`, `MetadataCountryCode` and `PreferredMetadataLanguage`. A non-empty `ServerName` is what `GET /System/Info/Public` and `GET /System/Info` report as `ServerName`; an empty one makes them report the server's default name (the Startup configuration itself keeps answering `""`).

---

<a id="user"></a>
## GET and POST /Startup/User

**Purpose.** The second wizard page: the first administrator account.

- **startup.user.R1** `GET /Startup/User` and `GET /Startup/FirstUser` answer `200` with `{"Name":"<name>"}`. Before the account is configured the name is `"root"`; afterwards it is the configured account's name (the response is the same JSON object, only `Name`).
- **startup.user.R2** `POST /Startup/User` before the first `GET /Startup/User` of the wizard answers `404` (a problem document, `wire.problem.R1`): the page that offers the account has to be opened first.
- **startup.user.R3** Validation order: (1) the password is missing, `null`, empty or only whitespace: `400` with the JSON string body `"Password must not be empty"`; (2) a given `Name` that is not a valid account name (empty, only whitespace, leading or trailing whitespace, a character outside letters, digits, space and `-._@+'`): `400` with the plain-text body `Error processing request.`. Nothing is created or changed when either fails.
- **startup.user.R4** Success answers `204` with an empty body and creates the first account: an administrator named `Name` (`"root"` when `Name` is absent or `null`) with the given password. Its policy is the default policy of `POST /Users/New` (`users.md`) with `IsAdministrator`, `EnableContentDeletion` and `EnableRemoteControlOfOtherUsers` all `true`; it stays hidden from `GET /Users/Public`. A password of any length and content is accepted (no complexity rule).
- **startup.user.R5** The account can sign in with `POST /Users/AuthenticateByName` as soon as it exists, before `POST /Startup/Complete`, and its token is a normal administrator token (it may call `POST /Users/New`).
- **startup.user.R6** Once the first account is configured, `POST /Startup/User` answers `403` for any body, before and after completion; nothing changes. The check precedes body validation (`startup.user.R3`).
- **startup.user.R7** `GET /Startup/User` after the account is configured returns its name.

---

<a id="remote-access"></a>
## POST /Startup/RemoteAccess

**Purpose.** The third wizard page: remote access and automatic port mapping.

- **startup.remote.R1** Answers `204`. A body that is not valid JSON answers `400` (`wire.problem.R2`) and changes nothing. There is no `GET` (`startup.access.R5`).
- **startup.remote.R2** `EnableRemoteAccess` is stored as the network configuration's `EnableRemoteAccess` (`GET /System/Configuration/network`, administrator); an absent member stores `false`. `EnableAutomaticPortMapping` is accepted and has no effect: the network configuration's `EnableUPnP` stays `false` for every value.

---

<a id="complete"></a>
## POST /Startup/Complete

- **startup.complete.R1** Answers `204` with an empty body and marks setup complete (`startup.state.R1`); from then on `startup.access.R2`-`startup.access.R4` apply. Posting it again as an administrator is `204` with no further effect.
- **startup.complete.R2** [untestable: needs a second fresh server in the same run; covered by Go tests] Divergence, safety. The reference accepts `POST /Startup/Complete` when no account was ever configured, which leaves a server nobody can sign in to and whose setup routes are closed. Tinyfin answers `400` (a problem document) and leaves setup open, because no client can need the permissive outcome: it is a dead end for the web client as well.
- **startup.complete.R3** [untestable: needs a server that already holds accounts; covered by Go tests] A server whose database already holds accounts, or whose administrator was created out of band, is set up: `StartupWizardCompleted` is `true` and the wizard's anonymous access (`startup.access.R1`) never re-opens, also if every account is deleted later.

---

<a id="safety"></a>
## Safety divergences from the reference (Tinyfin)

The reference creates the account `root` with an *empty password* as soon as `GET /Startup/User` is called, and that account (an administrator) can be signed in to anonymously with `POST /Users/AuthenticateByName` (`Username` `root`, `Pw` empty) until the page's `POST` sets the password: on a server exposed before the wizard is finished, or left unfinished, anyone gets an administrator session.

- **startup.safety.R1** Divergence. Tinyfin creates no account on `GET /Startup/User`: the answer `{"Name":"root"}` is only the default offered name, the account appears with `POST /Startup/User` and always carries the posted password. `POST /Users/AuthenticateByName` as `root` with an empty password is `401` after `GET /Startup/User`. The wire-visible outcome of the wizard flow is otherwise the same; no client relies on the unauthenticated administrator.
