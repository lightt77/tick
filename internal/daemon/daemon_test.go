package daemon

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lightt77/tick/internal/ops"
	"github.com/lightt77/tick/internal/runlog"
	"github.com/lightt77/tick/internal/schedule"
	"github.com/lightt77/tick/internal/store"
	"github.com/lightt77/tick/internal/timer"
)

type recorder struct {
	mu    sync.Mutex
	notes []string
	plays int
}

func (r *recorder) Play(string, float64) error {
	r.mu.Lock()
	r.plays++
	r.mu.Unlock()
	return nil
}

func (r *recorder) Notify(title, msg string) error {
	r.mu.Lock()
	r.notes = append(r.notes, title+" | "+msg)
	r.mu.Unlock()
	return nil
}

func (r *recorder) snapshot() ([]string, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.notes...), r.plays
}

type fakeTray struct{ updates int }

func (f *fakeTray) Update(*store.State, time.Time, string) { f.updates++ }

type rig struct {
	d    *Daemon
	ops  ops.Ops
	rec  *recorder
	tray *fakeTray
	now  time.Time
	dir  string
}

func newRig(t *testing.T) *rig {
	dir := t.TempDir()
	r := &rig{rec: &recorder{}, tray: &fakeTray{}, dir: dir, now: time.Date(2026, 11, 16, 8, 0, 0, 0, time.Local)}
	st := store.New(filepath.Join(dir, "state.json"), filepath.Join(dir, "state.lock"))
	clock := func() time.Time { return r.now }
	r.d = &Daemon{Store: st, RunLog: filepath.Join(dir, "runs.jsonl"), ConfigPath: filepath.Join(dir, "config.toml"),
		Player: r.rec, Tray: r.tray, Now: clock}
	r.ops = ops.Ops{Store: st, RunLog: r.d.RunLog, Now: clock}
	return r
}

func (r *rig) step(t *testing.T) {
	t.Helper()
	if err := r.d.Step(); err != nil {
		t.Fatal(err)
	}
}

func readLog(t *testing.T, path string) []runlog.Entry {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []runlog.Entry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e runlog.Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

func waitNotes(r *recorder, n int) []string {
	for i := 0; i < 100; i++ {
		if notes, _ := r.snapshot(); len(notes) >= n {
			return notes
		}
		time.Sleep(5 * time.Millisecond)
	}
	notes, _ := r.snapshot()
	return notes
}

func TestCountdownEndToEnd(t *testing.T) {
	r := newRig(t)
	tm, _ := timer.NewCountdown("pasta", 10*time.Minute, false, r.now)
	if err := r.ops.Create(tm); err != nil {
		t.Fatal(err)
	}
	r.step(t)
	r.now = r.now.Add(10*time.Minute + time.Second)
	r.step(t)

	notes := waitNotes(r.rec, 1)
	if len(notes) != 1 || notes[0] != "⏱ pasta done | 10m finished" {
		t.Fatalf("notifications %q", notes)
	}
	log := readLog(t, r.d.RunLog)
	if len(log) != 1 || log[0].Outcome != "done" || log[0].Amount != 10 || log[0].Unit != "min" || log[0].Tag != "pasta" {
		t.Fatalf("run log %+v", log)
	}
	st, _ := r.d.Store.Load()
	if p := st.Find("pasta"); p == nil || p.State != timer.Done || p.Ringing == nil {
		t.Fatalf("after finishing: %+v", p)
	}
	time.Sleep(20 * time.Millisecond)
	if _, plays := r.rec.snapshot(); plays == 0 {
		t.Fatal("no sound played")
	}

	// cancel from the "CLI" stops the ring and logs it.
	if _, err := r.ops.Apply("pasta", "", ops.Cancel, nil); err != nil {
		t.Fatal(err)
	}
	r.step(t)
	st, _ = r.d.Store.Load()
	if st.Find("pasta") != nil {
		t.Fatal("cancel did not delete")
	}
	if r.tray.updates == 0 {
		t.Fatal("tray never updated")
	}
}

func TestScheduledReminderEndToEnd(t *testing.T) {
	r := newRig(t)
	s, err := schedule.New([]string{"FREQ=DAILY;BYHOUR=18;BYMINUTE=30"}, nil, r.now)
	if err != nil {
		t.Fatal(err)
	}
	tm, _ := timer.NewScheduled("guitar", 15*time.Minute, s, r.now)
	tm.Label = "Daily 18:30"
	r.ops.Create(tm)

	// The daemon was down from 18:00 to 19:00 (lid closed); it fires late on wake.
	r.now = time.Date(2026, 11, 16, 19, 0, 0, 0, time.Local)
	r.step(t)
	notes := waitNotes(r.rec, 1)
	if len(notes) != 1 || notes[0] != "⏰ guitar · Daily 18:30 | Was due at 18:30 while the Mac slept. Due now. Start it from the menu bar or: tick timer guitar start" {
		t.Fatalf("due notification %q", notes)
	}
	if _, err := r.ops.Apply("guitar", timer.KindTimer, ops.Start, nil); err != nil {
		t.Fatal(err)
	}
	r.now = r.now.Add(15 * time.Minute)
	r.step(t)
	log := readLog(t, r.d.RunLog)
	if len(log) != 1 || log[0].Outcome != "done" || log[0].Note != "late" {
		t.Fatalf("run log %+v", log)
	}
	st, _ := r.d.Store.Load()
	if g := st.Find("guitar"); g == nil || g.State != timer.Scheduled || !g.NextAt.Equal(time.Date(2026, 11, 17, 18, 30, 0, 0, time.Local)) {
		t.Fatalf("after the run: %+v", g)
	}
}

func TestQuietTickWritesNothing(t *testing.T) {
	r := newRig(t)
	sw := timer.NewStopwatch("work", r.now)
	r.ops.Create(sw)
	before, _ := os.Stat(r.d.Store.Path)
	time.Sleep(10 * time.Millisecond)
	r.now = r.now.Add(time.Minute)
	r.step(t)
	after, _ := os.Stat(r.d.Store.Path)
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("a tick that changed nothing rewrote the state file")
	}
}

func TestSingleInstanceLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.lock")
	if ok, _ := Running(path); ok {
		t.Fatal("nothing is running yet")
	}
	f, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if ok, pid := Running(path); !ok || pid != os.Getpid() {
		t.Fatalf("running=%v pid=%d", ok, pid)
	}
	if _, err := Lock(path); err == nil {
		t.Fatal("a second daemon got the lock")
	}
}

func TestShort(t *testing.T) {
	for d, want := range map[time.Duration]string{
		15 * time.Minute: "15m", 90 * time.Minute: "1h30m", 2 * time.Hour: "2h", 45 * time.Second: "45s", 0: "0s",
	} {
		if got := Short(d); got != want {
			t.Errorf("Short(%v) = %q, want %q", d, got, want)
		}
	}
}
