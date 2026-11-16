package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lightt77/tick/internal/paths"
	"github.com/lightt77/tick/internal/runlog"
	"github.com/lightt77/tick/internal/store"
	"github.com/lightt77/tick/internal/timer"
)

// tickEnv points tick at a throwaway data folder and runs commands in-process.
type tickEnv struct{ t *testing.T }

func newEnv(t *testing.T) tickEnv {
	t.Setenv("TICK_HOME", t.TempDir())
	t.Setenv("TICK_ALERT", "log")
	return tickEnv{t}
}

// run executes `tick args...` and returns its output.
func (e tickEnv) run(args ...string) (string, error) {
	var out, errOut bytes.Buffer
	a := &app{out: &out, errOut: &errOut}
	root := a.rootCmd()
	root.SetArgs(args)
	root.SetOut(&out)
	root.SetErr(&errOut)
	err := root.Execute()
	return out.String(), err
}

func (e tickEnv) must(args ...string) string {
	e.t.Helper()
	out, err := e.run(args...)
	if err != nil {
		e.t.Fatalf("tick %v: %v\n%s", args, err, out)
	}
	return out
}

func (e tickEnv) fails(want string, args ...string) {
	e.t.Helper()
	out, err := e.run(args...)
	if err == nil || !strings.Contains(err.Error(), want) {
		e.t.Fatalf("tick %v: error %v (output %q), want one containing %q", args, err, out, want)
	}
}

func (e tickEnv) state() *store.State {
	e.t.Helper()
	st, err := store.New(paths.State(), paths.StateLock()).Load()
	if err != nil {
		e.t.Fatal(err)
	}
	return st
}

func (e tickEnv) log() []runlog.Entry {
	e.t.Helper()
	es, err := runlog.Since(paths.RunLog(), time.Time{})
	if err != nil {
		e.t.Fatal(err)
	}
	return es
}

func TestShortGrammarEndToEnd(t *testing.T) {
	e := newEnv(t)

	if out := e.must(); !strings.Contains(out, "Nothing here yet") {
		t.Fatalf("empty overview: %q", out)
	}
	e.must("pasta", "10m")
	e.must("s", "work")
	e.must("deep work", "25m", "-r")

	st := e.state()
	if p := st.Find("pasta"); p == nil || p.Kind != timer.KindTimer || p.State != timer.Running || p.Repeat {
		t.Fatalf("pasta: %+v", p)
	}
	if w := st.Find("work"); w == nil || w.Kind != timer.KindStopwatch {
		t.Fatalf("work: %+v", w)
	}
	if d := st.Find("deep work"); d == nil || !d.Repeat {
		t.Fatalf("deep work: %+v", d)
	}

	// views
	if out := e.must("pasta"); !strings.Contains(out, "pasta") || !strings.Contains(out, "left") {
		t.Fatalf("show: %q", out)
	}
	e.fails("No such timer or stopwatch exists", "nope")
	e.fails("No such timer or stopwatch exists", "nope", "pause")

	all := e.must()
	for _, want := range []string{"Running", "Stopwatches", "pasta", "work", "deep work", "repeats every 25m"} {
		if !strings.Contains(all, want) {
			t.Errorf("overview lacks %q:\n%s", want, all)
		}
	}
	tOnly, sOnly := e.must("t"), e.must("s")
	if strings.Contains(tOnly, "work  ↑") || !strings.Contains(tOnly, "pasta") {
		t.Errorf("tick t:\n%s", tOnly)
	}
	if !strings.Contains(sOnly, "work") || strings.Contains(sOnly, "pasta") {
		t.Errorf("tick s:\n%s", sOnly)
	}

	// duplicates are refused, whatever the kind
	e.fails("already exists", "pasta", "5m")
	e.fails("already exists", "s", "pasta")
	e.fails("already exists", "s", "work")

	// verbs
	e.must("pasta", "pause")
	if e.state().Find("pasta").State != timer.Paused {
		t.Fatal("pause")
	}
	e.must("pasta", "resume")
	if e.state().Find("pasta").State != timer.Running {
		t.Fatal("resume")
	}
	e.fails("is a stopwatch, not a timer", "t", "work", "pause")
	e.fails("is a timer, not a stopwatch", "s", "pasta", "pause")

	// done / cancel / rm
	if out := e.must("work", "done"); !strings.Contains(out, "work done") {
		t.Fatalf("done: %q", out)
	}
	if e.state().Find("work") != nil {
		t.Fatal("done removes a stopwatch")
	}
	if out := e.must("pasta", "cancel"); !strings.Contains(out, "cancelled") {
		t.Fatalf("cancel: %q", out)
	}
	e.must("deep work", "rm")
	log := e.log()
	if len(log) != 3 || log[0].Outcome != "done" || log[0].Tag != "work" || log[1].Outcome != "cancelled" || log[2].Outcome != "cancelled" {
		t.Fatalf("run log %+v", log)
	}
}

