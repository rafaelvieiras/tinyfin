// scheduled_tasks.go implements GET /ScheduledTasks — the admin Dashboard
// overview page's task list. The reference server ships a real
// task-scheduling engine (cron-like triggers, plugin-registered tasks) with
// ~18 entries out of the box (plugin updates, subtitle/lyric downloads,
// trickplay generation, audio normalization, database optimization,
// activity-log cleanup, ...). Tinyfin has none of that machinery — no
// plugins (docs/01), no trickplay subsystem, no subtitle/lyric fetching, no
// scheduling engine at all — so none of those are fabricated here.
//
// This reports exactly one entry: a library scan (POST /Library/Refresh,
// library_admin.go; POST /Items/{itemId}/Refresh when itemId is a library's
// own ID, item_refresh.go), backed by Server.scanState (server.go's
// scanTracker). The dashboard overview page this list serves only reads
// Name/State off each entry, so State is the one field that must be live;
// everything else is a fixed description of a capability that exists, not
// a schedule that runs it.
//
// Deliberately excluded: POST /Items/{itemId}/Refresh's *other* branch, an
// ordinary catalog item's per-item metadata refresh (Server.runItemRefresh).
// An early version of this project routed that through this same
// scanState, on the reasoning that something should show as "running" for
// it — found live to be its own bug: it made this task's State report
// Running (so jellyfin-web's Dashboard "Scan All Libraries" button spins/
// disables) for a single-item refresh with no library scan running at all.
// The reference server never conflates the two: a per-item refresh is
// tracked entirely separately (BaseItemDto.RefreshProgress/RefreshStatus on
// the item itself, plus a per-item RefreshProgress WebSocket message) and
// never touches GET /ScheduledTasks or its ScheduledTasksInfo WebSocket
// push at all. Tinyfin doesn't implement that per-item mechanism yet — a
// documented gap (docs/11), not a reason to borrow this one.
package api

import (
	"net/http"
)

// scheduledTaskLibraryScanID doubles as this task's Id and Key. It MUST be
// exactly "RefreshLibrary": jellyfin-web's Dashboard (both the home overview
// widget and the Libraries page) locates "the library scan task" by
// filtering the GET /ScheduledTasks list on Key === 'RefreshLibrary'
// (react/src/apps/dashboard/routes/index.tsx and .../routes/libraries/
// index.tsx in jellyfin-web — this is client-side JS, not the reference
// server's C#, so reading it isn't subject to AGENTS.md's clean-room
// requirement), then reads that task's Id back to call
// POST /ScheduledTasks/Running/{taskId} (handleScheduledTaskStart below).
// Found live: with any other value here, that .find() always returns
// undefined, so the widget's "isScanning" check (librariesTask?.State !==
// Idle) evaluates true forever — the Scan All Libraries button spins from
// page load, click or no click, and never stops.
const scheduledTaskLibraryScanID = "RefreshLibrary"

// taskTriggerInfo mirrors the reference server's TaskTriggerInfo. Nothing
// in this project ever populates one — see scheduledTaskInfo.Triggers.
type taskTriggerInfo struct {
	Type string `json:"Type"`
}

// taskExecutionResultInfo mirrors the reference server's
// TaskExecutionResultInfo. Name/Key/Id here just echo the parent task's own
// fields — this project tracks the outcome of only the single
// most-recently-finished scan (scanTracker), not a per-run history with its
// own identity, so there is no separate per-execution ID to report.
type taskExecutionResultInfo struct {
	StartTimeUtc string `json:"StartTimeUtc"`
	EndTimeUtc   string `json:"EndTimeUtc"`
	Status       string `json:"Status"`
	Name         string `json:"Name"`
	Key          string `json:"Key"`
	Id           string `json:"Id"`
}

