package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lightt77/tick/internal/schedule"
	"github.com/lightt77/tick/internal/timer"
)

func TestBuildRules(t *testing.T) {
	c := func(h, m int) clock { return clock{h, m} }
	cases := []struct {
		name  string
		r     recurrence
		rules []string
		label string
	}{
		{"daily", recurrence{Freq: freqDaily, Times: []clock{c(18, 30)}},
			[]string{"FREQ=DAILY;BYHOUR=18;BYMINUTE=30"}, "Daily 18:30"},
		{"daily twice", recurrence{Freq: freqDaily, Times: []clock{c(9, 0), c(21, 15)}},
			[]string{"FREQ=DAILY;BYHOUR=9;BYMINUTE=0", "FREQ=DAILY;BYHOUR=21;BYMINUTE=15"}, "Daily 09:00, 21:15"},
		{"weekdays", recurrence{Freq: freqWeekdays, Times: []clock{c(9, 0)}},
			[]string{"FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR;BYHOUR=9;BYMINUTE=0"}, "Weekdays 09:00"},
		{"weekends", recurrence{Freq: freqWeekends, Times: []clock{c(10, 0)}},
			[]string{"FREQ=WEEKLY;BYDAY=SA,SU;BYHOUR=10;BYMINUTE=0"}, "Weekends 10:00"},
		{"some days", recurrence{Freq: freqWeekly, Days: []string{"MO", "WE", "FR"}, Times: []clock{c(7, 0)}},
			[]string{"FREQ=WEEKLY;BYDAY=MO,WE,FR;BYHOUR=7;BYMINUTE=0"}, "Mon, Wed, Fri 07:00"},
		{"monthly", recurrence{Freq: freqMonthly, MonthDay: 15, Times: []clock{c(9, 0)}},
			[]string{"FREQ=MONTHLY;BYMONTHDAY=15;BYHOUR=9;BYMINUTE=0"}, "Monthly on day 15 09:00"},
		{"last day", recurrence{Freq: freqMonthly, MonthDay: -1, Times: []clock{c(20, 0)}},
			[]string{"FREQ=MONTHLY;BYMONTHDAY=-1;BYHOUR=20;BYMINUTE=0"}, "Last day of the month 20:00"},
		{"hours", recurrence{Freq: freqHours, N: 2, Minute: 30},
			[]string{"FREQ=HOURLY;INTERVAL=2;BYMINUTE=30"}, "Every 2 h at :30"},
		{"hourly", recurrence{Freq: freqHours, N: 1, Minute: 0},
			[]string{"FREQ=HOURLY;INTERVAL=1;BYMINUTE=0"}, "Hourly at :00"},
		{"minutes", recurrence{Freq: freqMinutes, N: 20},
			[]string{"FREQ=MINUTELY;INTERVAL=20"}, "Every 20 min"},
		{"custom", recurrence{Freq: freqCustom, Custom: []string{"FREQ=YEARLY;BYMONTH=3;BYMONTHDAY=1;BYHOUR=9;BYMINUTE=0"}},
			[]string{"FREQ=YEARLY;BYMONTH=3;BYMONTHDAY=1;BYHOUR=9;BYMINUTE=0"}, ""},
	}
	now := time.Date(2026, 11, 16, 8, 0, 0, 0, time.Local)
	for _, tc := range cases {
		rules, label, err := buildRules(tc.r)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if strings.Join(rules, "|") != strings.Join(tc.rules, "|") || label != tc.label {
			t.Errorf("%s: %q %q, want %q %q", tc.name, rules, label, tc.rules, tc.label)
		}
		// whatever the questions produce must be accepted by the schedule engine
		s, err := schedule.New(rules, nil, now)
		if err != nil {
			t.Errorf("%s: schedule rejected %q: %v", tc.name, rules, err)
		} else if _, ok := s.Next(now); !ok {
			t.Errorf("%s: no next occurrence", tc.name)
		}
	}
}

func TestBuildRulesRefusesIncompleteAnswers(t *testing.T) {
	for _, r := range []recurrence{
		{Freq: freqDaily},
		{Freq: freqWeekly, Times: []clock{{9, 0}}},
		{Freq: freqMonthly, Times: []clock{{9, 0}}},
		{Freq: freqMonthly, MonthDay: 40, Times: []clock{{9, 0}}},
		{Freq: freqHours, N: 0},
		{Freq: freqMinutes},
		{Freq: freqCustom},
	} {
		if _, _, err := buildRules(r); err == nil {
			t.Errorf("accepted %+v", r)
		}
	}
}

