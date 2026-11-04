# Devices

Per-device-and-user entries, device options (custom name), refresh from request headers and deletion. Probed black-box on the 12.2 reference. Every route here is administrator-only (`403` for a user, `401` anonymous). The list/info shape is `admin.devices.R1`-`R3` in [`admin.md`](admin.md).

Scenarios: [`../scenarios/devices.json`](../scenarios/devices.json). The scenarios create their own accounts (`tmp-dev-a1`, `tmp-dev-a2`) and use run-specific DeviceIds.

## <a id="entries"></a>Entries and tokens

- **devices.R1** A device is registered per `DeviceId` **and user**: when two accounts sign in on the same `DeviceId`, `GET /Devices` has two entries with that `Id` (each with its own `LastUserId`/`LastUserName`). Signing in again as the same account refreshes its entry and adds no new one.
- **devices.R2** `GET /Devices` is ordered by `DateLastActivity`, most recent first; a login refreshes it. `userId` must be a GUID (`400` otherwise) but does **not** narrow the list on the 12.2 reference (the old `admin.devices.R3` wording "keeps the devices that user used" does not hold there; every `userId` returns the full list, also one with no devices), and Tinyfin matches that. `GET /Devices/Info?id=` answers the most recently active entry of the `DeviceId`.
- **devices.R3** Tokens are per user and device: a login revokes the same account's earlier token on that `DeviceId` and leaves other accounts' tokens on it valid.
- **devices.R4** `GET`/`POST /Devices/Options` and `DELETE /Devices` are administrator-only: non-admin `403`, anonymous `401`.
- **devices.R5** An authenticated request that carries `Device` and/or `Version` in its `Authorization` header updates the `Name` and `AppVersion` of the token owner's entry (`DateLastActivity` is not touched). `AppName` stays what the login registered; a header with only the token changes nothing.

## <a id="options"></a>Options and custom names

- **devices.R6** `GET /Devices/Options?id=` is `{Id, DeviceId, CustomName?}` (`Id` a number) or `404` when no options record exists; no `id` is a `400` validation problem (`errors.id`). `POST /Devices/Options?id=` with `{CustomName}` answers `204` and **replaces** the record, creating it when needed, also for an id that is not a registered device; a missing/`null` `CustomName` clears it (the member is absent in `GET`), `""` is stored as `""`. No `id` is the same `400`.
- **devices.R7** A custom name is added to the entries as `CustomName` (`GET /Devices`, `/Devices/Info`); `Name` stays the name the client sent. The record is per `DeviceId`, so it applies to every user's entry and outlives the entries (it survives `DELETE /Devices`).

## <a id="delete"></a>Deletion

- **devices.R8** `DELETE /Devices?id=` answers `204`, removes every entry of the `DeviceId` and revokes every token issued to it, whatever the account (also the caller's own). Tokens of other devices are untouched; `/Devices/Info` answers `404` afterwards.
- **devices.R9** An unknown `id` is a `400` problem document (`title` "Bad Request"); no `id` is a `204` no-op.
- **devices.R10** Deleting an account removes its device entries (other accounts' entries on the same `DeviceId` stay).
- **devices.R13** Parameter names on the device routes are matched without regard to case, as on every other route (`items.list.R3`): `DELETE /Devices?Id=`, `?ID=` and `?id=` are one request, likewise `userId` on `GET /Devices` and `id` on `GET /Devices/Info` and `GET`/`POST /Devices/Options`. Unknown parameters are ignored. The routes are still documented with the lower-case spelling the reference's OpenAPI declares.

## <a id="legacy-id"></a>Legacy per-user DeviceId

- **devices.R11** [divergence] A `DeviceId` whose suffix is the caller's own account id — the hyphenated UUID form or the bare 32-character form — is the device with that suffix removed: the device entry, the token, the `Device`/`Version` refresh (`R5`) and the session all use the stripped id, so the two spellings one install sends land on one device. Only the caller's own id is stripped; a suffix that names no account stays part of the id (the reference keeps such an id as a device of its own, `573c553d8a24e29f18d15755-…`). Why: the Android app appends the id of the account that signed in to its own device id and then sends both spellings (`573c553d8a24e29f` and `573c553d8a24e29f18de42d3-…`), so on the reference one phone becomes two devices and two sessions, and a sign-in looks like a duplicate. Tinyfin keeps one.
- **devices.R12** A successful login removes the same account's other entries whose `DeviceId` is this device's id with that account's id appended, together with their tokens and sessions: an install that stored the suffixed id before `R11` collapses into the bare one on its next login. Entries of other accounts are untouched.

## Divergences / open

* The reference also shows the custom name as the `DeviceName` of the device's sessions (`HasCustomDeviceName` true); Tinyfin does that in `GET /Sessions` only for sessions it tracks (one per `DeviceId`). Not asserted by a scenario.
* The `Id` of an options record is an auto-increment number on both servers; only its type is asserted.
* `devices.R11`/`R12` are deliberate: the reference stores the account-suffixed `DeviceId` as a second device, Tinyfin collapses it. `R12` (collapsing entries stored before `R11`) is only reachable in-process — a black-box run has no way to seed the suffixed entry — so it is covered by a Go test (`TestLoginCollapsesLegacyDeviceEntry`); the scenario covers `R11` and its consequence for a second sign-in.

## Rule -> scenarios

| Rule | Scenarios |
|---|---|
| `devices.R1`, `R2`, `R3` | `devices.per-user.two-accounts` |
| `devices.R4`, `R6`, `R7` | `devices.options.crud` |
| `devices.R5` | `devices.refresh.request-headers` |
| `devices.R4`, `R8`, `R9`, `R13` | `devices.delete.revokes-tokens` |
| `devices.R10` | `devices.user-delete.removes-entries` |
| `devices.R11`, `R12` | `devices.id-suffix.collapses` (R12 partially, see the note above) |