// scheduledTaskInfo mirrors the reference server's TaskInfo, trimmed to the
// fields it actually sends (see the package doc comment for the ones this
// project can't honestly answer without a scheduling engine).
type scheduledTaskInfo struct {
	Name                string                   `json:"Name"`
	State               string                   `json:"State"`
	Id                  string                   `json:"Id"`
	LastExecutionResult *taskExecutionResultInfo `json:"LastExecutionResult,omitempty"`
	Triggers            []taskTriggerInfo        `json:"Triggers"`
	Description         string                   `json:"Description"`
	Category            string                   `json:"Category"`
	IsHidden            bool                     `json:"IsHidden"`
	Key                 string                   `json:"Key"`
}

// handleScheduledTasks is GET /ScheduledTasks (admin-only, matching the
// reference server's own scoping for the Dashboard's task list — nothing
// here should be visible to a non-admin user).
//
// State is derived live from Server.scanState: "Running" while any library
// scan is in flight anywhere in the process, "Idle" otherwise. There is no
// "Cancelling" state to report — nothing about a Tinyfin scan can be
// cancelled once started.
//
// Triggers is always an empty array, not omitted: Tinyfin has no
// scheduling engine (no cron-like trigger configuration exists anywhere in
// this project), so there is genuinely nothing to list — this is a real
// gap, documented here rather than silently defaulted, not the same thing
// as "this task doesn't exist" (which would be leaving the task out of the
// response entirely).
//
// LastExecutionResult is omitted entirely until a scan has finished at
// least once this process's lifetime (scanState.snapshot's lastEnd is the
// zero time.Time) — sending a fabricated one before any scan has run would
// misrepresent history that doesn't exist yet.
func (s *Server) handleScheduledTasks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.scheduledTasksSnapshot())
}

// scheduledTasksSnapshot builds the same task list handleScheduledTasks
// serves, factored out so websocket.go's notifyScheduledTasks (the
// ScheduledTasksInfo push channel, docs/12) can push the identical shape
// without duplicating this logic.
func (s *Server) scheduledTasksSnapshot() []scheduledTaskInfo {
	running, lastStart, lastEnd, lastErr := s.scanState.snapshot()

	state := "Idle"
	if running {
		state = "Running"
	}

	task := scheduledTaskInfo{
		Name:  "Scan Media Libraries",
		State: state,
		Id:    scheduledTaskLibraryScanID,
		// Non-nil so this serializes as [], never null (encoding/json would
		// otherwise emit null for a nil slice) — see the doc comment above
		// for why an empty list, not omission, is the honest answer here.
		Triggers:    []taskTriggerInfo{},
		Description: "Scans every configured library for new, changed, or removed media.",
		Category:    "Library",
		IsHidden:    false,
		Key:         scheduledTaskLibraryScanID,
	}

	if !lastEnd.IsZero() {
		status := "Completed"
		if lastErr != nil {
			status = "Failed"
		}
		task.LastExecutionResult = &taskExecutionResultInfo{
			// Wire date format (wire.date.R1-R2); the reference writes the
			// same 7-digit form here as everywhere else.
			StartTimeUtc: formatWireTime(lastStart),
			EndTimeUtc:   formatWireTime(lastEnd),
			Status:       status,
			Name:         task.Name,
			Key:          task.Key,
			Id:           task.Id,
		}
	}

	return []scheduledTaskInfo{task}
}

// handleScheduledTaskStart is POST /ScheduledTasks/Running/{taskId} — the
// route jellyfin-web's Dashboard "Scan All Libraries" button (and the
// Libraries page's equivalent) actually calls, once it has located the task
// via GET /ScheduledTasks' Key === 'RefreshLibrary' (see
// scheduledTaskLibraryScanID's doc comment). Any taskId other than that
// constant addresses a task this project doesn't have, so it 404s rather
// than silently starting the only scan this project knows how to run.
func (s *Server) handleScheduledTaskStart(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("taskId") != scheduledTaskLibraryScanID {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if err := s.refreshAllLibraries(r.Context()); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
