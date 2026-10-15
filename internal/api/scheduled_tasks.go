// scheduled_tasks.go implements the admin Dashboard's task list and its
// control routes: GET /ScheduledTasks, GET /ScheduledTasks/{taskId},
// POST|DELETE /ScheduledTasks/Running/{taskId} and
// POST /ScheduledTasks/{taskId}/Triggers.
//
// Tinyfin exposes only tasks that exist for real (scheduled_tasks_engine.go
// holds the registry, the trigger persistence and the small scheduler):
// "Scan Media Library" (RefreshLibrary), "Generate Trickplay Images"
// (RefreshTrickplayImages) and "Clean Activity Log" (CleanActivityLog). The
// reference's other entries (plugin updates, subtitle/lyric downloads,
// audio normalization, ...) belong to subsystems this project does not have
// and are not fabricated. Ids are the reference's: a 32-hex id distinct
// from the Key; jellyfin-web finds the library scan by Key === 'RefreshLibrary'
// and then uses the Id it reads back.
//
// Deliberately excluded: POST /Items/{itemId}/Refresh's per-item branch
// (Server.runItemRefresh). The reference tracks it on the item itself, never
// on the task list, so it must not make the library scan task report Running.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
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
const scheduledTaskLibraryScanID = "7738148ffcd07979c7ceb148e06b3aed"

// taskTriggerInfo mirrors the reference's TaskTriggerInfo. Only the fields
// relevant to a trigger Type are sent.
type taskTriggerInfo struct {
	Type            string `json:"Type"`
	TimeOfDayTicks  *int64 `json:"TimeOfDayTicks,omitempty"`
	IntervalTicks   *int64 `json:"IntervalTicks,omitempty"`
	DayOfWeek       string `json:"DayOfWeek,omitempty"`
	MaxRuntimeTicks *int64 `json:"MaxRuntimeTicks,omitempty"`
}

// taskExecutionResultInfo mirrors TaskExecutionResultInfo; Name/Key/Id echo
// the parent task.
type taskExecutionResultInfo struct {
	StartTimeUtc string `json:"StartTimeUtc"`
	EndTimeUtc   string `json:"EndTimeUtc"`
	Status       string `json:"Status"`
	Name         string `json:"Name"`
	Key          string `json:"Key"`
	Id           string `json:"Id"`
}

// scheduledTaskInfo mirrors the reference's TaskInfo.
type scheduledTaskInfo struct {
	Name                      string                   `json:"Name"`
	State                     string                   `json:"State"`
	CurrentProgressPercentage *float64                 `json:"CurrentProgressPercentage,omitempty"`
	Id                        string                   `json:"Id"`
	LastExecutionResult       *taskExecutionResultInfo `json:"LastExecutionResult,omitempty"`
	Triggers                  []taskTriggerInfo        `json:"Triggers"`
	Description               string                   `json:"Description"`
	Category                  string                   `json:"Category"`
	IsHidden                  bool                     `json:"IsHidden"`
	Key                       string                   `json:"Key"`
}

// handleScheduledTasks is GET /ScheduledTasks (admin-only). isHidden and
// isEnabled are accepted like the reference: isHidden filters on IsHidden,
// isEnabled=true keeps only tasks that have at least one trigger.
func (s *Server) handleScheduledTasks(w http.ResponseWriter, r *http.Request) {
	all := s.scheduledTasksSnapshot()
	q := r.URL.Query()
	out := make([]scheduledTaskInfo, 0, len(all))
	for _, t := range all {
		if v := q.Get("isHidden"); v != "" {
			if b, err := strconv.ParseBool(v); err == nil && t.IsHidden != b {
				continue
			}
		}
		if v := q.Get("isEnabled"); v != "" {
			if b, err := strconv.ParseBool(v); err == nil && (len(t.Triggers) > 0) != b {
				continue
			}
		}
		out = append(out, t)
	}
	writeJSON(w, http.StatusOK, out)
}

// scheduledTasksSnapshot builds the task list; websocket.go's
// notifyScheduledTasks pushes the identical shape.
func (s *Server) scheduledTasksSnapshot() []scheduledTaskInfo {
	out := make([]scheduledTaskInfo, 0, len(taskRegistry))
	for _, def := range taskRegistry {
		out = append(out, s.taskInfo(def))
	}
	return out
}

func (s *Server) taskInfo(def taskDef) scheduledTaskInfo {
	st, pct, last := s.taskStatus(def)
	info := scheduledTaskInfo{
		Name:        def.name,
		State:       st,
		Id:          def.id,
		Triggers:    s.taskTriggers(def),
		Description: def.description,
		Category:    def.category,
		Key:         def.key,
	}
	if st == "Running" || st == "Cancelling" {
		info.CurrentProgressPercentage = &pct
	}
	if last != nil {
		info.LastExecutionResult = &taskExecutionResultInfo{
			StartTimeUtc: formatWireTime(last.start),
			EndTimeUtc:   formatWireTime(last.end),
			Status:       last.status,
			Name:         def.name,
			Key:          def.key,
			Id:           def.id,
		}
	}
	return info
}

// handleScheduledTaskGet is GET /ScheduledTasks/{taskId}; the id is the
// 32-hex Id, not the Key (the reference 404s on the Key).
func (s *Server) handleScheduledTaskGet(w http.ResponseWriter, r *http.Request) {
	def, ok := taskByID(r.PathValue("taskId"))
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, s.taskInfo(def))
}

// handleScheduledTaskPost routes POST /ScheduledTasks/Running/{taskId} and
// POST /ScheduledTasks/{taskId}/Triggers (distinct only by position).
func (s *Server) handleScheduledTaskPost(w http.ResponseWriter, r *http.Request) {
	first, second := r.PathValue("first"), r.PathValue("second")
	switch {
	case strings.EqualFold(first, "Running"):
		r.SetPathValue("taskId", second)
		s.handleScheduledTaskStart(w, r)
	case strings.EqualFold(second, "Triggers"):
		r.SetPathValue("taskId", first)
		s.handleScheduledTaskTriggers(w, r)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// handleScheduledTaskStart is POST /ScheduledTasks/Running/{taskId}: 204
// once the task is queued (a task already running is left alone), 404 for an
// unknown id.
func (s *Server) handleScheduledTaskStart(w http.ResponseWriter, r *http.Request) {
	def, ok := taskByID(r.PathValue("taskId"))
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if err := s.startTask(r.Context(), def); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleScheduledTaskStop is DELETE /ScheduledTasks/Running/{taskId}. 404
// for an unknown id; 204 otherwise, including for a task that is not running
// (the reference answers 500 there, a defect, see the spec).
func (s *Server) handleScheduledTaskStop(w http.ResponseWriter, r *http.Request) {
	def, ok := taskByID(r.PathValue("taskId"))
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	s.stopTask(def)
	w.WriteHeader(http.StatusNoContent)
}

// handleScheduledTaskTriggers is POST /ScheduledTasks/{taskId}/Triggers: the
// body is the full replacement list of triggers.
func (s *Server) handleScheduledTaskTriggers(w http.ResponseWriter, r *http.Request) {
	def, ok := taskByID(r.PathValue("taskId"))
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var body []taskTriggerInfo
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body == nil {
		writeValidationErrors(w, map[string][]string{"triggerInfos": {"The triggerInfos field is required."}})
		return
	}
	for i, t := range body {
		if !validTriggerType(t.Type) {
			writeValidationErrors(w, map[string][]string{fmt.Sprintf("$[%d].Type", i): {"The JSON value could not be converted to a trigger type."}})
			return
		}
	}
	if err := s.setTaskTriggers(r.Context(), def, body); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
