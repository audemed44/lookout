package cron

import (
	"testing"
	"time"
)

func TestNext(t *testing.T) {
	base := time.Date(2026, 10, 2, 9, 30, 0, 0, time.UTC) // a Friday
	cases := []struct {
		spec string
		want string
	}{
		{"8 */8 * * *", "2026-10-02 16:08"},
		{"* * * * *", "2026-10-02 09:31"},
		{"0 0 * * *", "2026-10-03 00:00"},
		{"30 9 * * *", "2026-10-03 09:30"},
		{"0 12 * * 1-5", "2026-10-02 12:00"},
		{"0 12 * * 0", "2026-10-04 12:00"},
		{"0 12 * * 7", "2026-10-04 12:00"},
		{"0 0 1 * *", "2026-11-01 00:00"},
		{"15,45 * * * *", "2026-10-02 09:45"},
		{"0 0 29 2 *", "2028-02-29 00:00"},
		{"@hourly", "2026-10-02 10:00"},
		{"5/20 * * * *", "2026-10-02 09:45"},
	}
	for _, c := range cases {
		s, err := Parse(c.spec)
		if err != nil {
			t.Fatalf("%s: %v", c.spec, err)
		}
		if got := s.Next(base).Format("2006-01-02 15:04"); got != c.want {
			t.Errorf("%s: next = %s, want %s", c.spec, got, c.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, spec := range []string{"", "* * * *", "60 * * * *", "* 24 * * *", "*/0 * * * *", "a * * * *", "5-1 * * * *"} {
		if _, err := Parse(spec); err == nil {
			t.Errorf("%q: expected an error", spec)
		}
	}
}
