package alert

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type countPlayer struct {
	mu    sync.Mutex
	plays map[string]int
}

func (c *countPlayer) Play(path string, _ float64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.plays[path]++
	return nil
}

func (c *countPlayer) Notify(string, string) error { return nil }

func (c *countPlayer) count(p string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.plays[p]
}

const iv = 20 * time.Millisecond

func TestChimePlaysOnce(t *testing.T) {
	p := &countPlayer{plays: map[string]int{}}
	r := NewRinger(p, iv)
	now := time.Now()
	r.Reconcile(map[string]Ring{"tick": {Sound: "chime", Since: now, Until: now}})
	time.Sleep(10 * iv)
	// The same ring listed again must not replay.
	r.Reconcile(map[string]Ring{"tick": {Sound: "chime", Since: now, Until: now}})
	time.Sleep(5 * iv)
	if n := p.count("chime"); n != 1 {
		t.Fatalf("chime played %d times, want 1", n)
	}
}

func TestNagRingsThreeTimes(t *testing.T) {
	p := &countPlayer{plays: map[string]int{}}
	r := NewRinger(p, iv)
	now := time.Now()
	r.Reconcile(map[string]Ring{"guitar": {Sound: "nag", Since: now, Until: now.Add(2*iv + iv/2)}})
	time.Sleep(12 * iv)
	if n := p.count("nag"); n != 3 {
		t.Fatalf("nag played %d times, want 3", n)
	}
}

func TestReconcileStopsAndRestarts(t *testing.T) {
	p := &countPlayer{plays: map[string]int{}}
	r := NewRinger(p, iv)
	now := time.Now()
	r.Reconcile(map[string]Ring{"pasta": {Sound: "done", Since: now, Until: now.Add(time.Hour)}})
	time.Sleep(3 * iv)
	r.Reconcile(nil) // cancelled from the CLI
	stopped := p.count("done")
	time.Sleep(5 * iv)
	if p.count("done") != stopped {
		t.Fatal("ringing went on after the timer stopped ringing")
	}
	// A new ring for the same name (new Since) starts again.
	later := time.Now()
	r.Reconcile(map[string]Ring{"pasta": {Sound: "done", Since: later, Until: later}})
	time.Sleep(3 * iv)
	if p.count("done") != stopped+1 {
		t.Fatal("a new ring did not play")
	}
}

func TestResolveSound(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Bell.aiff"), []byte("x"), 0o600)
	soundDirs = func() []string { return []string{dir} }
	if p, err := ResolveSound("bell"); err != nil || p != filepath.Join(dir, "Bell.aiff") {
		t.Fatalf("by name: %q %v", p, err)
	}
	file := filepath.Join(dir, "custom.mp3")
	os.WriteFile(file, []byte("x"), 0o600)
	if p, err := ResolveSound(file); err != nil || p != file {
		t.Fatalf("by path: %q %v", p, err)
	}
	if _, err := ResolveSound("nope"); err == nil {
		t.Fatal("unknown sound accepted")
	}
	if _, err := ResolveSound(filepath.Join(dir, "missing.mp3")); err == nil {
		t.Fatal("missing file accepted")
	}
}

func TestSoundsDeduplicatedAndSorted(t *testing.T) {
	user, system := t.TempDir(), t.TempDir()
	for _, f := range []string{"glass.wav", "Zap.aiff", ".DS_Store"} {
		os.WriteFile(filepath.Join(user, f), []byte("x"), 0o600)
	}
	for _, f := range []string{"Glass.aiff", "Hero.aiff"} {
		os.WriteFile(filepath.Join(system, f), []byte("x"), 0o600)
	}
	soundDirs = func() []string { return []string{user, system} }
	got := strings.Join(Sounds(), ",")
	if got != "glass,Hero,Zap" {
		t.Fatalf("Sounds() = %s", got)
	}
}

func TestYourSoundsComeFirstAndWin(t *testing.T) {
	yoursDir, user, system := t.TempDir(), t.TempDir(), t.TempDir()
	for _, f := range []string{"elevator-ding.mp3", "Glass.wav", "airhorn.m4a"} {
		os.WriteFile(filepath.Join(yoursDir, f), []byte("x"), 0o600)
	}
	os.WriteFile(filepath.Join(user, "Zap.aiff"), []byte("x"), 0o600)
	for _, f := range []string{"Glass.aiff", "Hero.aiff"} {
		os.WriteFile(filepath.Join(system, f), []byte("x"), 0o600)
	}
	soundDirs = func() []string { return []string{user, system} }
	SetCustomDir(yoursDir)
	defer SetCustomDir("")

	yours, others := SoundGroups()
	if strings.Join(yours, ",") != "airhorn,elevator-ding,Glass" || strings.Join(others, ",") != "Hero,Zap" {
		t.Fatalf("groups: yours=%v others=%v", yours, others)
	}
	if got := strings.Join(Sounds(), ","); got != "airhorn,elevator-ding,Glass,Hero,Zap" {
		t.Fatalf("Sounds() = %s", got)
	}
	// Your Glass shadows the macOS one.
	if p, err := ResolveSound("glass"); err != nil || p != filepath.Join(yoursDir, "Glass.wav") {
		t.Fatalf("resolve glass: %q %v", p, err)
	}
	if p, err := ResolveSound("elevator-ding"); err != nil || p != filepath.Join(yoursDir, "elevator-ding.mp3") {
		t.Fatalf("resolve elevator-ding: %q %v", p, err)
	}

	SetCustomDir("")
	if yours, _ := SoundGroups(); len(yours) != 0 {
		t.Fatal("no sounds_dir, no group")
	}
}

func TestStampMovesWhenAFileIsAdded(t *testing.T) {
	dir := t.TempDir()
	soundDirs = func() []string { return nil }
	SetCustomDir(dir)
	defer SetCustomDir("")
	before := Stamp()
	time.Sleep(20 * time.Millisecond)
	os.WriteFile(filepath.Join(dir, "new.mp3"), []byte("x"), 0o600)
	if Stamp() == before {
		t.Fatal("adding a sound did not change the stamp")
	}
}
