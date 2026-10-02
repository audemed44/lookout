// Package cron parses five-field cron expressions ("8 */8 * * *") and
// finds the next time one matches.
package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Schedule struct {
	minute, hour, dom, month, dow uint64
	// Cron's rule: when both day fields are restricted, either may match.
	domAny, dowAny bool
}

var aliases = map[string]string{
	"@hourly":   "0 * * * *",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@weekly":   "0 0 * * 0",
	"@monthly":  "0 0 1 * *",
}

func Parse(spec string) (*Schedule, error) {
	spec = strings.TrimSpace(spec)
	if a, ok := aliases[spec]; ok {
		spec = a
	}
	f := strings.Fields(spec)
	if len(f) != 5 {
		return nil, fmt.Errorf("cron: expected 5 fields (minute hour day month weekday), got %d", len(f))
	}
	s := &Schedule{domAny: f[2] == "*", dowAny: f[4] == "*"}
	var err error
	if s.minute, err = field(f[0], 0, 59); err != nil {
		return nil, fmt.Errorf("cron minute: %w", err)
	}
	if s.hour, err = field(f[1], 0, 23); err != nil {
		return nil, fmt.Errorf("cron hour: %w", err)
	}
	if s.dom, err = field(f[2], 1, 31); err != nil {
		return nil, fmt.Errorf("cron day: %w", err)
	}
	if s.month, err = field(f[3], 1, 12); err != nil {
		return nil, fmt.Errorf("cron month: %w", err)
	}
	if s.dow, err = field(f[4], 0, 7); err != nil {
		return nil, fmt.Errorf("cron weekday: %w", err)
	}
	if s.dow&(1<<7) != 0 {
		s.dow |= 1 // 7 is Sunday too
	}
	return s, nil
}

func field(s string, lo, hi int) (uint64, error) {
	var bits uint64
	for _, part := range strings.Split(s, ",") {
		rng, stepStr, hasStep := strings.Cut(part, "/")
		step := 1
		if hasStep {
			n, err := strconv.Atoi(stepStr)
			if err != nil || n <= 0 {
				return 0, fmt.Errorf("bad step %q", stepStr)
			}
			step = n
		}
		a, b := lo, hi
		if rng != "*" {
			x, y, isRange := strings.Cut(rng, "-")
			var err error
			if a, err = strconv.Atoi(x); err != nil {
				return 0, fmt.Errorf("bad value %q", x)
			}
			b = a
			if isRange {
				if b, err = strconv.Atoi(y); err != nil {
					return 0, fmt.Errorf("bad value %q", y)
				}
			} else if hasStep {
				b = hi
			}
		}
		if a < lo || b > hi || a > b {
			return 0, fmt.Errorf("%q is out of range %d-%d", part, lo, hi)
		}
		for i := a; i <= b; i += step {
			bits |= 1 << uint(i)
		}
	}
	return bits, nil
}

func has(bits uint64, v int) bool { return bits&(1<<uint(v)) != 0 }

// Next returns the first matching minute after t.
func (s *Schedule) Next(t time.Time) time.Time {
	t = t.Truncate(time.Minute).Add(time.Minute)
	// Four years covers every valid day/month combination.
	limit := t.AddDate(4, 0, 0)
	for t.Before(limit) {
		if !has(s.month, int(t.Month())) {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
			continue
		}
		if !s.dayMatches(t) {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
			continue
		}
		if !has(s.hour, t.Hour()) {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, t.Location())
			continue
		}
		if !has(s.minute, t.Minute()) {
			t = t.Add(time.Minute)
			continue
		}
		return t
	}
	return time.Time{}
}

func (s *Schedule) dayMatches(t time.Time) bool {
	dom, dow := has(s.dom, t.Day()), has(s.dow, int(t.Weekday()))
	switch {
	case s.domAny && s.dowAny:
		return true
	case s.domAny:
		return dow
	case s.dowAny:
		return dom
	}
	return dom || dow
}
