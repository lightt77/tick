package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lightt77/tick/internal/timer"
)

func newStore(t *testing.T) *Store {
	dir := t.TempDir()
	return New(filepath.Join(dir, "state.json"), filepath.Join(dir, "state.lock"))
}

func TestEmptyLoad(t *testing.T) {
	st, err := newStore(t).Load()
	if err != nil || len(st.Timers) != 0 {
		t.Fatalf("empty load: %v %v", st, err)
	}
}

func TestRoundTrip(t *testing.T) {
	s := newStore(t)
	now := time.Now().Truncate(time.Second)
	err := s.Update(func(st *State) error {
		tm, _ := timer.NewCountdown("pasta", 10*time.Minute, false, now)
		st.Timers = append(st.Timers, tm)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	tm := st.Find("pasta")
	if tm == nil || time.Duration(tm.Duration) != 10*time.Minute || !tm.Since.Equal(now) {
		t.Fatalf("round trip lost data: %+v", tm)
	}
	info, _ := os.Stat(s.Path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("state file mode %v, want 0600", info.Mode().Perm())
	}
}

func TestFailedUpdateWritesNothing(t *testing.T) {
	s := newStore(t)
	s.Update(func(st *State) error {
		st.Timers = append(st.Timers, timer.NewStopwatch("a", time.Now()))
		return nil
	})
	err := s.Update(func(st *State) error {
		st.Remove("a")
		return fmt.Errorf("boom")
	})
	if err == nil {
		t.Fatal("error not returned")
	}
	st, _ := s.Load()
	if st.Find("a") == nil {
		t.Fatal("a failed update must not be saved")
	}
}

// Each goroutine opens the lock file itself, so flock contends exactly as two
// processes (CLI and daemon) would.
func TestConcurrentUpdatesLoseNothing(t *testing.T) {
	s := newStore(t)
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := s.Update(func(st *State) error {
				st.Timers = append(st.Timers, timer.NewStopwatch(fmt.Sprintf("sw%02d", i), time.Now()))
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	st, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Timers) != 40 {
		t.Fatalf("%d timers after 40 concurrent adds", len(st.Timers))
	}
}

func TestCorruptFileIsAnError(t *testing.T) {
	s := newStore(t)
	os.WriteFile(s.Path, []byte("{not json"), 0o600)
	if _, err := s.Load(); err == nil {
		t.Fatal("corrupt state accepted")
	}
	if err := s.Update(func(*State) error { return nil }); err == nil {
		t.Fatal("update over a corrupt file must fail, not overwrite it")
	}
}
