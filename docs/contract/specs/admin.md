# Administration: scheduled tasks, logs, backups, plugins

The dashboard's maintenance routes. Probed on the 12.2 reference (fresh server, online metadata off). Every route here is administrator-only: anonymous callers get `401`, a non-admin user `403`.

Scenarios: [`../scenarios/admin.json`](../scenarios/admin.json). Fixture state: [`../fixtures.json`](../fixtures.json) (user `alice` for the permission checks).

## <a id="tasks"></a>Scheduled tasks

- **admin.tasks.R1** `GET /ScheduledTasks` is a list of task objects `{Name, State, CurrentProgressPercentage?, Id, LastExecutionResult?, Triggers, Description, Category, IsHidden, Key}`. `Id` is 32 lowercase hex digits and differs from `Key`. `State` is `Idle`, `Running` or `Cancelling`; `CurrentProgressPercentage` is present only while running; `LastExecutionResult` (`{StartTimeUtc, EndTimeUtc, Status, Name, Key, Id}`, `Status` `Completed`, `Failed` or `Cancelled`) only after a run. Query `isHidden` / `isEnabled` filter the list.
- **admin.tasks.R2** The tasks Tinyfin exposes are the ones it can really run, with the reference's keys and ids: `RefreshLibrary` (`Scan Media Library`, Id `7738148ffcd07979c7ceb148e06b3aed`, category `Library`), `RefreshTrickplayImages` (`Generate Trickplay Images`, `64f5f44cd30dc273cb9890205473bbcc`, `Library`) and `CleanActivityLog` (`Clean Activity Log`, `b461ef918ab28520928183e794350e3c`, `Maintenance`). The jellyfin-web dashboard finds the library scan by `Key === "RefreshLibrary"` and reads its `Id` back.
- **admin.tasks.R3** `GET /ScheduledTasks/{taskId}` is one task by `Id`; the `Key` is not accepted (`404`), nor is an unknown id.
- **admin.tasks.R4** `POST /ScheduledTasks/{taskId}/Triggers` replaces the whole trigger list (`204`); an empty list removes every trigger. A trigger is `{Type, TimeOfDayTicks?, IntervalTicks?, DayOfWeek?, MaxRuntimeTicks?}` with `Type` one of `DailyTrigger`, `WeeklyTrigger`, `IntervalTrigger`, `StartupTrigger`. The list persists and is returned by later reads.
- **admin.tasks.R5** An unknown trigger `Type` (or a missing body) is a `400` validation problem; an unknown task is `404`.
- **admin.tasks.R6** `POST /ScheduledTasks/Running/{taskId}` queues the task and answers `204` at once; `State` becomes `Running` while it works. Starting a task that is already running changes nothing.
- **admin.tasks.R7** An unknown task id is `404` on start and on stop.
- **admin.tasks.R8** `DELETE /ScheduledTasks/Running/{taskId}` cancels a running task (`State` `Cancelling`, then `LastExecutionResult.Status` `Cancelled`).
- **admin.tasks.R9** The routes are administrator-only.

Defaults when no trigger was ever saved: `RefreshLibrary` every 12 h (`IntervalTicks` 432000000000), `RefreshTrickplayImages` daily at 03:00 (`TimeOfDayTicks` 108000000000), `CleanActivityLog` none.

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

## Known differences

* **Task list.** The reference lists about twenty tasks (plugin updates, subtitle and lyric downloads, audio normalization, people refresh, ...). Tinyfin lists only the three it implements; the other keys do not exist (`404` by id). Clients that look tasks up by key already tolerate this.
* **Stopping an idle task (R8).** The reference answers `500` (unhandled error, plain text). Tinyfin answers `204` as a no-op; the scenario `admin.tasks.stop-idle` is divergent.
* **Task progress.** `CurrentProgressPercentage` for `RefreshLibrary` is the share of libraries finished in the current run; the trickplay task reports `0` while running.
* **Backups.** Only the database is saved (a consistent SQLite snapshot, `database/tinyfin.db` inside the zip next to `manifest.json`); `Metadata`, `Trickplay` and `Subtitles` are echoed in `Options` but ignored and `Database` is always `true`. Archives are written to `<data dir>/backups/tinyfin-backup-<timestamp>.zip`. **`POST /Backup/Restore` validates its input and then answers `501`**: replacing the database of a running server is not planned (restore by stopping the server and copying `database/tinyfin.db` over the database file).
* **Backup during a scan.** The reference refuses `POST /Backup/Create` with a `500` while a library scan runs ("Cannot create a backup while a library scan is running"). Tinyfin's snapshot is consistent at any time, so it creates the archive regardless; the scenario waits for the fixture scan to finish before creating one.
* **Plugins (divergent by design).** No plugin system: `GET /Plugins`, `/Packages` and `/Repositories` are empty lists (the reference lists its bundled providers and one repository). Installing, enabling, configuring and uninstalling plugins (`/Packages/Installed/*`, `/Plugins/{id}/*`, `POST /Repositories`) are not implemented.
* **Logs.** `cmd/tinyfin` tees its log output into `<data dir>/log/log_YYYYMMDD.log`; the format is the Go standard logger's, not the reference's. There is no log retention (the reference has a cleanup task).

## Rule → scenarios

| Rule | Scenarios |
|---|---|
| `admin.tasks.R1`, `R2` | `admin.tasks.list` |
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
