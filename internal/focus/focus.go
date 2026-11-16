// Package focus is the Focus cube: one session that keeps a focus stopwatch running
// while a lap timer repeats on a ladder of durations (1m, 5m, 10m, ...). Like
// package timer, everything here is a pure function of the session and an injected
// time, so it is testable without sleeping. The session is stored in state.json.
//
// Time is never ticked inside the state. Each clock is a banked value plus a start
// time (the same idea as timer.Timer), and every reading is computed from `now`.
package focus

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lightt77/tick/internal/timer"
)

// State is where the session is.
type State string

const (
	Running   State = "running"
	Paused    State = "paused"
	Break     State = "break"
	BreakOver State = "breakover" // the break ended and rings until the user answers
)

const (
	// SleepGap is how long the daemon may go without seeing a running session before
	// the gap is treated as the Mac sleeping (or the daemon being down).
	SleepGap = 2 * time.Minute
	// SeenEvery is how often `seen` is saved while the session runs.
	SeenEvery = 30 * time.Second
	// AckRing is how long a finished break rings if the user never answers.
	AckRing = 12 * time.Hour
)

// PausedBySleep marks a pause that tick made itself.
const PausedBySleep = "sleep"

// Counts is how many laps of one duration were finished or thrown away.
type Counts struct {
	Done  int `json:"done,omitempty"`
	Reset int `json:"reset,omitempty"`
}

// Session is stored in state.json. At most one exists.
type Session struct {
	State    State  `json:"state"`
	PausedBy string `json:"paused_by,omitempty"` // PausedBySleep, else empty (the user)

	// The ladder and the break choices are copied from the config at the start, so
	// editing config.toml mid-session changes nothing.
	Ladder []timer.Duration `json:"ladder"`
	Breaks []timer.Duration `json:"breaks,omitempty"`
	Step   int              `json:"step"` // index into Ladder

	// Focus clock: focus time so far. Banked at the moment FocusSince was set.
	Focus      timer.Duration `json:"focus,omitempty"`
	FocusSince *time.Time     `json:"focus_since,omitempty"`
	// Lap clock: time left in the current lap, banked at the moment LapSince was set.
	Lap      timer.Duration `json:"lap"`
	LapSince *time.Time     `json:"lap_since,omitempty"`

	Laps map[string]Counts `json:"laps,omitempty"` // keyed by Short(lap length)

	BreakCount int            `json:"break_count,omitempty"`
	BreakTotal timer.Duration `json:"break_total,omitempty"` // real time spent on finished breaks
	BreakStart *time.Time     `json:"break_start,omitempty"`
	BreakEnds  *time.Time     `json:"break_ends,omitempty"`

	Seen    time.Time      `json:"seen"` // last time the daemon saw the session running
	Ringing *timer.Ringing `json:"ringing,omitempty"`
	Started time.Time      `json:"started"`
	Updated time.Time      `json:"updated"`
}

// EventKind says what Advance did.
type EventKind string

const (
	EvLap       EventKind = "lap"        // one or more laps finished; chime, no banner
	EvBreakOver EventKind = "break_over" // the break ended
	EvSleep     EventKind = "sleep"      // paused because the session was not seen
)

// Event is one thing Advance did that the daemon announces.
type Event struct {
	Kind EventKind
	At   time.Time
	Laps int           // EvLap: how many laps finished
	Lap  time.Duration // EvLap: the lap length
}

// Summary is what a finished or cancelled session leaves behind.
type Summary struct {
	Started    time.Time
	At         time.Time
	Focus      time.Duration
	Laps       map[string]Counts
	Breaks     int
	BreakTotal time.Duration
}

