package api

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"
)

// taskDef describes one real background task the server can run.
type taskDef struct {
	key, id, name, description, category string
	defaults                             []taskTriggerInfo
}

func ticks(v int64) *int64 { return &v }

const ticksPerHour = int64(36000000000)

// taskRegistry lists the tasks in the order GET /ScheduledTasks reports
// them. The ids are the reference server's (they are stable on the wire).
var taskRegistry = []taskDef{
	{
		key: "CleanActivityLog", id: "b461ef918ab28520928183e794350e3c",
		name: "Clean Activity Log", category: "Maintenance",
		description: "Deletes activity log entries older than the configured age.",
	},
	{
		key: "RefreshTrickplayImages", id: "64f5f44cd30dc273cb9890205473bbcc",
		name: "Generate Trickplay Images", category: "Library",
		description: "Creates trickplay previews for videos in enabled libraries.",
		defaults:    []taskTriggerInfo{{Type: "DailyTrigger", TimeOfDayTicks: ticks(3 * ticksPerHour)}},
	},
	{
		key: "RefreshLibrary", id: "7738148ffcd07979c7ceb148e06b3aed",
		name: "Scan Media Library", category: "Library",
		description: "Scans your media library for new files and refreshes metadata.",
		defaults:    []taskTriggerInfo{{Type: "IntervalTrigger", IntervalTicks: ticks(12 * ticksPerHour)}},
	},
}

const activityLogRetention = 30 * 24 * time.Hour

func taskByID(id string) (taskDef, bool) {
	for _, d := range taskRegistry {
		if d.id == id {
			return d, true
		}
	}
	return taskDef{}, false
}

func validTriggerType(t string) bool {
	switch t {
	case "DailyTrigger", "WeeklyTrigger", "IntervalTrigger", "StartupTrigger":
		return true
	}
	return false
}

type taskLastRun struct {
	start, end time.Time
	status     string
}

// taskRuntime is the in-memory run state of the tasks that are not backed by
// a tracker of their own (the library scan uses Server.scanState, trickplay
// the Generator).
type taskRuntime struct {
	mu       sync.Mutex
	running  map[string]bool
	last     map[string]*taskLastRun
	lastFire map[string]time.Time
}

func (s *Server) taskRT() *taskRuntime {
	s.taskRTOnce.Do(func() {
		s.taskRTv = &taskRuntime{running: map[string]bool{}, last: map[string]*taskLastRun{}, lastFire: map[string]time.Time{}}
	})
	return s.taskRTv
}

func triggerSettingKey(def taskDef) string { return "ScheduledTaskTriggers:" + def.key }

// taskTriggers returns the stored triggers, or the task's defaults when none
// were ever saved. Always non-nil (serializes as []).
func (s *Server) taskTriggers(def taskDef) []taskTriggerInfo {
	if v, ok, err := s.store.GetSetting(context.Background(), triggerSettingKey(def)); err == nil && ok {
		var t []taskTriggerInfo
		if json.Unmarshal([]byte(v), &t) == nil && t != nil {
			return t
		}
	}
	if def.defaults == nil {
		return []taskTriggerInfo{}
	}
	return append([]taskTriggerInfo{}, def.defaults...)
}

func (s *Server) setTaskTriggers(ctx context.Context, def taskDef, t []taskTriggerInfo) error {
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return s.store.SetSetting(ctx, triggerSettingKey(def), string(b))
}

// taskStatus reports State, progress percentage and the last execution.
func (s *Server) taskStatus(def taskDef) (state string, pct float64, last *taskLastRun) {
	switch def.key {
	case "RefreshLibrary":
		running, start, end, err := s.scanState.snapshot()
		if running {
			return "Running", s.scanState.progress(), nil
		}
		if end.IsZero() {
			return "Idle", 0, nil
		}
		status := "Completed"
		s.scanState.mu.Lock()
		cancelled := s.scanState.lastCancelled
		s.scanState.mu.Unlock()
		switch {
		case cancelled:
			status = "Cancelled"
		case err != nil:
			status = "Failed"
		}
		return "Idle", 0, &taskLastRun{start: start, end: end, status: status}
	case "RefreshTrickplayImages":
		if s.trickplay.TaskRunning() {
			return "Running", 0, nil
		}
		r := s.trickplay.TaskLast()
		if r.Start.IsZero() {
			return "Idle", 0, nil
		}
		status := "Completed"
		if r.Cancelled {
			status = "Cancelled"
		}
		return "Idle", 0, &taskLastRun{start: r.Start, end: r.End, status: status}
	}
	rt := s.taskRT()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.running[def.key] {
		return "Running", 0, nil
	}
	return "Idle", 0, rt.last[def.key]
}

