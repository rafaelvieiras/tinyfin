package api

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"net/http"
)

// forgotPinTTL is how long a recovery PIN stays redeemable
// (users.forgot.R1: about 30 minutes).
const forgotPinTTL = 30 * time.Minute

// pinResetFile is the document written for the server's owner to read: the
// PIN, the account it resets and its expiry. The owner redeems the PIN
// through POST /Users/ForgotPassword/Pin.
type pinResetFile struct {
	Pin            string    `json:"Pin"`
	UserName       string    `json:"UserName"`
	ExpirationDate time.Time `json:"ExpirationDate"`
}

type forgotPasswordResult struct {
	Action            string `json:"Action"`
	PinFile           string `json:"PinFile"`
	PinExpirationDate string `json:"PinExpirationDate"`
}

func (s *Server) passwordResetDir() string {
	base := s.cfg.DataDir
	if base == "" {
		base = filepath.Join(os.TempDir(), "tinyfin")
	}
	return filepath.Join(base, "passwordreset")
}

// newResetPin returns a PIN in the XX-XX-XX-XX form of uppercase hex pairs.
func newResetPin() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	const hexd = "0123456789ABCDEF"
	parts := make([]string, 4)
	for i, v := range b {
		parts[i] = string([]byte{hexd[v>>4], hexd[v&0xf]})
	}
	return strings.Join(parts, "-"), nil
}

// handleForgotPassword is POST /Users/ForgotPassword (users.forgot.R1-R5).
// The answer is identical for known and unknown accounts and for local and
// remote callers; a PIN file is only written for an existing account asked
// for from the local network. PinFile carries a bare file name — not the
// server's absolute path, which the reference discloses (finding S11) —
// and the PIN itself is never part of the response.
func (s *Server) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EnteredUsername string `json:"EnteredUsername"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.EnteredUsername) == "" {
		writeInvalidBody(w)
		return
	}
	expires := time.Now().Add(forgotPinTTL).UTC()
	name, err := newResetFileName()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if user, err := s.store.GetUserByName(r.Context(), req.EnteredUsername); err == nil && fromLocalNetwork(r) {
		pin, err := newResetPin()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if err := s.writeResetFile(name, pinResetFile{Pin: pin, UserName: user.Name, ExpirationDate: expires}); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}
	writeJSON(w, http.StatusOK, forgotPasswordResult{
		Action:            "PinCode",
		PinFile:           name,
		PinExpirationDate: expires.Format("2006-01-02T15:04:05.0000000Z"),
	})
}

func newResetFileName() (string, error) {
	h, err := randomHex(8)
	if err != nil {
		return "", err
	}
	return "passwordreset" + h + ".json", nil
}

func (s *Server) writeResetFile(name string, f pinResetFile) error {
	dir := s.passwordResetDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), raw, 0o600)
}

// handleForgotPasswordPin is POST /Users/ForgotPassword/Pin
// (users.forgot.R6-R8). A matching, unexpired PIN sets the matching
// account's password to the PIN text as sent and consumes the request;
// tokens and lockouts are left as they are.
func (s *Server) handleForgotPasswordPin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Pin string `json:"Pin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Pin == "" {
		writeInvalidBody(w)
		return
	}
	want := strings.ToUpper(strings.ReplaceAll(req.Pin, "-", ""))

	dir := s.passwordResetDir()
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	now := time.Now()
	reset := []string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var f pinResetFile
		if json.Unmarshal(raw, &f) != nil {
			continue
		}
		if now.After(f.ExpirationDate) {
			_ = os.Remove(path)
			continue
		}
		if strings.ToUpper(strings.ReplaceAll(f.Pin, "-", "")) != want {
			continue
		}
		_ = os.Remove(path)
		user, err := s.store.GetUserByName(r.Context(), f.UserName)
		if err != nil {
			continue
		}
		if err := s.setPassword(r, user, req.Pin); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		reset = append(reset, user.Name)
	}
	if len(reset) == 0 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"Success": true, "UsersReset": reset})
}
