# Spec: QuickConnect (device pairing without typing a password)

Wire-level behavior of the QuickConnect routes of a Jellyfin 12+ server, verified black-box against a 12.2 reference server. Every rule has a stable ID (`quickconnect.<topic>.R<n>`) that scenarios in `../scenarios/quickconnect.json` reference through `covers`. Scope follows `../README.md`; all routes here are `modern`.

The flow has two actors. A *device* (for example a TV app) that cannot type a password asks for a request (`Initiate`), shows the six-digit **code** and polls with its **secret** (`Connect`). A *signed-in user* on another device types the code (`Authorize`). When the request is authorized, the device trades its secret for a normal login result (`POST /Users/AuthenticateWithQuickConnect`). One script plays both actors in the scenarios.

Index of routes:

| Route | Section |
|---|---|
| `GET /QuickConnect/Enabled` | [enabled](#enabled) |
| `POST /QuickConnect/Initiate` | [initiate](#initiate) |
| `GET /QuickConnect/Connect` | [connect](#connect) |
| `POST /QuickConnect/Authorize` | [authorize](#authorize) |
| `POST /Users/AuthenticateWithQuickConnect` | [exchange](#exchange) |
| (request lifetime) | [expiry](#expiry) |
| (server setting off) | [disabled](#disabled) |

Vocabulary used below. The *result object* returned by `Initiate` and `Connect` is `{Authenticated (boolean), Secret (string), Code (string), DeviceId, DeviceName, AppName, AppVersion (strings), DateAdded (ISO-8601)}`. `Secret` is an opaque string clients only echo back; the reference uses 64 upper-case hex digits and Tinyfin does not copy that format. A pending request lives about 10 minutes.

---

<a id="enabled"></a>
## GET /QuickConnect/Enabled

**Auth.** Anonymous. **Responses.** `200` with a JSON boolean.

- **quickconnect.enabled.R1** The body is `true` when the server's `QuickConnectAvailable` configuration is on and `false` when it is off. Anonymous callers are answered.

---

<a id="initiate"></a>
## POST /QuickConnect/Initiate

**Auth.** Anonymous. The device identity is read from the `Authorization: MediaBrowser ...` header (`Client`, `Device`, `DeviceId`, `Version`); the token issued later is bound to that identity.

- **quickconnect.initiate.R1** A call answers `200` with a result object: `Authenticated` is `false`, `Code` is exactly six digits, `Secret` is a non-empty string, `DeviceId`, `DeviceName`, `AppName` and `AppVersion` echo the header's `DeviceId`, `Device`, `Client` and `Version`, and `DateAdded` is a date-time.
- **quickconnect.initiate.R2** A header that lacks any of `Client`, `Device`, `DeviceId` or `Version`, or no `Authorization` header at all, answers `400`.
- **quickconnect.initiate.R3** A `Token` in the header that is invalid is ignored: the call is anonymous and answers `200`.
- **quickconnect.initiate.R4** Every call gets its own `Secret` and `Code`.

---

<a id="connect"></a>
## GET /QuickConnect/Connect

**Auth.** Anonymous (the secret is the credential). **Request.** Query `secret`.

- **quickconnect.connect.R1** For a pending request `200` with its result object: the same `Secret`, `Code` and device fields as `Initiate`, and `Authenticated` `false` until the request is authorized, `true` afterwards.
- **quickconnect.connect.R2** A secret that matches no request (never issued, expired, or differing in case) answers `404`; a missing `secret` answers `400`.
- **quickconnect.connect.R3** Polling never changes `DateAdded`: while the request is pending it is the value `Initiate` returned. Authorizing moves it forward: afterwards `Connect` reports a `DateAdded` one minute past the moment of the authorization (so about a minute and the elapsed time later than at `Initiate`). Clients do not use it; the movement matters because the lifetime is counted from it (`quickconnect.expiry.R2`).

---

<a id="authorize"></a>
## POST /QuickConnect/Authorize

**Auth.** A signed-in user. **Request.** Query `code` (required) and optional `userId`.

- **quickconnect.authorize.R1** A signed-in user presenting the code of a pending request answers `200` with the JSON boolean `true`; the request's `Authenticated` becomes `true`, and its secret can then be exchanged. Without `userId` the login belongs to the authorizing user.
- **quickconnect.authorize.R2** Without a valid credential the call answers `401` and the request stays unauthorized.
- **quickconnect.authorize.R3** A code that matches no pending request answers `404`; a missing `code` answers `400`.
- **quickconnect.authorize.R4** An administrator may name another existing account in `userId`; the login issued by the exchange then belongs to that account.
- **quickconnect.authorize.R5** A non-administrator naming an account other than their own answers `403`; naming themselves is allowed.
- **quickconnect.authorize.R6** A `userId` that is not a user id answers `400`.
- **quickconnect.authorize.R7** A well-formed `userId` that names no account answers `400` and authorizes nothing. The all-zero id is treated as if `userId` were not given (the login belongs to the authorizing user).
- **quickconnect.authorize.R8** Authorizing a code that is already authorized answers `500` with the plain-text body `Error processing request.` (an unhandled error in the reference, reproduced for wire compatibility), whoever calls and whatever `userId` is named. Nothing changes: the request stays authorized for the first authorization's user and can still be exchanged.

---

<a id="exchange"></a>
## POST /Users/AuthenticateWithQuickConnect

**Auth.** Anonymous (possession of an authorized secret is the credential). The `Authorization` header is not read; the device identity was fixed by `Initiate`. **Request.** Body `{"Secret": "<secret>"}`. **Responses.** `200` login result; `404` secret unknown, expired or not yet authorized; `400` missing `Secret` or a body that is not JSON; `401` QuickConnect disabled.

- **quickconnect.exchange.R1** After the request has been authorized, presenting its `Secret` answers `200` with the same `{User, SessionInfo, AccessToken, ServerId}` shape as a password login. The token belongs to the authorizing user (or the account named in `userId`), is bound to the device identity sent to `Initiate`, and works as a normal token.
- **quickconnect.exchange.R2** Presenting the `Secret` before it is authorized answers `404`; the device keeps polling.
- **quickconnect.exchange.R3** A secret that does not exist (or has expired) answers `404`.
- **quickconnect.exchange.R4** A body without `Secret`, or not JSON, answers `400`.
- **quickconnect.exchange.R5** Issuing the token revokes any earlier token of the same user on the same `DeviceId`, exactly like a password login.
- **quickconnect.exchange.R6** *Security divergence (finding S9).* A secret can be exchanged once: a second exchange answers `404`. The reference lets an authorized secret be exchanged repeatedly for about ten minutes, returning a working login result each time, and keeps answering `Connect` for it; the scenario `users.quickconnect.secret-single-use` carries a `divergence` record and is skipped on the reference.
- **quickconnect.exchange.R7** *Security divergence.* An account that has been disabled between the authorization and the exchange is refused with `403`, as on a password login (`users.auth.R8`). The reference issues a login result anyway; the scenario carries a `divergence` record.

---

<a id="expiry"></a>
## Request lifetime

A request is held in memory only for a short time; the rules below were measured by polling `Connect` every 20 s.

- **quickconnect.expiry.R1** A pending request is redeemable for 10 minutes after `Initiate`: it is still there at 9 minutes and gone at 10 (between 580 s and 600 s). Afterwards `Connect` and the exchange answer `404` for its secret and `Authorize` answers `404` for its code.
- **quickconnect.expiry.R2** An authorized request has two windows. `Connect` keeps reporting it until 10 minutes after its `DateAdded`, which authorization moved forward (`connect.R3`): about 11 minutes after the authorization (alive at 640 s, gone at 660 s). The exchange is possible for 10 minutes after the authorization only, counted from the authorization and not from `Initiate`: a request authorized at once is refused with `404` from 10 minutes on while `Connect` still shows it, and one authorized five minutes after `Initiate` can still be exchanged at 10 minutes 15 seconds after `Initiate`.

The scenario for these rules sleeps 615 s in total and carries the tags `slow` and `expiry`; run without `-skip-tags expiry` to include it.

---

<a id="disabled"></a>
## QuickConnect switched off

- **quickconnect.disabled.R1** When `QuickConnectAvailable` is off, `GET /QuickConnect/Enabled` answers `false` and `Initiate`, `Connect`, `Authorize` and the exchange answer `401`. Parameter validation comes first: a missing `secret` or `code` still answers `400`, and `Authorize` without a credential still answers `401`.

---

## Rule -> scenarios

| Rule | Scenarios |
|---|---|
| `quickconnect.enabled.R1` | `quickconnect.enabled.default`, `users.quickconnect.disabled` |
| `quickconnect.initiate.R1` | `quickconnect.initiate.shape` |
| `quickconnect.initiate.R2` | `quickconnect.initiate.device-headers-required` |
| `quickconnect.initiate.R3` | `quickconnect.initiate.invalid-token-ignored` |
| `quickconnect.initiate.R4` | `quickconnect.initiate.shape` |
| `quickconnect.connect.R1` | `quickconnect.connect.poll` |
| `quickconnect.connect.R2` | `quickconnect.connect.unknown-or-missing-secret` |
| `quickconnect.connect.R3` | `quickconnect.connect.date-added-advances` |
| `quickconnect.authorize.R1` | `users.quickconnect.exchange` |
| `quickconnect.authorize.R2` | `quickconnect.authorize.anonymous-refused` |
| `quickconnect.authorize.R3` | `quickconnect.authorize.unknown-or-missing-code` |
| `quickconnect.authorize.R4` | `users.quickconnect.admin-authorizes-other-user` |
| `quickconnect.authorize.R5` | `quickconnect.authorize.non-admin-other-user-forbidden` |
| `quickconnect.authorize.R6` | `quickconnect.authorize.invalid-user-id` |
| `quickconnect.authorize.R7` | `quickconnect.authorize.unknown-user-id` |
| `quickconnect.authorize.R8` | `quickconnect.authorize.already-authorized` |
| `quickconnect.exchange.R1` | `users.quickconnect.exchange` |
| `quickconnect.exchange.R2` | `users.quickconnect.exchange` |
| `quickconnect.exchange.R3` | `users.quickconnect.unknown-secret` |
| `quickconnect.exchange.R4` | `users.quickconnect.secret-missing` |
| `quickconnect.exchange.R5` | `users.quickconnect.exchange-revokes-device-token` |
| `quickconnect.exchange.R6` | `users.quickconnect.secret-single-use` |
| `quickconnect.exchange.R7` | `quickconnect.exchange.disabled-account` |
| `quickconnect.expiry.R1` | `quickconnect.expiry.pending-and-authorized` |
| `quickconnect.expiry.R2` | `quickconnect.expiry.pending-and-authorized` |
| `quickconnect.disabled.R1` | `users.quickconnect.disabled`, `quickconnect.disabled.parameters-and-authorize` |

The pre-existing scenario ids `users.quickconnect.*` are kept so the committed baseline keeps matching them.

## Divergences from the reference

| Scenario | Reference behavior | Required behavior |
|---|---|---|
| `users.quickconnect.secret-single-use` | An authorized secret can be exchanged repeatedly for ~10 minutes, returning the same token (finding S9). | Exchangeable once; the second attempt answers `404`. |
| `quickconnect.exchange.disabled-account` | A disabled account still gets a login result. | `403`. |

## Resolved questions

Settled by probing the 12.2 reference:

1. Authorizing a code that is already authorized: `500`, `authorize.R8`. (Tinyfin answered `200` before.)
2. `DateAdded` after authorization: moved one minute past the authorization, `connect.R3`.
3. Expiry of a pending request: 10 minutes after `Initiate`, `expiry.R1`; exercised by `quickconnect.expiry.pending-and-authorized` (tagged `slow`, `expiry`).
