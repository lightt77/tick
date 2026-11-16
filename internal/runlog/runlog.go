// Package runlog appends one JSON line per finished, skipped, missed or cancelled
// run. The fields (ts, tag, amount, unit, note) are the ones agreed for the future
// event ledger, so this file can become that ledger without a migration.
package runlog

import (
	"bufio"
	"encoding/json"
	"errors"
	"math"
	"os"
	"time"

	"github.com/lightt77/tick/internal/timer"
)

// Entry is one line of runs.jsonl.
type Entry struct {
	TS      time.Time `json:"ts"`
	Tag     string    `json:"tag"`
	Amount  float64   `json:"amount,omitempty"`
	Unit    string    `json:"unit,omitempty"`
	Note    string    `json:"note,omitempty"`
	Outcome string    `json:"outcome"`
	Kind    string    `json:"kind"`
	Detail  *Detail   `json:"detail,omitempty"` // a focus session's breakdown; absent on other lines
}

// Detail is the breakdown a Focus cube session leaves with its single line.
type Detail struct {
	Laps     map[string]LapCount `json:"laps,omitempty"` // per lap length, e.g. "5m"
	Breaks   int                 `json:"breaks,omitempty"`
	BreakMin float64             `json:"break_min,omitempty"`
}

// LapCount is how many laps of one length were finished or thrown away.
type LapCount struct {
	Done  int `json:"done,omitempty"`
	Reset int `json:"reset,omitempty"`
}

// FromEvent turns an event worth keeping into an entry. Due and nag alerts are not
// outcomes, and ticker laps would flood the log, so those return false.
func FromEvent(ev timer.Event, kind timer.Kind) (Entry, bool) {
	switch ev.Kind {
	case timer.EvFinished, timer.EvMissed, timer.EvSkipped, timer.EvCancelled:
	default:
		return Entry{}, false
	}
	if ev.Chime {
		return Entry{}, false
	}
	e := Entry{TS: ev.At, Tag: ev.Timer, Outcome: string(ev.Kind), Kind: string(kind), Note: ev.Note}
	if a := math.Round(ev.Amount.Minutes()*10) / 10; a > 0 {
		e.Amount, e.Unit = a, "min"
	}
	if ev.Late && e.Note == "" {
		e.Note = "late"
	}
	return e, true
}

// Append writes entries to the log at path, one line each.
func Append(path string, entries ...Entry) error {
	if len(entries) == 0 {
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, e := range entries {
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			return err
		}
	}
	return nil
}

// Since reads the entries at or after from, oldest first. A missing file is an
// empty log; lines that do not parse are skipped.
func Since(path string, from time.Time) ([]Entry, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.TS.Before(from) {
			continue
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// Events logs every event that is an outcome.
func Events(path string, kind timer.Kind, evs []timer.Event) error {
	var es []Entry
	for _, ev := range evs {
		if e, ok := FromEvent(ev, kind); ok {
			es = append(es, e)
		}
	}
	return Append(path, es...)
}
