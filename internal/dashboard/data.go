// Package dashboard feeds tick's static HTML page. A page opened from file:// can
// load a script but cannot fetch state.json, and it cannot evaluate RRULEs. So the
// daemon writes everything the page needs into dashboard-data.js (this file builds
// that data), and the page loads it with a <script> tag and ticks the clocks itself.
package dashboard

import (
	"sort"
	"time"

	"github.com/lightt77/tick/internal/focus"
	"github.com/lightt77/tick/internal/runlog"
	"github.com/lightt77/tick/internal/store"
	"github.com/lightt77/tick/internal/timer"
)

const (
	// AgendaDays is how far ahead the page lists occurrences.
	AgendaDays = 14
	// HistoryDays is how much of runs.jsonl the page gets.
	HistoryDays = 30

	maxTimesPerDay  = 6    // times listed per timer per day; the rest is a count
	maxOccurrences  = 2000 // per timer, a safety net for "every minute" rules
	dataVersion     = 1
	agendaDayFormat = "2006-01-02"

	SourceDaemon   = "daemon"
	SourceSnapshot = "snapshot"
)

// Data is the whole payload. Times are Unix milliseconds, which the page can use
// directly with its own clock.
type Data struct {
	Version     int          `json:"version"`
	Source      string       `json:"source"` // "daemon" keeps it fresh; "snapshot" is a one-off
	PID         int          `json:"pid,omitempty"`
	GeneratedMs int64        `json:"generated_ms"`
	AgendaDays  int          `json:"agenda_days"`
	Timers      []TimerView  `json:"timers"`
	Focus       *FocusView   `json:"focus,omitempty"` // the Focus cube session, if one is running
	Agenda      []AgendaItem `json:"agenda"`
	History     []Run        `json:"history"`
}

// TimerView is one timer as the page needs it.
type TimerView struct {
	Name       string   `json:"name"`
	Kind       string   `json:"kind"`
	State      string   `json:"state"`
	DurationMs int64    `json:"duration_ms,omitempty"`
	Repeat     bool     `json:"repeat,omitempty"`
	Laps       int      `json:"laps,omitempty"` // finished laps of a repeating timer
	Label      string   `json:"label,omitempty"`
	Describe   string   `json:"describe,omitempty"` // the rules, when there is no label
	Rules      []string `json:"rules,omitempty"`
	AtMs       *int64   `json:"at_ms,omitempty"` // a one-time reminder

	// Clock fields: exactly what the page needs to draw a ticking reading.
	EndsMs      *int64 `json:"ends_ms,omitempty"`      // running countdown reaches zero
	StartedMs   *int64 `json:"started_ms,omitempty"`   // running stopwatch: now - elapsed
	RemainingMs *int64 `json:"remaining_ms,omitempty"` // paused countdown
	ElapsedMs   *int64 `json:"elapsed_ms,omitempty"`   // paused stopwatch

	NextMs           *int64 `json:"next_ms,omitempty"`
	DueMs            *int64 `json:"due_ms,omitempty"`
	Late             bool   `json:"late,omitempty"`
	Suspended        bool   `json:"suspended,omitempty"`
	SuspendedUntilMs *int64 `json:"suspended_until_ms,omitempty"`
	Ringing          bool   `json:"ringing,omitempty"`
	CreatedMs        int64  `json:"created_ms"`
}

// FocusView is the Focus cube session as the page needs it, with the same clock
// idea as TimerView: the page draws the readings from these fixed points in time.
type FocusView struct {
	State    string `json:"state"` // running, paused, break, breakover
	PausedBy string `json:"paused_by,omitempty"`
	LapMs    int64  `json:"lap_ms"`
	Lap      string `json:"lap"` // the rung, like "5m"

	EndsMs      *int64 `json:"ends_ms,omitempty"`      // running: the lap reaches zero
	RemainingMs *int64 `json:"remaining_ms,omitempty"` // paused or on a break: what is left of the lap
	StartedMs   *int64 `json:"started_ms,omitempty"`   // running: now - focus time
	ElapsedMs   *int64 `json:"elapsed_ms,omitempty"`   // not running: focus time

	BreakEndsMs *int64 `json:"break_ends_ms,omitempty"` // on a break, or the moment it ended

	Laps        map[string]runlog.LapCount `json:"laps,omitempty"`
	Breaks      int                        `json:"breaks,omitempty"`
	BreakMs     int64                      `json:"break_ms,omitempty"` // time spent on breaks so far
	Ringing     bool                       `json:"ringing,omitempty"`
	StartedAtMs int64                      `json:"started_at_ms"`
}

// AgendaItem is one timer's occurrences on one local day.
type AgendaItem struct {
	Day   string  `json:"day"` // YYYY-MM-DD, local
	Name  string  `json:"name"`
	Times []int64 `json:"times"` // the first few, in order
	Count int     `json:"count"` // all of that day
}

// Run is one line of runs.jsonl.
type Run struct {
	Ms      int64   `json:"ms"`
	Tag     string  `json:"tag"`
	Outcome string  `json:"outcome"`
	Kind    string  `json:"kind"`
	Amount  float64 `json:"amount,omitempty"`
	Unit    string  `json:"unit,omitempty"`
	Note    string  `json:"note,omitempty"`

	Detail *runlog.Detail `json:"detail,omitempty"` // a focus session's per-lap breakdown
}