func TestUnnamedCountdownsGetNamesFromTheirLength(t *testing.T) {
	e := newEnv(t)
	e.must("10m")
	e.must("10m")
	e.must("10m", "-r")
	st := e.state()
	for _, name := range []string{"10m", "10m-2", "10m-3"} {
		if st.Find(name) == nil {
			t.Errorf("no timer %q: %v", name, st.Timers)
		}
	}
	if !st.Find("10m-3").Repeat {
		t.Error("-r on an unnamed countdown repeats it")
	}
	e.must("10m-2", "pause") // reached through its name
	if e.state().Find("10m-2").State != timer.Paused {
		t.Fatal("pause an auto-named countdown")
	}
}

func TestFlagMisuseIsRefused(t *testing.T) {
	e := newEnv(t)
	e.must("pasta", "10m")
	e.fails("-r goes with a duration", "pasta", "-r")
	e.fails("-r goes with a duration", "s", "work", "-r")
	e.fails("-w goes with", "pasta", "-w")
	e.fails("--until goes with pause", "10m", "--until", "2030-01-01")
	e.fails("looks like a duration", "s", "5m")
}

func TestOldFormsStillWork(t *testing.T) {
	e := newEnv(t)
	e.must("timer", "tea", "start", "5m")
	e.must("stopwatch", "gym", "start")
	e.must("15m", "start") // the ticker
	st := e.state()
	if st.Find("tea") == nil || st.Find("gym") == nil {
		t.Fatal("long forms")
	}
	if tk := st.Find("tick"); tk == nil || !tk.Repeat {
		t.Fatalf("tick 15m start: %+v", tk)
	}
	e.must("pause") // pauses the ticker
	if e.state().Find("tick").State != timer.Paused {
		t.Fatal("bare verb targets the ticker")
	}
	e.must("timer", "tea", "pause")
	e.must("timer", "tea", "resume")
	e.must("timer", "tea", "done")
	if e.state().Find("tea") != nil {
		t.Fatal("done through the long form")
	}
}

