# Small utility routes

Contract for the small routes clients call for reachability, clock synchronisation and dropdown data: `/System/Ping`,
`/GetUtcTime` and the `/Localization/*` lists.

Scenarios: [`../scenarios/misc.json`](../scenarios/misc.json). Rule IDs are stable (`misc.<topic>.R<n>`).

## <a id="ping"></a>GET and POST /System/Ping

### Rules

- **misc.ping.R1** `GET /System/Ping` and `POST /System/Ping` answer `200` with the product name as a JSON string (the `ProductName` of `GET /System/Info/Public`), with no authentication.

## <a id="utc-time"></a>GET /GetUtcTime

### Rules

- **misc.time.R1** `GET /GetUtcTime` answers `200` with `{"RequestReceptionTime", "ResponseTransmissionTime"}`, both server UTC instants in the wire date format (`YYYY-MM-DDTHH:MM:SS.fffffffZ`, wire.date), the second not before the first; no authentication.

## <a id="localization"></a>GET /Localization/*

**Auth.** An authenticated user once the first-run setup is complete (the lists are open while it is not).

### Rules

- **misc.localization.R1** `GET /Localization/Options` answers the interface languages the server offers as `[{Name, Value}]` (`Value` a culture code such as `en-US`); anonymous is `401` after setup.
- **misc.localization.R2** `GET /Localization/ParentalRatings` answers the rating table as `[{Name, Value, RatingScore: {score, subScore}}]` ordered by value, starting with `Unrated` (no value): `G` 0, `PG` 10, `PG-13` 13, `R` 17, `NC-17` 17 (sub score 1), `TV-MA` 17 (sub score 1), up to `Banned` 1001.
- **misc.localization.R3** `GET /Localization/Cultures` and `GET /Localization/Countries` answer the language and country lists (`Name`, `DisplayName`, two- and three-letter codes); anonymous is `401` after setup.

## <a id="home"></a>Probes the web home page makes

The official web client asks for these two lists before it lays out the home page; when either answers `404` the page stops after the library tiles and shows no "Recently Added" rows.

- **misc.home.R1** `GET /SyncPlay/List` answers `200` with a JSON array, empty while nobody has started a group; anonymous is `401`.
- **misc.home.R2** `GET /LiveTv/Programs/Recommended` (`userId`, `limit`, `isAiring`, ... as sent by the client) answers `200` with `{"Items":[],"TotalRecordCount":0,"StartIndex":0}` while no live TV source exists; anonymous is `401`.

## Index of rules to scenarios

| Rule | Scenario |
|---|---|
| `misc.ping.R1` | `misc.ping` |
| `misc.time.R1` | `misc.utc-time` |
| `misc.localization.R1` | `misc.localization` |
| `misc.localization.R2` | `misc.localization` |
| `misc.localization.R3` | `misc.localization` |
| `misc.home.R1` | `misc.home.syncplay-list` |
| `misc.home.R2` | `misc.home.recommended-programs` |
