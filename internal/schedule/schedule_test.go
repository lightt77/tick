package schedule

import (
	"os"
	"testing"
	"time"
)

// A fixed zone with a half-hour offset catches code that truncates in UTC.
var zone = time.FixedZone("T", 5*3600+30*60)

func TestMain(m *testing.M) {
	time.Local = zone
	os.Exit(m.Run())
}

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, zone)
	if err != nil {
		panic(err)
	}
	return t
}

// Monday 2026-11-16 08:00 local.
var anchor = at("2026-11-16 08:00")

func mustNew(t *testing.T, rules ...string) Schedule {
	t.Helper()
	s, err := New(rules, nil, anchor)
	if err != nil {
		t.Fatalf("New(%v): %v", rules, err)
	}
	return s
}

func expect(t *testing.T, got []time.Time, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d occurrences %v, want %d %v", len(got), got, len(want), want)
	}
	for i, w := range want {
		if !got[i].Equal(at(w)) {
			t.Errorf("occurrence %d: got %s, want %s", i, got[i].In(zone).Format("2006-01-02 15:04 Mon"), w)
		}
	}
}

func TestRuleShapes(t *testing.T) {
	cases := []struct {
		name  string
		rules []string
		after string
		want  []string
	}{
		{"daily", []string{"FREQ=DAILY;BYHOUR=18;BYMINUTE=30"}, "2026-11-16 08:00",
			[]string{"2026-11-16 18:30", "2026-11-17 18:30", "2026-11-18 18:30"}},
		{"mon wed fri", []string{"FREQ=WEEKLY;BYDAY=MO,WE,FR;BYHOUR=18;BYMINUTE=30"}, "2026-11-16 08:00",
			[]string{"2026-11-16 18:30", "2026-11-18 18:30", "2026-11-20 18:30"}},
		{"1st and 15th", []string{"FREQ=MONTHLY;BYMONTHDAY=1,15;BYHOUR=9;BYMINUTE=0"}, "2026-11-16 08:00",
			[]string{"2026-12-01 09:00", "2026-12-15 09:00", "2027-01-01 09:00"}},
		{"last day of month", []string{"FREQ=MONTHLY;BYMONTHDAY=-1;BYHOUR=9;BYMINUTE=0"}, "2026-11-16 08:00",
			[]string{"2026-11-30 09:00", "2026-12-31 09:00", "2027-01-31 09:00", "2027-02-28 09:00"}},
		{"last friday", []string{"FREQ=MONTHLY;BYDAY=-1FR;BYHOUR=17;BYMINUTE=0"}, "2026-11-16 08:00",
			[]string{"2026-11-27 17:00", "2026-12-25 17:00", "2027-01-29 17:00"}},
		{"last weekday (bysetpos)", []string{"FREQ=MONTHLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=-1;BYHOUR=17;BYMINUTE=0"}, "2026-11-16 08:00",
			[]string{"2026-11-30 17:00", "2026-12-31 17:00", "2027-01-29 17:00"}},
		{"yearly", []string{"FREQ=YEARLY;BYMONTH=3;BYMONTHDAY=14;BYHOUR=10;BYMINUTE=0"}, "2026-11-16 08:00",
			[]string{"2027-03-14 10:00", "2028-03-14 10:00"}},
		{"quarterly", []string{"FREQ=MONTHLY;INTERVAL=3;BYMONTHDAY=1;BYHOUR=9;BYMINUTE=0"}, "2026-11-16 08:00",
			[]string{"2027-02-01 09:00", "2027-05-01 09:00", "2027-08-01 09:00"}},
		{"twice a day", []string{"FREQ=DAILY;BYHOUR=9,21;BYMINUTE=0"}, "2026-11-16 08:00",
			[]string{"2026-11-16 09:00", "2026-11-16 21:00", "2026-11-17 09:00"}},
		{"every 20m in work hours", []string{"FREQ=MINUTELY;INTERVAL=20;BYHOUR=9,10,11,12,13,14,15,16;BYDAY=MO,TU,WE,TH,FR"}, "2026-11-16 08:00",
			[]string{"2026-11-16 09:00", "2026-11-16 09:20", "2026-11-16 09:40", "2026-11-16 10:00"}},
		{"every 20m over a weekend", []string{"FREQ=MINUTELY;INTERVAL=20;BYHOUR=9,10,11,12,13,14,15,16;BYDAY=MO,TU,WE,TH,FR"}, "2026-11-20 16:30",
			[]string{"2026-11-20 16:40", "2026-11-23 09:00"}},
		{"every 2 days", []string{"FREQ=DAILY;INTERVAL=2;BYHOUR=7;BYMINUTE=0"}, "2026-11-16 08:00",
			[]string{"2026-11-18 07:00", "2026-11-20 07:00", "2026-11-22 07:00"}},
		{"weekdays 18:30 + saturday 10:00", []string{
			"FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR;BYHOUR=18;BYMINUTE=30",
			"FREQ=WEEKLY;BYDAY=SA;BYHOUR=10;BYMINUTE=0"}, "2026-11-20 19:00",
			[]string{"2026-11-21 10:00", "2026-11-23 18:30", "2026-11-24 18:30"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := mustNew(t, c.rules...)
			expect(t, s.Upcoming(at(c.after), len(c.want)), c.want...)
		})
	}
}

