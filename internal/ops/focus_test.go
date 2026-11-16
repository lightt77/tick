package ops

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/lightt77/tick/internal/runlog"
	"github.com/lightt77/tick/internal/store"
	"github.com/lightt77/tick/internal/timer"
)

var ladder = []time.Duration{time.Minute, 5 * time.Minute}
var breaks = []time.Duration{10 * time.Minute, 30 * time.Minute}

type fix struct {
	o   Ops
	now time.Time
}

func newFix(t *testing.T) *fix {
	dir := t.TempDir()
	f := &fix{now: time.Date(2026, 11, 16, 9, 0, 0, 0, time.Local)}
	f.o = Ops{Store: store.New(filepath.Join(dir, "state.json"), filepath.Join(dir, "state.lock")),
		RunLog: filepath.Join(dir, "runs.jsonl"), Now: func() time.Time { return f.now }}
	return f
}

func (f *fix) log(t *testing.T) []runlog.Entry {
	t.Helper()
	got, err := runlog.Since(f.o.RunLog, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestFocusStartIsExclusive(t *testing.T) {
	f := newFix(t)
	if _, err := f.o.FocusStart(ladder, breaks, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.o.FocusStart(ladder, breaks, 0); err == nil {
		t.Fatal("a second session was started")
	}
	if _, err := f.o.FocusDo("fly", 0); err == nil || timer.IsNoop(err) {
		t.Fatalf("unknown verb: %v", err)
	}
}

func TestFocusVerbsWithoutASessionSayHowToStart(t *testing.T) {
	f := newFix(t)
	if _, err := f.o.FocusDo(FocusPause, 0); err != ErrNoFocus {
		t.Fatalf("%v", err)
	}
	if _, err := f.o.FocusStart(ladder, breaks, 5); err == nil {
		t.Fatal("a rung that is not on the ladder")
	}
}

func TestFocusDoneWritesOneLineWithTheBreakdown(t *testing.T) {
	f := newFix(t)
	f.o.FocusStart(ladder, breaks, 0)
	st, _ := f.o.Store.Load()
	// Two finished laps, as the daemon would count them.
	f.now = f.now.Add(2 * time.Minute)
	f.o.Store.Update(func(s *store.State) error { s.Focus.Advance(f.now); return nil })
	if _, err := f.o.FocusDo(FocusBreak, 0); err != nil { // the first choice, 10m
		t.Fatal(err)
	}
	if st, _ = f.o.Store.Load(); st.Focus.BreakRemaining(f.now) != 10*time.Minute {
		t.Fatalf("default break: %v", st.Focus.BreakRemaining(f.now))
	}
	f.now = f.now.Add(6 * time.Minute)
	res, err := f.o.FocusDo(FocusDone, 0)
	if err != nil || res.Session != nil || res.Ended == nil {
		t.Fatalf("%+v %v", res, err)
	}
	if st, _ = f.o.Store.Load(); st.Focus != nil {
		t.Fatal("the session was not removed")
	}
	log := f.log(t)
	if len(log) != 1 {
		t.Fatalf("one line expected: %+v", log)
	}
	e := log[0]
	if e.Tag != "focus" || e.Kind != "focus" || e.Outcome != "done" || e.Amount != 2 || e.Unit != "min" || e.Note != "2 laps, 1 break" {
		t.Fatalf("%+v", e)
	}
	if e.Detail == nil || e.Detail.Laps["1m"].Done != 2 || e.Detail.Breaks != 1 || e.Detail.BreakMin != 6 {
		t.Fatalf("detail %+v", e.Detail)
	}
}

func TestFocusCancelLogsCancelledAndNoopsSaveNothing(t *testing.T) {
	f := newFix(t)
	f.o.FocusStart(ladder, breaks, 1)
	if _, err := f.o.FocusDo(FocusResume, 0); !timer.IsNoop(err) {
		t.Fatalf("resume while running: %v", err)
	}
	if _, err := f.o.FocusDo(FocusSilence, 0); !timer.IsNoop(err) {
		t.Fatalf("silence while quiet: %v", err)
	}
	f.now = f.now.Add(30 * time.Second)
	if _, err := f.o.FocusDo(FocusCancel, 0); err != nil {
		t.Fatal(err)
	}
	log := f.log(t)
	if len(log) != 1 || log[0].Outcome != "cancelled" || log[0].Note != "0 laps" {
		t.Fatalf("%+v", log)
	}
}
