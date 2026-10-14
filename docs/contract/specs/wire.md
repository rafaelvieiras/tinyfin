# Spec: wire (cross-cutting wire format)

Behavior that every route shares and that clients observe directly: how request paths are matched, how identifiers and dates are written and what a failure body looks like. Rules have stable IDs (`wire.<topic>.R<n>`) that scenarios in `../scenarios/wire.json` reference through `covers`. The area-specific specs say which status a route answers; this one says what the body looks like.

## Identifiers

- **wire.id.R1** Every identifier in a JSON response body (`Id`, `ServerId`, `ParentId`, user and item ids, ...) is written as 32 lowercase hexadecimal digits with no dashes (`0123456789abcdef0123456789abcdef`). Upper case and the dashed 8-4-4-4-12 form are not emitted.
- **wire.id.R2** Identifiers in a path or query parameter are accepted in the dashed form as well as the undashed one, in any letter case, and name the same object as the undashed form.

## Request paths

- **wire.path.R1** One trailing slash is ignored: `GET /Users/`, `GET /Users/Public/`, `GET /Users/Me/`, `GET /Users/{id}/`, `GET /System/Info/Public/`, `GET /Items/`, `GET /Sessions/` and `GET /System/ActivityLog/Entries/` answer exactly what the same path without the slash answers (status, headers and body, query string included). It holds for every method: `POST /Users/AuthenticateByName/`, `POST /Users/New/`, `POST /Users/Configuration/` and `DELETE /Users/{id}/` behave like their plain forms, and a route that does not exist for the method answers the same `405`/`404` with or without the slash.
- **wire.path.R2** Only a single trailing slash is dropped. A path with an empty segment anywhere (`/Users//`, `/Users/Me//`, `/Users//Me`, `//Users`) answers `404` with an empty body, never a redirect; a trailing slash on an unknown route is `404` as without it.
- **wire.path.R3** Literal path segments are matched case-insensitively (`/users/`, `/USERS/ME`, `/system/info/public`); identifier segments keep their value (`wire.id.R2`).
- **wire.path.R4** Authentication and authorization are evaluated as for the plain path: an anonymous `GET /Users/` answers `401`, `GET /Users/{id}/` is subject to the same checks as `GET /Users/{id}`.

## Date-times

- **wire.date.R1** Every date-time member of a JSON response (user, session, device, activity-log, item, user-data, API-key, QuickConnect, scheduled-task and push-message objects alike) is written in UTC as `YYYY-MM-DDTHH:MM:SS`, an optional fraction, and the suffix `Z`; never an offset, never a space.
- **wire.date.R2** The fraction has at most 7 digits (100 ns ticks); finer values are truncated, not rounded (`...:09.123456789Z` is `...:09.1234567Z`). Trailing zeros of a non-zero fraction are dropped (`.5`, `.3295740` is `.329574`); a value whose fraction is exactly zero is written with seven zeros (`2024-05-06T07:08:09.0000000Z`). So the fraction is either exactly `.0000000` or 1 to 7 digits whose last digit is not `0`; a bare `:09Z` is never emitted.
- **wire.date.R3** A date that was never set is the zero date `0001-01-01T00:00:00.0000000Z` (a session's `LastPlaybackCheckIn` without playback, an unused API key's `DateLastActivity`), not `null` and not the 2-digit-less form `0001-01-01T00:00:00Z`.
- **wire.date.R4** Inputs are more lenient than outputs: a date-time sent by a client may carry any fraction length, an offset (`+02:00`, converted to UTC) or no zone at all (read as UTC); the value read back is always in the output form above.

## Failure bodies

A failure that the route does not describe with an explicit message of its own is an RFC 9110 problem document: a JSON object with `type` (a URI string), `title`, `status` (equal to the HTTP status) and a `traceId` string. Some routes answer with an explicit bare JSON string instead (for example an unknown user id on `GET /Users/{userId}` answers `404` with `"User not found"`); those are specified in the area specs and are not problem documents.

- **wire.problem.R1** A `404` for an unknown item id (`GET /Items/{itemId}`) is a problem document with `status: 404` and `title: "Not Found"`.
- **wire.problem.R2** A `400` caused by a value that cannot be bound (a path or query id that is not a GUID, a body that is not valid JSON) is a problem document with `status: 400`, `title: "One or more validation errors occurred."` and an `errors` object that names the offending parameter or body location.
- **wire.problem.R3** A `400` that the route raises without a message, such as `GET /Users/Me` with an API key, is a problem document with `status: 400` and `title: "Bad Request"`.

## Rule -> scenarios

| Rule | Scenarios |
|---|---|
| `wire.path.R1` | `wire.path.trailing-slash-reads`, `wire.path.trailing-slash-writes` |
| `wire.path.R2` | `wire.path.empty-segment` |
| `wire.path.R3` | `wire.path.case-insensitive` |
| `wire.path.R4` | `wire.path.trailing-slash-reads` |
| `wire.date.R1` | `wire.date.login-dates`, `wire.date.other-members` |
| `wire.date.R2` | `wire.date.login-dates`, `wire.date.user-data-precision` |
| `wire.date.R3` | `wire.date.login-dates`, `wire.date.other-members` |
| `wire.date.R4` | `wire.date.user-data-precision` |
| `wire.id.R1` | `wire.id.user-objects`, `wire.id.item-objects` |
| `wire.id.R2` | `wire.id.dashed-input-accepted` |
| `wire.problem.R1` | `wire.problem.not-found` |
| `wire.problem.R2` | `wire.problem.validation` |
| `wire.problem.R3` | `wire.problem.api-key-is-not-a-user` |

<a id="server-identity"></a>
## Server identity

- **wire.identity.R1** `GET /System/Info/Public` (anonymous) and `GET /System/Info` report `ProductName` `"Jellyfin Server"` (a literal that clients compare against) and a `Version` that is the numeric semantic version of the Jellyfin generation the server implements, `12.<minor>.<patch>` with no pre-release suffix (the reference 12.2 image reports `12.2.0`). jellyfin-web gates features on this value, so a server reporting an older generation is offered the older feature set.
