# Administration: scheduled tasks, logs, backups, plugins

The dashboard's maintenance routes. Probed on the 12.2 reference (fresh server, online metadata off). Every route here is administrator-only: anonymous callers get `401`, a non-admin user `403`.

Scenarios: [`../scenarios/admin.json`](../scenarios/admin.json). Fixture state: [`../fixtures.json`](../fixtures.json) (user `alice` for the permission checks).

## <a id="tasks"></a>Scheduled tasks

- **admin.tasks.R1** `GET /ScheduledTasks` is a list of task objects `{Name, State, CurrentProgressPercentage?, Id, LastExecutionResult?, Triggers, Description, Category, IsHidden, Key}`. `Id` is 32 lowercase hex digits and differs from `Key`. `State` is `Idle`, `Running` or `Cancelling`; `CurrentProgressPercentage` is present only while running; `LastExecutionResult` (`{StartTimeUtc, EndTimeUtc, Status, Name, Key, Id}`, `Status` `Completed`, `Failed` or `Cancelled`) only after a run. Query `isHidden` / `isEnabled` filter the list.
- **admin.tasks.R2** The list is the reference's: twenty tasks, sorted by `Name`, each with the reference's `Key`, `Id`, `Name`, `Category`, `Description` and `IsHidden` (`RefreshInternetChannels` and `RefreshGuide` are the hidden ones; `isHidden=false` lists the other eighteen). Among them `RefreshLibrary` (`Scan Media Library`, Id `7738148ffcd07979c7ceb148e06b3aed`, category `Library`), `RefreshTrickplayImages` (`Generate Trickplay Images`, `64f5f44cd30dc273cb9890205473bbcc`, `Library`), `RefreshChapterImages` (`Extract Chapter Images`, `4e6637c832ed644d1af3370a2506e80a`, `Library`), `CleanActivityLog` (`Clean Activity Log`, `b461ef918ab28520928183e794350e3c`, `Maintenance`), `DeleteCacheFiles` (`Clean Cache Directory`), `CleanLogFiles` (`Clean Log Directory`) and `PluginUpdates` (`Update Plugins`, `Application`). The jellyfin-web dashboard finds the library scan by `Key === "RefreshLibrary"` and reads its `Id` back, and lists the tasks by `Category`.
- **admin.tasks.R3** `GET /ScheduledTasks/{taskId}` is one task by `Id`; the `Key` is not accepted (`404`), nor is an unknown id.
- **admin.tasks.R4** `POST /ScheduledTasks/{taskId}/Triggers` replaces the whole trigger list (`204`); an empty list removes every trigger. A trigger is `{Type, TimeOfDayTicks?, IntervalTicks?, DayOfWeek?, MaxRuntimeTicks?}` with `Type` one of `DailyTrigger`, `WeeklyTrigger`, `IntervalTrigger`, `StartupTrigger`. The list persists and is returned by later reads.
- **admin.tasks.R5** An unknown trigger `Type` (or a missing body) is a `400` validation problem; an unknown task is `404`.
- **admin.tasks.R6** `POST /ScheduledTasks/Running/{taskId}` queues the task and answers `204` at once; `State` becomes `Running` while it works. Starting a task that is already running changes nothing.
- **admin.tasks.R7** An unknown task id is `404` on start and on stop.
- **admin.tasks.R8** `DELETE /ScheduledTasks/Running/{taskId}` cancels a running task (`State` `Cancelling`, then `LastExecutionResult.Status` `Cancelled`).
- **admin.tasks.R9** The routes are administrator-only.

