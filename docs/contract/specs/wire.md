# Spec: wire (cross-cutting wire format)

Behavior that every route shares and that clients observe directly: how identifiers are written and what a failure body looks like. Rules have stable IDs (`wire.<topic>.R<n>`) that scenarios in `../scenarios/wire.json` reference through `covers`. The area-specific specs say which status a route answers; this one says what the body looks like.

## Identifiers

- **wire.id.R1** Every identifier in a JSON response body (`Id`, `ServerId`, `ParentId`, user and item ids, ...) is written as 32 lowercase hexadecimal digits with no dashes (`0123456789abcdef0123456789abcdef`). Upper case and the dashed 8-4-4-4-12 form are not emitted.
- **wire.id.R2** Identifiers in a path or query parameter are accepted in the dashed form as well as the undashed one, in any letter case, and name the same object as the undashed form.

## Failure bodies

A failure that the route does not describe with an explicit message of its own is an RFC 9110 problem document: a JSON object with `type` (a URI string), `title`, `status` (equal to the HTTP status) and a `traceId` string. Some routes answer with an explicit bare JSON string instead (for example an unknown user id on `GET /Users/{userId}` answers `404` with `"User not found"`); those are specified in the area specs and are not problem documents.

- **wire.problem.R1** A `404` for an unknown item id (`GET /Items/{itemId}`) is a problem document with `status: 404` and `title: "Not Found"`.
- **wire.problem.R2** A `400` caused by a value that cannot be bound (a path or query id that is not a GUID, a body that is not valid JSON) is a problem document with `status: 400`, `title: "One or more validation errors occurred."` and an `errors` object that names the offending parameter or body location.
- **wire.problem.R3** A `400` that the route raises without a message, such as `GET /Users/Me` with an API key, is a problem document with `status: 400` and `title: "Bad Request"`.

## Rule -> scenarios

| Rule | Scenarios |
|---|---|
| `wire.id.R1` | `wire.id.user-objects`, `wire.id.item-objects` |
| `wire.id.R2` | `wire.id.dashed-input-accepted` |
| `wire.problem.R1` | `wire.problem.not-found` |
| `wire.problem.R2` | `wire.problem.validation` |
| `wire.problem.R3` | `wire.problem.api-key-is-not-a-user` |
