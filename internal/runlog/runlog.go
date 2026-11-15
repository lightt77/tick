// Package runlog appends one JSON line per finished, skipped, missed or cancelled
// run. The fields (ts, tag, amount, unit, note) are the ones agreed for the future
// event ledger, so this file can become that ledger without a migration.
package runlog

import (
	"encoding/json"
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