func TestCountAndUntil(t *testing.T) {
	count := mustNew(t, "FREQ=DAILY;COUNT=3;BYHOUR=18;BYMINUTE=30")
	expect(t, count.Upcoming(anchor, 10), "2026-11-16 18:30", "2026-11-17 18:30", "2026-11-18 18:30")

	until := mustNew(t, "FREQ=DAILY;UNTIL=20261118T235900;BYHOUR=18;BYMINUTE=30")
	expect(t, until.Upcoming(anchor, 10), "2026-11-16 18:30", "2026-11-17 18:30", "2026-11-18 18:30")
}

func TestSkipCountsTowardsCount(t *testing.T) {
	s := mustNew(t, "FREQ=DAILY;COUNT=3;BYHOUR=18;BYMINUTE=30")
	s.Skip(at("2026-11-17 18:30"))
	expect(t, s.Upcoming(anchor, 10), "2026-11-16 18:30", "2026-11-18 18:30")
}

func TestSkipEveryOccurrenceAfterward(t *testing.T) {
	s := mustNew(t, "FREQ=DAILY;BYHOUR=18;BYMINUTE=30")
	s.Skip(at("2026-11-16 18:30"))
	s.Skip(at("2026-11-17 18:30"))
	expect(t, s.Upcoming(anchor, 1), "2026-11-18 18:30")
}

func TestOnce(t *testing.T) {
	when := at("2026-11-20 09:00")
	s, err := New(nil, &when, anchor)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, s.Upcoming(anchor, 5), "2026-11-20 09:00")
	if _, ok := s.Next(when); ok {
		t.Error("a one-time schedule must have nothing after its time")
	}
	s.Skip(when)
	if _, ok := s.Next(anchor); ok {
		t.Error("a skipped one-time schedule must be exhausted")
	}
}

func TestBetween(t *testing.T) {
	s := mustNew(t, "FREQ=DAILY;BYHOUR=18;BYMINUTE=30")
	all := s.Between(anchor, at("2026-11-20 20:00"), 0)
	expect(t, all, "2026-11-16 18:30", "2026-11-17 18:30", "2026-11-18 18:30", "2026-11-19 18:30", "2026-11-20 18:30")
	expect(t, s.Between(anchor, at("2026-11-20 20:00"), 1), "2026-11-20 18:30")
	// (from, to]: an occurrence exactly at `from` is excluded, one at `to` included.
	expect(t, s.Between(at("2026-11-16 18:30"), at("2026-11-17 18:30"), 0), "2026-11-17 18:30")
}

func TestAnchorRounding(t *testing.T) {
	s, err := New([]string{"FREQ=MINUTELY;INTERVAL=20"}, nil, at("2026-11-16 08:07"))
	if err != nil {
		t.Fatal(err)
	}
	expect(t, s.Upcoming(at("2026-11-16 08:07"), 3), "2026-11-16 08:20", "2026-11-16 08:40", "2026-11-16 09:00")
}

func TestValidateRejects(t *testing.T) {
	bad := []string{
		"",
		"BYHOUR=9;BYMINUTE=0", // no FREQ
		"FREQ=SECONDLY",       // unsupported freq
		"FREQ=DAILY",          // no time of day
		"FREQ=DAILY;BYHOUR=9", // no minute
		"FREQ=HOURLY",         // no minute
		"FREQ=DAILY;BYHOUR=9;BYMINUTE=0;BYSECOND=5", // unsupported key
		"DTSTART=20260101T000000;FREQ=DAILY;BYHOUR=9;BYMINUTE=0",
		"FREQ=DAILY;BYHOUR=9;BYMINUTE=0;COUNT=3;UNTIL=20270101T000000",
		"FREQ=DAILY;BYHOUR=9;BYMINUTE=0;FREQ=WEEKLY",
		"FREQ=DAILY;BYHOUR=25;BYMINUTE=0",
		"FREQ=WEEKLY;BYDAY=XX;BYHOUR=9;BYMINUTE=0",
		"nonsense",
	}
	for _, r := range bad {
		if _, err := Validate(r); err == nil {
			t.Errorf("Validate(%q) accepted a rule it should reject", r)
		}
	}
	if got, err := Validate("rrule:freq=daily;byhour=9;byminute=0"); err != nil || got != "FREQ=DAILY;BYHOUR=9;BYMINUTE=0" {
		t.Errorf("normalising: got %q, %v", got, err)
	}
}

// The daemon caches the next fire time, so Next runs about once per occurrence. It
// must still be quick for a minutely rule that has been running for a year.
func TestNextSpeedOnOldMinutelyRule(t *testing.T) {
	s, err := New([]string{"FREQ=MINUTELY;INTERVAL=20;BYHOUR=9,10,11,12,13,14,15,16;BYDAY=MO,TU,WE,TH,FR"}, nil, anchor.AddDate(-1, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, ok := s.Next(anchor); !ok {
		t.Fatal("no next occurrence")
	}
	took := time.Since(start)
	t.Logf("Next() on a year-old minutely rule took %v", took)
	if took > 500*time.Millisecond {
		t.Errorf("Next() took %v; re-anchor the rule forward", took)
	}
}