func ms(t time.Time) int64 { return t.UnixMilli() }

func msp(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	v := ms(*t)
	return &v
}

func dur(d time.Duration) *int64 { v := d.Milliseconds(); return &v }

// Build turns the state and the recent run log into the page data. It is a pure
// function of its arguments.
func Build(st *store.State, now time.Time, runs []runlog.Entry, source string, pid int) Data {
	d := Data{Version: dataVersion, Source: source, PID: pid, GeneratedMs: ms(now), AgendaDays: AgendaDays,
		Timers: []TimerView{}, Agenda: []AgendaItem{}, History: []Run{}}
	end := now.Add(AgendaDays * 24 * time.Hour)
	d.Focus = focusView(st.Focus, now)
	for _, t := range st.Timers {
		d.Timers = append(d.Timers, view(t, now))
		if t.Schedule == nil || t.Suspended {
			continue
		}
		occs := t.Schedule.Between(now, end, 0)
		if len(occs) > maxOccurrences {
			occs = occs[:maxOccurrences]
		}
		d.Agenda = append(d.Agenda, group(t.Name, occs, now.Location())...)
	}
	sort.SliceStable(d.Agenda, func(i, j int) bool {
		a, b := d.Agenda[i], d.Agenda[j]
		if a.Day != b.Day {
			return a.Day < b.Day
		}
		return a.Times[0] < b.Times[0]
	})
	for _, e := range runs {
		d.History = append(d.History, Run{Ms: ms(e.TS), Tag: e.Tag, Outcome: e.Outcome, Kind: e.Kind,
			Amount: e.Amount, Unit: e.Unit, Note: e.Note, Detail: e.Detail})
	}
	return d
}

func focusView(f *focus.Session, now time.Time) *FocusView {
	if f == nil {
		return nil
	}
	sum := f.Summary(now)
	v := &FocusView{State: string(f.State), PausedBy: f.PausedBy, LapMs: f.LapLen().Milliseconds(), Lap: focus.Short(f.LapLen()),
		Breaks: sum.Breaks, BreakMs: sum.BreakTotal.Milliseconds(), Ringing: f.Ringing != nil, StartedAtMs: ms(f.Started)}
	if len(sum.Laps) > 0 {
		v.Laps = map[string]runlog.LapCount{}
		for k, c := range sum.Laps {
			v.Laps[k] = runlog.LapCount{Done: c.Done, Reset: c.Reset}
		}
	}
	switch f.State {
	case focus.Running:
		if at, ok := f.LapEnds(); ok {
			e := ms(at)
			v.EndsMs = &e
		}
		start := ms(now.Add(-f.FocusElapsed(now)))
		v.StartedMs = &start
	case focus.Paused:
		v.RemainingMs, v.ElapsedMs = dur(f.LapRemaining(now)), dur(f.FocusElapsed(now))
	default: // break, breakover
		v.RemainingMs, v.ElapsedMs = dur(f.LapRemaining(now)), dur(f.FocusElapsed(now))
		v.BreakEndsMs = msp(f.BreakEnds)
	}
	return v
}

func view(t *timer.Timer, now time.Time) TimerView {
	v := TimerView{Name: t.Name, Kind: string(t.Kind), State: string(t.State), DurationMs: time.Duration(t.Duration).Milliseconds(),
		Repeat: t.Repeat, Laps: t.Laps, Label: t.Label, DueMs: msp(t.DueAt), Late: t.Late, Suspended: t.Suspended,
		SuspendedUntilMs: msp(t.SuspendedUntil), Ringing: t.Ringing != nil, CreatedMs: ms(t.Created)}
	if !t.Suspended {
		v.NextMs = msp(t.NextAt)
	}
	if t.Schedule != nil {
		v.Rules, v.AtMs = t.Schedule.Rules, msp(t.Schedule.At)
		v.Describe = t.Schedule.Describe()
	}
	switch {
	case t.Kind == timer.KindStopwatch && t.State == timer.Running && t.Since != nil:
		start := ms(t.Since.Add(-time.Duration(t.Banked)))
		v.StartedMs = &start
	case t.Kind == timer.KindStopwatch:
		v.ElapsedMs = dur(t.Elapsed(now))
	case t.State == timer.Running:
		if at, ok := t.EndsAt(now); ok {
			e := ms(at)
			v.EndsMs = &e
		}
	case t.State == timer.Paused:
		v.RemainingMs = dur(t.Remaining(now))
	}
	return v
}

// group splits one timer's occurrences by local day.
func group(name string, occs []time.Time, loc *time.Location) []AgendaItem {
	var out []AgendaItem
	for _, o := range occs {
		day := o.In(loc).Format(agendaDayFormat)
		if n := len(out); n == 0 || out[n-1].Day != day {
			out = append(out, AgendaItem{Day: day, Name: name})
		}
		it := &out[len(out)-1]
		it.Count++
		if len(it.Times) < maxTimesPerDay {
			it.Times = append(it.Times, ms(o))
		}
	}
	return out
}
