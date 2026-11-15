package dashboard

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/lightt77/tick/internal/runlog"
	"github.com/lightt77/tick/internal/store"
)

//go:embed page.html
var page []byte

// Heartbeat is how often the daemon rewrites an unchanged data file, so the page
// can tell a quiet daemon from a stopped one.
const Heartbeat = 30 * time.Second

// WriteData writes window.TICK = {...}; atomically (temp file and rename), readable
// only by the user.
func WriteData(path string, d Data) error {
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	out := append([]byte("window.TICK = "), b...)
	out = append(out, ';', '\n')
	return writeAtomic(path, out)
}

// InstallPage copies the embedded page to path unless it is already identical.
func InstallPage(path string) error {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, page) {
		return nil
	}
	return writeAtomic(path, page)
}

// Snapshot writes the data file once, from the state as it is now. The page marks
// it as a snapshot, because no daemon is keeping it fresh.
func Snapshot(s *store.Store, runLogPath, out string, now time.Time) error {
	st, err := s.Load()
	if err != nil {
		return err
	}
	runs, err := runlog.Since(runLogPath, now.Add(-HistoryDays*24*time.Hour))
	if err != nil {
		return err
	}
	return WriteData(out, Build(st, now, runs, SourceSnapshot, 0))
}

func writeAtomic(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".dashboard-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
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

// Writer keeps the data file fresh for the daemon. Update is called every tick, so
// it first does a cheap check (the mtime and size of state.json and runs.jsonl)
// and does nothing while those are unchanged, apart from the heartbeat.
type Writer struct {
	StatePath, RunLogPath, Out string
	PID                        int
	Heartbeat                  time.Duration // zero means the package default

	seen      bool // Update has run before
	wrote     bool // and its last write succeeded
	fp        [4]int64
	lastWrite time.Time
}

func statOf(path string) (mod, size int64) {
	info, err := os.Stat(path)
	if err != nil {
		return -1, -1
	}
	return info.ModTime().UnixNano(), info.Size()
}

// Update rewrites the data file when something changed, the heartbeat is due, or
// the file has gone missing. A failed write is retried after the heartbeat, not on
// the next tick, so a full disk cannot flood the log.
func (w *Writer) Update(st *store.State, now time.Time) error {
	hb := w.Heartbeat
	if hb == 0 {
		hb = Heartbeat
	}
	var fp [4]int64
	fp[0], fp[1] = statOf(w.StatePath)
	fp[2], fp[3] = statOf(w.RunLogPath)
	_, statErr := os.Stat(w.Out)
	gone := statErr != nil && w.wrote // deleted behind our back; after a failed write, wait
	if w.seen && fp == w.fp && !gone && now.Sub(w.lastWrite) < hb {
		return nil
	}
	w.seen, w.wrote, w.fp, w.lastWrite = true, false, fp, now
	runs, err := runlog.Since(w.RunLogPath, now.Add(-HistoryDays*24*time.Hour))
	if err != nil {
		return err
	}
	if err := WriteData(w.Out, Build(st, now, runs, SourceDaemon, w.PID)); err != nil {
		return err
	}
	w.wrote = true
	return nil
}