// startTask queues the task; a task that is already running is left alone.
func (s *Server) startTask(ctx context.Context, def taskDef) error {
	switch def.key {
	case "RefreshLibrary":
		if running, _, _, _ := s.scanState.snapshot(); running {
			return nil
		}
		return s.refreshAllLibraries(ctx)
	case "RefreshTrickplayImages":
		libs, err := s.store.QueryLibraries(ctx)
		if err != nil {
			return err
		}
		s.trickplay.RunTask(libs)
		s.notifyScheduledTasks(context.Background(), true)
		go func() {
			// push the final state once the batch is done
			for s.trickplay.TaskRunning() {
				time.Sleep(200 * time.Millisecond)
			}
			s.notifyScheduledTasks(context.Background(), true)
		}()
		return nil
	case "CleanActivityLog":
		rt := s.taskRT()
		rt.mu.Lock()
		if rt.running[def.key] {
			rt.mu.Unlock()
			return nil
		}
		rt.running[def.key] = true
		rt.mu.Unlock()
		start := time.Now().UTC()
		s.notifyScheduledTasks(context.Background(), true)
		go func() {
			status := "Completed"
			if _, err := s.store.DeleteActivityEntriesBefore(context.Background(), start.Add(-activityLogRetention)); err != nil {
				log.Printf("api: clean activity log: %v", err)
				status = "Failed"
			}
			rt.mu.Lock()
			rt.running[def.key] = false
			rt.last[def.key] = &taskLastRun{start: start, end: time.Now().UTC(), status: status}
			rt.mu.Unlock()
			s.notifyScheduledTasks(context.Background(), true)
		}()
	}
	return nil
}

// stopTask cancels a running task; a task that is not running (or cannot be
// interrupted, like the activity-log sweep) is a no-op.
func (s *Server) stopTask(def taskDef) {
	switch def.key {
	case "RefreshLibrary":
		s.scanState.cancelAll()
	case "RefreshTrickplayImages":
		s.trickplay.CancelTask()
	}
}

// runTaskScheduler fires triggers: StartupTrigger once at boot, Interval
// every IntervalTicks since the last fire, Daily/Weekly at the local time
// of day (and weekday). Checked every 30 seconds.
func (s *Server) runTaskScheduler(ctx context.Context) {
	const step = 30 * time.Second
	boot := time.Now()
	prev := boot
	first := true
	t := time.NewTicker(step)
	defer t.Stop()
	for {
		now := time.Now()
		for _, def := range taskRegistry {
			if s.triggerDue(def, boot, prev, now, first) {
				s.taskRT().mu.Lock()
				s.taskRT().lastFire[def.key] = now
				s.taskRT().mu.Unlock()
				if err := s.startTask(ctx, def); err != nil {
					log.Printf("api: scheduled task %s: %v", def.key, err)
				}
			}
		}
		prev, first = now, false
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) triggerDue(def taskDef, boot, prev, now time.Time, first bool) bool {
	for _, tr := range s.taskTriggers(def) {
		switch tr.Type {
		case "StartupTrigger":
			if first {
				return true
			}
		case "IntervalTrigger":
			if tr.IntervalTicks == nil || *tr.IntervalTicks <= 0 {
				continue
			}
			rt := s.taskRT()
			rt.mu.Lock()
			ref, ok := rt.lastFire[def.key]
			rt.mu.Unlock()
			if !ok {
				ref = boot
			}
			if now.Sub(ref) >= time.Duration(*tr.IntervalTicks)*100*time.Nanosecond {
				return true
			}
		case "DailyTrigger", "WeeklyTrigger":
			if tr.TimeOfDayTicks == nil {
				continue
			}
			y, m, d := now.Date()
			at := time.Date(y, m, d, 0, 0, 0, 0, now.Location()).Add(time.Duration(*tr.TimeOfDayTicks) * 100 * time.Nanosecond)
			if tr.Type == "WeeklyTrigger" && at.Weekday().String() != tr.DayOfWeek {
				continue
			}
			if at.After(prev) && !at.After(now) && !first {
				return true
			}
		}
	}
	return false
}
