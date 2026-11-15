package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/lightt77/tick/internal/daemon"
	"github.com/lightt77/tick/internal/store"
	"github.com/lightt77/tick/internal/timer"
	"github.com/lightt77/tick/internal/tray"
)

// View is the JSON shape of a timer for --json (stable, for the LLM).
type View struct {
	Name           string      `json:"name"`
	Kind           timer.Kind  `json:"kind"`
	State          timer.State `json:"state"`
	Duration       string      `json:"duration,omitempty"`
	Remaining      string      `json:"remaining,omitempty"`
	RemainingMs    *int64      `json:"remaining_ms,omitempty"`
	Elapsed        string      `json:"elapsed,omitempty"`
	ElapsedMs      *int64      `json:"elapsed_ms,omitempty"`
	Repeat         bool        `json:"repeat,omitempty"`
	Label          string      `json:"label,omitempty"`
	Sound          string      `json:"sound,omitempty"`
	Rules          []string    `json:"rules,omitempty"`
	At             *time.Time  `json:"at,omitempty"`
	Next           *time.Time  `json:"next,omitempty"`
	Upcoming       []time.Time `json:"upcoming,omitempty"`
	DueAt          *time.Time  `json:"due_at,omitempty"`
	Late           bool        `json:"late,omitempty"`
	Suspended      bool        `json:"paused_schedule,omitempty"`
	SuspendedUntil *time.Time  `json:"paused_until,omitempty"`
	Ringing        bool        `json:"ringing,omitempty"`
	Grace          string      `json:"grace,omitempty"`
}

func ms(d time.Duration) *int64 { v := d.Milliseconds(); return &v }

func viewOf(t *timer.Timer, now time.Time, upcoming int) View {
	v := View{Name: t.Name, Kind: t.Kind, State: t.State, Repeat: t.Repeat, Label: t.Label, Sound: t.Sound,
		DueAt: t.DueAt, Late: t.Late, Suspended: t.Suspended, SuspendedUntil: t.SuspendedUntil,
		Ringing: t.Ringing != nil, Next: t.NextAt}
	if t.Duration > 0 {
		v.Duration = daemon.Short(time.Duration(t.Duration))
	}
	if t.Grace > 0 {
		v.Grace = daemon.Short(time.Duration(t.Grace))
	}
	switch {
	case t.Kind == timer.KindStopwatch:
		e := t.Elapsed(now)
		v.Elapsed, v.ElapsedMs = daemon.Short(e.Truncate(time.Second)), ms(e)
	case t.State == timer.Running || t.State == timer.Paused:
		r := t.Remaining(now)
		v.Remaining, v.RemainingMs = daemon.Short(r.Round(time.Second)), ms(r)
	}
	if t.Schedule != nil {
		v.Rules, v.At = t.Schedule.Rules, t.Schedule.At
		if upcoming > 0 && !t.Suspended {
			v.Upcoming = t.Schedule.Upcoming(now, upcoming)
		}
	}
	return v
}

func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// clockMs formats with milliseconds for the live view: 04:59.123, 1:02:03.456.
func clockMs(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return fmt.Sprintf("%s.%03d", tray.Clock(d), d.Milliseconds()%1000)
}

func whenText(t time.Time) string {
	t = t.Local()
	now := time.Now()
	if y, m, d := now.Date(); t.Year() == y && t.Month() == m && t.Day() == d {
		return "today " + t.Format("15:04")
	}
	return t.Format("Mon Jan 2 15:04")
}

// statusText is one human line describing where a timer is.
func statusText(t *timer.Timer, now time.Time, withMs bool) string {
	clock := tray.Clock
	if withMs {
		clock = clockMs
	}
	switch {
	case t.Kind == timer.KindStopwatch && t.State == timer.Running:
		return "↑" + clock(t.Elapsed(now))
	case t.Kind == timer.KindStopwatch:
		return "↑" + clock(t.Elapsed(now)) + " paused"
	case t.State == timer.Running:
		s := clock(t.Remaining(now)) + " left"
		if t.Repeat {
			s += ", repeats every " + daemon.Short(time.Duration(t.Duration))
		}
		return s
	case t.State == timer.Paused:
		return clock(t.Remaining(now)) + " left, paused"
	case t.State == timer.Done:
		return "finished"
	case t.State == timer.Due:
		s := "due since " + t.DueAt.Local().Format("15:04")
		if t.Late {
			s += " (fired late, after sleep)"
		}
		return s
	case t.Suspended && t.SuspendedUntil != nil:
		return "schedule paused until " + whenText(*t.SuspendedUntil)
	case t.Suspended:
		return "schedule paused"
	case t.NextAt != nil:
		s := "next " + whenText(*t.NextAt)
		if withMs {
			s += " (in " + tray.Clock(t.NextAt.Sub(now)) + ")"
		}
		return s
	}
	return "no more occurrences"
}

func printList(w io.Writer, st *store.State, kind timer.Kind, now time.Time) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tKIND\tSTATE\tSTATUS\tLABEL")
	n := 0
	for _, t := range st.Timers {
		if kind != "" && t.Kind != kind {
			continue
		}
		n++
		state := string(t.State)
		if t.Ringing != nil && !t.Repeat {
			state += " 🔔"
		}
		label := t.Label
		if label == "" && t.Schedule != nil {
			label = t.Schedule.Describe()
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", t.Name, t.Kind, state, statusText(t, now, false), label)
	}
	if n == 0 {
		fmt.Fprintln(w, "Nothing here yet.")
		return
	}
	tw.Flush()
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// live redraws one line until Ctrl+C, or until the countdown finishes or the
// timer is deleted. State is re-read every 250 ms; the clock is drawn every 33 ms.
func live(s *store.Store, name string, w io.Writer) error {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)

	st, err := s.Load()
	if err != nil {
		return err
	}
	t := st.Find(name)
	if t == nil {
		return fmt.Errorf("no timer or stopwatch named %q", name)
	}
	fmt.Fprint(w, "\033[?25l") // hide the cursor
	defer fmt.Fprint(w, "\033[?25h\n")
	draw := time.NewTicker(33 * time.Millisecond)
	defer draw.Stop()
	lastLoad := time.Now()
	for {
		now := time.Now()
		if now.Sub(lastLoad) >= 250*time.Millisecond {
			if st, err = s.Load(); err != nil {
				return err
			}
			lastLoad = now
			if t = st.Find(name); t == nil {
				fmt.Fprintf(w, "\r\033[K%s  cancelled", name)
				return nil
			}
		}
		line := fmt.Sprintf("%s  %s", t.Name, statusText(t, now, true))
		if t.Label != "" {
			line += "  · " + t.Label
		}
		fmt.Fprintf(w, "\r\033[K%s", line)
		if t.Kind == timer.KindTimer && t.State == timer.Running && !t.Repeat && t.Remaining(now) == 0 {
			fmt.Fprintf(w, "\r\033[K%s  finished", t.Name)
			return nil
		}
		select {
		case <-sig:
			return nil
		case <-draw.C:
		}
	}
}

func joinLines(lines []string) string { return strings.Join(lines, "\n") }
