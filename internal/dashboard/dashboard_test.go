package dashboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lightt77/tick/internal/runlog"
	"github.com/lightt77/tick/internal/schedule"
	"github.com/lightt77/tick/internal/store"
	"github.com/lightt77/tick/internal/timer"
)

var now = time.Date(2026, 11, 16, 8, 0, 0, 0, time.Local)

func sched(t *testing.T, name string, d time.Duration, rule string) *timer.Timer {
	t.Helper()
	s, err := schedule.New([]string{rule}, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	tm, err := timer.NewScheduled(name, d, s, now)
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

func byName(d Data, name string) TimerView {
	for _, v := range d.Timers {
		if v.Name == name {
			return v
		}
	}
	return TimerView{}
}

func TestBuildClockFieldsPerState(t *testing.T) {
	pasta, _ := timer.NewCountdown("pasta", 10*time.Minute, false, now)
	work := timer.NewStopwatch("work", now.Add(-90*time.Second))
	tea, _ := timer.NewCountdown("tea", 5*time.Minute, false, now)
	tea.Pause(now.Add(time.Minute), nil) // 4 minutes left
	st := &store.State{Timers: []*timer.Timer{pasta, work, tea}}

	d := Build(st, now, nil, SourceDaemon, 42)
	if d.Source != SourceDaemon || d.PID != 42 || d.GeneratedMs != now.UnixMilli() {
		t.Fatalf("header: %+v", d)
	}
	if v := byName(d, "pasta"); v.EndsMs == nil || *v.EndsMs != now.Add(10*time.Minute).UnixMilli() || v.State != "running" {
		t.Fatalf("pasta: %+v", v)
	}
	// The page shows now - started as the reading, so started = now - elapsed.
	if v := byName(d, "work"); v.StartedMs == nil || *v.StartedMs != now.Add(-90*time.Second).UnixMilli() {
		t.Fatalf("work: %+v", v)
	}
	if v := byName(d, "tea"); v.State != "paused" || v.RemainingMs == nil || *v.RemainingMs != 4*60*1000 || v.EndsMs != nil {
		t.Fatalf("tea: %+v", v)
	}
}

func TestBuildScheduledTimer(t *testing.T) {
	g := sched(t, "guitar", 15*time.Minute, "FREQ=DAILY;BYHOUR=18;BYMINUTE=30")
	g.Label = "Daily 18:30"
	away := sched(t, "yoga", 0, "FREQ=DAILY;BYHOUR=7;BYMINUTE=0")
	away.Pause(now, nil)
	d := Build(&store.State{Timers: []*timer.Timer{g, away}}, now, nil, SourceSnapshot, 0)

	v := byName(d, "guitar")
	if v.State != "scheduled" || v.Label != "Daily 18:30" || v.DurationMs != 15*60*1000 || len(v.Rules) != 1 ||
		v.NextMs == nil || *v.NextMs != time.Date(2026, 11, 16, 18, 30, 0, 0, time.Local).UnixMilli() {
		t.Fatalf("guitar: %+v", v)
	}
	if y := byName(d, "yoga"); !y.Suspended || y.NextMs != nil {
		t.Fatalf("a paused schedule must not show a next time: %+v", y)
	}
	for _, it := range d.Agenda {
		if it.Name == "yoga" {
			t.Fatal("a paused schedule is in the agenda")
		}
	}
	if len(d.Agenda) != AgendaDays {
		t.Fatalf("daily rule over %d days gave %d agenda items", AgendaDays, len(d.Agenda))
	}
}

func TestAgendaGroupsByDayAndCapsTimes(t *testing.T) {
	busy := sched(t, "pomodoro", 0, "FREQ=MINUTELY;INTERVAL=20;BYHOUR=9,10,11,12;BYDAY=MO,TU,WE,TH,FR")
	d := Build(&store.State{Timers: []*timer.Timer{busy}}, now, nil, SourceDaemon, 1)
	if len(d.Agenda) == 0 {
		t.Fatal("empty agenda")
	}
	end := now.Add(AgendaDays * 24 * time.Hour).UnixMilli()
	for _, it := range d.Agenda {
		if len(it.Times) > maxTimesPerDay || it.Count < len(it.Times) {
			t.Fatalf("%s: %d times listed of %d", it.Day, len(it.Times), it.Count)
		}
		for i, ms := range it.Times {
			if ms <= now.UnixMilli() || ms > end {
				t.Fatalf("%s: time %d outside the window", it.Day, ms)
			}
			if i > 0 && ms <= it.Times[i-1] {
				t.Fatalf("%s: times out of order", it.Day)
			}
			if time.UnixMilli(ms).Format(agendaDayFormat) != it.Day {
				t.Fatalf("%s holds a time from another day", it.Day)
			}
		}
	}
	if first := d.Agenda[0]; first.Count != 12 { // 9:00 to 12:40, every 20 minutes, from Monday 16 Nov
		t.Fatalf("a full day has %d occurrences, want 12", first.Count)
	}
}

func TestAgendaSortedAcrossTimers(t *testing.T) {
	a := sched(t, "late", 0, "FREQ=DAILY;BYHOUR=21;BYMINUTE=0")
	b := sched(t, "early", 0, "FREQ=DAILY;BYHOUR=7;BYMINUTE=0")
	d := Build(&store.State{Timers: []*timer.Timer{a, b}}, now, nil, SourceDaemon, 1)
	if d.Agenda[0].Name != "late" { // 7:00 has passed today; 21:00 is the first left
		t.Fatalf("first item %+v", d.Agenda[0])
	}
	if d.Agenda[1].Day != d.Agenda[2].Day || d.Agenda[1].Name != "early" || d.Agenda[2].Name != "late" {
		t.Fatalf("a day's items are not in time order: %+v", d.Agenda[:4])
	}
}

func TestWriteDataFormatAndMode(t *testing.T) {
	out := filepath.Join(t.TempDir(), "dashboard-data.js")
	d := Build(&store.State{}, now, []runlog.Entry{{TS: now, Tag: "x </script>", Outcome: "done", Kind: "timer"}}, SourceDaemon, 1)
	if err := WriteData(out, d); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out)
	s := string(b)
	if !strings.HasPrefix(s, "window.TICK = {") || !strings.HasSuffix(s, "};\n") {
		t.Fatalf("format: %q", s[:40])
	}
	var back Data
	if err := json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(s, "window.TICK = "), ";\n")), &back); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if len(back.History) != 1 || back.History[0].Tag != "x </script>" {
		t.Fatalf("history %+v", back.History)
	}
	if strings.ContainsRune(s, ' ') {
		t.Fatal("a raw U+2028 would break a script")
	}
	if info, _ := os.Stat(out); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode().Perm())
	}
}

