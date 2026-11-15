package cli

import (
	"testing"
	"time"
)

func TestParseWhen(t *testing.T) {
	now := time.Date(2026, 11, 16, 20, 0, 0, 0, time.Local)
	cases := map[string]time.Time{
		"2026-11-20 09:00":   time.Date(2026, 11, 20, 9, 0, 0, 0, time.Local),
		"2026-11-20T09:00":   time.Date(2026, 11, 20, 9, 0, 0, 0, time.Local),
		"2026-11-20":         time.Date(2026, 11, 20, 0, 0, 0, 0, time.Local),
		"21:30":              time.Date(2026, 11, 16, 21, 30, 0, 0, time.Local),
		"07:00":              time.Date(2026, 11, 17, 7, 0, 0, 0, time.Local), // already passed today
		" 2026-11-20 09:00 ": time.Date(2026, 11, 20, 9, 0, 0, 0, time.Local),
	}
	for in, want := range cases {
		got, err := parseWhen(in, now)
		if err != nil || !got.Equal(want) {
			t.Errorf("parseWhen(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"tomorrow", "20/11/2026", "25:00", ""} {
		if _, err := parseWhen(bad, now); err == nil {
			t.Errorf("parseWhen(%q) accepted", bad)
		}
	}
}

func TestValidName(t *testing.T) {
	for _, ok := range []string{"guitar", "deep work", "tick", "écrire"} {
		if err := validName(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "  ", "list", "a\tb", "this name is far too long to be a useful timer name"} {
		if err := validName(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestClockMs(t *testing.T) {
	if got := clockMs(4*time.Minute + 59*time.Second + 123*time.Millisecond); got != "04:59.123" {
		t.Errorf("clockMs = %q", got)
	}
	if got := clockMs(3723*time.Second + 456*time.Millisecond); got != "1:02:03.456" {
		t.Errorf("clockMs = %q", got)
	}
}
