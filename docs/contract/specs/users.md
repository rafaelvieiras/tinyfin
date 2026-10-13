# Spec: users (authentication and user accounts)

Wire-level behavior of the account and authentication routes of a Jellyfin 12+ server. Every rule has a stable ID (`users.<topic>.R<n>`) that scenarios in `../scenarios/users.json` reference through `covers`. Scope follows `../README.md`: current routes are `modern`; three aliases the official web client still calls are `legacy-web-required`; legacy authentication channels appear only as `scope-negative` scenarios.

Routes **not** specified here (documented by the lead as excluded legacy): `POST /Users/{userId}/Authenticate` (replaced by `POST /Users/AuthenticateByName`).

## Conventions used in this area

- All bodies and answers are JSON (`Content-Type: application/json`). A request body that is not valid JSON, or whose enum values are invalid, answers `400`.
- User and device identifiers: user ids are GUID strings. An id in a path or query that is not a GUID answers `400`.
- **Roles.** `Administrator` is a user whose policy has `IsAdministrator=true` (or an API key). Role is re-evaluated on every request, so policy changes apply to existing tokens immediately.
- **Status vocabulary.** `401` = no valid credential (none, unknown/revoked token, disabled account). `403` = authenticated but not allowed. `400` = malformed request or rejected value. `404` = unknown user or secret.
- **Local-time rules.** `AccessSchedules` are evaluated in the server's local time zone.
- **State between scenarios.** Scenarios create throw-away accounts (prefix `tmp-`) and delete them. A scenario that logs in itself uses its own `DeviceId` because a new login on a `DeviceId` revokes that device's previous token (`users.auth.R10`).

The *user object* (returned by `GET /Users/{userId}`, `GET /Users/Me`, `GET /Users`, `GET /Users/Public`, `POST /Users/New` and inside the login answers) has these fields that clients rely on: `Id` (string), `Name`, `ServerId` (string), `HasPassword` (boolean), `HasConfiguredPassword` (boolean), `LastLoginDate` / `LastActivityDate` (ISO-8601, absent or null until the user has logged in), `Configuration` (object, see *Configuration* below) and `Policy` (object, see *Policy* below).

Default **Configuration** (new accounts): `SubtitleMode="Default"`, `PlayDefaultAudioTrack=true`, `HidePlayedInLatest=true`, `DisplayMissingEpisodes=false`, `DisplayCollectionsView=false`, `EnableNextEpisodeAutoPlay=true`, `RememberAudioSelections=true`, `RememberSubtitleSelections=true`, `EnableLocalPassword=false`, language preferences null/empty.

The **Policy** object carries: `IsAdministrator`, `IsDisabled`, `IsHidden`, `EnableRemoteAccess`, `EnableAllFolders` / `EnabledFolders`, `EnableAllChannels` / `EnabledChannels`, `EnableAllDevices` / `EnabledDevices`, `BlockedTags`, `AllowedTags`, `BlockUnratedItems`, `MaxParentalRating` / `MaxParentalSubRating`, `AccessSchedules`, `EnableMediaPlayback`, `EnableAudioPlaybackTranscoding`, `EnableVideoPlaybackTranscoding`, `EnablePlaybackRemuxing`, `ForceRemoteSourceTranscoding`, `EnableContentDownloading`, `EnableContentDeletion`, `EnableContentDeletionFromFolders`, `EnableLiveTvAccess`, `EnableLiveTvManagement`, `EnableCollectionManagement`, `EnableSubtitleManagement`, `EnableLyricManagement`, `EnableMediaConversion`, `EnableSharedDeviceControl`, `EnableRemoteControlOfOtherUsers`, `EnablePublicSharing`, `EnableSyncTranscoding`, `EnableUserPreferenceAccess`, `LoginAttemptsBeforeLockout`, `InvalidLoginAttemptCount`, `MaxActiveSessions`, `RemoteClientBitrateLimit`, `SyncPlayAccess` (`CreateAndJoinGroups` | `JoinGroups` | `None`), `AuthenticationProviderId`, `PasswordResetProviderId`. The legacy fields `BlockedMediaFolders` and `BlockedChannels` are accepted but have no effect.

Default values of a newly created account: true for `IsHidden`, `EnableAllChannels`, `EnableAllDevices`, `EnableAllFolders`, `EnableContentDownloading`, `EnableMediaConversion`, `EnableMediaPlayback`, `EnablePlaybackRemuxing`, `EnablePublicSharing`, `EnableRemoteAccess`, `EnableSyncTranscoding`, `EnableAudioPlaybackTranscoding`, `EnableLiveTvAccess`, `EnableSharedDeviceControl`, `EnableVideoPlaybackTranscoding`; false for `IsAdministrator`, `IsDisabled`, `EnableContentDeletion`, `EnableLiveTvManagement`, `ForceRemoteSourceTranscoding`, `EnableRemoteControlOfOtherUsers`, `EnableCollectionManagement`, `EnableSubtitleManagement`, `EnableLyricManagement`.

Index of routes:

