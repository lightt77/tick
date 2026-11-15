// Package ops is what the CLI and the tray both do to timers: create one, apply a
// verb, silence. Each call is one locked store update followed by run-log lines.
package ops

import (
	"fmt"
	"time"

	"github.com/lightt77/tick/internal/runlog"
	"github.com/lightt77/tick/internal/store"
	"github.com/lightt77/tick/internal/timer"
)

// Verbs a user can apply.
const (
	Start   = "start"
	Pause   = "pause"
	Restart = "restart"
	Cancel  = "cancel"
	Skip    = "skip"
	Silence = "silence"
)

// Ops binds the operations to a store and run log.
type Ops struct {
	Store  *store.Store
	RunLog string
	Now    func() time.Time
}

func (o Ops) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// Create adds a new timer; the name must be free.
func (o Ops) Create(t *timer.Timer) error {
	return o.Store.Update(func(st *store.State) error {
		if existing := st.Find(t.Name); existing != nil {
			return fmt.Errorf("%s already exists (%s %s); use its verbs, or cancel it first", t.Name, existing.Kind, existing.State)
		}
		st.Timers = append(st.Timers, t)
		return nil
	})
}

// Result is what a verb left behind. Timer is nil when the timer was deleted.
type Result struct {
	Name    string
	Timer   *timer.Timer
	Deleted bool
	Events  []timer.Event
}

// Apply runs one verb on the named timer. kind, when not empty, must match.
// A no-op comes back as a timer.ErrNoop error and saves nothing.
func (o Ops) Apply(name string, kind timer.Kind, verb string, until *time.Time) (Result, error) {
	res := Result{Name: name}
	var tkind timer.Kind
	err := o.Store.Update(func(st *store.State) error {
		res = Result{Name: name}
		t := st.Find(name)
		if t == nil {
			return fmt.Errorf("no timer or stopwatch named %q", name)
		}
		if kind != "" && t.Kind != kind {
			return fmt.Errorf("%s is a %s, not a %s", name, t.Kind, kind)
		}
		tkind = t.Kind
		now := o.now()
		if until != nil && verb != Pause {
			return fmt.Errorf("--until only goes with pause")
		}
		var evs []timer.Event
		var err error
		switch verb {
		case Start:
			evs, err = t.Start(now)
		case Pause:
			evs, err = t.Pause(now, until)
		case Restart:
			evs, err = t.Restart(now)
		case Skip:
			var exhausted bool
			evs, exhausted, err = t.Skip(now)
			if err == nil && exhausted {
				st.Remove(name)
				res.Deleted = true
			}
		case Cancel:
			// A finished countdown was already logged as done; cancelling it is
			// just clearing it away.
			if t.State != timer.Done {
				evs = []timer.Event{t.Cancel(now)}
			}
			st.Remove(name)
			res.Deleted = true
		case Silence:
			if !t.Silence(now) {
				err = timer.ErrNoop{Msg: name + " is not ringing"}
			}
		default:
			return fmt.Errorf("unknown verb %q (start, pause, restart, cancel, skip)", verb)
		}
		if err != nil {
			return err
		}
		res.Events = evs
		if !res.Deleted {
			res.Timer = t
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return res, runlog.Events(o.RunLog, tkind, res.Events)
}

// SilenceAll stops every ringing timer and returns how many there were.
func (o Ops) SilenceAll() (int, error) {
	n := 0
	err := o.Store.Update(func(st *store.State) error {
		n = 0
		now := o.now()
		for _, t := range st.Timers {
			if t.Silence(now) {
				n++
			}
		}
		return nil
	})
	return n, err
}
