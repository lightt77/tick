// Package tray is the menu bar item. view.go decides what to show (pure, tested);
// tray.go draws it with systray.
package tray

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lightt77/tick/internal/ops"
	"github.com/lightt77/tick/internal/store"
	"github.com/lightt77/tick/internal/timer"
)

// Idle is the title when nothing is running.
const Idle = "◷"

// Clock formats 754s as 12:34 and 3754s as 1:02:34.
func Clock(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d.Seconds())
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

// Title picks what the menu bar shows: something that needs you (ringing or due),
// else the soonest-finishing countdown, else a running stopwatch, else Idle.
func Title(st *store.State, now time.Time) string {
	var attention []string
	var soonest *timer.Timer
	var soonestEnd time.Time
	var sw *timer.Timer
	for _, t := range st.Timers {
		if t.State == timer.Due || (t.Ringing != nil && !t.Repeat) {
			attention = append(attention, t.Name)
		}
		if end, ok := t.EndsAt(now); ok && (soonest == nil || end.Before(soonestEnd)) {
			soonest, soonestEnd = t, end
		}
		if t.Kind == timer.KindStopwatch && t.State == timer.Running && sw == nil {
			sw = t
		}
	}
	switch {
	case len(attention) > 0:
		sort.Strings(attention)
		title := "⏰ " + attention[0]
		if len(attention) > 1 {
			title += fmt.Sprintf(" +%d", len(attention)-1)
		}
		return title
	case soonest != nil:
		return soonest.Name + " " + Clock(soonest.Remaining(now))
	case sw != nil:
		return sw.Name + " ↑" + Clock(sw.Elapsed(now))
	}
	return Idle
}

// Action is one entry of a timer's submenu.
type Action struct {
	Label string
	Verb  string
}

// Row is one timer in the menu.
type Row struct {
	Section string
	Name    string
	Text    string
	Actions []Action
}

var sectionOrder = []string{"Due", "Running", "Paused", "Done", "Stopwatches", "Scheduled"}

func when(t time.Time, now time.Time) string {
	t = t.Local()
	y1, m1, d1 := now.Local().Date()
	y2, m2, d2 := t.Date()
	switch {
	case y1 == y2 && m1 == m2 && d1 == d2:
		return "today " + t.Format("15:04")
	case t.Sub(now) < 6*24*time.Hour:
		return t.Format("Mon 15:04")
	}
	return t.Format("Jan 2 15:04")
}

