// Package store keeps all timers in one JSON file. The CLI and the daemon both
// change it, so every read-modify-write holds an flock on a separate lock file and
// writes through a temp file + rename (a reader never sees half a file).
package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"

	"github.com/lightt77/tick/internal/focus"
	"github.com/lightt77/tick/internal/timer"
)

const version = 1

// State is the whole file. Focus is the Focus cube session, if one is running; at
// most one exists. The field is additive, so the file version stays 1 (an older
// daemon would drop it on its next write, so restart the daemon after installing).
type State struct {
	Version int            `json:"version"`
	Timers  []*timer.Timer `json:"timers"`
	Focus   *focus.Session `json:"focus,omitempty"`
}

// Find returns the timer with that name, or nil.
func (s *State) Find(name string) *timer.Timer {
	for _, t := range s.Timers {
		if t.Name == name {
			return t
		}
	}
	return nil
}

// Remove deletes the timer with that name.
func (s *State) Remove(name string) {
	out := s.Timers[:0]
	for _, t := range s.Timers {
		if t.Name != name {
			out = append(out, t)
		}
	}
	s.Timers = out
}

// Store is the state file plus its lock file.
type Store struct {
	Path, Lock string
}

// New uses the given paths; the directory must exist.
func New(path, lock string) *Store { return &Store{Path: path, Lock: lock} }

func (s *Store) lock(how int) (*os.File, error) {
	f, err := os.OpenFile(s.Lock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func unlock(f *os.File) {
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	f.Close()
}

func (s *Store) read() (*State, []byte, error) {
	b, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return &State{Version: version}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, nil, fmt.Errorf("%s is not valid JSON: %v", s.Path, err)
	}
	if st.Version > version {
		return nil, nil, fmt.Errorf("%s was written by a newer tick (version %d)", s.Path, st.Version)
	}
	return &st, b, nil
}

// Load reads the state under a shared lock.
func (s *Store) Load() (*State, error) {
	f, err := s.lock(syscall.LOCK_SH)
	if err != nil {
		return nil, err
	}
	defer unlock(f)
	st, _, err := s.read()
	return st, err
}

// Update runs fn on the current state under an exclusive lock and saves the
// result when fn succeeds and something changed.
func (s *Store) Update(fn func(*State) error) error {
	f, err := s.lock(syscall.LOCK_EX)
	if err != nil {
		return err
	}
	defer unlock(f)
	st, old, err := s.read()
	if err != nil {
		return err
	}
	if err := fn(st); err != nil {
		return err
	}
	st.Version = version
	sort.SliceStable(st.Timers, func(i, j int) bool { return st.Timers[i].Name < st.Timers[j].Name })
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if bytes.Equal(b, old) {
		return nil
	}
	return writeAtomic(s.Path, b)
}

func writeAtomic(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
