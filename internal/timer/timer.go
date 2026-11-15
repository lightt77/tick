// Package timer is tick's one entity: a countdown, a stopwatch, a repeating ticker
// or a scheduled reminder, and the state machine that moves it along. Everything
// here is a pure function of the timer and an injected time, so it is testable
// without sleeping.
package timer

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lightt77/tick/internal/schedule"
)

// Kind separates countdowns (which may repeat or be scheduled) from stopwatches.
type Kind string

const (
	KindTimer     Kind = "timer"
	KindStopwatch Kind = "stopwatch"
)

// State is where a timer is in its life.
type State string

const (
	Scheduled State = "scheduled" // waiting for the next occurrence
	Due       State = "due"       // an occurrence fired; waiting for start
	Running   State = "running"
	Paused    State = "paused"
	Done      State = "done" // a one-off countdown that finished
)

// Duration is a time.Duration stored as "15m0s" so the JSON is readable.
type Duration time.Duration

func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(time.Duration(d).String()) }

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	*d = Duration(v)
	return err
}

// Ringing means the daemon is playing the alert sound until Until (at least once).
type Ringing struct {
	Since time.Time `json:"since"`
	Until time.Time `json:"until"`
}

// Timer is stored in state.json.
type Timer struct {
	Name     string             `json:"name"`
	Kind     Kind               `json:"kind"`
	State    State              `json:"state"`
	Duration Duration           `json:"duration,omitempty"` // full countdown; 0 for stopwatches and plain reminders
	Repeat   bool               `json:"repeat,omitempty"`
	Sound    string             `json:"sound,omitempty"`
	Label    string             `json:"label,omitempty"`
	Grace    Duration           `json:"grace,omitempty"`
	Schedule *schedule.Schedule `json:"schedule,omitempty"`

	// Banked is the remaining time of a countdown, or the elapsed time of a
	// stopwatch, at the moment Since was set (or now, when not running).
	Banked Duration   `json:"banked,omitempty"`
	Since  *time.Time `json:"since,omitempty"` // start of the running segment

	NextAt         *time.Time `json:"next_at,omitempty"`         // next occurrence of the schedule
	DueAt          *time.Time `json:"due_at,omitempty"`          // the occurrence that is due or being run
	Late           bool       `json:"late,omitempty"`            // fired after waking, not on time
	Nags           int        `json:"nags,omitempty"`            // alerts sent for the due occurrence
	LastNagAt      *time.Time `json:"last_nag_at,omitempty"`     //
	Suspended      bool       `json:"suspended,omitempty"`       // schedule paused
	SuspendedUntil *time.Time `json:"suspended_until,omitempty"` // auto-resume time

	Ringing *Ringing  `json:"ringing,omitempty"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

// Settings are the alert and grace defaults from config.toml.
type Settings struct {
	RingInterval time.Duration
	RingCap      time.Duration
	NagInterval  time.Duration
	NagCount     int
	Grace        time.Duration
}

// DefaultSettings match the documented config defaults.
var DefaultSettings = Settings{
	RingInterval: 5 * time.Second,
	RingCap:      2 * time.Minute,
	NagInterval:  10 * time.Minute,
	NagCount:     3,
	Grace:        2 * time.Hour,
}

// EventKind says what happened, for the run log and notifications.
type EventKind string

const (
	EvFinished  EventKind = "done"      // a countdown ran to zero
	EvDue       EventKind = "due"       // a scheduled occurrence fired
	EvNag       EventKind = "nag"       // a due occurrence was re-announced
	EvMissed    EventKind = "missed"    // an occurrence passed without being started
	EvSkipped   EventKind = "skipped"   // an occurrence was skipped on purpose
	EvCancelled EventKind = "cancelled" // the timer was deleted
)

// Event is one thing worth logging or announcing.
type Event struct {
	Kind   EventKind
	Timer  string
	At     time.Time     // when it happened, or the occurrence it is about
	Amount time.Duration // run length for done/cancelled
	Late   bool
	Note   string
	Chime  bool // a repeating ticker lap: one short sound, no ring loop
}

// ErrNoop reports a verb that changed nothing; the CLI prints it, exit code 0.
type ErrNoop struct{ Msg string }

func (e ErrNoop) Error() string { return e.Msg }

// IsNoop tells a harmless no-op from a real error.
func IsNoop(err error) bool {
	var n ErrNoop
	return errors.As(err, &n)
}

func ptr(t time.Time) *time.Time { return &t }

// NewCountdown is a timer that starts running now.
func NewCountdown(name string, d time.Duration, repeat bool, now time.Time) (*Timer, error) {
	if d <= 0 {
		return nil, fmt.Errorf("a countdown needs a duration above zero, e.g. 10m")
	}
	return &Timer{Name: name, Kind: KindTimer, State: Running, Duration: Duration(d), Repeat: repeat,
		Banked: Duration(d), Since: ptr(now), Created: now, Updated: now}, nil
}

// NewStopwatch starts counting up now.
func NewStopwatch(name string, now time.Time) *Timer {
	return &Timer{Name: name, Kind: KindStopwatch, State: Running, Since: ptr(now), Created: now, Updated: now}
}

// NewScheduled waits for the schedule's first occurrence after now.
func NewScheduled(name string, d time.Duration, s schedule.Schedule, now time.Time) (*Timer, error) {
	if d < 0 {
		return nil, fmt.Errorf("duration cannot be negative")
	}
	next, ok := s.Next(now)
	if !ok {
		return nil, fmt.Errorf("the schedule has no occurrence after now")
	}
	return &Timer{Name: name, Kind: KindTimer, State: Scheduled, Duration: Duration(d), Schedule: &s,
		NextAt: ptr(next), Created: now, Updated: now}, nil
}

// Remaining is the countdown left at now (zero for stopwatches).
func (t *Timer) Remaining(now time.Time) time.Duration {
	if t.Kind != KindTimer {
		return 0
	}
	r := time.Duration(t.Banked)
	if t.State == Running && t.Since != nil {
		r -= now.Sub(*t.Since)
	}
	if r < 0 {
		r = 0
	}
	return r
}

// Elapsed is the stopwatch reading at now.
func (t *Timer) Elapsed(now time.Time) time.Duration {
	e := time.Duration(t.Banked)
	if t.State == Running && t.Since != nil {
		e += now.Sub(*t.Since)
	}
	return e
}

// EndsAt is when a running countdown reaches zero.
func (t *Timer) EndsAt(now time.Time) (time.Time, bool) {
	if t.Kind != KindTimer || t.State != Running || t.Since == nil {
		return time.Time{}, false
	}
	return t.Since.Add(time.Duration(t.Banked)), true
}

func (t *Timer) runFull(now time.Time) {
	t.State = Running
	t.Banked = t.Duration
	t.Since = ptr(now)
}

func (t *Timer) clearDue() {
	t.DueAt, t.Late, t.Nags, t.LastNagAt = nil, false, 0, nil
}

// Start creates nothing; it resumes, begins a due run, resumes a paused schedule,
// or (on a scheduled timer with a duration) begins an off-schedule run now.
func (t *Timer) Start(now time.Time) ([]Event, error) {
	t.Ringing = nil
	defer func() { t.Updated = now }()
	switch t.State {
	case Running:
		return nil, ErrNoop{t.Name + " is already running"}
	case Paused:
		t.State = Running
		t.Since = ptr(now)
		return nil, nil
	case Done:
		t.runFull(now)
		return nil, nil
	case Due:
		if t.Duration == 0 {
			ev := Event{Kind: EvFinished, Timer: t.Name, At: now, Late: t.Late}
			t.clearDue()
			t.State = Scheduled
			return []Event{ev}, nil
		}
		t.runFull(now)
		return nil, nil
	case Scheduled:
		if t.Suspended {
			t.Suspended, t.SuspendedUntil = false, nil
			t.NextAt = nil
			if next, ok := t.Schedule.Next(now); ok {
				t.NextAt = ptr(next)
			}
			return nil, nil
		}
		if t.Duration == 0 {
			return nil, ErrNoop{fmt.Sprintf("%s is scheduled; next at %s", t.Name, fmtWhen(t.NextAt))}
		}
		t.runFull(now) // an early, off-schedule session; upcoming occurrences stay
		return nil, nil
	}
	return nil, fmt.Errorf("unknown state %q", t.State)
}

// Pause stops the clock of a running countdown or stopwatch, or suspends a
// schedule (until a time, when given).
func (t *Timer) Pause(now time.Time, until *time.Time) ([]Event, error) {
	t.Ringing = nil
	defer func() { t.Updated = now }()
	if until != nil && t.Schedule == nil {
		return nil, fmt.Errorf("--until only applies to scheduled timers")
	}
	if until != nil && !until.After(now) {
		return nil, fmt.Errorf("--until must be in the future")
	}
	switch t.State {
	case Running:
		if until != nil {
			return nil, fmt.Errorf("%s is running; pause it first, or skip the next occurrence", t.Name)
		}
		if t.Kind == KindStopwatch {
			t.Banked = Duration(t.Elapsed(now))
		} else {
			t.Banked = Duration(t.Remaining(now))
		}
		t.Since = nil
		t.State = Paused
		return nil, nil
	case Paused:
		return nil, ErrNoop{t.Name + " is already paused"}
	case Done:
		return nil, ErrNoop{t.Name + " has finished; restart or cancel it"}
	case Due, Scheduled:
		var evs []Event
		if t.State == Due {
			evs = append(evs, Event{Kind: EvSkipped, Timer: t.Name, At: *t.DueAt, Note: "schedule paused"})
			t.clearDue()
			t.State = Scheduled
		}
		t.Suspended = true
		t.SuspendedUntil = until
		return evs, nil
	}
	return nil, fmt.Errorf("unknown state %q", t.State)
}

// Restart runs the countdown again from its full duration, or zeroes a stopwatch.
func (t *Timer) Restart(now time.Time) ([]Event, error) {
	t.Ringing = nil
	defer func() { t.Updated = now }()
	if t.Kind == KindStopwatch {
		t.State, t.Banked, t.Since = Running, 0, ptr(now)
		return nil, nil
	}
	switch t.State {
	case Running, Paused, Done:
		t.runFull(now)
		return nil, nil
	}
	return nil, ErrNoop{fmt.Sprintf("%s is %s; nothing to restart (use start or skip)", t.Name, t.State)}
}

// Skip passes over the due occurrence, or else the next one.
// It returns exhausted=true when no occurrences remain and nothing is running.
func (t *Timer) Skip(now time.Time) (evs []Event, exhausted bool, err error) {
	if t.Schedule == nil {
		return nil, false, fmt.Errorf("%s has no schedule to skip", t.Name)
	}
	t.Ringing = nil
	t.Updated = now
	if t.State == Due {
		evs = append(evs, Event{Kind: EvSkipped, Timer: t.Name, At: *t.DueAt})
		t.clearDue()
		t.State = Scheduled
		return evs, t.NextAt == nil, nil
	}
	if t.NextAt == nil {
		return nil, false, ErrNoop{t.Name + " has no upcoming occurrence to skip"}
	}
	skipped := *t.NextAt
	t.Schedule.Skip(skipped)
	evs = append(evs, Event{Kind: EvSkipped, Timer: t.Name, At: skipped})
	t.NextAt = nil
	if next, ok := t.Schedule.Next(skipped); ok {
		t.NextAt = ptr(next)
	}
	return evs, t.NextAt == nil && t.State == Scheduled, nil
}

// Cancel returns the event to log when the timer is deleted.
func (t *Timer) Cancel(now time.Time) Event {
	ev := Event{Kind: EvCancelled, Timer: t.Name, At: now}
	switch {
	case t.Kind == KindStopwatch:
		ev.Amount = t.Elapsed(now)
	case t.State == Running || t.State == Paused:
		ev.Amount = time.Duration(t.Duration) - t.Remaining(now)
	}
	return ev
}

// Silence stops the alert sound and nothing else.
func (t *Timer) Silence(now time.Time) bool {
	if t.Ringing == nil {
		return false
	}
	t.Ringing = nil
	t.Updated = now
	return true
}

// Advance moves the timer to now: countdowns finish, rings time out, schedules
// fire (with the grace window after a sleep), due occurrences nag and then lapse.
// remove=true means the timer is finished for good and should be deleted.
func (t *Timer) Advance(now time.Time, cfg Settings) (evs []Event, changed, remove bool) {
	before, _ := json.Marshal(t)

	if t.Ringing != nil && now.After(t.Ringing.Until) {
		t.Ringing = nil
	}

	// 1. A running countdown reaching zero.
	if t.Kind == KindTimer && t.State == Running && t.Remaining(now) == 0 {
		end, _ := t.EndsAt(now)
		ev := Event{Kind: EvFinished, Timer: t.Name, At: end, Amount: time.Duration(t.Duration), Late: t.Late}
		switch {
		case t.Repeat:
			// Next lap starts where the last one ended, unless we slept through
			// several laps; then it starts now (no replaying).
			next := end
			if now.Sub(end) >= time.Duration(t.Duration) {
				next = now
			}
			t.Banked, t.Since = t.Duration, ptr(next)
			ev.Chime = true
			t.Ringing = &Ringing{Since: now, Until: now}
		case t.Schedule != nil:
			t.State, t.Banked, t.Since = Scheduled, 0, nil
			t.clearDue()
			t.Ringing = &Ringing{Since: now, Until: now.Add(cfg.RingCap)}
		default:
			t.State, t.Banked, t.Since = Done, 0, nil
			t.Ringing = &Ringing{Since: now, Until: now.Add(cfg.RingCap)}
		}
		evs = append(evs, ev)
	}

	if t.Schedule != nil {
		// 2. Auto-resume of a paused schedule (no catch-up of what passed meanwhile).
		if t.Suspended && t.SuspendedUntil != nil && !now.Before(*t.SuspendedUntil) {
			t.Suspended, t.SuspendedUntil = false, nil
			t.NextAt = nil
			if next, ok := t.Schedule.Next(now); ok {
				t.NextAt = ptr(next)
			}
		}

		// 3. Occurrences that have arrived.
		if !t.Suspended && t.NextAt != nil && !t.NextAt.After(now) {
			occ := append([]time.Time{*t.NextAt}, t.Schedule.Between(*t.NextAt, now, 50)...)
			latest := occ[len(occ)-1]
			for _, o := range occ[:len(occ)-1] {
				evs = append(evs, Event{Kind: EvMissed, Timer: t.Name, At: o})
			}
			grace := time.Duration(t.Grace)
			if grace == 0 {
				grace = cfg.Grace
			}
			switch {
			case t.State == Running || t.State == Paused:
				// Busy with the previous run: this occurrence lapses.
				evs = append(evs, Event{Kind: EvMissed, Timer: t.Name, At: latest, Note: "previous run still going"})
			case now.Sub(latest) > grace:
				evs = append(evs, Event{Kind: EvMissed, Timer: t.Name, At: latest, Note: "outside the grace window"})
			default:
				if t.State == Due {
					evs = append(evs, Event{Kind: EvMissed, Timer: t.Name, At: *t.DueAt, Note: "next occurrence arrived"})
				}
				late := now.Sub(latest) > 90*time.Second
				t.State, t.DueAt, t.Late, t.Nags, t.LastNagAt = Due, ptr(latest), late, 1, ptr(now)
				t.Ringing = nagRing(now, cfg)
				evs = append(evs, Event{Kind: EvDue, Timer: t.Name, At: latest, Late: late})
			}
			t.NextAt = nil
			if next, ok := t.Schedule.Next(now); ok {
				t.NextAt = ptr(next)
			}
		}

		// 4. Nagging a due occurrence, then letting it lapse.
		if t.State == Due && t.LastNagAt != nil && now.Sub(*t.LastNagAt) >= cfg.NagInterval {
			if t.Nags >= cfg.NagCount {
				evs = append(evs, Event{Kind: EvMissed, Timer: t.Name, At: *t.DueAt, Note: "not started"})
				t.clearDue()
				t.State = Scheduled
			} else {
				t.Nags++
				t.LastNagAt = ptr(now)
				t.Ringing = nagRing(now, cfg)
				evs = append(evs, Event{Kind: EvNag, Timer: t.Name, At: *t.DueAt, Late: t.Late})
			}
		}

		// 5. Nothing left to wait for.
		if t.State == Scheduled && !t.Suspended && t.NextAt == nil && t.Ringing == nil {
			remove = true
		}
	}

	after, _ := json.Marshal(t)
	changed = string(before) != string(after)
	if changed {
		t.Updated = now
	}
	return evs, changed, remove
}

// A nag rings three times, ring_interval apart.
func nagRing(now time.Time, cfg Settings) *Ringing {
	return &Ringing{Since: now, Until: now.Add(2*cfg.RingInterval + cfg.RingInterval/2)}
}

func fmtWhen(t *time.Time) string {
	if t == nil {
		return "never"
	}
	return t.Local().Format("Mon 2006-01-02 15:04")
}
