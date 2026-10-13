package api

import (
	"sync"
	"time"
)

// Recovery-PIN redeem hardening (users.forgot.R9). A PIN is 32 bits and
// lives 30 minutes, so an anonymous caller must not be allowed to guess
// freely. Two independent limits, both answered with the same uniform 404:
//
//   - per source address: pinMaxFailuresPerSource wrong PINs lock that
//     address out of the redeem endpoint for pinLockout;
//   - per pending request: pinMaxFailuresPerRequest wrong PINs, summed over
//     all sources, invalidate every pending request (the owner asks again).
const (
	pinMaxFailuresPerSource  = 5
	pinMaxFailuresPerRequest = 5
	pinLockout               = 15 * time.Minute
)

type pinSourceState struct {
	failures int
	until    time.Time // lockout end once failures reach the limit
	last     time.Time
}

type pinThrottle struct {
	mu       sync.Mutex
	sources  map[string]*pinSourceState
	requests int // wrong PINs since the pending requests were last invalidated
}

func newPinThrottle() *pinThrottle {
	return &pinThrottle{sources: map[string]*pinSourceState{}}
}

// allow reports whether src may attempt a redeem at now.
func (t *pinThrottle) allow(src string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.sources[src]
	if st == nil {
		return true
	}
	if now.Before(st.until) {
		return false
	}
	if st.failures >= pinMaxFailuresPerSource {
		delete(t.sources, src) // lockout over
	}
	return true
}

// fail records a wrong PIN from src. It reports true when the per-request
// budget is exhausted and every pending request must be invalidated.
func (t *pinThrottle) fail(src string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	// Keep the table small: drop sources idle for a full lockout period.
	for k, v := range t.sources {
		if now.Sub(v.last) > pinLockout {
			delete(t.sources, k)
		}
	}
	st := t.sources[src]
	if st == nil {
		st = &pinSourceState{}
		t.sources[src] = st
	}
	st.failures++
	st.last = now
	if st.failures >= pinMaxFailuresPerSource {
		st.until = now.Add(pinLockout)
	}
	t.requests++
	if t.requests >= pinMaxFailuresPerRequest {
		t.requests = 0
		return true
	}
	return false
}

// success clears the per-request budget after a redeemed PIN.
func (t *pinThrottle) success() {
	t.mu.Lock()
	t.requests = 0
	t.mu.Unlock()
}