| Route | Scope | Section |
|---|---|---|
| `POST /Users/AuthenticateByName` | modern | [authenticate-by-name](#authenticate-by-name) |
| (all authenticated routes) | modern | [authenticated-requests](#authenticated-requests) |
| `POST /Sessions/Logout` | modern | [authenticated-requests](#authenticated-requests) |
| `POST /Users/AuthenticateWithQuickConnect` | modern | [authenticate-with-quick-connect](#authenticate-with-quick-connect) |
| `GET /Users/Public` | modern | [list-public-users](#list-public-users) |
| `GET /Users` | modern | [list-users](#list-users) |
| `GET /Users/{userId}` | modern | [get-user](#get-user) |
| `GET /Users/Me` | modern | [get-current-user](#get-current-user) |
| `POST /Users/New` | modern | [create-user](#create-user) |
| `DELETE /Users/{userId}` | modern | [delete-user](#delete-user) |
| `POST /Users/{userId}/Policy` | modern | [update-user-policy](#update-user-policy) |
| `POST /Users/Password` | modern | [update-password](#update-password) |
| `POST /Users/{userId}/Password` | legacy-web-required | [update-password-legacy-alias](#update-password-legacy-alias) |
| `POST /Users` | modern | [update-user](#update-user) |
| `POST /Users/{userId}` | legacy-web-required | [update-user-legacy-alias](#update-user-legacy-alias) |
| `POST /Users/Configuration` | modern | [update-user-configuration](#update-user-configuration) |
| `POST /Users/{userId}/Configuration` | legacy-web-required | [update-user-configuration-legacy-alias](#update-user-configuration-legacy-alias) |
| `POST /Users/ForgotPassword` | modern | [forgot-password](#forgot-password) |
| `POST /Users/ForgotPassword/Pin` | modern | [forgot-password-pin](#forgot-password-pin) |
| `GET /socket` (WebSocket) | modern | [realtime-socket](#realtime-socket) |

---

<a id="authenticate-by-name"></a>
## POST /Users/AuthenticateByName

**Purpose.** Username-and-password login. Creates a device token and a session.

**Auth.** Anonymous. The caller identifies itself with `Authorization: MediaBrowser Client="...", Device="...", DeviceId="...", Version="..."`.

**Request.**

| Part | Name | Effect |
|---|---|---|
| header | `Authorization` | `MediaBrowser` scheme; `Client`, `Device`, `DeviceId`, `Version` all required; `Token` optional and ignored for the credential check (an old token sent along does not block the login). |
| body (JSON) | `Username` | Account name, case-insensitive. Missing/empty/blank: `400`. |
| body (JSON) | `Pw` | Plain-text password. Omitted or empty only matches accounts without a password. |

**Responses.**

| Status | Condition |
|---|---|
| `200` | Credentials accepted and every access restriction passed. Body: `User`, `SessionInfo`, `AccessToken`, `ServerId`. |
| `400` | Missing `Client`/`Device`/`DeviceId`/`Version`, no usable `Authorization` header, missing `Username`, malformed body. |
| `401` | Unknown user or wrong password (including empty `Pw` for an account with a password). |
| `403` | Account disabled (checked before the password result is used), or `EnableRemoteAccess=false` from outside the local network, or outside `AccessSchedules`, or device not allowed, or `MaxActiveSessions` reached. A lockout caused by too many failures turns the account disabled, so later attempts answer `403`. |
| `503` | Request blocked by a network access rule (not specified further). |

**Order of checks.** header fields (`400`) -> username (`400`) -> account lookup and password -> disabled / remote access / schedule (`403`) -> device allow-list and session limit (`403`) -> token issued.

**Side effects.** A new token (and device record) is created; the user's previous token for the same `DeviceId` is revoked; `LastLoginDate` is updated; the failure counter is reset on success and incremented on a password failure for an existing, otherwise permitted account. Failures for unknown users touch no counter.


**Rules**

- **users.auth.R1** On success the response is `200` with a JSON object `{User, SessionInfo, AccessToken, ServerId}`: `User` is the full user object (same shape as `GET /Users/{userId}`), `SessionInfo` describes the session just created, `AccessToken` is a newly minted token of 32 lowercase hexadecimal characters and `ServerId` is a string.
- **users.auth.R2** The `Username` match is case-insensitive.
- **users.auth.R3** A wrong password for an existing account answers `401`. No token is issued.
- **users.auth.R4** An unknown username answers `401`, indistinguishable from a wrong password (no account enumeration through the status).
- **users.auth.R5** A missing, empty or blank `Username` answers `400`.
- **users.auth.R6** The `Authorization` header must be present, use the `MediaBrowser` scheme and carry non-empty `Client`, `Device`, `DeviceId` and `Version`. If any is missing the answer is `400` (not `401`), even when the credentials are wrong; this check comes before the credential check.
- **users.auth.R7** An account that has no password accepts an empty or omitted `Pw`. An account that has a password answers `401` for an empty or omitted `Pw`, and an account without a password answers `401` for a non-empty `Pw`.
- **users.auth.R8** A disabled account answers `403`, whether or not the password is correct. The refusal is not counted as a failed attempt.
- **users.auth.R9** The returned token authenticates later requests when sent as `Authorization: MediaBrowser ..., Token="<token>"`; the requests then act as that user.
- **users.auth.R10** A token is bound to one (user, `DeviceId`) pair. A new successful login by the same user from the same `DeviceId` revokes that pair's previous token (it answers `401` from then on). Tokens of other devices of the same user, and of other users using the same `DeviceId`, are not affected. Each login yields a different token.
- **users.auth.R11** Header parsing: the scheme name is matched case-insensitively; the remainder is a comma-separated list of `Key="value"` pairs; a comma inside a quoted value does not end the pair; values are percent-decoded (`%20` is a space, `+` is a space); keys are case-sensitive (`Client`, `Device`, `DeviceId`, `Version`, `Token`); when a key repeats, the last occurrence wins.
- **users.auth.R12** `SessionInfo.UserId`, `SessionInfo.DeviceId`, `SessionInfo.Client` and `SessionInfo.DeviceName` reflect the authenticated user and the `Client`, `DeviceId` and `Device` sent in the header.
- **users.auth.R13** By default (`LoginAttemptsBeforeLockout` is `-1`) there is no lockout: any number of consecutive wrong passwords is answered `401` each time and the correct password keeps working. There is also no rate limit.
- **users.auth.R14** When the user's policy sets `LoginAttemptsBeforeLockout` to a positive number `n`, the `n`-th consecutive failed password attempt disables the account: from then on every login (even with the correct password) answers `403` and the account's existing tokens answer `401`. A value of `0` behaves as `3`. An administrator can re-enable the account by posting its policy with `IsDisabled=false` and `InvalidLoginAttemptCount=0`.
- **users.auth.R15** A successful login resets the consecutive-failure counter, so failures separated by a success do not accumulate towards the lockout.
- **users.auth.R16** When the user's `MaxActiveSessions` is `n >= 1` and the user already has `n` active sessions, a login from another device answers `403`. `0` means unlimited.
- **users.auth.R17** When the user's `EnableAllDevices` is `false`, a login from a `DeviceId` that is not in `EnabledDevices` answers `403`. Saving such a policy also revokes the user's tokens issued to devices that are not allowed.
- **users.auth.R18** When the user has `AccessSchedules`, a login outside every allowed window answers `403` (day of week in the server's local time, `StartHour <= hour <= EndHour`). No schedule means no restriction. Unlike requests that are already authenticated, the login restriction applies to administrators too.
- **users.auth.R19** A body that is not valid JSON answers `400`.
- **users.auth.R20** The password field is named `Pw`. A body that sends the password under another name (for example `Password`) is treated as having no `Pw`.
- **users.auth.R21** A user with `EnableRemoteAccess=false` is refused with `403` when the login comes from outside the local network, even with correct credentials. "Local network" is loopback plus, when no `LocalNetworkSubnets` are configured, the private and link-local ranges; once the network configuration section (`GET`/`POST /System/Configuration/network`, administrator) lists subnets, only those and loopback are local, so a private address outside them is remote (confirmed on the reference: the runner's address, seen as a private container-gateway address, got `403` after `LocalNetworkSubnets` was set to a range not containing it; `X-Forwarded-For` has no effect). Observable from a runner whose peer address is not loopback (`users.auth.remote-access-disabled`).
- **users.auth.R22** A successful login updates the user's `LastLoginDate` (and `LastActivityDate`).
- **users.auth.R23** A successful login (password login or QuickConnect exchange) writes two activity-log entries, in this order: `SessionStarted`, then `AuthenticationSucceeded`. Both have `Severity` `Information`, `UserId` of the user who logged in and no `ItemId`. Listed newest first (`GET /System/ActivityLog/Entries`, administrator) the `AuthenticationSucceeded` entry is directly above the `SessionStarted` one, and both are visible before the login response is read.
- **users.auth.R24** The entry texts are `<user name> is online from <device name>` (`SessionStarted`) and `<user name> successfully authenticated` (`AuthenticationSucceeded`). `<user name>` is the account's stored name, not the (case-insensitive) spelling sent; `<device name>` is the percent-decoded `Device` of the login (for the QuickConnect exchange, the `Device` the secret was issued to), not `Client`.
- **users.auth.R25** `ShortOverview` of both login entries is `IP address: <remote address>`; for a QuickConnect exchange the address part is empty (`IP address: `).
- **users.auth.R26** A wrong password for an existing, enabled account writes one `AuthenticationFailed` entry: `Severity` `Error`, `Name` `Failed login attempt from <username>` with the username exactly as sent, `ShortOverview` as in `users.auth.R25`, and `UserId` the all-zero id (`00000000000000000000000000000000`). It is therefore returned by `hasUserId=false` and not by `hasUserId=true`.
- **users.auth.R27** No activity entry is written for an unknown username, for a disabled account (`403`, whether or not the password is correct, including after a lockout) or for a malformed request (`400`).
- **users.auth.R28** The failed attempt that triggers a lockout (`users.auth.R14`) also writes a `UserLockedOut` entry: `Severity` `Error`, `Name` `User <user name> has been locked out` (the account's stored name), `UserId` of the locked account (so `hasUserId=true` finds it), no `ShortOverview` and no `ItemId`. A lockout caused by a wrong `CurrentPw` on `POST /Users/Password` writes the same entry (`users.password.R16`).
- **users.auth.R29** On a password login the lockout entry is written between the entry of the previous failure and the attempt's own `AuthenticationFailed` entry: listed newest first, `AuthenticationFailed` (this attempt), `UserLockedOut`, `AuthenticationFailed` (the earlier failure). Later attempts on the locked account (`403`) write nothing.
- **users.auth.R30** A lockout does not revoke the account's tokens and writes no `SessionEnded` entry: the tokens answer `401` while the account is disabled (`users.token.R4`) and work again once an administrator re-enables the account.
- **users.auth.R31** When a successful login replaces the same device's previous token (`users.auth.R10`; password login or QuickConnect exchange), a `SessionEnded` entry is written for the replaced session, just before the new login's `SessionStarted` (listed newest first: `AuthenticationSucceeded`, `SessionStarted`, `SessionEnded`). A first login on a device, or a login on another device, writes none.
- **users.auth.R32** A `SessionEnded` entry has `Severity` `Information`, `UserId` of the session's user, no `ItemId` and `Name` `<user name> has disconnected from <device name>`, where `<device name>` is the `Device` recorded when the ended session was created (not the name of the login that replaces it). `ShortOverview` is `IP address: <address>` with the address recorded when the ended session was created: empty (`IP address: `) for a session created by a QuickConnect exchange, the caller's address for a password login.

---

<a id="authenticated-requests"></a>
## Authenticated requests (credential channels)

**Purpose.** How a request proves who it is, shared by every route in this area.

| Channel | Honored |
|---|---|
| `Authorization: MediaBrowser Client="...", Device="...", DeviceId="...", Version="...", Token="<token>"` | yes |
| `?ApiKey=<token>` query parameter | yes (user tokens and API keys) |
| `X-Emby-Authorization`, `X-Emby-Token`, `X-MediaBrowser-Token`, `Authorization: Emby ...`, `?api_key=` | **no** (`scope-negative` scenarios) |

When both the header token and `ApiKey` are present the header wins. Protected routes answer `401` for no credential, unknown/revoked token or disabled account, `403` for an authenticated caller that lacks the right. Public routes ignore an invalid token.


**Rules**

- **users.token.R1** A request carrying `Authorization: MediaBrowser ..., Token="<token>"` with a valid token is authenticated as the token's user.
- **users.token.R2** A token that is unknown, malformed or revoked answers `401` on endpoints that require authentication. An empty `Token=""` is treated as no credential (`401`). Endpoints that are public (for example `GET /Users/Public`) ignore an invalid token and answer normally.
- **users.token.R3** The `ApiKey` query parameter is honored as a token carrier (it accepts a user token as well as an API key). An invalid value answers `401`.
- **users.token.R4** Tokens of a disabled account stop working (`401`) even though they were valid when issued; disabling an account through its policy also revokes its tokens.
- **users.token.R5** Deleting a user revokes all of the user's tokens (`401` afterwards).
- **users.token.R6** Legacy channels are not honored: `X-Emby-Authorization` (login without a usable `Authorization` header answers `400`), `X-Emby-Token`, `X-MediaBrowser-Token`, `Authorization: Emby ...` and the `api_key` query parameter all behave as no credential (`401` on protected endpoints). Only `Authorization: MediaBrowser ...` and `ApiKey` are honored.
- **users.token.R7** `POST /Sessions/Logout` with a valid user token revokes that token (and only that one) and answers `204` with an empty body; the token answers `401` afterwards and the user's tokens of other devices keep working.
- **users.token.R8** `POST /Sessions/Logout` without a credential, or with an unknown or already revoked token, answers `401` and writes nothing. A successful logout writes a `SessionEnded` entry (`users.auth.R32`).
- **users.token.R9** Every token that the server revokes writes one `SessionEnded` entry (`users.auth.R32`): by logout (`R7`), by a password change (`users.password.R15`), by disabling the account or restricting its devices (`users.policy.R14`) and by deleting the user (`users.delete.R8`). When one operation revokes several tokens the entries are written in token creation order (oldest token first, so a newest-first listing shows the newest token first). A lockout (`users.auth.R30`) and `ResetPassword` (`users.password.R9`) revoke nothing and write none.

---

<a id="authenticate-with-quick-connect"></a>
## POST /Users/AuthenticateWithQuickConnect

**Purpose.** Trade an authorized QuickConnect secret for a normal login result.

**Auth.** Anonymous (possession of an authorized secret is the credential). The `Authorization` header is not read here; the device identity was fixed when the secret was issued.

**Request.** Body `{"Secret": "<secret from POST /QuickConnect/Initiate>"}`.

**Responses.** `200` login result; `404` secret unknown, expired or not yet authorized; `400` missing `Secret`; `401` QuickConnect disabled.

**Precondition for the scenarios.** The server has QuickConnect enabled (`GET /QuickConnect/Enabled` is `true`). The flows below need no second device: one script plays both the TV (`Initiate`, poll) and the signed-in phone (`Authorize`). `Initiate`/`Authorize` themselves are specified in the QuickConnect area; here only their effect on the exchange is used.

**Rules and scenarios.** The exchange, and the `Initiate` / `Connect` / `Authorize` routes it depends on, are specified in [`quickconnect.md`](quickconnect.md) (rules `quickconnect.<topic>.R<n>`, scenarios in `../scenarios/quickconnect.json`).

---

<a id="list-public-users"></a>
## GET /Users/Public

**Purpose.** Accounts to show on the login screen.

**Auth.** None. **Request.** No parameters.

**Responses.** `200` JSON array of user objects; never an error. The list shows only accounts that are neither hidden nor disabled.


**Rules**

- **users.public.R1** Anonymous access is allowed. The answer is `200` with a JSON array.
- **users.public.R2** Users whose policy has `IsHidden=true` are not listed. New accounts are hidden until an administrator clears `IsHidden`.
- **users.public.R3** Users whose policy has `IsDisabled=true` are not listed.
- **users.public.R4** Each entry is a full user object (`Id`, `Name`, `Policy`, `Configuration`, ...), not a reduced one.
- **users.public.R5** Entries are ordered by `Name` ascending.
- **users.public.R6** A request carrying an invalid or unknown token is served as an anonymous request (still `200`). When the caller is outside the local network, accounts without `EnableRemoteAccess` are omitted (not observable from a local runner).

---

<a id="list-users"></a>
## GET /Users

**Purpose.** All accounts, for administration screens.

**Auth.** Any authenticated user (not only administrators).

| Query | Default | Effect |
|---|---|---|
| `isHidden` | unset | `true`: only hidden; `false`: only not hidden; unset: both |
| `isDisabled` | unset | `true`: only disabled; `false`: only not disabled; unset: both |

**Responses.** `200` array of user objects; `401` unauthenticated; `403` non-administrator outside their `AccessSchedules`.

**Security note.** A non-administrator receives every account with its full `Policy`. This is the reference behavior (finding S4) and is replicated; see *Open questions*.


**Rules**

- **users.list.R1** Requires authentication (`401` for anonymous or invalid token). Any authenticated user, administrator or not, receives the complete list; there is no per-caller filtering.
- **users.list.R2** Without filters the list includes hidden and disabled users.
- **users.list.R3** `isHidden=true` returns only accounts with `IsHidden=true`; `isHidden=false` returns only the others.
- **users.list.R4** `isDisabled=true` returns only accounts with `IsDisabled=true`; `isDisabled=false` returns only the others. Both filters may be combined (logical AND).
- **users.list.R5** The list is ordered by `Name` ascending.
- **users.list.R6** Each entry is a full user object including `Policy` and `Configuration`.
- **users.list.R7** For a non-administrator who is outside an `AccessSchedule` window the request is refused with `403`.
- **users.list.R8** An API key (a credential that belongs to no user) is accepted as an administrator credential: the list is returned.

---

<a id="get-user"></a>
## GET /Users/{userId}

**Purpose.** One account by id.

**Auth.** Any authenticated user. The route is exempt from the access-schedule check but not from the remote-access check.

**Responses.** `200` user object; `400` empty or malformed id; `401`; `404` unknown id.


**Rules**

- **users.get.R1** `200` with the user object: `Id`, `Name`, `ServerId`, `HasPassword` (boolean), `LastLoginDate` (when the user has logged in), `Configuration` (object) and `Policy` (object).
- **users.get.R2** Any authenticated user may read any other user, including `Policy`; there is no self/admin check.
- **users.get.R3** An unknown id answers `404`.
- **users.get.R4** The empty id `00000000-0000-0000-0000-000000000000` answers `400`.
- **users.get.R5** An id that is not a GUID answers `400`.
- **users.get.R6** Anonymous or invalid token answers `401`.
- **users.get.R7** A user outside an `AccessSchedule` window can still read their own profile (this route is exempt from the schedule check).
- **users.get.R8** `HasPassword` must be `true` exactly when the account has a password. *Reference defect (intentional divergence):* the reference reports `HasPassword=true` and `HasConfiguredPassword=true` for every account, including accounts created without a password and accounts whose password was removed or reset; the only reliable way to tell is that a login with an empty `Pw` succeeds. Clients must not rely on either flag being `false`.
- **users.get.R9** A disabled user's profile is still readable.

---

<a id="get-current-user"></a>
## GET /Users/Me

**Purpose.** The account of the token in use.

**Auth.** Any authenticated user (subject to the access-schedule check).

**Responses.** `200` user object; `401`; `400` when the credential has no user (API key); `403` outside the schedule.


**Rules**

- **users.me.R1** `200` with the user object of the token's owner.
- **users.me.R2** Anonymous or invalid token answers `401`.
- **users.me.R3** The `ApiKey` query parameter can carry the token (see `users.token.R3`).
- **users.me.R4** When the credential is an API key (no user), the answer is `400`.
- **users.me.R5** For a non-administrator outside an `AccessSchedule` window the request is refused with `403`.

---

<a id="create-user"></a>
## POST /Users/New

**Purpose.** Create a local account.

**Auth.** Administrator.

| Body field | Required | Effect |
|---|---|---|
| `Name` | yes | New account name; see `users.create.R5` and `R6`. |
| `Password` | no | Initial password; omitted/null/empty means none. |

**Responses.** `200` user object with the default policy and configuration; `400` invalid/duplicate name or body; `401`; `403` non-administrator.


**Rules**

- **users.create.R1** An administrator receives `200` with the new user object: `Name` as sent, a new `Id`, and the default policy: `IsAdministrator=false`, `IsDisabled=false`, `IsHidden=true`, `EnableAllFolders=true`, `EnableRemoteAccess=true`, `EnableMediaPlayback=true`, `EnableContentDownloading=true`, `EnableLiveTvAccess=true`, `EnableRemoteControlOfOtherUsers=false`, `EnableContentDeletion=false`, `LoginAttemptsBeforeLockout=-1`, `MaxActiveSessions=0`, `SyncPlayAccess="CreateAndJoinGroups"`, non-empty `AuthenticationProviderId` and `PasswordResetProviderId`.
- **users.create.R2** A non-empty `Password` sets the password: the user can log in with it and a login with an empty `Pw` is refused.
- **users.create.R3** `Password` omitted, `null` or `""` creates an account without a password (login with an empty `Pw` succeeds).
- **users.create.R4** `Name` is stored exactly as sent (case preserved) while login by name is case-insensitive.
- **users.create.R5** `Name` must match `^(?!\s)[\w \-'._@+]+(?<!\s)$` (letters including non-ASCII, digits, underscore, space, `-`, `'`, `.`, `_`, `@`, `+`; no leading or trailing space) and must not be `.` or `..`. Anything else (including an empty name) answers `400`.
- **users.create.R6** `Name` must be unique, compared case-insensitively. A duplicate answers `400`.
- **users.create.R7** Non-administrator answers `403`; anonymous or invalid token answers `401`. Nothing is created.
- **users.create.R8** The new user appears in `GET /Users` but not in `GET /Users/Public` (hidden by default).
- **users.create.R9** A body without `Name` (including `{}`) or one that is not JSON answers `400`.
- **users.create.R10** A successful creation writes a `UserCreated` entry: `Severity` `Information`, `Name` `User <name> has been created`, `UserId` of the new account, no `ShortOverview`, no `ItemId`.
- **users.create.R11** When the request body carries a `Password` member (including an empty string), a `UserPasswordChanged` entry (`users.password.R15`) is written right after `UserCreated`; a body without `Password` writes only `UserCreated`.

---

<a id="delete-user"></a>
## DELETE /Users/{userId}

**Purpose.** Delete an account and its data.

**Auth.** Administrator.

**Responses.** `204` empty; `400` the only administrator; `401`; `403` non-administrator; `404` unknown id.

**Security divergence.** The reference revokes the target's tokens *before* checking that the deletion is allowed, so a refused attempt still signs the target out (finding S13). The scenario `users.delete.last-admin-refused` asserts that a refused deletion has no side effects and is skipped on the reference.


**Rules**

- **users.delete.R1** An administrator receives `204` with an empty body; the user, their policy, preferences, watch data and tokens are removed and the id answers `404` afterwards. Media files are never touched.
- **users.delete.R2** Non-administrator answers `403`; anonymous or invalid token answers `401`. The user is not deleted.
- **users.delete.R3** An unknown id answers `404` (a repeated delete answers `404`).
- **users.delete.R4** All tokens of the deleted user stop working (`401`) and the user can no longer log in.
- **users.delete.R5** The only administrator cannot be deleted (`400`). A refused deletion has no side effects: no token is revoked, in particular not the caller's.
- **users.delete.R6** An administrator may delete another administrator, and may delete their own account when at least one other administrator exists; a self-deletion revokes the caller's own tokens.
- **users.delete.R7** The deleted user vanishes from `GET /Users` and `GET /Users/Public`, and the name becomes available again; a re-created user with the same name is a new account (new `Id`, none of the old password).
- **users.delete.R8** A deletion writes one `SessionEnded` entry per revoked token of the user (`users.token.R9`), then a `UserDeleted` entry (so it is the newest): `Severity` `Information`, `Name` `User <name> has been deleted`, `UserId` the all-zero id (like a failed login, `hasUserId=false` finds it), no `ShortOverview`, no `ItemId`. A refused deletion writes nothing.

---

<a id="update-user-policy"></a>
## POST /Users/{userId}/Policy

**Purpose.** Replace an account's policy (permissions, restrictions, lockout settings, authentication settings).

**Auth.** Administrator.

**Request.** Body is a policy object (see the field list above). It is a full replacement.

**Responses.** `204` empty; `400` malformed body or an empty `AuthenticationProviderId` / `PasswordResetProviderId`; `401`; `403` non-administrator, or removing the only administrator, or disabling an administrator; `404` unknown id.

**Notes.** The scenarios obtain the two provider ids by reading the user first and echoing them back (they are opaque server strings).


**Rules**

- **users.policy.R1** Administrator only: non-administrator answers `403` (even for their own policy); anonymous or invalid token answers `401`. `204` with an empty body on success.
- **users.policy.R2** An unknown user id answers `404`.
- **users.policy.R3** The posted policy is stored and returned by `GET /Users/{userId}` under `Policy`: `IsHidden`, `EnableAllFolders`, `EnabledFolders`, `BlockedTags`, `AllowedTags`, `EnableContentDownloading`, `EnablePlaybackRemuxing`, `EnableContentDeletion`, `EnableRemoteControlOfOtherUsers`, `MaxActiveSessions`, `LoginAttemptsBeforeLockout`, `SyncPlayAccess`, `AccessSchedules` (list of `{DayOfWeek, StartHour, EndHour}`) and the other `Enable*` flags.
- **users.policy.R4** The policy is replaced as a whole, not patched: every field omitted from the body takes the default of a newly created user (see `users.create.R1`) and previous values are discarded.
- **users.policy.R5** `AuthenticationProviderId` and `PasswordResetProviderId` are required and must be non-empty; otherwise `400` and nothing changes. Clients should send back the values read from the user.
- **users.policy.R6** Removing administrator rights (`IsAdministrator=false`) from the only administrator answers `403`; the count includes disabled administrators.
- **users.policy.R7** An administrator cannot be disabled: `IsDisabled=true` for a current administrator answers `403` (also when the same request demotes them).
- **users.policy.R8** Setting `IsDisabled=true` on a user that was enabled revokes the user's tokens; their logins answer `403` until re-enabled, after which login works again.
- **users.policy.R9** Policy changes take effect on the user's next request using existing tokens: promoting to administrator grants administrator-only routes immediately, demoting removes them immediately.
- **users.policy.R10** An administrator can demote themselves when another administrator exists.
- **users.policy.R11** A body that is not valid JSON answers `400`.
- **users.policy.R12** `LoginAttemptsBeforeLockout`: `-1` means no lockout, `0` means the default of `3`, a positive `n` locks the account after `n` consecutive failures (see `users.auth.R14`). `InvalidLoginAttemptCount` from the body overwrites the stored counter, which lets an administrator unlock an account.
- **users.policy.R13** A successful policy update sends a `UserUpdated` WebSocket message to the target user's own open sockets; `Data` is the updated user object (including the new `Policy`). See *Realtime socket*.
- **users.policy.R14** A policy update writes no activity entry of its own (no "policy updated" entry), also when it changes values. Each token it revokes (disabling the account, `users.policy.R8`; devices no longer allowed, `users.auth.R17`) writes a `SessionEnded` entry (`users.token.R9`). The same holds for `POST /Users` and `POST /Users/Configuration`: renaming or saving preferences writes no entry.

---

<a id="update-password"></a>
## POST /Users/Password

**Purpose.** Change or clear a password.

**Auth.** Any authenticated user (subject to remote-access and schedule checks); the target rules are below.

| Part | Name | Effect |
|---|---|---|
| query | `userId` | Target account; default the caller. |
| body | `CurrentPw` | Present when re-authentication is required (`R2`, `R6`). Omitted counts as empty. |
| body | `NewPw` | New password; empty/omitted clears it (not allowed for administrators). |
| body | `ResetPassword` | `true` clears the password without `CurrentPw` (`R9`). |
| body | `CurrentPassword` | Legacy hashed field; accepted and ignored (`R12`). |

**Responses.** `204` empty; `400` administrator account would get an empty password; `401`; `403` re-authentication failed or caller not allowed; `404` unknown target.

**Security notes.** `ResetPassword` by the account holder needs no current password and keeps sessions alive (finding S10); replicated, see *Open questions*. A wrong `CurrentPw` counts as a failed login attempt for the target and can therefore trigger a lockout when one is configured.


**Rules**

- **users.password.R1** The target user is `userId` (query) when given, otherwise the caller.
- **users.password.R2** When the caller is not an administrator, or is an administrator naming themselves explicitly via `userId`, the current password must be supplied in `CurrentPw` (omitted counts as empty): a wrong or missing value answers `403` and nothing changes. An account without a password accepts an omitted or empty `CurrentPw`.
- **users.password.R3** An administrator changing another user's password does not need `CurrentPw`; `204` with an empty body.
- **users.password.R4** A non-administrator naming another user answers `403` (for both a change and `ResetPassword`). An account may only have its password changed by itself or by an administrator.
- **users.password.R5** `NewPw` empty, `null` or omitted by a non-administrator removes the password (login with an empty `Pw` works, the old password is refused).
- **users.password.R6** An administrator changing their own password with no `userId` does not need `CurrentPw`. The same request with `userId` equal to their own id requires `CurrentPw` (`403` when wrong or missing).
- **users.password.R7** A successful change (via `NewPw`) revokes all tokens of the target user except the token used by the caller: a user changing their own password stays signed in on that device only; when an administrator changes another user's password every token of the target is revoked.
- **users.password.R8** An administrator account can never end up without a password: `NewPw` empty or omitted, or `ResetPassword=true`, for an administrator answers `400` and the password is unchanged.
- **users.password.R9** `ResetPassword=true` removes the target's password without requiring `CurrentPw`, for the user themselves or for an administrator acting on anyone. It does not revoke any token, so existing sessions stay signed in.
- **users.password.R10** An unknown target answers `404`.
- **users.password.R11** Anonymous or invalid token answers `401`.
- **users.password.R12** The legacy hashed-password field `CurrentPassword` is accepted in the body but ignored: sending only it counts as sending no `CurrentPw`.
- **users.password.R13** A non-administrator whose `EnableUserPreferenceAccess` policy flag is `false` cannot change their own password (`403`).

---

<a id="update-password-legacy-alias"></a>
## POST /Users/{userId}/Password

**Scope.** `legacy-web-required`. The official web client still calls this route.

**Behavior.** Alias of `POST /Users/Password` with the target taken from the path (same body, same answers). The only difference: the path id counts as an explicitly provided `userId`, so an administrator changing their *own* password through it must supply `CurrentPw`.


**Rules**

- **users.password.R14** `POST /Users/{userId}/Password` is an alias with the target id in the path and the same body and rules, with one difference: the id is always treated as explicitly given, so an administrator changing their own password through the alias must supply `CurrentPw`.
- **users.password.R15** Every successful password change, removal or `ResetPassword` writes a `UserPasswordChanged` entry: `Severity` `Information`, `Name` `Password has been changed for user <name>`, `UserId` of the target, no `ShortOverview`, no `ItemId`. When the change revokes tokens (`users.password.R7`) the `SessionEnded` entries (`users.token.R9`) are written after it. A refused request (wrong `CurrentPw`, `403`) writes no such entry.
- **users.password.R16** A wrong `CurrentPw` that reaches the configured threshold (`users.auth.R14`) disables the target and writes a `UserLockedOut` entry (`users.auth.R28`); no `AuthenticationFailed` entry is written for a wrong `CurrentPw`.

---

<a id="update-user"></a>
## POST /Users

**Purpose.** Rename an account and set its preferences from a whole user object.

**Auth.** Any authenticated user for themselves (`userId` omitted or equal to own id); administrator for others.

| Part | Name | Effect |
|---|---|---|
| query | `userId` | Target; default the caller. |
| body | `Name` | Required; renames when different. |
| body | `Policy` | Must be present with non-empty `AuthenticationProviderId` and `PasswordResetProviderId` (or explicitly `null`); otherwise `400` (see `users.update.R12`). Its other contents are ignored. |
| body | `Configuration` | Applied as in `POST /Users/Configuration`. Omitted = defaults. |
| body | everything else | Ignored. |

**Responses.** `204` empty; `400`; `401`; `403`; `404`. The change of name is announced to the user's open sessions; the preferences change is not.


**Rules**

- **users.update.R1** The body is a user object of which only `Name` and `Configuration` are applied; success is `204` with an empty body. The target is `userId` (query), defaulting to the caller.
- **users.update.R2** `Configuration` replaces the user's preferences (see `users.configuration.R2`); the values are visible afterwards in the user object.
- **users.update.R3** All other body fields (`Policy`, `Id`, `HasPassword`, ...) are ignored. In particular this route cannot change policy, administrator status or the password.
- **users.update.R4** A user may update themselves; an administrator may update anyone.
- **users.update.R5** A non-administrator naming another user answers `403`; anonymous or invalid token answers `401`.
- **users.update.R6** A rename follows the name rules of `users.create.R5`; an invalid name, an empty name, a missing `Name`, or a name used by another user (case-insensitive; the user's own name does not count as a duplicate) answers `400` and nothing changes.
- **users.update.R7** A `Name` that differs from the current one only in letter case is applied; a `Name` identical to the current one is a no-op (`204`).
- **users.update.R8** An unknown user answers `404`; a body that is not valid JSON answers `400`.
- **users.update.R9** A body without `Configuration` resets the user's preferences to their defaults.
- **users.update.R12** Body validation runs before the user lookup and the authorization check: a body without a `Policy` member (or with a `Policy` lacking `AuthenticationProviderId` or `PasswordResetProviderId`) answers `400` with a validation-problem body naming `Policy.AuthenticationProviderId` / `Policy.PasswordResetProviderId`, even for an unknown or foreign target. A body with `"Policy": null` is accepted. The minimal valid body is `{"Name":..., "Configuration":{...}, "Policy":{"AuthenticationProviderId":"...","PasswordResetProviderId":"..."}}`; a `userId` that is not a GUID or is all zeros answers `400`.
- **users.update.R13** A successful `POST /Users` sends a `UserUpdated` WebSocket message to the target user's own open sockets (`Data` is the updated user object, with the new `Name`); sockets of other users receive none. See *Realtime socket*.
- **users.update.R11** A non-administrator whose `EnableUserPreferenceAccess` policy flag is `false` is refused (`403`) when updating themselves.

---

<a id="update-user-legacy-alias"></a>
## POST /Users/{userId}

**Scope.** `legacy-web-required`. Alias of `POST /Users` with the target in the path; identical body and answers.


**Rules**

- **users.update.R10** `POST /Users/{userId}` is an alias with the target in the path and identical behavior.

---

<a id="update-user-configuration"></a>
## POST /Users/Configuration

**Purpose.** Replace an account's preferences.

**Auth.** Any authenticated user for themselves; administrator for others.

**Request.** Query `userId` (default the caller); body is a configuration object. `CastReceiverId` is only kept when it names a cast receiver configured on the server; otherwise it is silently ignored.

**Responses.** `204` empty; `400` malformed body or invalid enum; `401`; `403`; `404`.


**Rules**

- **users.configuration.R1** `204` with an empty body. The target is `userId` (query), defaulting to the caller. The preferences (`SubtitleMode`, `AudioLanguagePreference`, `SubtitleLanguagePreference`, `PlayDefaultAudioTrack`, `HidePlayedInLatest`, `DisplayMissingEpisodes`, `DisplayCollectionsView`, `EnableNextEpisodeAutoPlay`, `RememberAudioSelections`, `RememberSubtitleSelections`, `EnableLocalPassword`, `OrderedViews`, `GroupedFolders`, `MyMediaExcludes`, `LatestItemsExcludes`) are returned under `Configuration` by `GET /Users/Me` and `GET /Users/{userId}`.
- **users.configuration.R2** The configuration is replaced as a whole: fields omitted from the body go back to their defaults.
- **users.configuration.R3** A user may update their own configuration; an administrator may update anyone's; a non-administrator naming another user answers `403`; anonymous answers `401`.
- **users.configuration.R4** An unknown user answers `404`; a body that is not valid JSON, or an invalid enum value (`SubtitleMode` accepts `Default`, `Always`, `OnlyForced`, `None`, `Smart`), answers `400`.

---

<a id="update-user-configuration-legacy-alias"></a>
## POST /Users/{userId}/Configuration

**Scope.** `legacy-web-required`. Alias of `POST /Users/Configuration` with the target in the path; identical body and answers.


**Rules**

- **users.configuration.R5** `POST /Users/{userId}/Configuration` is an alias with the target in the path and identical behavior.

---

<a id="realtime-socket"></a>
## GET /socket (WebSocket)

**Purpose.** The realtime channel clients keep open for server-pushed messages.

**Auth.** The credential travels in the `ApiKey` query parameter (a user token or an API key), or in the `Authorization` header. `DeviceId` may be given as a `deviceId` query parameter. The `api_key` parameter is not honored (`users.token.R6`).

**Messages.** Text frames, each a JSON object `{"MessageType": "...", "Data": ..., "MessageId": "..."}`.


**Rules**

- **users.socket.R1** With a valid user token or API key in `ApiKey` the handshake upgrades (`101`).
- **users.socket.R2** An unknown, malformed or missing credential is refused before the upgrade with `403`.
- **users.socket.R3** Right after the upgrade the server sends `{"MessageType": "ForceKeepAlive", "Data": <seconds>}`; `Data` is a number (the keep-alive interval clients must honor).
- **users.socket.R4** The legacy `api_key` query parameter is not a credential here either: a valid token sent that way is refused with `403`.

Messages pushed to the sockets of the affected user: `UserUpdated` on rename and policy change (`users.update.R13`, `users.policy.R13`). A user-configuration update (`POST /Users/Configuration`) sends none.

---

<a id="forgot-password"></a>
## POST /Users/ForgotPassword

**Purpose.** Start the PIN-based password recovery.

**Auth.** Anonymous.

**Request.** Body `{"EnteredUsername": "<name>"}`.

**Responses.** `200` `{Action, PinFile, PinExpirationDate}`; `400` missing username.

**Notes.** The PIN is written to a file on the server and must be read there by the owner. The reference puts the file's absolute server path in `PinFile`, which leaks the server's path layout (finding S11). Tinyfin answers with the bare file name only (`users.forgot.R10`, a deliberate divergence): the official web client just displays the string, and the owner knows where the server keeps its data.


**Rules**

- **users.forgot.R1** `POST /Users/ForgotPassword` is anonymous. With a body `{"EnteredUsername": "<name>"}` it answers `200` with `{Action, PinFile, PinExpirationDate}`: `Action` is always `"PinCode"`, `PinFile` is a string and `PinExpirationDate` is an ISO-8601 date-time about 30 minutes in the future. The PIN itself is never in the response.
- **users.forgot.R2** The PIN file is created only for an existing user and only when the request comes from the local network; the response is identical in every case, so it reveals neither the account's existence nor the caller's location.
- **users.forgot.R3** An unknown user receives a `200` of the same shape as a known one (no enumeration). The username lookup is case-insensitive.
- **users.forgot.R4** A missing `EnteredUsername`, or a body that is not JSON, answers `400`.
- **users.forgot.R5** Starting the flow does not change the account's password or invalidate its tokens.
- **users.forgot.R10** `PinFile` is a bare file name: it contains no path separator and no directory. *Tinyfin hardening, divergence from the reference* (which returns an absolute server path, finding S11); the scenario carries a `divergence` record and is skipped on the reference.

---

<a id="forgot-password-pin"></a>
## POST /Users/ForgotPassword/Pin

**Purpose.** Redeem a recovery PIN.

**Auth.** Anonymous (the PIN is the credential).

**Request.** Body `{"Pin": "<pin>"}` (dashes optional).

**Responses.** `200` `{Success, UsersReset}`; `404` no pending request matches; `400` missing `Pin`.


**Rules**

- **users.forgot.R6** `POST /Users/ForgotPassword/Pin` is anonymous. A `Pin` that matches no pending reset answers `404`. The comparison ignores `-`.
- **users.forgot.R7** A missing `Pin`, or a body that is not JSON, answers `400`.
- **users.forgot.R8** A matching, unexpired `Pin` answers `200` `{Success: true, UsersReset: [<user names>]}`; the matching users' password becomes the PIN text exactly as sent; the reset request is consumed; existing tokens are not revoked and a lockout is not cleared. (The scenario reads the PIN from the file on the server through the runner's server-file capability, see the README.)
- **users.forgot.R9** *Tinyfin hardening, not a reference behavior (the reference's behavior here is not specified or relied on).* The PIN has few bits and a 30 minute life, so redeem attempts are limited, and every refusal is the same uniform `404` as a wrong PIN (no distinct status, no `Retry-After`, nothing that tells a throttled attempt from a wrong one): (a) a source address that sends 5 wrong PINs is refused for 15 minutes without its PIN being evaluated, even if the PIN is right; (b) 5 wrong PINs in total, from any sources, invalidate every pending request, so the owner has to start the flow again; (c) the PIN is compared in constant time. Malformed bodies (`400`) are not counted; a successful redeem resets the total. Observing the effect on a right PIN needs the PIN file (server-file capability). The scenario is `risky` (it leaves the runner's source throttled for 15 minutes) and carries a `divergence` record, so it is skipped on the reference; one source address cannot tell (a) from (b), so they are exercised together: after five wrong PINs the right one is refused, the pending request's file is gone and the source stays refused after a new flow.

---

## Rule -> scenarios

| Rule | Scenarios |
|---|---|
| `users.auth.R1` | `users.auth.success-shape` |
| `users.auth.R2` | `users.auth.username-case-insensitive` |
| `users.auth.R3` | `users.auth.wrong-password` |
| `users.auth.R4` | `users.auth.unknown-user`, `users.auth.unknown-and-wrong-look-alike` |
| `users.auth.R5` | `users.auth.username-missing` |
| `users.auth.R6` | `users.auth.header-missing-client`, `users.auth.header-missing-device`, `users.auth.header-missing-deviceid`, `users.auth.header-missing-version`, `users.auth.header-absent` |
| `users.auth.R7` | `users.auth.passwordless-account` |
| `users.auth.R8` | `users.auth.disabled-account` |
| `users.auth.R9` | `users.auth.token-usable` |
| `users.auth.R10` | `users.auth.relogin-same-device-revokes`, `users.auth.different-devices-coexist`, `users.auth.relogin-other-user-same-device` |
| `users.auth.R11` | `users.auth.header-parsing` |
| `users.auth.R12` | `users.auth.success-shape` |
| `users.auth.R13` | `users.auth.no-default-lockout` |
| `users.auth.R14` | `users.auth.lockout-after-limit`, `users.auth.lockout-zero-means-three` |
| `users.auth.R15` | `users.auth.lockout-counter-reset-on-success` |
| `users.auth.R16` | `users.auth.max-active-sessions` |
| `users.auth.R17` | `users.auth.device-restriction`, `users.auth.device-restriction-revokes-existing` |
| `users.auth.R18` | `users.auth.access-schedule` |
| `users.auth.R19` | `users.auth.malformed-body` |
| `users.auth.R20` | `users.auth.password-field-name` |
| `users.auth.R21` | `users.auth.remote-access-disabled` |
| `users.auth.R22` | `users.auth.last-login-recorded` |
| `users.auth.R23` | `users.auth.activity-login-entries`, `users.auth.activity-quickconnect-entries` |
| `users.auth.R24` | `users.auth.activity-login-entries`, `users.auth.activity-quickconnect-entries` |
| `users.auth.R25` | `users.auth.activity-login-entries`, `users.auth.activity-quickconnect-entries` |
| `users.auth.R26` | `users.auth.activity-failed-login-entry` |
| `users.auth.R27` | `users.auth.activity-no-entry-for-refusals` |
| `users.auth.R28` | `users.auth.activity-lockout-entry` |
| `users.auth.R29` | `users.auth.activity-lockout-entry` |
| `users.auth.R30` | `users.auth.lockout-keeps-tokens` |
| `users.auth.R31` | `users.auth.activity-session-ended-relogin` |
| `users.auth.R32` | `users.auth.activity-session-ended-relogin`, `users.auth.activity-session-ended-address` |
| `users.token.R1` | `users.auth.token-usable` |
| `users.token.R2` | `users.token.invalid-token` |
| `users.token.R3` | `users.token.apikey-query` |
| `users.token.R4` | `users.token.disabled-user-token`, `users.policy.disable-revokes-tokens` |
| `users.token.R5` | `users.token.deleted-user-token` |
| `users.token.R6` | `users.token.legacy-xemby-authorization-login`, `users.token.legacy-xemby-token`, `users.token.legacy-api-key-query`, `users.token.legacy-emby-scheme` |
| `users.token.R7` | `users.token.logout-ends-session` |
| `users.token.R8` | `users.token.logout-ends-session` |
| `users.token.R9` | `users.token.logout-ends-session`, `users.password.activity-entries`, `users.policy.activity-session-ended`, `users.delete.activity-entries` |
| `users.public.R1` | `users.public.anonymous` |
| `users.public.R2` | `users.public.visible-user-listed` |
| `users.public.R3` | `users.public.disabled-user-excluded` |
| `users.public.R4` | `users.public.visible-user-listed` |
| `users.public.R5` | `users.public.sorted-by-name` |
| `users.public.R6` | `users.public.anonymous` |
| `users.list.R1` | `users.list.requires-auth`, `users.list.admin`, `users.list.non-admin-sees-everyone` |
| `users.list.R2` | `users.list.includes-hidden-and-disabled` |
| `users.list.R3` | `users.list.filter-is-hidden` |
| `users.list.R4` | `users.list.filter-is-disabled` |
| `users.list.R5` | `users.list.sorted-by-name` |
| `users.list.R6` | `users.list.admin` |
| `users.list.R7` | `users.me.outside-schedule` |
| `users.list.R8` | `users.list.api-key` |
| `users.get.R1` | `users.get.own-profile` |
| `users.get.R2` | `users.get.other-user-readable` |
| `users.get.R3` | `users.get.not-found` |
| `users.get.R4` | `users.get.bad-id` |
| `users.get.R5` | `users.get.bad-id` |
| `users.get.R6` | `users.get.anonymous` |
| `users.get.R7` | `users.me.outside-schedule` |
| `users.get.R8` | `users.get.own-profile`, `users.get.has-password-flag` |
| `users.get.R9` | `users.get.disabled-user-readable` |
| `users.me.R1` | `users.me.returns-caller` |
| `users.me.R2` | `users.me.anonymous` |
| `users.me.R3` | `users.token.apikey-query` |
| `users.me.R4` | `users.me.api-key-has-no-user` |
| `users.me.R5` | `users.me.outside-schedule` |
| `users.create.R1` | `users.create.defaults` |
| `users.create.R2` | `users.create.password-set` |
| `users.create.R3` | `users.create.no-password` |
| `users.create.R4` | `users.create.password-set` |
| `users.create.R5` | `users.create.invalid-names`, `users.create.allowed-name-characters` |
| `users.create.R6` | `users.create.duplicate-name` |
| `users.create.R7` | `users.create.admin-only` |
| `users.create.R8` | `users.public.visible-user-listed`, `users.create.listed-but-hidden` |
| `users.create.R9` | `users.create.name-missing` |
| `users.create.R10` | `users.create.activity-entries` |
| `users.create.R11` | `users.create.activity-entries` |
| `users.delete.R1` | `users.delete.removes-user` |
| `users.delete.R2` | `users.delete.admin-only` |
| `users.delete.R3` | `users.delete.not-found` |
| `users.delete.R4` | `users.token.deleted-user-token`, `users.delete.admin-deletes-self-with-other-admin` |
| `users.delete.R5` | `users.delete.last-admin-refused` |
| `users.delete.R6` | `users.delete.admin-can-delete-other-admin`, `users.delete.admin-deletes-self-with-other-admin` |
| `users.delete.R7` | `users.delete.removes-user`, `users.delete.name-reusable` |
| `users.delete.R8` | `users.delete.activity-entries` |
| `users.policy.R1` | `users.policy.admin-only` |
| `users.policy.R2` | `users.policy.not-found` |
| `users.policy.R3` | `users.policy.roundtrip`, `users.policy.parental-rating-roundtrip` |
| `users.policy.R4` | `users.policy.replaces-whole-policy` |
| `users.policy.R5` | `users.policy.provider-ids-required` |
| `users.policy.R6` | `users.policy.last-admin-cannot-be-demoted` |
| `users.policy.R7` | `users.policy.administrators-cannot-be-disabled` |
| `users.policy.R8` | `users.token.disabled-user-token`, `users.policy.disable-revokes-tokens` |
| `users.policy.R9` | `users.policy.promote-and-demote` |
| `users.policy.R10` | `users.policy.self-demotion` |
| `users.policy.R11` | `users.policy.malformed-body` |
| `users.policy.R12` | `users.auth.lockout-after-limit` |
| `users.policy.R13` | `users.policy.emits-user-updated` |
| `users.policy.R14` | `users.policy.activity-session-ended` |
| `users.password.R1` | `users.password.change-own` |
| `users.password.R2` | `users.password.change-own`, `users.password.wrong-current`, `users.password.set-on-passwordless` |
| `users.password.R3` | `users.password.admin-sets-other` |
| `users.password.R4` | `users.password.other-user-non-admin`, `users.password.reset-other-non-admin` |
| `users.password.R5` | `users.password.remove-own` |
| `users.password.R6` | `users.password.admin-self-without-user-id`, `users.password.admin-self-with-user-id` |
| `users.password.R7` | `users.password.admin-sets-other`, `users.password.keeps-current-token` |
| `users.password.R8` | `users.password.admin-account-cannot-be-empty` |
| `users.password.R9` | `users.password.reset-own`, `users.password.reset-by-admin`, `users.password-alias.reset` |
| `users.password.R10` | `users.password.unknown-user` |
| `users.password.R11` | `users.password.anonymous` |
| `users.password.R12` | `users.password.legacy-hash-field-ignored` |
| `users.password.R13` | `users.password.preference-access-disabled` |
| `users.password.R14` | `users.password-alias.change-own`, `users.password-alias.admin-sets-other`, `users.password-alias.reset`, `users.password-alias.admin-self-needs-current`, `users.password-alias.errors` |
| `users.password.R15` | `users.password.activity-entries` |
| `users.password.R16` | `users.password.wrong-current-lockout-entry` |
| `users.update.R1` | `users.update.rename-and-configuration` |
| `users.update.R2` | `users.update.rename-and-configuration` |
| `users.update.R3` | `users.update.policy-in-body-ignored` |
| `users.update.R4` | `users.update.admin-edits-other` |
| `users.update.R5` | `users.update.other-user-non-admin` |
| `users.update.R6` | `users.update.rename-validation` |
| `users.update.R7` | `users.update.rename-case-only` |
| `users.update.R8` | `users.update.not-found-and-bad-body` |
| `users.update.R9` | `users.update.omitted-configuration-resets` |
| `users.update.R10` | `users.update-alias.self`, `users.update-alias.admin-and-errors` |
| `users.update.R11` | `users.update.preference-access-disabled` |
| `users.update.R13` | `users.update.emits-user-updated` |
| `users.update.R12` | `users.update.not-found-and-bad-body` |
| `users.configuration.R1` | `users.configuration.update-own` |
| `users.configuration.R2` | `users.configuration.replaces-all` |
| `users.configuration.R3` | `users.configuration.other-user` |
| `users.configuration.R4` | `users.configuration.errors` |
| `users.configuration.R5` | `users.configuration-alias.update`, `users.configuration-alias.errors` |
| `users.forgot.R1` | `users.forgot.known-user` |
| `users.forgot.R2` | `users.forgot.known-user` |
| `users.forgot.R3` | `users.forgot.unknown-user-same-answer` |
| `users.forgot.R4` | `users.forgot.username-missing` |
| `users.forgot.R5` | `users.forgot.does-not-change-password` |
| `users.forgot.R6` | `users.forgot-pin.no-match` |
| `users.forgot.R7` | `users.forgot-pin.pin-missing` |
| `users.forgot.R8` | `users.forgot-pin.redeem` |
| `users.forgot.R9` | `users.forgot-pin.throttle` |
| `users.forgot.R10` | `users.forgot.pinfile-bare-name` |
| `users.socket.R1` | `users.socket.upgrade`, `users.socket.upgrade-api-key` |
| `users.socket.R2` | `users.socket.refused-without-credential` |
| `users.socket.R3` | `users.socket.upgrade` |
| `users.socket.R4` | `users.socket.legacy-api-key-param-refused` |


## Divergences from the reference

| Scenario | Reference behavior | Required behavior |
|---|---|---|
| `users.delete.last-admin-refused` | A refused deletion of the only administrator (`400`) still revokes all of that user's tokens (finding S13). | `400` and no side effect. |

## Behaviors replicated on purpose (security findings kept as-is)

- **S4** any authenticated user can list all users and read any user's full `Policy` (`users.list.R1`, `users.get.R2`). Restricting this would break clients that list users for non-administrators; left to the lead.
- **S5** no lockout by default, no rate limit (`users.auth.R13`).
- **S10** `ResetPassword=true` needs no current password and keeps sessions (`users.password.R9`).
- **S11** (diverged, `users.forgot.R10`) the reference's `ForgotPassword` returns the server path of the PIN file; Tinyfin returns a bare file name. PIN redeem is additionally rate limited (`users.forgot.R9`).

## Open questions

0. (resolved) Activity entries around authentication and account management: `UserLockedOut`, `SessionEnded`, `UserCreated`, `UserPasswordChanged` and `UserDeleted` are specified (`users.auth.R28`-`R32`, `users.token.R9`, `users.create.R10`, `users.password.R15`, `users.delete.R8`) and verified on the reference. Not written by the reference, so not specified: entries for policy updates, user renames, preference saves, `ResetPassword` revocations and lockout revocations.
1. `EnableUserPreferenceAccess` (resolved): the policy endpoint persists it and a non-administrator without it is refused (`403`) when changing their own password or updating their own account; verified on the reference (`users.password.R13`, `users.update.R11`).
2. Access-schedule hour semantics: `StartHour <= hour <= EndHour` is inclusive and uses the server's local time, but whether `hour` includes minutes (fractional hours) is not confirmed. Scenarios that depend on it use the 03:00 window and are tagged `time-dependent`; they would flip if run during the 03:xx hour.
3. `LoginAttemptsBeforeLockout=0` reads back as `0` or `3`? Only the lockout behavior (`0` acts as `3`) is asserted.
4. Failure counter double counting: the reference may increment the persisted counter twice on the failure that triggers a lockout. Only the externally visible lockout threshold is specified.
5. `MaxParentalRating` round trip (resolved): the value posted is read back unchanged (`users.policy.parental-rating-roundtrip`).
6. `HasPassword`, `HasConfiguredPassword`, `LastLoginDate` and `SessionInfo.{UserId,DeviceId,Client,DeviceName}` are part of the observed user/session objects but are not described field by field in the audit; they come from the shape of the official clients' use and must be confirmed against the reference.
7. Path ids that are not GUIDs (`400`) follow from request binding and are not separately documented in the audit.
8. A request whose body omits `Configuration` on `POST /Users` resets preferences to defaults (`users.update.R9`); the audit states the default but not the intent. Confirm on the reference.
9. QuickConnect is specified in `quickconnect.md`; it is assumed enabled on the test server.
10. Fixture `disabled` (in `fixtures.json`) is created and disabled through the policy endpoint, but the runner never logs in a user whose policy sets `IsDisabled` (login answers `403` by design), so it has no token: it is available for `{{user.disabled.id|name}}` templates and as a target, never as an `as` identity. Scenarios that need a disabled account with a token create it themselves and disable it mid-scenario.
11. Remote-access restriction (`EnableRemoteAccess=false`, `users.auth.R21`) is exercised by making the runner's own address remote through `LocalNetworkSubnets`; that needs a peer address that is not loopback (a published container port reaches the reference from its gateway address; Tinyfin must be reached through one of the host's own addresses). The PIN scenarios (`users.forgot-pin.redeem`, `users.forgot-pin.throttle`) read the PIN from the server's file through the runner's server-file capability; without it they are BLOCKED, not failed.
12. Admin self-service rules differ between the modern route (no `CurrentPw` without `userId`) and the alias (always required). They are replicated as observed.