// A schedule pending after it rang: done and cancel resolve it, rm deletes it.
func TestPendingScheduleVerbs(t *testing.T) {
	e := newEnv(t)
	e.must("timer", "guitar", "start", "--rrule", "FREQ=DAILY;BYHOUR=18;BYMINUTE=30", "--label", "Daily 18:30")

	pend := func() {
		t.Helper()
		s := store.New(paths.State(), paths.StateLock())
		err := s.Update(func(st *store.State) error {
			g := st.Find("guitar")
			g.State = timer.Due
			due := time.Now().Add(-time.Hour)
			g.DueAt = &due
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	pend()
	if out := e.must(); !strings.Contains(out, "Needs you") || !strings.Contains(out, "waiting for you") {
		t.Fatalf("overview of a pending reminder:\n%s", out)
	}
	if out := e.must("guitar", "done"); !strings.Contains(out, "guitar done") {
		t.Fatalf("done: %q", out)
	}
	if g := e.state().Find("guitar"); g == nil || g.State != timer.Scheduled {
		t.Fatalf("after done: %+v", g)
	}
	pend()
	e.must("guitar", "cancel")
	if g := e.state().Find("guitar"); g == nil || g.State != timer.Scheduled || g.DueAt != nil {
		t.Fatalf("after cancel: %+v", g)
	}
	log := e.log()
	if len(log) != 2 || log[0].Outcome != "done" || log[1].Outcome != "skipped" {
		t.Fatalf("run log %+v", log)
	}
	e.fails("nothing pending", "guitar", "done")
	e.must("guitar", "rm")
	if e.state().Find("guitar") != nil {
		t.Fatal("rm deletes the schedule")
	}
}

func TestFocusSessionFromTheCommandLine(t *testing.T) {
	e := newEnv(t)
	if out := e.must("focus"); !strings.Contains(out, "No focus session") {
		t.Fatalf("idle: %q", out)
	}
	e.fails("no focus session", "focus", "pause")

	out := e.must("focus", "start", "5m")
	if !strings.Contains(out, "focus: running · 5m lap, 04:5") && !strings.Contains(out, "focus: running · 5m lap, 05:00") {
		t.Fatalf("start: %q", out)
	}
	if f := e.state().Focus; f == nil || f.LapLen() != 5*time.Minute || len(f.Rungs()) != 6 {
		t.Fatalf("session %+v", f)
	}
	e.fails("already running", "focus", "start")
	if _, err := e.run("focus", "resume"); !timer.IsNoop(err) || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("a no-op comes back as ErrNoop (printed, exit 0): %v", err)
	}

	e.must("focus", "up")
	e.must("focus", "down")
	e.must("focus", "down")
	e.must("focus", "reset")
	if f := e.state().Focus; f.Laps["1m"].Reset != 1 || f.LapLen() != time.Minute {
		t.Fatalf("after up, down, down, reset: %+v", f)
	}
	if _, err := e.run("focus", "down"); !timer.IsNoop(err) || !strings.Contains(err.Error(), "shortest") {
		t.Fatalf("bottom of the ladder: %v", err)
	}

	e.must("focus", "pause")
	if out := e.must("focus"); !strings.Contains(out, "paused") {
		t.Fatalf("paused: %q", out)
	}
	e.must("focus", "break", "7m")
	if out := e.must("focus"); !strings.Contains(out, "on a break · 06:5") && !strings.Contains(out, "on a break · 07:00") {
		t.Fatalf("break: %q", out)
	}
	e.fails("on a break", "focus", "pause")
	e.fails("break length", "focus", "break", "soon")
	e.must("focus", "resume")

	var v FocusView
	if err := json.Unmarshal([]byte(e.must("focus", "--json")), &v); err != nil {
		t.Fatal(err)
	}
	if !v.Active || v.State != "running" || v.Lap != "1m" || v.Breaks != 1 || v.Laps["1m"].Reset != 1 || len(v.Ladder) != 6 {
		t.Fatalf("json %+v", v)
	}
	if !strings.Contains(e.must(), "focus: running") {
		t.Fatal("the overview does not show the session")
	}

	out = e.must("focus", "done")
	if !strings.Contains(out, "focus done") || !strings.Contains(out, "0 laps, 1 break") {
		t.Fatalf("done: %q", out)
	}
	if e.state().Focus != nil {
		t.Fatal("still there after done")
	}
	log, err := runlog.Since(paths.RunLog(), time.Time{})
	if err != nil || len(log) != 1 || log[0].Tag != "focus" || log[0].Detail == nil || log[0].Detail.Breaks != 1 {
		t.Fatalf("run log %+v %v", log, err)
	}
}

func TestFocusStartRefusesAnUnknownRungAndATimerCannotBeNamedFocus(t *testing.T) {
	e := newEnv(t)
	e.fails("no 7m lap", "focus", "start", "7m")
	e.fails("above zero", "focus", "start", "0s")
	e.fails("is a command", "timer", "focus", "start", "5m")
	e.fails("at most 1 arg", "focus", "start", "1m", "2m")
}

func TestFocusUsesTheConfiguredLadder(t *testing.T) {
	e := newEnv(t)
	if err := os.WriteFile(paths.Config(), []byte("focus_steps = [\"2m\", \"20m\"]\nfocus_breaks = [\"3m\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.must("focus", "start", "20m")
	if f := e.state().Focus; f == nil || len(f.Rungs()) != 2 || f.LapLen() != 20*time.Minute || f.BreakChoices()[0] != 3*time.Minute {
		t.Fatalf("%+v", f)
	}
	e.must("focus", "break")
	f := e.state().Focus
	if f.BreakRemaining(time.Now()) < 2*time.Minute+50*time.Second {
		t.Fatalf("the default break is the first focus_breaks choice: %v", f.BreakRemaining(time.Now()))
	}
}
