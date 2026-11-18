package api

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// taskDef describes one real background task the server can run.
type taskDef struct {
	key, id, name, description, category string
	defaults                             []taskTriggerInfo
	hidden                               bool
}

func ticks(v int64) *int64 { return &v }

// taskRegistry lists the tasks in the order GET /ScheduledTasks reports
// them. The ids are the reference server's (they are stable on the wire).
var taskRegistry = []taskDef{
	{
		key: "AudioNormalization", id: "ec2f221fd8e7706b3d3afd2c4591b4d7",
		name: "Audio Normalization", category: "Library",
		description: "Scans files for audio normalization data.",
		defaults:    []taskTriggerInfo{{Type: "IntervalTrigger", IntervalTicks: ticks(864000000000)}},
	},
	{
		key: "CleanActivityLog", id: "b461ef918ab28520928183e794350e3c",
		name: "Clean Activity Log", category: "Maintenance",
		description: "Deletes activity log entries older than the configured age.",
	},
	{
		key: "DeleteCacheFiles", id: "241d4fcb19a1d557ee62428e411da609",
		name: "Clean Cache Directory", category: "Maintenance",
		description: "Deletes cache files no longer needed by the system.",
		defaults:    []taskTriggerInfo{{Type: "IntervalTrigger", IntervalTicks: ticks(864000000000)}},
	},
	{
		key: "CleanLogFiles", id: "1c8ede62c521bea0bf851344f5b8ca40",
		name: "Clean Log Directory", category: "Maintenance",
		description: "Deletes log files that are more than 3 days old.",
		defaults:    []taskTriggerInfo{{Type: "IntervalTrigger", IntervalTicks: ticks(864000000000)}},
	},
	{
		key: "DeleteTranscodeFiles", id: "7d8088c10902f1bf4072ded42437bcfb",
		name: "Clean Transcode Directory", category: "Maintenance",
		description: "Deletes transcode files more than one day old.",
		defaults:    []taskTriggerInfo{{Type: "StartupTrigger"}, {Type: "IntervalTrigger", IntervalTicks: ticks(864000000000)}},
	},
	{
		key: "DownloadLyrics", id: "26649fe0aad57557245351f220da916c",
		name: "Download missing lyrics", category: "Library",
		description: "Downloads lyrics for songs",
		defaults:    []taskTriggerInfo{{Type: "IntervalTrigger", IntervalTicks: ticks(864000000000)}},
	},
	{
		key: "DownloadSubtitles", id: "2c66a88bca43e565d7f8099f825478f1",
		name: "Download missing subtitles", category: "Library",
		description: "Searches the internet for missing subtitles based on metadata configuration.",
		defaults:    []taskTriggerInfo{{Type: "IntervalTrigger", IntervalTicks: ticks(864000000000)}},
	},
	{
		key: "RefreshChapterImages", id: "4e6637c832ed644d1af3370a2506e80a",
		name: "Extract Chapter Images", category: "Library",
		description: "Creates thumbnails for videos that have chapters.",
		defaults:    []taskTriggerInfo{{Type: "DailyTrigger", TimeOfDayTicks: ticks(72000000000), MaxRuntimeTicks: ticks(144000000000)}},
	},
	{
		key: "RefreshTrickplayImages", id: "64f5f44cd30dc273cb9890205473bbcc",
		name: "Generate Trickplay Images", category: "Library",
		description: "Creates trickplay previews for videos in enabled libraries.",
		defaults:    []taskTriggerInfo{{Type: "DailyTrigger", TimeOfDayTicks: ticks(108000000000)}},
	},
	{
		key: "KeyframeExtraction", id: "f302d80f31bcacf76f979d277d448581",
		name: "Keyframe Extractor", category: "Library",
		description: "Extracts keyframes from video files to create more precise HLS playlists. This task may run for a long time.",
	},
	{
		key: "TaskExtractMediaSegments", id: "f861734dd71b37f9482b52a820e39013",
		name: "Media Segment Scan", category: "Library",
		description: "Extracts or obtains media segments from MediaSegment enabled plugins.",
		defaults:    []taskTriggerInfo{{Type: "IntervalTrigger", IntervalTicks: ticks(432000000000)}},
	},
	{
		key: "MoveTrickplayImages", id: "ebb6e58c4e9a8f5d1f77ab6ddefb3143",
		name: "Migrate Trickplay Image Location", category: "Maintenance",
		description: "Moves existing trickplay files according to the library settings.",
	},
	{
		key: "OptimizeDatabaseTask", id: "31de9ce83b9223d338c77b1a635e144b",
		name: "Optimize database", category: "Maintenance",
		description: "Compacts database and truncates free space. Running this task after scanning the library or doing other changes that imply database modifications might improve performance.",
		defaults:    []taskTriggerInfo{{Type: "IntervalTrigger", IntervalTicks: ticks(216000000000)}},
	},
	{
		key: "RefreshInternetChannels", id: "0c9ee3a88fc15547c6852205480da1fd",
		name: "Refresh Channels", category: "Internet Channels",
		description: "Refreshes internet channel information.",
		hidden:      true,
		defaults:    []taskTriggerInfo{{Type: "IntervalTrigger", IntervalTicks: ticks(864000000000)}},
	},
	{
		key: "RefreshGuide", id: "bea9b218c97bbf98c5dc1303bdb9a0ca",
		name: "Refresh Guide", category: "Live TV",
		description: "Downloads channel information from live tv services.",
		hidden:      true,
		defaults:    []taskTriggerInfo{{Type: "IntervalTrigger", IntervalTicks: ticks(864000000000)}},
	},
	{
		key: "RefreshPeople", id: "866456ed0d44e15468124ce33d85961e",
		name: "Refresh People", category: "Library",
		description: "Updates metadata for actors and directors in your media library.",
		defaults:    []taskTriggerInfo{{Type: "IntervalTrigger", IntervalTicks: ticks(6048000000000)}},
	},
	{
		key: "TmdbRefreshUpcomingEpisodes", id: "1ee23a5dd39fb4225715375c91bc2990",
		name: "Refresh upcoming and missing episodes (TheMovieDb)", category: "Library",
		description: "Checks TheMovieDb for newly announced episodes and creates virtual entries for unaired and missing episodes, according to the TMDb plugin settings. When both options are disabled, removes any virtual entries previously created.",
		defaults:    []taskTriggerInfo{{Type: "IntervalTrigger", IntervalTicks: ticks(6048000000000)}},
	},
	{
		key: "RefreshLibrary", id: "7738148ffcd07979c7ceb148e06b3aed",
		name: "Scan Media Library", category: "Library",
		description: "Scans your media library for new files and refreshes metadata.",
		defaults:    []taskTriggerInfo{{Type: "IntervalTrigger", IntervalTicks: ticks(432000000000)}},
	},
	{
		key: "PluginUpdates", id: "f9b057c054e9e6daee4a88ffd146a403",
		name: "Update Plugins", category: "Application",
		description: "Downloads and installs updates for plugins that are configured to update automatically.",
		defaults:    []taskTriggerInfo{{Type: "StartupTrigger"}, {Type: "IntervalTrigger", IntervalTicks: ticks(864000000000)}},
	},
	{
		key: "CleanupUserDataTask", id: "8240e309407dbbf43c99d30a6cbd239e",
		name: "User data cleanup task", category: "Maintenance",
		description: "Cleans all user data (Watch state, favorite status etc) from media that is no longer present for at least 90 days.",
	},
}

