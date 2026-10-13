package conformance

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Server-clock templates let a scenario build access-schedule windows around
// the moment it runs, so the scenario does not depend on the time of day:
//
//	{{clock.hour}}        the server's local time of day in hours, fractional (15.605139)
//	{{clock.hour+0.5}}    the same plus/minus a number of hours (no wrap-around: 23.8+1 is 24.8)
//	{{clock.day}}         the server's local weekday name ("Tuesday")
//	{{clock.day+1}}       the weekday plus/minus whole days, wrapping
//	{{clock.daygroup}}    "Weekday" (Monday-Friday) or "Weekend" for the server's local today
//	{{clock.otherdaygroup}} the other one of the two
//
// A string that is exactly one template followed by "|num" (for example
// "{{clock.hour-1|num}}") is sent as a JSON number instead of a string.
//
// The runner cannot read the server's time zone directly, so the first use
// calibrates it: a throwaway account gets an everyday schedule [0, X] and a
// bisection on X (login 200 = the local hour is <= X) yields the server's
// local time of day to a few seconds; the difference to the runner's UTC
// clock, rounded to a quarter hour, is the zone offset. Thereafter the
// server's local time is the runner's clock plus that offset.

var weekdayNames = []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}

// now is the runner's clock (replaceable in tests).
func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// serverNow returns the current time shifted so that its UTC fields read as
// the server's local wall clock.
func (c *Client) serverNow() (time.Time, error) {
	c.mu.Lock()
	ok, off := c.clockCalibrated, c.clockOffset
	c.mu.Unlock()
	if !ok {
		var err error
		if off, err = c.calibrateClock(context.Background()); err != nil {
			return time.Time{}, fmt.Errorf("server clock calibration: %w", err)
		}
		c.mu.Lock()
		c.clockCalibrated, c.clockOffset = true, off
		c.mu.Unlock()
	}
	return c.now().UTC().Add(off), nil
}

// clockValue renders one clock.* template body ("hour", "hour+0.5", "day-1").
func (c *Client) clockValue(spec string) (string, error) {
	kind, rest := spec, ""
	if i := strings.IndexAny(spec, "+-"); i > 0 {
		kind, rest = spec[:i], spec[i:]
	}
	delta := 0.0
	if rest != "" {
		v, err := strconv.ParseFloat(rest, 64)
		if err != nil {
			return "", fmt.Errorf("bad clock offset %q", rest)
		}
		delta = v
	}
	now, err := c.serverNow()
	if err != nil {
		return "", err
	}
	switch kind {
	case "daygroup", "otherdaygroup":
		weekend := now.Weekday() == time.Saturday || now.Weekday() == time.Sunday
		if (kind == "otherdaygroup") == weekend {
			return "Weekday", nil
		}
		return "Weekend", nil
	case "hour":
		h := float64(now.Hour()) + float64(now.Minute())/60 + float64(now.Second())/3600 +
			float64(now.Nanosecond())/3.6e12
		return strconv.FormatFloat(h+delta, 'f', 6, 64), nil
	case "day":
		if delta != math.Trunc(delta) {
			return "", fmt.Errorf("clock.day takes whole days")
		}
		d := (int(now.Weekday()) + int(delta)) % 7
		if d < 0 {
			d += 7
		}
		return weekdayNames[d], nil
	}
	return "", fmt.Errorf("unknown clock template %q", spec)
}

// calibrateClock measures the server's UTC offset (see the file comment).
func (c *Client) calibrateClock(ctx context.Context) (time.Duration, error) {
	name := "tmp-clock-probe" + c.DeviceSuffix
	name = strings.NewReplacer("_", "-").Replace(name)
	r, err := c.Do(ctx, "admin", "POST", "/Users/New", nil, map[string]any{"Name": name, "Password": "Tmp-Pass-1"}, nil)
	if err != nil {
		return 0, err
	}
	id, _ := lookupString(r.JSON, "Id")
	if r.Status != 200 || id == "" {
		return 0, fmt.Errorf("cannot create the probe account: status %d", r.Status)
	}
	defer func() { _, _ = c.Do(ctx, "admin", "DELETE", "/Users/"+id, nil, nil, nil) }()
	u, err := c.Do(ctx, "admin", "GET", "/Users/"+id, nil, nil, nil)
	if err != nil {
		return 0, err
	}
	pol, _ := lookupMap(u.JSON, "Policy")
	if pol == nil {
		return 0, fmt.Errorf("probe account has no policy")
	}
	login := func() (int, error) {
		resp, err := c.Do(ctx, "anonymous", "POST", "/Users/AuthenticateByName", nil,
			map[string]any{"Username": name, "Pw": "Tmp-Pass-1"},
			map[string]string{"Authorization": authHeader("conf-clock-probe"+c.DeviceSuffix, "")})
		if err != nil {
			return 0, err
		}
		return resp.Status, nil
	}
	lo, hi := 0.0, 24.0
	for i := 0; i < 14; i++ {
		mid := (lo + hi) / 2
		pol["AccessSchedules"] = []any{map[string]any{"DayOfWeek": "Everyday", "StartHour": 0, "EndHour": mid}}
		resp, err := c.Do(ctx, "admin", "POST", "/Users/"+id+"/Policy", nil, pol, nil)
		if err != nil {
			return 0, err
		}
		if resp.Status != 204 {
			return 0, fmt.Errorf("probe policy refused: status %d", resp.Status)
		}
		st, err := login()
		if err != nil {
			return 0, err
		}
		switch st {
		case 200:
			hi = mid
		case 403:
			lo = mid
		default:
			return 0, fmt.Errorf("probe login answered %d", st)
		}
	}
	end := c.now().UTC()
	serverSecs := (lo + hi) / 2 * 3600
	utcSecs := float64(end.Hour()*3600+end.Minute()*60+end.Second()) + float64(end.Nanosecond())/1e9
	diff := math.Mod(serverSecs-utcSecs, 86400)
	if diff > 43200 {
		diff -= 86400
	} else if diff <= -43200 {
		diff += 86400
	}
	return time.Duration(math.Round(diff/900)) * 15 * time.Minute, nil
}

func lookupString(v any, key string) (string, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return "", false
	}
	s, ok := m[key].(string)
	return s, ok
}

func lookupMap(v any, key string) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	out, ok := m[key].(map[string]any)
	return out, ok
}