func TestParseAnswers(t *testing.T) {
	if cs, err := parseClocks("18:30, 9:05 18:30"); err != nil || len(cs) != 2 || cs[0] != (clock{18, 30}) || cs[1] != (clock{9, 5}) {
		t.Errorf("parseClocks: %v %v", cs, err)
	}
	for _, bad := range []string{"", "6pm", "25:00", "18:5", "18"} {
		if _, err := parseClocks(bad); err == nil {
			t.Errorf("parseClocks(%q) accepted", bad)
		}
	}
	if d, err := parseDays("Fri, mon wednesday"); err != nil || strings.Join(d, ",") != "MO,WE,FR" {
		t.Errorf("parseDays: %v %v", d, err)
	}
	for _, bad := range []string{"", "someday", "mon,xyz"} {
		if _, err := parseDays(bad); err == nil {
			t.Errorf("parseDays(%q) accepted", bad)
		}
	}
	if n, err := parseMonthDay("-1"); err != nil || n != -1 {
		t.Errorf("parseMonthDay: %v %v", n, err)
	}
	for _, bad := range []string{"0", "32", "x"} {
		if _, err := parseMonthDay(bad); err == nil {
			t.Errorf("parseMonthDay(%q) accepted", bad)
		}
	}
}

func scheduleRun(t *testing.T, e tickEnv, input string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	a := &app{out: &out, errOut: &bytes.Buffer{}}
	err := a.runSchedule(strings.NewReader(input), time.Date(2026, 11, 16, 8, 0, 0, 0, time.Local))
	return out.String(), err
}

func TestScheduleConversationCreatesARecurringReminder(t *testing.T) {
	e := newEnv(t)
	// name (a bad one first), repeating, daily, two bad times then a good one, no extras, create
	out, err := scheduleRun(t, e, "status\nguitar\n2\n1\n6pm\n18:30\nn\ny\n")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"can't be a name", "use a 24-hour time", "Would create", "Daily 18:30"} {
		if !strings.Contains(out, want) {
			t.Errorf("transcript lacks %q:\n%s", want, out)
		}
	}
	g := e.state().Find("guitar")
	if g == nil || g.State != timer.Scheduled || g.Schedule == nil || g.Duration != 0 || g.Label != "Daily 18:30" {
		t.Fatalf("created: %+v", g)
	}
	if len(g.Schedule.Rules) != 1 || g.Schedule.Rules[0] != "FREQ=DAILY;BYHOUR=18;BYMINUTE=30" {
		t.Fatalf("rules %v", g.Schedule.Rules)
	}
}

func TestScheduleConversationOneTimeWithExtras(t *testing.T) {
	e := newEnv(t)
	// name, once, a past date's time is refused, then a later time; extras: sound, label, grace
	in := "dentist\n1\n2026-11-20\n09:00\ny\nGlass\nTeeth\n1h\ny\n"
	out, err := scheduleRun(t, e, in)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	d := e.state().Find("dentist")
	if d == nil || d.Schedule == nil || d.Schedule.At == nil || !d.Schedule.At.Equal(time.Date(2026, 11, 20, 9, 0, 0, 0, time.Local)) {
		t.Fatalf("created: %+v", d)
	}
	if d.Label != "Teeth" || d.Sound != "Glass" || time.Duration(d.Grace) != time.Hour {
		t.Fatalf("extras: label %q sound %q grace %v", d.Label, d.Sound, d.Grace)
	}
}

func TestScheduleConversationCanBeDeclinedAndEnded(t *testing.T) {
	e := newEnv(t)
	out, err := scheduleRun(t, e, "guitar\n2\n1\n18:30\nn\nn\n")
	if err != nil || !strings.Contains(out, "Nothing created") {
		t.Fatalf("declined: %v\n%s", err, out)
	}
	if e.state().Find("guitar") != nil {
		t.Fatal("declining created it")
	}
	if _, err := scheduleRun(t, e, "guitar\n2\n"); err == nil || !strings.Contains(err.Error(), "input ended") {
		t.Fatalf("ended early: %v", err)
	}
	if e.state().Find("guitar") != nil {
		t.Fatal("an unfinished conversation created something")
	}
}

func TestScheduleNeedsATerminal(t *testing.T) {
	e := newEnv(t)
	r, w, err := os.Pipe() // a pipe, like a script or an LLM would give
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old }()
	e.fails("needs a terminal", "schedule")
}

func TestScheduleRefusesATakenName(t *testing.T) {
	e := newEnv(t)
	e.must("guitar", "10m")
	out, err := scheduleRun(t, e, "guitar\nguitar2\n2\n1\n18:30\nn\ny\n")
	if err != nil || !strings.Contains(out, "already exists") {
		t.Fatalf("%v\n%s", err, out)
	}
	if e.state().Find("guitar2") == nil {
		t.Fatal("the second name was not used")
	}
}
