package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// activityLogEntryDto mirrors the OpenAPI schema's ActivityLogEntry,
// trimmed to the fields this project's deliberately narrow activity log
// (storage.ActivityEntry's doc comment) can honestly populate. Confirmed
// live against a real Jellyfin server (called twice by jellyfin-web's
// Dashboard overview page — once per widget), e.g. a successful login:
//
//	{"Id":140,"Name":"admin is online on Chrome","ShortOverview":"IP Address: 203.0.113.7",
//	 "Type":"SessionStarted","Date":"2026-09-04T23:24:40.095896Z","UserId":"b3017418aebd460699f4f2cadadcb542",
//	 "Severity":"Information"}
//
// A failed-login entry has no stored user (storage.ActivityEntry.UserID's
// doc comment, what hasUserId=false matches); on the wire its UserId is the
// all-zero id, as the 12.2 reference does (users.auth.R26). Name/
// ShortOverview are plain English here even though the reference server
// localizes them (its own live example above is Portuguese): this project
// has no i18n system (internal/api/localization.go's scope), so English
// text is the honest, consistent choice, not a deviation.
type activityLogEntryDto struct {
	Id            int64     `json:"Id"`
	Name          string    `json:"Name"`
	ShortOverview string    `json:"ShortOverview"`
	Type          string    `json:"Type"`
	Date          time.Time `json:"Date"`
	ItemId        *string   `json:"ItemId,omitempty"`
	UserId        *string   `json:"UserId,omitempty"`
	Severity      string    `json:"Severity"`
}

// activityLogEntriesResult mirrors the OpenAPI schema's
// QueryResult<ActivityLogEntry>, same envelope shape as every other
// paginated list response in this package (StartIndex echoes back the
// request's own startIndex, TotalRecordCount is the pre-pagination match
// count).
type activityLogEntriesResult struct {
	Items            []activityLogEntryDto `json:"Items"`
	TotalRecordCount int                   `json:"TotalRecordCount"`
	StartIndex       int                   `json:"StartIndex"`
}

// defaultActivityLogLimit is used when a caller omits limit entirely — the
// reference server's own dashboard widgets always send one (7 or 4 in the
// confirmed-live examples below), but the query param is optional on the
// wire, so this project still needs an honest default rather than silently
// requiring it (storage.ActivityEntryFilter.Limit must be > 0).
const defaultActivityLogLimit = 100

// handleActivityLogEntries serves GET /System/ActivityLog/Entries
// (admin-only — jellyfin-web's Dashboard overview page is the only known
// caller, and every other admin-dashboard-only route in this package is
// wired the same way). Confirmed live, called twice by that page on load:
//
//	GET /System/ActivityLog/Entries?startIndex=0&limit=7&minDate=2026-09-03T23:24:40.205Z&hasUserId=true
//	GET /System/ActivityLog/Entries?startIndex=0&limit=4&minDate=2026-08-28T23:24:40.207Z&hasUserId=false
//
// for its "Activity" widget (recent successful logins, hasUserId=true) and
// its "Alerts" widget (recent failed logins, hasUserId=false) respectively
// — this project generates no other null-UserId activity type, so
// hasUserId=false is exactly "failed logins" here, not a general filter.
// Query param names are lowerCamelCase (startIndex/limit/minDate/
// hasUserId), matching the reference server's own casing for this route
// exactly as observed live, unlike this package's usual PascalCase query
// params elsewhere.
func (s *Server) handleActivityLogEntries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	limit := defaultActivityLogLimit
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	startIndex := 0
	if v := q.Get("startIndex"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			startIndex = n
		}
	}

	filter := storage.ActivityEntryFilter{Limit: limit, Offset: startIndex}
	if v := q.Get("minDate"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			filter.MinDate = &t
		}
	}
	if v := q.Get("hasUserId"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			filter.HasUserID = &b
		}
	}

	entries, total, err := s.store.QueryActivityEntries(r.Context(), filter)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	items := make([]activityLogEntryDto, 0, len(entries))
	for _, e := range entries {
		items = append(items, activityEntryToDto(e))
	}

	writeJSON(w, http.StatusOK, activityLogEntriesResult{
		Items:            items,
		TotalRecordCount: total,
		StartIndex:       startIndex,
	})
}

// activityEntryToDto maps a stored entry to its wire shape, shared by the
// REST route and the ActivityLogEntry WebSocket push.
func activityEntryToDto(e storage.ActivityEntry) activityLogEntryDto {
	dto := activityLogEntryDto{
		Id:            e.ID,
		Name:          e.Name,
		ShortOverview: e.ShortOverview,
		Type:          string(e.Type),
		Date:          e.Date,
		Severity:      string(e.Severity),
	}
	if e.UserID != nil {
		uid := string(*e.UserID)
		dto.UserId = &uid
	} else {
		// An entry with no user (a failed login) shows the all-zero id on
		// the wire, as the reference does (users.auth.R26); the stored
		// NULL is what hasUserId=false matches.
		zero := "00000000000000000000000000000000"
		dto.UserId = &zero
	}
	if e.ItemID != nil {
		iid := string(*e.ItemID)
		dto.ItemId = &iid
	}
	return dto
}