- **admin.tasks.R10** Every task can be started and completes: `State` goes back to `Idle` and `LastExecutionResult.Status` is `Completed`. The tasks that belong to subsystems the server does not have (plugin updates, subtitle and lyric downloads, audio normalization, keyframe extraction, media segments, people, guide and channel refreshes, ...) have nothing to do and finish at once without changing anything; the cache, transcode and database tasks are likewise empty. `CleanLogFiles` deletes the log files older than three days.
- **admin.tasks.R11** The default triggers (while none was saved) are the reference's: `DeleteCacheFiles`, `CleanLogFiles`, `AudioNormalization`, `DownloadLyrics`, `DownloadSubtitles`, `RefreshInternetChannels` and `RefreshGuide` every 24 h; `DeleteTranscodeFiles` and `PluginUpdates` at startup and every 24 h; `RefreshChapterImages` daily at 02:00 with a 4 h `MaxRuntimeTicks`; `RefreshTrickplayImages` daily at 03:00; `MediaSegment` extraction (`TaskExtractMediaSegments`) every 12 h; `OptimizeDatabaseTask` every 6 h; `RefreshPeople` and `TmdbRefreshUpcomingEpisodes` every 7 days; `RefreshLibrary` every 12 h (`IntervalTicks` 432000000000); `CleanActivityLog`, `KeyframeExtraction`, `MoveTrickplayImages` and `CleanupUserDataTask` none. The exact ticks are asserted by the scenario.
- **admin.tasks.R12** [untestable: the runner's WebSocket steps cannot subscribe to a channel; covered by a Go test] When a running task is stopped, the final state (`Idle`, with its last result) is pushed over the `ScheduledTasksInfo` WebSocket channel like every other state change, so a dashboard that stopped the task does not keep showing its progress.

## <a id="logs"></a>Logs

- **admin.logs.R1** `GET /System/Logs` lists the server's log files as `{DateCreated, DateModified, Size, Name}`, newest first; names are `log_YYYYMMDD.log`.
- **admin.logs.R2** `GET /System/Logs/Log?name=<file>` returns the file as `text/plain`.
- **admin.logs.R3** A name that is not in the list (including a path with directory parts) is `404` with the JSON string body `"Log file not found."`.

## <a id="backup"></a>Backups

- **admin.backup.R1** `POST /Backup/Create` takes `{Metadata, Trickplay, Subtitles, Database}` (all optional; an empty body is accepted) and answers a manifest `{ServerVersion, BackupEngineVersion, DateCreated, Path, Options}` with `Path` the archive's `.zip` location.
- **admin.backup.R2** `GET /Backup` lists the manifests of the archives that exist.
- **admin.backup.R3** `GET /Backup/Manifest?path=` returns one manifest; the archive is found by its file name, so the `Path` from the manifest or a bare name both work. Unknown is `404`.
- **admin.backup.R4** `POST /Backup/Restore` takes `{ArchiveFileName}`; a missing member is a `400` validation problem and an unknown archive `404`.

## <a id="plugins"></a>Plugins, packages, repositories

- **admin.plugins.R1** `GET /Plugins`, `GET /Packages` and `GET /Repositories` answer `200` with a list.

## <a id="pages"></a>Dashboard probes

The administration pages call these on load; none carries state.

- **admin.pages.R1** `GET /LiveTv/Info` (any signed-in user) is `{Services:[{Name:"Emby", Status:"Ok", HasUpdateAvailable:false, IsVisible:false, Tuners:[]}], IsEnabled:true, EnabledUsers:[]}`. Live TV is not implemented; this is the report of a server without tuners.
- **admin.pages.R2** `GET /Channels` (any signed-in user) is `{Items:[], TotalRecordCount:0, StartIndex:0}`: there are no channel plugins.
- **admin.pages.R3** `GET /Auth/Providers` and `GET /Auth/PasswordResetProviders` (administrator) are lists of `{Name, Id}` with one entry each (`Default` and `Default Password Reset Provider`). The `Id` values are opaque: clients put them back unchanged into `Policy.AuthenticationProviderId` / `PasswordResetProviderId` (`users.update.R14`), and a profile save with an empty one is refused.
- **admin.pages.R4** `GET /Library/MediaFolders` (administrator) is the list of library roots (`CollectionFolder` items, sorted by name) plus the playlists folder (`ManualPlaylistsFolder`, `CollectionType` `playlists`). `isHidden=false` leaves the playlists folder out, `isHidden=true` returns only it.

## <a id="devices"></a>Devices

- **admin.devices.R1** `GET /Devices` (administrator) is `{Items, TotalRecordCount, StartIndex}` with one entry per registered device: `{Name, Id, LastUserName, AppName, AppVersion, LastUserId, DateLastActivity, Capabilities}`, most recently active first. `Id` is the `DeviceId` the client sent, `Name` its `Device`, `AppName` its `Client`; a sign-in registers or refreshes the device.
- **admin.devices.R2** `Capabilities` is the same block for every device: `{PlayableMediaTypes:[], SupportedCommands:[], SupportsMediaControl:false, SupportsPersistentIdentifier:true}`, whatever the client registered for its session.
- **admin.devices.R3** `userId` keeps the devices that user used; one that is not a GUID is a `400` validation problem. `GET /Devices/Info?id=` is one entry (`404` for an unknown id). Both are administrator-only (`403`, anonymous `401`).

## <a id="config"></a>Configuration sections

- **admin.config.R1** `GET /System/Configuration/{network|encoding|branding}` is readable by any signed-in user (anonymous `401`); `POST` is administrator-only (`403`). `GET /Branding/Configuration` (anonymous) is the branding section, `GET /Branding/Css` and `/Branding/Css.css` (anonymous) its `CustomCss` as `text/css` (empty when unset).
- **admin.config.R2** The encoding section (the Transcoding page) has the members `EncodingThreadCount` (-1), `HardwareAccelerationType` (`none`), `EnableHardwareEncoding` (true), `H264Crf` (23), `H265Crf` (28), `EncoderPreset` (`auto`), `DeinterlaceMethod` (`yadif`), `EnableTonemapping` (false), `TonemappingAlgorithm` (`bt2390`), `HardwareDecodingCodecs` (`["h264","vc1"]`), `EnableSubtitleExtraction` (true), `SubtitleExtractionTimeoutMinutes` (30), `VaapiDevice`, `QsvDevice`, the throttling, segment-deletion, down-mix and decoding-depth switches, and `EncoderAppPathDisplay` (the encoder path, absent when unknown); the scenario asserts the types and the defaults the page reads.
- **admin.config.R3** A `POST` replaces the whole section: members left out fall back to their default (`{}` resets the section) and unknown members are ignored. The branding section is `{LoginDisclaimer, CustomCss, SplashscreenEnabled}`; the two strings are `null` (left out of the JSON) until set, `SplashscreenEnabled` is `false`.
- **admin.config.R4** The network section has `BaseUrl` (""), `EnableHttps`/`RequireHttps` (false), `CertificatePath`/`CertificatePassword` (""), `InternalHttpPort` 8096, `InternalHttpsPort` 8920, `PublicHttpPort` 8096, `PublicHttpsPort` 8920, `AutoDiscovery` true, `EnableUPnP` false, `EnableIPv4` true, `EnableIPv6` false, `EnableRemoteAccess` true, `LocalNetworkSubnets`, `LocalNetworkAddresses`, `KnownProxies` (empty lists), `IgnoreVirtualInterfaces` true, `VirtualInterfaceNames` `["veth"]`, `EnablePublishedServerUriByRequest` false, `PublishedServerUriBySubnet`, `RemoteIPFilter` (empty), `IsRemoteIPFilterBlacklist` false. Same replace semantics as `admin.config.R3`.

## <a id="libraries"></a>Library rename

`POST /Library/VirtualFolders/Name?name=<current>&newName=<new>[&refreshLibrary=true]` (administrator).

- **admin.libraries.R1** The rename answers `204`; `GET /Library/VirtualFolders` then lists the library under the new name and no longer under the old one. A library scan that is still running from `POST /Library/VirtualFolders` (or started by the rename itself with `refreshLibrary=true`) does not bring the old name back.
- **admin.libraries.R2** A new name that another library already has (compared exactly, case-sensitive) answers `409` and changes nothing.
- **admin.libraries.R3** Renaming a library to its own name answers `204` and changes nothing; an unknown `name` answers `404`.

## Known differences

* **Task list.** Twenty tasks like the reference, but most only exist to keep the list and the dashboard identical: they complete at once and do nothing (`admin.tasks.R10`). `isEnabled` and `isHidden=true` filtering is by the trigger list and `IsHidden` here; the reference additionally treats the library tasks as disabled while no library exists.
* **Stopping an idle task (R8).** The reference answers `500` (unhandled error, plain text). Tinyfin answers `204` as a no-op; the scenario `admin.tasks.stop-idle` is divergent.
* **Task progress.** `CurrentProgressPercentage` for `RefreshLibrary` is the share of libraries finished in the current run; the trickplay task reports `0` while running.
* **Backups.** Only the database is saved (a consistent SQLite snapshot, `database/tinyfin.db` inside the zip next to `manifest.json`); `Metadata`, `Trickplay` and `Subtitles` are echoed in `Options` but ignored and `Database` is always `true`. Archives are written to `<data dir>/backups/tinyfin-backup-<timestamp>.zip`. **`POST /Backup/Restore` validates its input and then answers `501`**: replacing the database of a running server is not planned (restore by stopping the server and copying `database/tinyfin.db` over the database file).
* **Backup during a scan.** The reference refuses `POST /Backup/Create` with a `500` while a library scan runs ("Cannot create a backup while a library scan is running"). Tinyfin's snapshot is consistent at any time, so it creates the archive regardless; the scenario waits for the fixture scan to finish before creating one.
* **Plugins (divergent by design).** No plugin system: `GET /Plugins`, `/Packages` and `/Repositories` are empty lists (the reference lists its bundled providers and one repository). Installing, enabling, configuring and uninstalling plugins (`/Packages/Installed/*`, `/Plugins/{id}/*`, `POST /Repositories`) are not implemented.
* **Logs.** `cmd/tinyfin` tees its log output into `<data dir>/log/log_YYYYMMDD.log`; the format is the Go standard logger's, not the reference's. There is no log retention (the reference has a cleanup task).
* **Configuration sections.** Tinyfin stores and serves back every member but acts only on `EnableRemoteAccess` and `LocalNetworkSubnets` (network), `EnableUPnP` stays `false`, and the encoding section is not applied to the transcoder. A `BaseUrl`, port, HTTPS or address-family change does not move the listener (the reference re-roots its routes under a new `BaseUrl`); an unknown `HardwareAccelerationType` is stored (the reference answers `500`); `EncoderAppPathDisplay` is always the server's configured encoder path (the reference drops it after a save). The network `GET` is open to any signed-in user like the reference's; `GET /System/Configuration` stays administrator-only.
* **Devices.** One entry per `DeviceId` (the most recent user); the reference keeps one per device and user. `userId` selects by the last user; the reference selects by any token of that user. `Devices/Options`, device renaming and `DELETE /Devices` are not implemented.
* **Media folders.** `Path`, `Etag`, `ImageTags` and the rest of the folder item's detail are not reported; the entries are the same as `GET /UserViews` gives.
* **Provider ids.** The authentication and password-reset provider `Id` values are Tinyfin's own (opaque to clients); scenarios assert only that they exist and round-trip.

## Rule → scenarios

| Rule | Scenarios |
|---|---|
| `admin.tasks.R1`, `R2` | `admin.tasks.list` |
| `admin.tasks.R2`, `R10` | `admin.tasks.catalog` |
| `admin.tasks.R11` | `admin.tasks.default-triggers` |
| `admin.tasks.R10` | `admin.tasks.run-maintenance` |
| `admin.tasks.R2`, `R3` | `admin.tasks.get` |
| `admin.tasks.R4`, `R5` | `admin.tasks.triggers` |
| `admin.tasks.R6`, `R7` | `admin.tasks.run` |
| `admin.tasks.R8` | `admin.tasks.stop-idle` |
| `admin.tasks.R9` | `admin.tasks.auth` |
| `admin.logs.R1` | `admin.logs.list` |
| `admin.logs.R2`, `R3` | `admin.logs.file` |
| `admin.backup.R1`, `R2`, `R3` | `admin.backup.create` |
| `admin.backup.R4` | `admin.backup.restore-validation` |
| `admin.plugins.R1` | `admin.plugins.list` |
| `admin.pages.R1` | `admin.livetv.info` |
| `admin.pages.R2` | `admin.channels.list` |
| `admin.pages.R3` | `admin.auth.providers` |
| `admin.pages.R4` | `admin.mediafolders.list` |
| `admin.devices.R1`, `R2`, `R3` | `admin.devices.list` |
| `admin.config.R1`, `R2`, `R3` | `admin.config.encoding` |
| `admin.config.R1`, `R3` | `admin.config.branding` |
| `admin.config.R1`, `R4` | `admin.config.network-fields` |
| `admin.libraries.R1` | `admin.libraries.rename-during-scan` |
| `admin.libraries.R2`, `R3` | `admin.libraries.rename-rules` |