const (
	activityLogRetention = 30 * 24 * time.Hour
	logFileRetention     = 3 * 24 * time.Hour
)

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
	// runToken counts the runs started per task, so a runtime-limit timer
	// armed for one run cannot cancel the one that replaced it.
	runToken map[string]int
}

func (s *Server) taskRT() *taskRuntime {
	s.taskRTOnce.Do(func() {
		s.taskRTv = &taskRuntime{running: map[string]bool{}, last: map[string]*taskLastRun{}, lastFire: map[string]time.Time{}, runToken: map[string]int{}}
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
	case "RefreshChapterImages":
		if s.chapterimg.TaskRunning() {
			pct, _ := s.chapterimg.TaskProgress()
			return "Running", pct, nil
		}
		r := s.chapterimg.TaskLast()
		if r.Start.IsZero() {
			return "Idle", 0, nil
		}
		status := "Completed"
		if r.Cancelled {
			status = "Cancelled"
		}
		return "Idle", 0, &taskLastRun{start: r.Start, end: r.End, status: status}
	case "RefreshTrickplayImages":
		if s.trickplay.TaskRunning() {
			pct, _ := s.trickplay.TaskProgress()
			return "Running", pct, nil
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
// limit is the MaxRuntimeTicks of the trigger that fired it, nil when a client
// started the run by hand: a run that outlives its limit is cancelled
// (admin.tasks.R13).
func (s *Server) startTask(ctx context.Context, def taskDef, limit *int64) error {
	started, err := s.startTaskOnce(ctx, def)
	if err != nil || !started {
		return err
	}
	s.armRuntimeLimit(def, limit)
	return nil
}

// armRuntimeLimit cancels a run that outlives the limit its trigger set. The
// token is bumped for every run started, limited or not, so a timer armed for
// an older run finds a different token and leaves the current one alone.
func (s *Server) armRuntimeLimit(def taskDef, limit *int64) {
	rt := s.taskRT()
	rt.mu.Lock()
	rt.runToken[def.key]++
	token := rt.runToken[def.key]
	rt.mu.Unlock()
	if limit == nil || *limit <= 0 {
		return
	}
	d := time.Duration(*limit) * 100 * time.Nanosecond
	time.AfterFunc(d, func() {
		rt.mu.Lock()
		current := rt.runToken[def.key]
		rt.mu.Unlock()
		if current != token {
			return // that run is over and a newer one owns the token
		}
		log.Printf("api: scheduled task %s passed its runtime limit of %s; stopping it", def.key, d)
		s.stopTask(def)
	})
}

// startTaskOnce does the work of startTask and reports whether a run was
// actually started; a task that was already running is not.
func (s *Server) startTaskOnce(ctx context.Context, def taskDef) (bool, error) {
	switch def.key {
	case "RefreshLibrary":
		if running, _, _, _ := s.scanState.snapshot(); running {
			return false, nil
		}
		return true, s.refreshAllLibraries(ctx)
	case "RefreshChapterImages":
		libs, err := s.store.QueryLibraries(ctx)
		if err != nil {
			return false, err
		}
		s.chapterimg.RunTask(libs)
		s.notifyScheduledTasks(context.Background(), true)
		s.watchBatch(def)
		return true, nil
	case "RefreshTrickplayImages":
		libs, err := s.store.QueryLibraries(ctx)
		if err != nil {
			return false, err
		}
		s.trickplay.RunTask(libs)
		s.notifyScheduledTasks(context.Background(), true)
		s.watchBatch(def)
		return true, nil
	default:
		// CleanActivityLog and CleanLogFiles do real work; every other entry
		// belongs to a subsystem Tinyfin does not have (plugins, live TV,
		// lyrics, ...) and completes at once without touching anything
		// (admin.tasks.R10).
		rt := s.taskRT()
		rt.mu.Lock()
		if rt.running[def.key] {
			return false, nil
		}
		rt.running[def.key] = true
		rt.mu.Unlock()
		start := time.Now().UTC()
		s.notifyScheduledTasks(context.Background(), true)
		go func() {
			status := "Completed"
			if err := s.runMaintenanceTask(def.key, start); err != nil {
				log.Printf("api: scheduled task %s: %v", def.key, err)
				status = "Failed"
			}
			rt.mu.Lock()
			rt.running[def.key] = false
			rt.last[def.key] = &taskLastRun{start: start, end: time.Now().UTC(), status: status}
			rt.mu.Unlock()
			s.notifyScheduledTasks(context.Background(), true)
		}()
		return true, nil
	}
}

// watchBatch pushes the task list once per percentage point while a chapter or
// trickplay batch runs, then the final state, so a dashboard sees the bar move
// instead of a readout frozen at zero (the scan does the same through
// scanTracker.setProgress).
func (s *Server) watchBatch(def taskDef) {
	go func() {
		last := -1
		for {
			pct, running := float64(0), false
			switch def.key {
			case "RefreshChapterImages":
				running = s.chapterimg.TaskRunning()
				pct, _ = s.chapterimg.TaskProgress()
			case "RefreshTrickplayImages":
				running = s.trickplay.TaskRunning()
				pct, _ = s.trickplay.TaskProgress()
			}
			if !running {
				break
			}
			if whole := int(pct); whole != last {
				last = whole
				s.notifyScheduledTasks(context.Background(), false)
			}
			time.Sleep(200 * time.Millisecond)
		}
		s.notifyScheduledTasks(context.Background(), true)
	}()
}

// runMaintenanceTask is the work of the tasks that run on the shared
// run-state tracker: the activity-log sweep and the log-file sweep do real
// work, the rest is a no-op.
func (s *Server) runMaintenanceTask(key string, start time.Time) error {
	switch key {
	case "CleanActivityLog":
		_, err := s.store.DeleteActivityEntriesBefore(context.Background(), start.Add(-activityLogRetention))
		return err
	case "CleanLogFiles":
		entries, err := os.ReadDir(s.logDir())
		if err != nil {
			return nil // no log directory, nothing to clean
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasPrefix(e.Name(), "log_") || !strings.HasSuffix(e.Name(), ".log") {
				continue
			}
			if st, err := e.Info(); err == nil && start.Sub(st.ModTime()) > logFileRetention {
				_ = os.Remove(filepath.Join(s.logDir(), e.Name()))
			}
		}
	}
	return nil
}

// stopTask cancels a running task; a task that is not running (or cannot be
// interrupted, like the activity-log sweep) is a no-op.
func (s *Server) stopTask(def taskDef) {
	switch def.key {
	case "RefreshLibrary":
		s.scanState.cancelAll()
	case "RefreshChapterImages":
		s.chapterimg.CancelTask()
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
			tr := s.dueTrigger(def, boot, prev, now, first)
			if tr == nil {
				continue
			}
			s.taskRT().mu.Lock()
			s.taskRT().lastFire[def.key] = now
			s.taskRT().mu.Unlock()
			if err := s.startTask(ctx, def, tr.MaxRuntimeTicks); err != nil {
				log.Printf("api: scheduled task %s: %v", def.key, err)
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

// dueTrigger reports the trigger of def that is due now, or nil; the
// scheduler needs the trigger itself, not just the fact that it fired: its
// MaxRuntimeTicks bounds the run.
func (s *Server) dueTrigger(def taskDef, boot, prev, now time.Time, first bool) *taskTriggerInfo {
	for _, tr := range s.taskTriggers(def) {
		switch tr.Type {
		case "StartupTrigger":
			if first {
				return &tr
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
				return &tr
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
				return &tr
			}
		}
	}
	return nil
}