// Rows lists every timer with the verbs valid in its state.
func Rows(st *store.State, now time.Time) []Row {
	var rows []Row
	for _, t := range st.Timers {
		r := Row{Name: t.Name}
		label := ""
		if t.Label != "" {
			label = " · " + t.Label
		}
		sched := t.Schedule != nil
		switch {
		case t.Kind == timer.KindStopwatch:
			r.Section = "Stopwatches"
			r.Text = fmt.Sprintf("%s  ↑%s", t.Name, Clock(t.Elapsed(now)))
			if t.State == timer.Running {
				r.Actions = []Action{{"Pause", ops.Pause}, {"Restart", ops.Restart}, {"Done", ops.Done}, {"Cancel", ops.Cancel}}
			} else {
				r.Text += "  (paused)"
				r.Actions = []Action{{"Resume", ops.Start}, {"Restart", ops.Restart}, {"Done", ops.Done}, {"Cancel", ops.Cancel}}
			}
		case t.State == timer.Due:
			r.Section = "Due"
			r.Text = fmt.Sprintf("%s  due %s%s", t.Name, t.DueAt.Local().Format("15:04"), label)
			start, verb := "Done", ops.Done
			if t.Duration > 0 {
				start, verb = "Start "+Clock(time.Duration(t.Duration)), ops.Start
			}
			r.Actions = []Action{{start, verb}, {"Skip", ops.Skip}, {"Pause schedule", ops.Pause}, {"Remove schedule", ops.Rm}}
		case t.State == timer.Running:
			r.Section = "Running"
			r.Text = fmt.Sprintf("%s  %s", t.Name, Clock(t.Remaining(now)))
			if t.Repeat {
				r.Text += "  ↻"
			}
			r.Actions = []Action{{"Pause", ops.Pause}, {"Restart", ops.Restart}, {"Done", ops.Done}, {"Cancel", ops.Cancel}}
		case t.State == timer.Paused:
			r.Section = "Paused"
			r.Text = fmt.Sprintf("%s  %s left", t.Name, Clock(t.Remaining(now)))
			r.Actions = []Action{{"Resume", ops.Start}, {"Restart", ops.Restart}, {"Done", ops.Done}, {"Cancel", ops.Cancel}}
		case t.State == timer.Done:
			r.Section = "Done"
			r.Text = t.Name + "  finished"
			r.Actions = []Action{{"Restart", ops.Restart}, {"Clear", ops.Done}}
		case t.State == timer.Scheduled:
			r.Section = "Scheduled"
			switch {
			case t.Suspended && t.SuspendedUntil != nil:
				r.Text = fmt.Sprintf("%s  paused until %s%s", t.Name, when(*t.SuspendedUntil, now), label)
				r.Actions = []Action{{"Resume schedule", ops.Start}}
			case t.Suspended:
				r.Text = fmt.Sprintf("%s  paused%s", t.Name, label)
				r.Actions = []Action{{"Resume schedule", ops.Start}}
			case t.NextAt != nil:
				r.Text = fmt.Sprintf("%s  %s%s", t.Name, when(*t.NextAt, now), label)
				r.Actions = []Action{{"Skip next", ops.Skip}, {"Pause schedule", ops.Pause}}
				if t.Duration > 0 {
					r.Actions = append([]Action{{"Start now", ops.Start}}, r.Actions...)
				}
			default:
				r.Text = t.Name + "  (no more occurrences)"
			}
			r.Actions = append(r.Actions, Action{"Remove schedule", ops.Rm})
		}
		if sched && t.State == timer.Running {
			r.Actions = append(r.Actions[:len(r.Actions)-1], Action{"Skip next", ops.Skip}, Action{"Cancel", ops.Cancel})
		}
		if t.Ringing != nil && !t.Repeat {
			r.Actions = append([]Action{{"Silence", ops.Silence}}, r.Actions...)
		}
		rows = append(rows, r)
	}
	rank := map[string]int{}
	for i, s := range sectionOrder {
		rank[s] = i
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Section != rows[j].Section {
			return rank[rows[i].Section] < rank[rows[j].Section]
		}
		return rows[i].Name < rows[j].Name
	})
	return rows
}

// SoundItem is one entry of the Sound submenu; Separator items draw a line.
type SoundItem struct {
	Label     string
	Value     string // what goes into config.toml
	Checked   bool
	Separator bool
}

// SoundItems lists your sounds first, a separator, then the rest, with the current
// default checked. A default that is a file path comes first as "Custom: <file>".
func SoundItems(current string, yours, others []string) []SoundItem {
	var out []SoundItem
	if isPath(current) {
		out = append(out, SoundItem{Label: "Custom: " + filepath.Base(current), Value: current, Checked: true})
	}
	add := func(names []string) {
		if len(names) > 0 && len(out) > 0 {
			out = append(out, SoundItem{Separator: true})
		}
		for _, name := range names {
			out = append(out, SoundItem{Label: name, Value: name, Checked: strings.EqualFold(name, current)})
		}
	}
	add(yours)
	add(others)
	return out
}

// SoundTitle is the Sound submenu's own label.
func SoundTitle(current string) string {
	if isPath(current) {
		return "Sound: " + filepath.Base(current)
	}
	return "Sound: " + current
}

func isPath(s string) bool { return strings.ContainsRune(s, '/') || strings.HasPrefix(s, "~") }

// Signature changes only when the menu's structure does (not every second), so
// the menu is rebuilt rarely and its texts are updated in place. The default
// sound is part of it, so the checkmark moves when the sound changes.
func Signature(rows []Row, sound string) string {
	var b strings.Builder
	b.WriteString("sound=" + sound + "\n")
	for _, r := range rows {
		b.WriteString(r.Section + "|" + r.Name)
		for _, a := range r.Actions {
			b.WriteString("|" + a.Label)
		}
		b.WriteString("\n")
	}
	return b.String()
}
