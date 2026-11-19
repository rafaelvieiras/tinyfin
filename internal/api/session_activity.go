package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// writeActivity appends entries to the activity log in order, stamping each
// with the current time, and tells subscribed sockets once. Like
// logAuthFailure it never changes the answer the client sees: a failure is
// logged and dropped.
func (s *Server) writeActivity(ctx context.Context, entries ...storage.ActivityEntry) {
	wrote := false
	for _, e := range entries {
		e.Date = time.Now().UTC()
		if _, err := s.store.AppendActivityEntry(ctx, e); err != nil {
			log.Printf("api: appending %s activity log entry: %v", e.Type, err)
			break
		}
		wrote = true
	}
	if wrote {
		s.notifyActivityLog(ctx)
	}
}

// sessionEndedEntry is the entry written when a session's token goes away
// (users.auth.R32): the device name and address are those recorded when the
// token was issued.
func sessionEndedEntry(userID storage.UserID, userName, deviceName, address string) storage.ActivityEntry {
	uid := userID
	return storage.ActivityEntry{
		Name:          fmt.Sprintf("%s has disconnected from %s", userName, deviceName),
		ShortOverview: "IP address: " + address,
		Type:          storage.ActivityTypeSessionEnded,
		UserID:        &uid,
		Severity:      storage.ActivitySeverityInfo,
	}
}

// tokenDeviceName is the device name recorded on a token, falling back to
// the device record for tokens issued before it was recorded.
func (s *Server) tokenDeviceName(ctx context.Context, t storage.AuthToken) string {
	if t.DeviceName != "" {
		return t.DeviceName
	}
	if d, err := s.store.GetDevice(ctx, t.DeviceID); err == nil {
		return d.Name
	}
	return ""
}

// revokeUserSessions revokes the user's tokens except those of keep (all
// when keep is empty) and writes one SessionEnded entry per revoked token,
// oldest token first (users.token.R9).
func (s *Server) revokeUserSessions(ctx context.Context, user storage.User, keep []storage.DeviceID) error {
	toks, err := s.store.QueryUserTokens(ctx, user.ID)
	if err != nil {
		return err
	}
	kept := make(map[storage.DeviceID]bool, len(keep))
	for _, d := range keep {
		kept[d] = true
	}
	var entries []storage.ActivityEntry
	for _, t := range toks {
		if kept[t.DeviceID] {
			continue
		}
		entries = append(entries, sessionEndedEntry(user.ID, user.Name, s.tokenDeviceName(ctx, t), t.RemoteAddress))
	}
	if err := s.store.RevokeUserTokens(ctx, user.ID, keep); err != nil {
		return err
	}
	s.writeActivity(ctx, entries...)
	return nil
}

// handleSessionsLogout is POST /Sessions/Logout (users.token.R7-R8): it
// revokes the token the request carries.
func (s *Server) handleSessionsLogout(w http.ResponseWriter, r *http.Request) {
	ac, _ := AuthFromContext(r.Context())
	if ac == nil || ac.IsAPIKey {
		// An API key belongs to no session; there is nothing to end.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	ctx := r.Context()
	if err := s.store.RevokeToken(ctx, ac.AuthToken.Token); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	s.writeActivity(ctx, sessionEndedEntry(ac.User.ID, ac.User.Name, s.tokenDeviceName(ctx, ac.AuthToken), ac.AuthToken.RemoteAddress))
	// The reference drops the ended session from GET /Sessions (users.token.R10),
	// rather than leaving it listed until the process restarts.
	s.sessions.dropDevice(ctx, string(ac.AuthToken.DeviceID))
	w.WriteHeader(http.StatusNoContent)
}

func passwordChangedEntry(u storage.User) storage.ActivityEntry {
	uid := u.ID
	return storage.ActivityEntry{
		Name:     fmt.Sprintf("Password has been changed for user %s", u.Name),
		Type:     storage.ActivityTypeUserPasswordChanged,
		UserID:   &uid,
		Severity: storage.ActivitySeverityInfo,
	}
}