// Short formats a duration compactly: 1m, 1h30m, 45s.
func Short(d time.Duration) string {
	s := d.Round(time.Second).String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

func ptr(t time.Time) *time.Time { return &t }

// count adds to the counters of a lap length. Laps is omitted from the JSON while
// empty, so it comes back nil after a save and a load.
func (s *Session) count(lap time.Duration, add Counts) {
	if s.Laps == nil {
		s.Laps = map[string]Counts{}
	}
	key := Short(lap)
	c := s.Laps[key]
	c.Done += add.Done
	c.Reset += add.Reset
	s.Laps[key] = c
}

func pos(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}

func durations(in []time.Duration) []timer.Duration {
	out := make([]timer.Duration, len(in))
	for i, d := range in {
		out[i] = timer.Duration(d)
	}
	return out
}

// New starts a session on rung `start` of the ladder, running from now.
func New(ladder, breaks []time.Duration, start int, now time.Time) (*Session, error) {
	if len(ladder) == 0 {
		return nil, errors.New("the focus ladder is empty")
	}
	for _, d := range append(append([]time.Duration{}, ladder...), breaks...) {
		if d <= 0 {
			return nil, fmt.Errorf("focus durations must be above zero (got %s)", d)
		}
	}
	if start < 0 || start >= len(ladder) {
		return nil, fmt.Errorf("no rung %d on the ladder", start)
	}
	s := &Session{Ladder: durations(ladder), Breaks: durations(breaks), Step: start,
		Lap: timer.Duration(ladder[start]), Laps: map[string]Counts{}, Started: now, Updated: now}
	s.run(now)
	return s, nil
}

// FindRung returns the ladder index of a duration.
func FindRung(ladder []time.Duration, d time.Duration) (int, bool) {
	for i, l := range ladder {
		if l == d {
			return i, true
		}
	}
	return 0, false
}

// LapLen is the length of the current lap.
func (s *Session) LapLen() time.Duration { return time.Duration(s.Ladder[s.Step]) }

// Rungs returns the ladder as plain durations.
func (s *Session) Rungs() []time.Duration { return plain(s.Ladder) }

// BreakChoices returns the break durations as plain durations.
func (s *Session) BreakChoices() []time.Duration { return plain(s.Breaks) }

func plain(in []timer.Duration) []time.Duration {
	out := make([]time.Duration, len(in))
	for i, d := range in {
		out[i] = time.Duration(d)
	}
	return out
}

// FocusElapsed is the focus stopwatch at now.
func (s *Session) FocusElapsed(now time.Time) time.Duration {
	e := time.Duration(s.Focus)
	if s.State == Running && s.FocusSince != nil {
		e += pos(now.Sub(*s.FocusSince))
	}
	return e
}

// LapRemaining is what is left of the current lap at now.
func (s *Session) LapRemaining(now time.Time) time.Duration {
	r := time.Duration(s.Lap)
	if s.State == Running && s.LapSince != nil {
		r -= pos(now.Sub(*s.LapSince))
	}
	return pos(r)
}

// LapEnds is when a running lap reaches zero.
func (s *Session) LapEnds() (time.Time, bool) {
	if s.State != Running || s.LapSince == nil {
		return time.Time{}, false
	}
	return s.LapSince.Add(time.Duration(s.Lap)), true
}

// BreakRemaining is what is left of a break in progress (zero once it is over).
func (s *Session) BreakRemaining(now time.Time) time.Duration {
	if s.State != Break || s.BreakEnds == nil {
		return 0
	}
	return pos(s.BreakEnds.Sub(now))
}

// BreakOverBy is how long ago the break ended, while it waits for an answer.
func (s *Session) BreakOverBy(now time.Time) time.Duration {
	if s.State != BreakOver || s.BreakEnds == nil {
		return 0
	}
	return pos(now.Sub(*s.BreakEnds))
}

// run starts both clocks at now.
func (s *Session) run(now time.Time) {
	s.State, s.PausedBy = Running, ""
	s.FocusSince, s.LapSince = ptr(now), ptr(now)
	s.Seen = now
	s.Updated = now
}

// bank stops both clocks at `at`, keeping what they read.
func (s *Session) bank(at time.Time) {
	if s.State != Running || s.FocusSince == nil || s.LapSince == nil {
		return
	}
	s.Focus += timer.Duration(pos(at.Sub(*s.FocusSince)))
	s.Lap -= timer.Duration(pos(at.Sub(*s.LapSince)))
	if s.Lap < 0 {
		s.Lap = 0
	}
	s.FocusSince, s.LapSince = nil, nil
}

func (s *Session) leaveBreak(now time.Time) {
	if s.BreakStart != nil {
		s.BreakTotal += timer.Duration(pos(now.Sub(*s.BreakStart)))
	}
	s.BreakStart, s.BreakEnds = nil, nil
	s.Ringing = nil
}

func (s *Session) onBreak() bool { return s.State == Break || s.State == BreakOver }

var errOnBreak = errors.New("on a break; resume ends it")

// Pause stops both clocks.
func (s *Session) Pause(now time.Time) error {
	switch {
	case s.onBreak():
		return errOnBreak
	case s.State == Paused:
		return timer.ErrNoop{Msg: "focus is already paused"}
	}
	s.bank(now)
	s.State, s.PausedBy, s.Updated = Paused, "", now
	return nil
}

// Resume continues a paused session, or ends a break and continues the lap.
func (s *Session) Resume(now time.Time) error {
	switch s.State {
	case Running:
		return timer.ErrNoop{Msg: "focus is already running"}
	case Break, BreakOver:
		s.leaveBreak(now)
	}
	s.Ringing = nil
	s.run(now)
	return nil
}

func (s *Session) setLap(now time.Time) {
	s.Lap = timer.Duration(s.LapLen())
	s.Ringing = nil
	if s.State == Running {
		s.LapSince = ptr(now)
	}
	s.Updated = now
}

func (s *Session) move(now time.Time, delta int) error {
	if s.onBreak() {
		return errors.New("on a break; resume first")
	}
	next := s.Step + delta
	if next < 0 {
		return timer.ErrNoop{Msg: "already on the shortest lap (" + Short(s.LapLen()) + ")"}
	}
	if next >= len(s.Ladder) {
		return timer.ErrNoop{Msg: "already on the longest lap (" + Short(s.LapLen()) + ")"}
	}
	// The focus clock keeps what it read; the unfinished lap is simply not counted.
	s.Step = next
	s.setLap(now)
	return nil
}

// Up moves to the next longer lap and restarts it.
func (s *Session) Up(now time.Time) error { return s.move(now, 1) }

// Down moves to the next shorter lap and restarts it.
func (s *Session) Down(now time.Time) error { return s.move(now, -1) }

// Reset throws the current lap away: it restarts, the part already run is taken off
// the focus time, and the rung's reset counter goes up.
func (s *Session) Reset(now time.Time) error {
	if s.onBreak() {
		return errors.New("on a break; resume first")
	}
	spent := s.LapLen() - s.LapRemaining(now)
	if s.State == Running {
		s.bank(now)
		s.FocusSince = ptr(now)
		s.LapSince = ptr(now)
	}
	s.Focus -= timer.Duration(spent)
	if s.Focus < 0 {
		s.Focus = 0
	}
	s.count(s.LapLen(), Counts{Reset: 1})
	s.setLap(now)
	return nil
}

// TakeBreak pauses both clocks and starts a break of d. During a break it restarts
// the break with the new length; the time already spent still counts as break time.
func (s *Session) TakeBreak(now time.Time, d time.Duration) error {
	if d <= 0 {
		return errors.New("a break needs a length above zero")
	}
	if s.onBreak() {
		s.leaveBreak(now)
	} else {
		s.bank(now)
		s.BreakCount++
	}
	s.State, s.PausedBy = Break, ""
	s.BreakStart, s.BreakEnds = ptr(now), ptr(now.Add(d))
	s.Ringing = nil
	s.Updated = now
	return nil
}

// Silence stops the sound only; the state stays.
func (s *Session) Silence(now time.Time) bool {
	if s.Ringing == nil {
		return false
	}
	s.Ringing = nil
	s.Updated = now
	return true
}

// Summary is what the session has done up to now. It changes nothing.
func (s *Session) Summary(now time.Time) Summary {
	sum := Summary{Started: s.Started, At: now, Focus: s.FocusElapsed(now), Laps: map[string]Counts{},
		Breaks: s.BreakCount, BreakTotal: time.Duration(s.BreakTotal)}
	for k, v := range s.Laps {
		sum.Laps[k] = v
	}
	if s.BreakStart != nil {
		sum.BreakTotal += pos(now.Sub(*s.BreakStart))
	}
	return sum
}

// Advance moves the session to now: finished laps are counted (one chime however
// many), a finished break starts ringing, and a session the daemon has not seen for
// SleepGap is paused where it was last seen. changed says whether anything in the
// session differs (so a quiet tick writes nothing).
func (s *Session) Advance(now time.Time) (evs []Event, changed bool) {
	before, _ := json.Marshal(s)

	if s.Ringing != nil && now.After(s.Ringing.Until) {
		s.Ringing = nil
	}

	// The Mac slept, or the daemon was down: what nobody saw is not focus time.
	if s.State == Running && now.Sub(s.Seen) > SleepGap {
		s.bank(s.Seen)
		s.State, s.PausedBy = Paused, PausedBySleep
		evs = append(evs, Event{Kind: EvSleep, At: s.Seen})
	}

	if s.State == Running && s.LapSince != nil {
		lap := s.LapLen()
		spent := now.Sub(*s.LapSince)
		if spent >= time.Duration(s.Lap) {
			n := 1 + int((spent-time.Duration(s.Lap))/lap)
			end := s.LapSince.Add(time.Duration(s.Lap) + time.Duration(n-1)*lap)
			s.count(lap, Counts{Done: n})
			s.Lap, s.LapSince = timer.Duration(lap), ptr(end)
			s.Ringing = &timer.Ringing{Since: now, Until: now} // exactly one play
			s.Seen = now
			evs = append(evs, Event{Kind: EvLap, At: end, Laps: n, Lap: lap})
		}
	}

	if s.State == Break && s.BreakEnds != nil && !now.Before(*s.BreakEnds) {
		s.State = BreakOver
		s.Ringing = &timer.Ringing{Since: now, Until: now.Add(AckRing)}
		evs = append(evs, Event{Kind: EvBreakOver, At: *s.BreakEnds})
	}

	if s.State == Running && now.Sub(s.Seen) >= SeenEvery {
		s.Seen = now
	}

	after, _ := json.Marshal(s)
	changed = string(before) != string(after)
	if changed {
		s.Updated = now
	}
	return evs, changed
}
