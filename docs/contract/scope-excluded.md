# Routes excluded from the contract (and legacy routes kept for the web client)

Scope rules: see [`README.md`](README.md). This list covers the pilot areas (users, items, playstate); it grows as areas are added.

## Excluded — legacy with a modern replacement, not used by the official web client

Tinyfin is not required to implement these. The replacement is specified instead.

| Excluded route | Use instead |
|---|---|
| `DELETE /PlayingItems/{itemId}` | `POST /Sessions/Playing/Stopped` |
| `DELETE /Users/{userId}/PlayingItems/{itemId}` | `POST /Sessions/Playing/Stopped` |
| `GET /Users/{userId}/Items/Resume` | `GET /UserItems/Resume` |
| `GET /Users/{userId}/Items/{itemId}/UserData` | `GET /UserItems/{itemId}/UserData` |
| `POST /PlayingItems/{itemId}` | `POST /Sessions/Playing` |
| `POST /PlayingItems/{itemId}/Progress` | `POST /Sessions/Playing/Progress` |
| `POST /Users/{userId}/Authenticate` | `POST /Users/AuthenticateByName` |
| `POST /Users/{userId}/Items/{itemId}/UserData` | `POST /UserItems/{itemId}/UserData` |
| `POST /Users/{userId}/PlayingItems/{itemId}` | `POST /Sessions/Playing` |
| `POST /Users/{userId}/PlayingItems/{itemId}/Progress` | `POST /Sessions/Playing/Progress` |

## Kept — legacy, but still called by the official web client (`legacy-web-required`)

Specified as thin aliases of the modern route (user id taken from the path). Isolated under `scope: legacy-web-required` so they can be dropped once the client stops using them.

| Route kept | Modern equivalent | Evidence |
|---|---|---|
| `DELETE /Users/{userId}/PlayedItems/{itemId}` | `DELETE /UserPlayedItems/{itemId}?userId=` | static:2 |
| `GET /Users/{userId}/Items` | `GET /Items?userId=` | static:37 |
| `GET /Users/{userId}/Items/{itemId}` | `/Items/{itemId}?userId=` | static:46, live-capture |
| `POST /Users/{userId}` | `POST /Users?userId=` | static:1 |
| `POST /Users/{userId}/Configuration` | `POST /Users/Configuration?userId=` | static:6 |
| `POST /Users/{userId}/Password` | `POST /Users/Password?userId=` | static:2 |
| `POST /Users/{userId}/PlayedItems/{itemId}` | `POST /UserPlayedItems/{itemId}?userId=` | static:2, live-capture |

Evidence legend: `static:N` = N call sites in the web client source (via its embedded API client); `live-capture` = observed on the wire from the official 12.2 web client in a real session.
