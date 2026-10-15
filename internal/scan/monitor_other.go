//go:build !linux

package scan

import (
	"errors"
	"time"
)

// MonitorSupported reports whether this build can watch folders.
const MonitorSupported = false

// Monitor is not available on this platform.
type Monitor struct{}

// NewMonitor reports that watching is not supported here.
func NewMonitor(roots []string, quiet time.Duration, onChange func(root, top string)) (*Monitor, error) {
	return nil, errors.New("scan: folder monitoring is only available on Linux")
}

// Close does nothing.
func (m *Monitor) Close() {}
