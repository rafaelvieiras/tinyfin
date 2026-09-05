// scheduled_tasks.go implements GET /ScheduledTasks — the admin Dashboard
// overview page's task list. The reference server ships a real
// task-scheduling engine (cron-like triggers, plugin-registered tasks) with
// ~18 entries out of the box (plugin updates, subtitle/lyric downloads,
// trickplay generation, audio normalization, database optimization,
// activity-log cleanup, ...). Tinyfin has none of that machinery — no
// plugins (docs/01), no trickplay subsystem, no subtitle/lyric fetching, no
// scheduling engine at all — so none of those are fabricated here.
//
// This reports exactly one entry: a library scan, the one real background
// operation this project actually has (POST /Library/Refresh,
// POST /Items/{itemId}/Refresh — library_admin.go, item_refresh.go), backed
// by Server.scanState (server.go's scanTracker). The dashboard overview
// page this list serves only reads Name/State off each entry, so State is
// the one field that must be live; everything else is a fixed description
// of a capability that exists, not a schedule that runs it.
package api

import (
	"net/http"
	"time"
)

// scheduledTaskLibraryScanID is Tinyfin's own identifier for the one task
// this endpoint reports — not required to match the reference server's
// internal key for its equivalent task, since nothing in this project's
// client-facing flow acts on Key beyond echoing it back.
const scheduledTaskLibraryScanID = "library-scan"

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
			// RFC3339Nano matches this package's other wire-visible
			// timestamp formatting (dto.go's DateCreated etag derivation) —
			// not separately confirmed against the reference server's own
			// TaskExecutionResultInfo timestamp precision for this new
			// endpoint.
			StartTimeUtc: lastStart.Format(time.RFC3339Nano),
			EndTimeUtc:   lastEnd.Format(time.RFC3339Nano),
			Status:       status,
			Name:         task.Name,
			Key:          task.Key,
			Id:           task.Id,
		}
	}

	writeJSON(w, http.StatusOK, []scheduledTaskInfo{task})
}
