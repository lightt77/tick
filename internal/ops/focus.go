package ops

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/lightt77/tick/internal/focus"
	"github.com/lightt77/tick/internal/runlog"
	"github.com/lightt77/tick/internal/store"
	"github.com/lightt77/tick/internal/timer"
)

// Verbs of the Focus cube session. The CLI and the tray both use them.
const (
	FocusPause   = "pause"
	FocusResume  = "resume" // also ends a break and continues the lap
	FocusUp      = "up"
	FocusDown    = "down"
	FocusReset   = "reset"
	FocusBreak   = "break"
	FocusDone    = "done"
	FocusCancel  = "cancel"
	FocusSilence = "silence"
)

// FocusVerbs lists what FocusDo accepts, for messages.
const FocusVerbs = "pause, resume, up, down, reset, break, done, cancel, silence"

// defaultBreak is used when a session has no break choices and none is given.
const defaultBreak = 5 * time.Minute

// ErrNoFocus is returned by FocusDo when no session is running.
var ErrNoFocus = errors.New("no focus session; start one with: tick focus start")

// FocusStart begins the session on rung `rung` of the ladder. Only one can exist.
func (o Ops) FocusStart(ladder, breaks []time.Duration, rung int) (*focus.Session, error) {
	var out *focus.Session
	err := o.Store.Update(func(st *store.State) error {
		if st.Focus != nil {
			return fmt.Errorf("a focus session is already %s; use its verbs (%s)", st.Focus.State, FocusVerbs)
		}
		s, err := focus.New(ladder, breaks, rung, o.now())
		if err != nil {
			return err
		}
		st.Focus, out = s, s
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// FocusResult is what a verb left behind. Session is nil when the session ended.
type FocusResult struct {
	Session *focus.Session
	Ended   *focus.Summary
}

// FocusDo applies one verb to the session. d is only used by break (zero means the
// first break choice). A no-op comes back as a timer.ErrNoop error and saves nothing.
func (o Ops) FocusDo(verb string, d time.Duration) (FocusResult, error) {
	var res FocusResult
	var entry *runlog.Entry
	err := o.Store.Update(func(st *store.State) error {
		res, entry = FocusResult{}, nil
		s := st.Focus
		if s == nil {
			return ErrNoFocus
		}
		now := o.now()
		var err error
		switch verb {
		case FocusPause:
			err = s.Pause(now)
		case FocusResume:
			err = s.Resume(now)
		case FocusUp:
			err = s.Up(now)
		case FocusDown:
			err = s.Down(now)
		case FocusReset:
			err = s.Reset(now)
		case FocusBreak:
			if d == 0 {
				d = defaultBreak
				if b := s.BreakChoices(); len(b) > 0 {
					d = b[0]
				}
			}
			err = s.TakeBreak(now, d)
		case FocusSilence:
			if !s.Silence(now) {
				err = timer.ErrNoop{Msg: "focus is not ringing"}
			}
		case FocusDone, FocusCancel:
			sum := s.Summary(now)
			outcome := "done"
			if verb == FocusCancel {
				outcome = "cancelled"
			}
			e := FocusEntry(sum, outcome)
			entry = &e
			res.Ended = &sum
			st.Focus = nil
			return nil
		default:
			return fmt.Errorf("unknown focus verb %q (%s)", verb, FocusVerbs)
		}
		if err != nil {
			return err
		}
		res.Session = s
		return nil
	})
	if err != nil {
		return FocusResult{}, err
	}
	if entry != nil {
		if err := runlog.Append(o.RunLog, *entry); err != nil {
			return res, err
		}
	}
	return res, nil
}

// FocusEntry is the one line a finished or cancelled session writes to runs.jsonl:
// the focus time, and the per-lap-length counts as detail.
func FocusEntry(sum focus.Summary, outcome string) runlog.Entry {
	e := runlog.Entry{TS: sum.At, Tag: "focus", Outcome: outcome, Kind: "focus"}
	if a := math.Round(sum.Focus.Minutes()*10) / 10; a > 0 {
		e.Amount, e.Unit = a, "min"
	}
	d := &runlog.Detail{Breaks: sum.Breaks, BreakMin: math.Round(sum.BreakTotal.Minutes()*10) / 10}
	laps := 0
	for k, c := range sum.Laps {
		if c.Done == 0 && c.Reset == 0 {
			continue
		}
		if d.Laps == nil {
			d.Laps = map[string]runlog.LapCount{}
		}
		d.Laps[k] = runlog.LapCount{Done: c.Done, Reset: c.Reset}
		laps += c.Done
	}
	e.Detail = d
	e.Note = plural(laps, "lap")
	if sum.Breaks > 0 {
		e.Note += ", " + plural(sum.Breaks, "break")
	}
	return e
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
