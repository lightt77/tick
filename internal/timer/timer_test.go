package timer

import (
	"os"
	"testing"
	"time"

	"github.com/lightt77/tick/internal/schedule"
)

var zone = time.FixedZone("T", 5*3600+30*60)

func TestMain(m *testing.M) {
	time.Local = zone
	os.Exit(m.Run())
}

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", s, zone)
	if err != nil {
		panic(err)
	}
	return t
}

var cfg = DefaultSettings

func kinds(evs []Event) []EventKind {
	var out []EventKind
	for _, e := range evs {
		out = append(out, e.Kind)
	}
	return out
}

func wantKinds(t *testing.T, evs []Event, want ...EventKind) {
	t.Helper()
	got := kinds(evs)
	if len(got) != len(want) {
		t.Fatalf("events %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events %v, want %v", got, want)
		}
	}
}

func wantState(t *testing.T, tm *Timer, s State) {
	t.Helper()
	if tm.State != s {
		t.Fatalf("state %s, want %s", tm.State, s)
	}
}

func TestCountdownPauseResumeFinish(t *testing.T) {
	t0 := at("2026-11-16 08:00:00")
	tm, err := NewCountdown("pasta", 10*time.Minute, false, t0)
	if err != nil {
		t.Fatal(err)
	}
	if r := tm.Remaining(t0.Add(4 * time.Minute)); r != 6*time.Minute {
		t.Fatalf("remaining %v", r)
	}
	if _, err := tm.Pause(t0.Add(4*time.Minute), nil); err != nil {
		t.Fatal(err)
	}
	if r := tm.Remaining(t0.Add(9 * time.Minute)); r != 6*time.Minute {
		t.Fatalf("paused remaining %v, want 6m", r)
	}
	if _, err := tm.Pause(t0.Add(5*time.Minute), nil); !IsNoop(err) {
		t.Fatalf("second pause: %v, want no-op", err)
	}
	if _, err := tm.Start(t0.Add(10 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	evs, changed, remove := tm.Advance(t0.Add(15*time.Minute), cfg)
	if len(evs) != 0 || changed || remove {
		t.Fatalf("advance before the end: %v %v %v", evs, changed, remove)
	}
	evs, _, _ = tm.Advance(t0.Add(16*time.Minute+time.Second), cfg)
	wantKinds(t, evs, EvFinished)
	if !evs[0].At.Equal(t0.Add(16 * time.Minute)) {
		t.Errorf("finished at %v, want the exact end", evs[0].At)
	}
	wantState(t, tm, Done)
	if tm.Ringing == nil {
		t.Fatal("a finished countdown must ring")
	}
	tm.Advance(t0.Add(16*time.Minute+cfg.RingCap+2*time.Second), cfg)
	if tm.Ringing != nil {
		t.Fatal("ringing must stop at the cap")
	}
	if _, err := tm.Restart(t0.Add(20 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	wantState(t, tm, Running)
	if r := tm.Remaining(t0.Add(20 * time.Minute)); r != 10*time.Minute {
		t.Fatalf("restart remaining %v", r)
	}
}

func TestStartOnDoneRestarts(t *testing.T) {
	t0 := at("2026-11-16 08:00:00")
	tm, _ := NewCountdown("tea", time.Minute, false, t0)
	tm.Advance(t0.Add(time.Minute), cfg)
	wantState(t, tm, Done)
	tm.Start(t0.Add(2 * time.Minute))
	wantState(t, tm, Running)
	if tm.Ringing != nil {
		t.Fatal("start must silence")
	}
}

func TestRepeatingTicker(t *testing.T) {
	t0 := at("2026-11-16 08:00:00")
	tm, _ := NewCountdown("tick", 15*time.Minute, true, t0)
	evs, _, _ := tm.Advance(t0.Add(15*time.Minute+200*time.Millisecond), cfg)
	wantKinds(t, evs, EvFinished)
	if !evs[0].Chime {
		t.Fatal("a ticker lap must be a chime")
	}
	wantState(t, tm, Running)
	// The next lap is measured from the end of the last one, not from when we noticed.
	if end, _ := tm.EndsAt(t0); !end.Equal(t0.Add(30 * time.Minute)) {
		t.Fatalf("next lap ends %v, want 08:30", end)
	}
	// Slept through several laps: one chime, next lap from now.
	wake := t0.Add(80 * time.Minute)
	evs, _, _ = tm.Advance(wake, cfg)
	wantKinds(t, evs, EvFinished)
	if end, _ := tm.EndsAt(wake); !end.Equal(wake.Add(15 * time.Minute)) {
		t.Fatalf("after a long sleep the lap ends %v, want now+15m", end)
	}
}

func TestStopwatch(t *testing.T) {
	t0 := at("2026-11-16 08:00:00")
	sw := NewStopwatch("work", t0)
	sw.Pause(t0.Add(30*time.Minute), nil)
	sw.Start(t0.Add(40 * time.Minute))
	if e := sw.Elapsed(t0.Add(50 * time.Minute)); e != 40*time.Minute {
		t.Fatalf("elapsed %v, want 40m", e)
	}
	if ev := sw.Cancel(t0.Add(50 * time.Minute)); ev.Amount != 40*time.Minute {
		t.Fatalf("cancel amount %v", ev.Amount)
	}
	sw.Restart(t0.Add(50 * time.Minute))
	if e := sw.Elapsed(t0.Add(51 * time.Minute)); e != time.Minute {
		t.Fatalf("after restart elapsed %v", e)
	}
	evs, _, remove := sw.Advance(t0.Add(time.Hour*30), cfg)
	if len(evs) != 0 || remove {
		t.Fatal("a stopwatch never fires")
	}
}

func daily(t *testing.T, d time.Duration, created time.Time, rule string) *Timer {
	t.Helper()
	s, err := schedule.New([]string{rule}, nil, created)
	if err != nil {
		t.Fatal(err)
	}
	tm, err := NewScheduled("guitar", d, s, created)
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

func TestScheduledHabitFlow(t *testing.T) {
	tm := daily(t, 15*time.Minute, at("2026-11-16 08:00:00"), "FREQ=DAILY;BYHOUR=18;BYMINUTE=30")
	if !tm.NextAt.Equal(at("2026-11-16 18:30:00")) {
		t.Fatalf("next %v", tm.NextAt)
	}
	evs, _, _ := tm.Advance(at("2026-11-16 18:00:00"), cfg)
	wantKinds(t, evs)
	evs, _, _ = tm.Advance(at("2026-11-16 18:30:00"), cfg)
	wantKinds(t, evs, EvDue)
	wantState(t, tm, Due)
	if tm.Late || tm.Ringing == nil {
		t.Fatal("an on-time occurrence rings and is not late")
	}
	if !tm.NextAt.Equal(at("2026-11-17 18:30:00")) {
		t.Fatalf("next after firing %v", tm.NextAt)
	}
	if _, err := tm.Restart(at("2026-11-16 18:31:00")); !IsNoop(err) {
		t.Fatalf("restart on due: %v", err)
	}
	tm.Start(at("2026-11-16 18:35:00"))
	wantState(t, tm, Running)
	evs, _, remove := tm.Advance(at("2026-11-16 18:50:00"), cfg)
	wantKinds(t, evs, EvFinished)
	wantState(t, tm, Scheduled)
	if remove || tm.Ringing == nil {
		t.Fatal("a finished scheduled run rings and stays scheduled")
	}
}

func TestDueRingsOnceThenWaitsForTheUser(t *testing.T) {
	tm := daily(t, 0, at("2026-11-16 08:00:00"), "FREQ=DAILY;BYHOUR=18;BYMINUTE=30")
	tm.Advance(at("2026-11-16 18:30:00"), cfg)
	wantState(t, tm, Due)
	if r := tm.Ringing; r == nil || r.Until.Sub(r.Since) != 20*time.Second {
		t.Fatalf("a due occurrence rings for due_ring, got %+v", tm.Ringing)
	}
	// Hours later it is still pending: no nag, no lapse, and the ring has stopped.
	evs, _, _ := tm.Advance(at("2026-11-16 23:00:00"), cfg)
	wantKinds(t, evs)
	wantState(t, tm, Due)
	if tm.Ringing != nil {
		t.Fatal("the ring stops after due_ring")
	}
	// The next occurrence arrives first: the old one is logged missed, the new one is due.
	evs, _, _ = tm.Advance(at("2026-11-17 18:30:00"), cfg)
	wantKinds(t, evs, EvMissed, EvDue)
	if !evs[0].At.Equal(at("2026-11-16 18:30:00")) {
		t.Fatalf("missed %v", evs[0].At)
	}
	wantState(t, tm, Due)
}

func TestDoneOnADueOccurrence(t *testing.T) {
	tm := daily(t, 0, at("2026-11-16 08:00:00"), "FREQ=DAILY;BYHOUR=18;BYMINUTE=30")
	if _, _, err := tm.Done(at("2026-11-16 12:00:00")); !IsNoop(err) {
		t.Fatalf("done with nothing pending: %v", err)
	}
	tm.Advance(at("2026-11-16 18:30:00"), cfg)
	evs, remove, err := tm.Done(at("2026-11-16 21:00:00"))
	if err != nil || remove {
		t.Fatal(err, remove)
	}
	wantKinds(t, evs, EvFinished)
	if !evs[0].User {
		t.Fatal("a user-concluded run must not announce itself")
	}
	wantState(t, tm, Scheduled)
	if tm.Ringing != nil || tm.DueAt != nil {
		t.Fatal("done clears the pending occurrence and its ring")
	}
}

func TestDoneOnTheLastOccurrenceRemoves(t *testing.T) {
	when := at("2026-11-20 09:00:00")
	s, _ := schedule.New(nil, &when, at("2026-11-16 08:00:00"))
	tm, _ := NewScheduled("dentist", 0, s, at("2026-11-16 08:00:00"))
	tm.Advance(when, cfg)
	if _, remove, err := tm.Done(when.Add(time.Minute)); err != nil || !remove {
		t.Fatalf("remove=%v err=%v", remove, err)
	}
}

func TestCancelOfAScheduleRunKeepsTheSchedule(t *testing.T) {
	tm := daily(t, 15*time.Minute, at("2026-11-16 08:00:00"), "FREQ=DAILY;BYHOUR=18;BYMINUTE=30")
	tm.Start(at("2026-11-16 17:00:00"))
	evs, err := tm.AbortRun(at("2026-11-16 17:05:00"))
	if err != nil {
		t.Fatal(err)
	}
	wantKinds(t, evs, EvCancelled)
	if evs[0].Amount != 5*time.Minute {
		t.Fatalf("amount %v", evs[0].Amount)
	}
	wantState(t, tm, Scheduled)
	if tm.NextAt == nil {
		t.Fatal("the schedule stays")
	}
}

func TestStopwatchDone(t *testing.T) {
	sw := NewStopwatch("work", at("2026-11-16 09:00:00"))
	evs, remove, err := sw.Done(at("2026-11-16 10:30:00"))
	if err != nil || !remove {
		t.Fatal(err, remove)
	}
	wantKinds(t, evs, EvFinished)
	if evs[0].Amount != 90*time.Minute {
		t.Fatalf("amount %v", evs[0].Amount)
	}
}

func TestCountdownDoneEarlyAndAfterRinging(t *testing.T) {
	tm, _ := NewCountdown("pasta", 10*time.Minute, false, at("2026-11-16 09:00:00"))
	evs, remove, err := tm.Done(at("2026-11-16 09:04:00"))
	if err != nil || !remove {
		t.Fatal(err, remove)
	}
	wantKinds(t, evs, EvFinished)
	if evs[0].Amount != 4*time.Minute {
		t.Fatalf("amount %v", evs[0].Amount)
	}

	// Once it has rung it was already logged as done; clearing it logs nothing more.
	tm, _ = NewCountdown("pasta", 10*time.Minute, false, at("2026-11-16 09:00:00"))
	tm.Advance(at("2026-11-16 09:10:00"), cfg)
	wantState(t, tm, Done)
	evs, remove, err = tm.Done(at("2026-11-16 09:11:00"))
	if err != nil || !remove || len(evs) != 0 {
		t.Fatal(evs, remove, err)
	}
}

func TestRepeatingTimerCountsLaps(t *testing.T) {
	tm, _ := NewCountdown("water", 10*time.Minute, true, at("2026-11-16 09:00:00"))
	tm.Advance(at("2026-11-16 09:10:00"), cfg)
	tm.Advance(at("2026-11-16 09:20:00"), cfg)
	tm.Advance(at("2026-11-16 09:30:00"), cfg)
	if tm.Laps != 3 {
		t.Fatalf("laps %d", tm.Laps)
	}
	evs, remove, err := tm.Done(at("2026-11-16 09:34:00"))
	if err != nil || !remove {
		t.Fatal(err, remove)
	}
	if evs[0].Note != "3 laps" || evs[0].Amount != 34*time.Minute {
		t.Fatalf("note %q amount %v", evs[0].Note, evs[0].Amount)
	}
	ev := tm.Cancel(at("2026-11-16 09:34:00"))
	if ev.Note != "3 laps" {
		t.Fatalf("cancel note %q", ev.Note)
	}
}

func TestGraceWindow(t *testing.T) {
	// Asleep from 18:00 to 20:00: 18:30 is 1.5h old, inside the 2h window.
	tm := daily(t, 15*time.Minute, at("2026-11-16 08:00:00"), "FREQ=DAILY;BYHOUR=18;BYMINUTE=30")
	tm.Advance(at("2026-11-16 18:00:00"), cfg)
	evs, _, _ := tm.Advance(at("2026-11-16 20:00:00"), cfg)
	wantKinds(t, evs, EvDue)
	if !tm.Late {
		t.Fatal("a woken occurrence is late")
	}

	// Asleep for a day and a bit: yesterday's is missed, today's is 2.5h old: missed.
	tm = daily(t, 15*time.Minute, at("2026-11-16 08:00:00"), "FREQ=DAILY;BYHOUR=18;BYMINUTE=30")
	evs, _, _ = tm.Advance(at("2026-11-17 21:00:00"), cfg)
	wantKinds(t, evs, EvMissed, EvMissed)
	wantState(t, tm, Scheduled)
	if !evs[0].At.Equal(at("2026-11-16 18:30:00")) || !evs[1].At.Equal(at("2026-11-17 18:30:00")) {
		t.Fatalf("missed occurrences %v %v", evs[0].At, evs[1].At)
	}
	if !tm.NextAt.Equal(at("2026-11-18 18:30:00")) {
		t.Fatalf("next %v", tm.NextAt)
	}

	// A per-timer grace overrides the default.
	tm = daily(t, 15*time.Minute, at("2026-11-16 08:00:00"), "FREQ=DAILY;BYHOUR=18;BYMINUTE=30")
	tm.Grace = Duration(3 * time.Hour)
	evs, _, _ = tm.Advance(at("2026-11-16 21:00:00"), cfg)
	wantKinds(t, evs, EvDue)
}

func TestSkip(t *testing.T) {
	tm := daily(t, 15*time.Minute, at("2026-11-16 08:00:00"), "FREQ=DAILY;BYHOUR=18;BYMINUTE=30")
	evs, exhausted, err := tm.Skip(at("2026-11-16 09:00:00"))
	if err != nil || exhausted {
		t.Fatal(err, exhausted)
	}
	wantKinds(t, evs, EvSkipped)
	if !tm.NextAt.Equal(at("2026-11-17 18:30:00")) {
		t.Fatalf("next after skip %v", tm.NextAt)
	}
	evs, _, _ = tm.Advance(at("2026-11-16 18:30:00"), cfg)
	wantKinds(t, evs)

	// Skipping while due.
	tm.Advance(at("2026-11-17 18:30:00"), cfg)
	wantState(t, tm, Due)
	evs, _, _ = tm.Skip(at("2026-11-17 18:31:00"))
	wantKinds(t, evs, EvSkipped)
	wantState(t, tm, Scheduled)
	if tm.Ringing != nil {
		t.Fatal("skip silences")
	}
}

func TestSkipExhaustsCount(t *testing.T) {
	tm := daily(t, 0, at("2026-11-16 08:00:00"), "FREQ=DAILY;COUNT=2;BYHOUR=18;BYMINUTE=30")
	if _, ex, _ := tm.Skip(at("2026-11-16 09:00:00")); ex {
		t.Fatal("one occurrence is left")
	}
	_, ex, _ := tm.Skip(at("2026-11-16 09:00:00"))
	if !ex {
		t.Fatal("skipping the last occurrence exhausts the schedule")
	}
}

func TestPauseUntil(t *testing.T) {
	tm := daily(t, 15*time.Minute, at("2026-11-16 08:00:00"), "FREQ=DAILY;BYHOUR=18;BYMINUTE=30")
	until := at("2026-11-19 00:00:00")
	if _, err := tm.Pause(at("2026-11-16 09:00:00"), &until); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"2026-11-16 18:30:00", "2026-11-17 18:30:00", "2026-11-18 18:30:00"} {
		if evs, _, remove := tm.Advance(at(d), cfg); len(evs) != 0 || remove {
			t.Fatalf("suspended schedule fired at %s: %v", d, kinds(evs))
		}
	}
	tm.Advance(at("2026-11-19 00:00:00"), cfg)
	if tm.Suspended || !tm.NextAt.Equal(at("2026-11-19 18:30:00")) {
		t.Fatalf("auto-resume: suspended=%v next=%v", tm.Suspended, tm.NextAt)
	}

	// Indefinite pause, resumed by start.
	tm.Pause(at("2026-11-19 09:00:00"), nil)
	tm.Advance(at("2026-11-20 20:00:00"), cfg)
	tm.Start(at("2026-11-21 09:00:00"))
	if tm.Suspended || !tm.NextAt.Equal(at("2026-11-21 18:30:00")) {
		t.Fatalf("resume by start: suspended=%v next=%v", tm.Suspended, tm.NextAt)
	}

	if _, err := tm.Pause(at("2026-11-21 09:00:00"), ptr(at("2026-11-20 00:00:00"))); err == nil {
		t.Fatal("--until in the past must be rejected")
	}
	cd, _ := NewCountdown("x", time.Minute, false, at("2026-11-21 09:00:00"))
	if _, err := cd.Pause(at("2026-11-21 09:00:00"), &until); err == nil {
		t.Fatal("--until on a plain countdown must be rejected")
	}
}

func TestPauseWhileDueSkipsIt(t *testing.T) {
	tm := daily(t, 15*time.Minute, at("2026-11-16 08:00:00"), "FREQ=DAILY;BYHOUR=18;BYMINUTE=30")
	tm.Advance(at("2026-11-16 18:30:00"), cfg)
	evs, _ := tm.Pause(at("2026-11-16 18:31:00"), nil)
	wantKinds(t, evs, EvSkipped)
	if !tm.Suspended || tm.State != Scheduled {
		t.Fatal("pausing a due timer suspends its schedule")
	}
}

func TestReminderWithoutDuration(t *testing.T) {
	tm := daily(t, 0, at("2026-11-16 08:00:00"), "FREQ=DAILY;BYHOUR=9;BYMINUTE=0")
	tm.Advance(at("2026-11-17 09:00:00"), cfg)
	wantState(t, tm, Due)
	evs, err := tm.Start(at("2026-11-17 09:02:00"))
	if err != nil {
		t.Fatal(err)
	}
	wantKinds(t, evs, EvFinished)
	wantState(t, tm, Scheduled)
	if _, err := tm.Start(at("2026-11-17 09:03:00")); !IsNoop(err) {
		t.Fatalf("start on a scheduled reminder: %v", err)
	}
}

func TestOneTimeReminderIsRemovedAfterwards(t *testing.T) {
	when := at("2026-11-20 09:00:00")
	s, _ := schedule.New(nil, &when, at("2026-11-16 08:00:00"))
	tm, err := NewScheduled("dentist", 0, s, at("2026-11-16 08:00:00"))
	if err != nil {
		t.Fatal(err)
	}
	tm.Advance(when, cfg)
	wantState(t, tm, Due)
	tm.Start(when.Add(time.Minute))
	_, _, remove := tm.Advance(when.Add(2*time.Minute), cfg)
	if !remove {
		t.Fatal("an exhausted one-time reminder is removed")
	}
}

func TestOccurrenceDuringARunLapses(t *testing.T) {
	tm := daily(t, 30*time.Minute, at("2026-11-16 08:00:00"), "FREQ=MINUTELY;INTERVAL=20")
	tm.Advance(at("2026-11-16 08:20:00"), cfg)
	tm.Start(at("2026-11-16 08:21:00"))
	evs, _, _ := tm.Advance(at("2026-11-16 08:40:00"), cfg)
	wantKinds(t, evs, EvMissed)
	wantState(t, tm, Running)
}

func TestEarlyOffScheduleRun(t *testing.T) {
	tm := daily(t, 15*time.Minute, at("2026-11-16 08:00:00"), "FREQ=DAILY;BYHOUR=18;BYMINUTE=30")
	tm.Start(at("2026-11-16 17:00:00"))
	wantState(t, tm, Running)
	evs, _, _ := tm.Advance(at("2026-11-16 17:15:00"), cfg)
	wantKinds(t, evs, EvFinished)
	wantState(t, tm, Scheduled)
	if !tm.NextAt.Equal(at("2026-11-16 18:30:00")) {
		t.Fatal("an early run leaves the schedule alone")
	}
}

func TestNewRejectsBadInput(t *testing.T) {
	if _, err := NewCountdown("x", 0, false, time.Now()); err == nil {
		t.Fatal("zero countdown accepted")
	}
	past := at("2020-01-01 00:00:00")
	s, _ := schedule.New(nil, &past, at("2026-11-16 08:00:00"))
	if _, err := NewScheduled("x", 0, s, at("2026-11-16 08:00:00")); err == nil {
		t.Fatal("a schedule without future occurrences accepted")
	}
}
