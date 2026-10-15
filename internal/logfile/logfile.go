// Package logfile is a small io.Writer that appends to a daily log file
// (log_YYYYMMDD.log) so the admin log routes have something to list.
package logfile

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Writer appends to <dir>/log_<date>.log, switching file when the day changes.
type Writer struct {
	dir string
	now func() time.Time

	mu   sync.Mutex
	day  string
	file *os.File
}

// New returns a Writer for dir (created on first write).
func New(dir string) *Writer { return &Writer{dir: dir, now: time.Now} }

// Dir is the directory the log files live in.
func (w *Writer) Dir() string { return w.dir }

// Write implements io.Writer. Failures to open the file drop the line rather
// than failing the caller (logging must never break the server).
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	day := w.now().Format("20060102")
	if w.file == nil || day != w.day {
		if w.file != nil {
			w.file.Close()
			w.file = nil
		}
		if err := os.MkdirAll(w.dir, 0o755); err != nil {
			return len(p), nil
		}
		f, err := os.OpenFile(filepath.Join(w.dir, "log_"+day+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return len(p), nil
		}
		w.file, w.day = f, day
	}
	_, _ = w.file.Write(p)
	return len(p), nil
}

// Close closes the current file.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}