type rig struct {
	dir string
	st  *store.Store
	w   *Writer
}

func newRig(t *testing.T) *rig {
	dir := t.TempDir()
	r := &rig{dir: dir, st: store.New(filepath.Join(dir, "state.json"), filepath.Join(dir, "state.lock"))}
	r.w = &Writer{StatePath: r.st.Path, RunLogPath: filepath.Join(dir, "runs.jsonl"), Out: filepath.Join(dir, "dashboard-data.js"), PID: 7}
	return r
}

func (r *rig) update(t *testing.T, at time.Time) {
	t.Helper()
	st, err := r.st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.w.Update(st, at); err != nil {
		t.Fatal(err)
	}
}

func (r *rig) mtime(t *testing.T) time.Time {
	t.Helper()
	info, err := os.Stat(r.w.Out)
	if err != nil {
		t.Fatal(err)
	}
	return info.ModTime()
}

func TestWriterOnlyWritesOnChangeHeartbeatOrMissingFile(t *testing.T) {
	r := newRig(t)
	r.update(t, now)
	first := r.mtime(t)

	time.Sleep(15 * time.Millisecond)
	r.update(t, now.Add(time.Second))
	if !r.mtime(t).Equal(first) {
		t.Fatal("an unchanged state rewrote the data file")
	}

	// The state changes (someone ran the CLI).
	time.Sleep(15 * time.Millisecond)
	sw := timer.NewStopwatch("work", now)
	r.st.Update(func(s *store.State) error { s.Timers = append(s.Timers, sw); return nil })
	r.update(t, now.Add(2*time.Second))
	second := r.mtime(t)
	if !second.After(first) {
		t.Fatal("a state change was not written")
	}
	if b, _ := os.ReadFile(r.w.Out); !strings.Contains(string(b), `"name":"work"`) {
		t.Fatal("the new timer is not in the data file")
	}

	// A new run-log line counts as a change too.
	time.Sleep(15 * time.Millisecond)
	runlog.Append(r.w.RunLogPath, runlog.Entry{TS: now, Tag: "work", Outcome: "done", Kind: "stopwatch"})
	r.update(t, now.Add(3*time.Second))
	third := r.mtime(t)
	if !third.After(second) {
		t.Fatal("a run-log change was not written")
	}

	// Nothing changed, but the heartbeat is due.
	time.Sleep(15 * time.Millisecond)
	r.update(t, now.Add(3*time.Second+Heartbeat))
	fourth := r.mtime(t)
	if !fourth.After(third) {
		t.Fatal("no heartbeat")
	}

	// Someone deleted the file.
	os.Remove(r.w.Out)
	r.update(t, now.Add(4*time.Second+Heartbeat))
	if _, err := os.Stat(r.w.Out); err != nil {
		t.Fatal("a deleted data file was not restored")
	}
}

func TestWriterFailureWaitsForTheHeartbeat(t *testing.T) {
	r := newRig(t)
	r.w.Out = filepath.Join(r.dir, "no-such-dir", "dashboard-data.js")
	st, _ := r.st.Load()
	if err := r.w.Update(st, now); err == nil {
		t.Fatal("expected a write error")
	}
	if err := r.w.Update(st, now.Add(time.Second)); err != nil {
		t.Fatalf("retried before the heartbeat: %v", err)
	}
	if err := r.w.Update(st, now.Add(Heartbeat)); err == nil {
		t.Fatal("no retry after the heartbeat")
	}
}

func TestSnapshotAndInstallPage(t *testing.T) {
	r := newRig(t)
	if err := Snapshot(r.st, r.w.RunLogPath, r.w.Out, now); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(r.w.Out); !strings.Contains(string(b), `"source":"snapshot"`) {
		t.Fatal("not marked as a snapshot")
	}
	pg := filepath.Join(r.dir, "dashboard.html")
	if err := InstallPage(pg); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(pg)
	time.Sleep(15 * time.Millisecond)
	if err := InstallPage(pg); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.Stat(pg); !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("an identical page was rewritten")
	}
	os.WriteFile(pg, []byte("old"), 0o600)
	InstallPage(pg)
	if b, _ := os.ReadFile(pg); string(b) != string(page) {
		t.Fatal("an outdated page was not replaced")
	}
}

func TestPageLoadsDataSafely(t *testing.T) {
	p := string(page)
	if !strings.Contains(p, "dashboard-data.js") {
		t.Fatal("the page does not load the data file")
	}
	// Timer names are user text: they may only reach the page as text.
	if strings.Contains(p, "innerHTML") || strings.Contains(p, "insertAdjacentHTML") || strings.Contains(p, "document.write") {
		t.Fatal("the page builds markup from data")
	}
	if strings.Contains(p, "http://") || strings.Contains(p, "https://") {
		t.Fatal("the page must not reach the network")
	}
}
