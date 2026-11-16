package cli

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/lightt77/tick/internal/store"
	"github.com/lightt77/tick/internal/timer"
)

// overviewLines is `tick` (kind ""), `tick t` and `tick s`. Everything is grouped by
// what needs you first. `tick t` and `tick s` list only what is running, paused or
// waiting on you; the full list, schedules included, is plain `tick`.
func overviewLines(st *store.State, now time.Time, kind timer.Kind) []string {
	type section struct {
		title string
		rows  []*timer.Timer
	}
	sections := []*section{{title: "Needs you"}, {title: "Running"}, {title: "Paused"}, {title: "Stopwatches"}, {title: "Scheduled"}}
	by := map[string]*section{}
	for _, s := range sections {
		by[s.title] = s
	}
	for _, t := range st.Timers {
		if kind != "" && t.Kind != kind {
			continue
		}
		var key string
		switch {
		case t.State == timer.Due || t.State == timer.Done:
			key = "Needs you"
		case t.Kind == timer.KindStopwatch:
			key = "Stopwatches"
		case t.State == timer.Running:
			key = "Running"
		case t.State == timer.Paused:
			key = "Paused"
		default:
			if kind != "" {
				continue // tick t / tick s: only what is running
			}
			key = "Scheduled"
		}
		by[key].rows = append(by[key].rows, t)
	}

	width := 0
	for _, s := range sections {
		for _, t := range s.rows {
			if n := utf8.RuneCountInString(t.Name); n > width {
				width = n
			}
		}
	}
	var out []string
	for _, s := range sections {
		if len(s.rows) == 0 {
			continue
		}
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, s.title)
		for _, t := range s.rows {
			line := fmt.Sprintf("  %s%s  %s", t.Name, strings.Repeat(" ", width-utf8.RuneCountInString(t.Name)), statusText(t, now, false))
			if t.Ringing != nil && !t.Repeat {
				line += " 🔔"
			}
			label := t.Label
			if label == "" && t.Schedule != nil {
				label = t.Schedule.Describe()
			}
			if label != "" {
				line += "  · " + label
			}
			out = append(out, line)
		}
	}
	if kind == "" && st.Focus != nil {
		head := focusLines(st.Focus, now)
		if len(out) > 0 {
			head = append(head, "")
		}
		out = append(head, out...)
	}
	if len(out) == 0 {
		return []string{"Nothing here yet. Try: tick pasta 10m"}
	}
	return out
}

// overview prints the overview once, or with watch redraws it until Ctrl+C.
func (a *app) overview(kind timer.Kind) error {
	s, err := a.store()
	if err != nil {
		return err
	}
	st, err := s.Load()
	if err != nil {
		return err
	}
	now := time.Now()
	if a.f.json {
		views := []View{}
		for _, t := range st.Timers {
			if kind == "" || t.Kind == kind {
				views = append(views, viewOf(t, now, 0))
			}
		}
		return printJSON(a.out, views)
	}
	if !a.f.watch || !isTerminal(os.Stdout) {
		fmt.Fprintln(a.out, strings.Join(overviewLines(st, now, kind), "\n"))
		return nil
	}
	return watchScreen(s, a.out, func(st *store.State, now time.Time) []string {
		return overviewLines(st, now, kind)
	})
}

// watchScreen redraws the whole screen four times a second until Ctrl+C.
func watchScreen(s *store.Store, w io.Writer, render func(*store.State, time.Time) []string) error {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)

	fmt.Fprint(w, "\033[?25l\033[2J") // hide the cursor, clear once
	defer fmt.Fprint(w, "\033[?25h\n")
	tk := time.NewTicker(250 * time.Millisecond)
	defer tk.Stop()
	for {
		st, err := s.Load()
		if err != nil {
			return err
		}
		var b strings.Builder
		b.WriteString("\033[H")
		for _, l := range render(st, time.Now()) {
			b.WriteString(l + "\033[K\n")
		}
		b.WriteString("\033[K\n(Ctrl+C to leave)\033[J")
		fmt.Fprint(w, b.String())
		select {
		case <-sig:
			return nil
		case <-tk.C:
		}
	}
}
