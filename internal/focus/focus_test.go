package focus

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/lightt77/tick/internal/timer"
)

var t0 = time.Date(2026, 11, 16, 9, 0, 0, 0, time.Local)

func at(d time.Duration) time.Time { return t0.Add(d) }

var ladder = []time.Duration{time.Minute, 5 * time.Minute, 10 * time.Minute}
var breaks = []time.Duration{5 * time.Minute, 10 * time.Minute}

func start(t *testing.T, rung int) *Session {
	t.Helper()
	s, err := New(ladder, breaks, rung, t0)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestStartRunsTheChosenRung(t *testing.T) {
	s := start(t, 1)
	if s.State != Running || s.LapLen() != 5*time.Minute || s.LapRemaining(t0) != 5*time.Minute || s.FocusElapsed(t0) != 0 {
		t.Fatalf("%+v", s)
	}
	if got := s.LapRemaining(at(time.Minute)); got != 4*time.Minute {
		t.Fatalf("lap after 1m: %v", got)
	}
	if got := s.FocusElapsed(at(time.Minute)); got != time.Minute {
		t.Fatalf("focus after 1m: %v", got)
	}
	for _, bad := range []struct {
		l, b []time.Duration
		rung int
	}{{nil, nil, 0}, {ladder, breaks, 3}, {ladder, breaks, -1}, {[]time.Duration{0}, nil, 0}, {ladder, []time.Duration{-time.Second}, 0}} {
		if _, err := New(bad.l, bad.b, bad.rung, t0); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if i, ok := FindRung(ladder, 10*time.Minute); !ok || i != 2 {
		t.Fatalf("FindRung %d %v", i, ok)
	}
	if _, ok := FindRung(ladder, 7*time.Minute); ok {
		t.Fatal("found a rung that is not there")
	}
}

func TestLapEndChimesOnceCountsAndRestarts(t *testing.T) {
	s := start(t, 0)
	evs, changed := s.Advance(at(time.Minute))
	if !changed || len(evs) != 1 || evs[0].Kind != EvLap || evs[0].Laps != 1 {
		t.Fatalf("events %+v changed=%v", evs, changed)
	}
	if s.Laps["1m"].Done != 1 || s.LapRemaining(at(time.Minute)) != time.Minute {
		t.Fatalf("after a lap: %+v", s)
	}
	if s.Ringing == nil || !s.Ringing.Since.Equal(s.Ringing.Until) {
		t.Fatalf("a lap chime must play exactly once: %+v", s.Ringing)
	}
	// The next lap runs from where the last one ended.
	evs, _ = s.Advance(at(2*time.Minute + time.Second))
	if len(evs) != 1 || s.Laps["1m"].Done != 2 {
		t.Fatalf("second lap: %+v %+v", evs, s.Laps)
	}
	if got := s.FocusElapsed(at(2*time.Minute + time.Second)); got != 2*time.Minute+time.Second {
		t.Fatalf("focus keeps running across laps: %v", got)
	}
}

func TestAStallCountsEveryLapWithOneChime(t *testing.T) {
	s, _ := New([]time.Duration{10 * time.Second}, nil, 0, t0)
	evs, _ := s.Advance(at(35 * time.Second))
	if len(evs) != 1 || evs[0].Laps != 3 || s.Laps["10s"].Done != 3 {
		t.Fatalf("%+v %+v", evs, s.Laps)
	}
	if got := s.LapRemaining(at(35 * time.Second)); got != 5*time.Second {
		t.Fatalf("the next lap starts where the third ended: %v", got)
	}
}

func TestPauseAndResumeBankBothClocks(t *testing.T) {
	s := start(t, 1)
	mustOK(t, s.Pause(at(2*time.Minute)))
	if s.State != Paused || s.FocusElapsed(at(10*time.Minute)) != 2*time.Minute || s.LapRemaining(at(10*time.Minute)) != 3*time.Minute {
		t.Fatalf("paused: %+v", s)
	}
	if err := s.Pause(at(3 * time.Minute)); !timer.IsNoop(err) {
		t.Fatalf("pausing twice: %v", err)
	}
	mustOK(t, s.Resume(at(10*time.Minute)))
	if got := s.LapRemaining(at(11 * time.Minute)); got != 2*time.Minute {
		t.Fatalf("lap after resume: %v", got)
	}
	if got := s.FocusElapsed(at(11 * time.Minute)); got != 3*time.Minute {
		t.Fatalf("focus after resume: %v", got)
	}
	if err := s.Resume(at(12 * time.Minute)); !timer.IsNoop(err) {
		t.Fatalf("resuming a running session: %v", err)
	}
}

func TestUpAndDownKeepPartialTimeAndCountNoLap(t *testing.T) {
	s := start(t, 0)
	s.Advance(at(time.Minute)) // one finished lap
	mustOK(t, s.Up(at(time.Minute+30*time.Second)))
	if s.LapLen() != 5*time.Minute || s.LapRemaining(at(time.Minute+30*time.Second)) != 5*time.Minute {
		t.Fatalf("up: %+v", s)
	}
	if got := s.FocusElapsed(at(time.Minute + 30*time.Second)); got != time.Minute+30*time.Second {
		t.Fatalf("the partial lap stays as focus time: %v", got)
	}
	if s.Laps["5m"].Done != 0 || s.Laps["5m"].Reset != 0 || s.Laps["1m"].Done != 1 {
		t.Fatalf("an abandoned lap is not counted: %+v", s.Laps)
	}
	mustOK(t, s.Up(at(2*time.Minute)))
	if err := s.Up(at(3 * time.Minute)); !timer.IsNoop(err) {
		t.Fatalf("top of the ladder: %v", err)
	}
	mustOK(t, s.Down(at(3*time.Minute)))
	mustOK(t, s.Down(at(3*time.Minute)))
	if err := s.Down(at(3 * time.Minute)); !timer.IsNoop(err) {
		t.Fatalf("bottom of the ladder: %v", err)
	}
	// A paused session moves too, and stays paused.
	mustOK(t, s.Pause(at(4*time.Minute)))
	mustOK(t, s.Up(at(5*time.Minute)))
	if s.State != Paused || s.LapRemaining(at(6*time.Minute)) != 5*time.Minute {
		t.Fatalf("up while paused: %+v", s)
	}
}

func TestResetTakesTheLapOffTheFocusTime(t *testing.T) {
	s := start(t, 1)
	mustOK(t, s.Reset(at(2*time.Minute+30*time.Second)))
	if got := s.FocusElapsed(at(2*time.Minute + 30*time.Second)); got != 0 {
		t.Fatalf("2m30s of focus were all in the lap: %v", got)
	}
	if s.Laps["5m"].Reset != 1 || s.LapRemaining(at(2*time.Minute+30*time.Second)) != 5*time.Minute {
		t.Fatalf("after reset: %+v", s)
	}
	if s.State != Running {
		t.Fatalf("still running: %s", s.State)
	}
	// Only the lap's elapsed part comes off, never more than there is.
	s2 := start(t, 0)
	s2.Advance(at(time.Minute))                         // 1m of focus banked in a finished lap
	mustOK(t, s2.Reset(at(time.Minute+20*time.Second))) // 20s into the next lap
	if got := s2.FocusElapsed(at(time.Minute + 20*time.Second)); got != time.Minute {
		t.Fatalf("only the 20s of this lap come off: %v", got)
	}
	s3 := start(t, 0)
	s3.Focus = timer.Duration(10 * time.Second) // as if time had been taken off already
	s3.FocusSince, s3.LapSince = nil, nil
	s3.State = Paused
	s3.Lap = timer.Duration(time.Minute - 40*time.Second) // 40s into the lap
	mustOK(t, s3.Reset(at(0)))
	if s3.FocusElapsed(at(0)) != 0 {
		t.Fatalf("focus time never goes below zero: %v", s3.FocusElapsed(at(0)))
	}
	if s3.State != Paused {
		t.Fatalf("reset while paused stays paused: %s", s3.State)
	}
}

func TestBreakPausesEverythingAndRingsUntilAnswered(t *testing.T) {
	s := start(t, 1)
	mustOK(t, s.TakeBreak(at(2*time.Minute), 5*time.Minute))
	if s.State != Break || s.BreakRemaining(at(4*time.Minute)) != 3*time.Minute {
		t.Fatalf("break: %+v", s)
	}
	if s.FocusElapsed(at(20*time.Minute)) != 2*time.Minute || s.LapRemaining(at(20*time.Minute)) != 3*time.Minute {
		t.Fatalf("clocks frozen during a break: %+v", s)
	}
	if evs, changed := s.Advance(at(6 * time.Minute)); changed || len(evs) != 0 {
		t.Fatalf("nothing happens before the break ends: %+v", evs)
	}
	evs, _ := s.Advance(at(7 * time.Minute))
	if s.State != BreakOver || len(evs) != 1 || evs[0].Kind != EvBreakOver {
		t.Fatalf("break over: %s %+v", s.State, evs)
	}
	if s.Ringing == nil || s.Ringing.Until.Sub(s.Ringing.Since) != AckRing {
		t.Fatalf("it rings until answered: %+v", s.Ringing)
	}
	if s.BreakOverBy(at(9*time.Minute)) != 2*time.Minute {
		t.Fatalf("overrun: %v", s.BreakOverBy(at(9*time.Minute)))
	}
	if !s.Silence(at(9*time.Minute)) || s.State != BreakOver {
		t.Fatal("silence stops the sound only")
	}
	mustOK(t, s.Resume(at(10*time.Minute)))
	if s.State != Running || s.Ringing != nil {
		t.Fatalf("resumed: %+v", s)
	}
	if got := s.LapRemaining(at(11 * time.Minute)); got != 2*time.Minute {
		t.Fatalf("the lap continues where it stopped: %v", got)
	}
	sum := s.Summary(at(11 * time.Minute))
	if sum.Breaks != 1 || sum.BreakTotal != 8*time.Minute {
		t.Fatalf("break time is the real time until the answer: %+v", sum)
	}
}

func TestRestartingABreakKeepsTheTimeAlreadySpent(t *testing.T) {
	s := start(t, 0)
	mustOK(t, s.TakeBreak(at(10*time.Second), 5*time.Minute))
	mustOK(t, s.TakeBreak(at(2*time.Minute+10*time.Second), 10*time.Minute))
	if s.State != Break || s.BreakCount != 1 || s.BreakRemaining(at(2*time.Minute+10*time.Second)) != 10*time.Minute {
		t.Fatalf("%+v", s)
	}
	sum := s.Summary(at(5*time.Minute + 10*time.Second))
	if sum.Breaks != 1 || sum.BreakTotal != 5*time.Minute {
		t.Fatalf("2m on the first break plus 3m on the second: %+v", sum)
	}
	if err := s.TakeBreak(at(time.Hour), 0); err == nil {
		t.Fatal("a zero break was accepted")
	}
	// A break can start from a paused session too.
	p := start(t, 0)
	mustOK(t, p.Pause(at(10*time.Second)))
	mustOK(t, p.TakeBreak(at(20*time.Second), time.Minute))
	mustOK(t, p.Resume(at(30*time.Second)))
	if p.State != Running || p.FocusElapsed(at(30*time.Second)) != 10*time.Second {
		t.Fatalf("%+v", p)
	}
}

func TestRefusalsOnABreak(t *testing.T) {
	s := start(t, 0)
	mustOK(t, s.TakeBreak(at(time.Second), time.Minute))
	for name, err := range map[string]error{"pause": s.Pause(at(2 * time.Second)), "up": s.Up(at(2 * time.Second)),
		"down": s.Down(at(2 * time.Second)), "reset": s.Reset(at(2 * time.Second))} {
		if err == nil || timer.IsNoop(err) {
			t.Errorf("%s on a break: %v", name, err)
		}
	}
}

func TestAnUnseenGapPausesAtTheLastSeenTime(t *testing.T) {
	s := start(t, 2)
	s.Advance(at(30 * time.Second)) // the daemon sees it: seen moves to 30s
	evs, _ := s.Advance(at(2 * time.Hour))
	if s.State != Paused || s.PausedBy != PausedBySleep || len(evs) != 1 || evs[0].Kind != EvSleep || !evs[0].At.Equal(at(30*time.Second)) {
		t.Fatalf("%s by %q %+v", s.State, s.PausedBy, evs)
	}
	if got := s.FocusElapsed(at(2 * time.Hour)); got != 30*time.Second {
		t.Fatalf("the slept time must not count: %v", got)
	}
	if got := s.LapRemaining(at(2 * time.Hour)); got != 10*time.Minute-30*time.Second {
		t.Fatalf("lap: %v", got)
	}
	mustOK(t, s.Resume(at(2*time.Hour+time.Minute)))
	if s.State != Running || s.PausedBy != "" {
		t.Fatalf("%+v", s)
	}
	// A gap during a break is just break time.
	b := start(t, 0)
	mustOK(t, b.TakeBreak(at(time.Second), time.Hour))
	if evs, _ := b.Advance(at(30 * time.Minute)); len(evs) != 0 || b.State != Break {
		t.Fatalf("break: %s %+v", b.State, evs)
	}
}

func TestSeenIsRefreshedSlowlyAndQuietTicksChangeNothing(t *testing.T) {
	s := start(t, 2)
	if _, changed := s.Advance(at(time.Second)); changed {
		t.Fatal("a quiet tick changed the session")
	}
	if _, changed := s.Advance(at(29 * time.Second)); changed {
		t.Fatal("seen was rewritten too soon")
	}
	if _, changed := s.Advance(at(31 * time.Second)); !changed || !s.Seen.Equal(at(31*time.Second)) {
		t.Fatal("seen was not refreshed after 30s")
	}
	// A paused session has nothing to refresh.
	mustOK(t, s.Pause(at(32*time.Second)))
	if _, changed := s.Advance(at(10 * time.Minute)); changed {
		t.Fatal("a paused session changed")
	}
}

func TestSummaryAndRoundTrip(t *testing.T) {
	s := start(t, 0)
	s.Advance(at(time.Minute))
	s.Advance(at(2 * time.Minute))
	mustOK(t, s.Up(at(2*time.Minute+10*time.Second)))
	mustOK(t, s.Reset(at(3*time.Minute)))
	sum := s.Summary(at(3 * time.Minute))
	want := map[string]Counts{"1m": {Done: 2}, "5m": {Reset: 1}}
	// 3m of focus, minus the 50s of the abandoned 5m lap that the reset takes off.
	if !reflect.DeepEqual(sum.Laps, want) || sum.Focus != 2*time.Minute+10*time.Second {
		t.Fatalf("summary %+v", sum)
	}

	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var back Session
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	b2, _ := json.Marshal(&back)
	if string(b) != string(b2) {
		t.Fatalf("round trip changed it:\n%s\n%s", b, b2)
	}
}

func TestShort(t *testing.T) {
	for d, want := range map[time.Duration]string{
		time.Minute: "1m", 5 * time.Minute: "5m", time.Hour: "1h", 90 * time.Minute: "1h30m",
		10 * time.Second: "10s", 30 * time.Second: "30s", 90 * time.Second: "1m30s", 0: "0s",
	} {
		if got := Short(d); got != want {
			t.Errorf("Short(%v) = %q, want %q", d, got, want)
		}
	}
}

// Laps is omitted from the JSON while empty, so a session that went through
// state.json has a nil map; counting must still work.
func TestCountersWorkAfterASaveAndLoad(t *testing.T) {
	s := start(t, 0)
	b, _ := json.Marshal(s)
	var back Session
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Laps != nil {
		t.Skip("the map survived the round trip; nothing to guard")
	}
	back.Advance(at(time.Minute))
	if back.Laps["1m"].Done != 1 {
		t.Fatalf("%+v", back.Laps)
	}
	b, _ = json.Marshal(s)
	var again Session
	json.Unmarshal(b, &again)
	if err := again.Reset(at(10 * time.Second)); err != nil || again.Laps["1m"].Reset != 1 {
		t.Fatalf("reset after a load: %v %+v", err, again.Laps)
	}
}
