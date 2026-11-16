// Package tray is the menu bar item. view.go decides what to show (pure, tested);
// tray.go draws it with systray.
package tray

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lightt77/tick/internal/focus"
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

// Title picks what the menu bar shows: something that needs you (ringing, due, or
// a finished break), else the Focus cube, else the soonest-finishing countdown,
// else a running stopwatch, else Idle.
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
	if st.Focus != nil && st.Focus.State == focus.BreakOver {
		attention = append(attention, "break over")
	}
	switch {
	case len(attention) > 0:
		sort.Strings(attention)
		title := "⏰ " + attention[0]
		if len(attention) > 1 {
			title += fmt.Sprintf(" +%d", len(attention)-1)
		}
		return title
	case st.Focus != nil:
		return focusTitle(st.Focus, now)
	case soonest != nil:
		return soonest.Name + " " + Clock(soonest.Remaining(now))
	case sw != nil:
		return sw.Name + " ↑" + Clock(sw.Elapsed(now))
	}
	return Idle
}

// focusTitle is the menu bar text while a Focus cube session is not waiting on you:
// the lap on its ladder, a pause sign when paused, or the break clock.
func focusTitle(f *focus.Session, now time.Time) string {
	lap := focus.Short(f.LapLen()) + " " + Clock(f.LapRemaining(now))
	switch f.State {
	case focus.Paused:
		return "⏸ " + lap
	case focus.Break:
		return "☕ " + Clock(f.BreakRemaining(now))
	}
	return "◉ " + lap
}

// FocusAction is one entry of the Focus cube submenu. Dur is the lap length for
// "start" and the break length for "break".
type FocusAction struct {
	Label string
	Verb  string
	Dur   time.Duration
}

// FocusRow is the Focus cube's own item at the top of the menu.
type FocusRow struct {
	Text    string
	Actions []FocusAction
}

// FocusStarts are the entries of the idle "Focus cube" submenu: one per rung.
func FocusStarts(steps []time.Duration) []FocusAction {
	var out []FocusAction
	for _, d := range steps {
		out = append(out, FocusAction{Label: "Start at " + focus.Short(d), Verb: "start", Dur: d})
	}
	return out
}

// FocusMenu is the Focus cube row for a running session (nil when there is none).
// The submenu is flat: no nested menus.
func FocusMenu(f *focus.Session, now time.Time) *FocusRow {
	if f == nil {
		return nil
	}
	focused := Clock(f.FocusElapsed(now)) + " focused"
	var acts []FocusAction
	act := func(label, verb string, d time.Duration) { acts = append(acts, FocusAction{label, verb, d}) }
	breaks := func(prefix string) {
		for _, d := range f.BreakChoices() {
			act(prefix+" "+focus.Short(d), ops.FocusBreak, d)
		}
	}
	lap := focus.Short(f.LapLen())
	row := &FocusRow{}
	switch f.State {
	case focus.Break:
		row.Text = fmt.Sprintf("Focus  ☕ break  %s left · %s", Clock(f.BreakRemaining(now)), focused)
		act("Resume focus", ops.FocusResume, 0)
		breaks("Restart break")
	case focus.BreakOver:
		row.Text = fmt.Sprintf("Focus  break over %s ago · %s", Clock(f.BreakOverBy(now)), focused)
		if f.Ringing != nil {
			act("Silence", ops.FocusSilence, 0)
		}
		act("Resume focus", ops.FocusResume, 0)
		breaks("Restart break")
	default:
		mark := "◉"
		if f.State == focus.Paused {
			mark = "⏸"
			act("Resume", ops.FocusResume, 0)
		} else {
			act("Pause", ops.FocusPause, 0)
		}
		row.Text = fmt.Sprintf("Focus  %s %s  %s left · %s", mark, lap, Clock(f.LapRemaining(now)), focused)
		if f.State == focus.Paused && f.PausedBy == focus.PausedBySleep {
			row.Text += " · Mac slept"
		}
		if r := f.Rungs(); f.Step < len(r)-1 {
			act("Longer lap ("+focus.Short(r[f.Step+1])+")", ops.FocusUp, 0)
		}
		if f.Step > 0 {
			act("Shorter lap ("+focus.Short(f.Rungs()[f.Step-1])+")", ops.FocusDown, 0)
		}
		act("Reset lap", ops.FocusReset, 0)
		breaks("Break")
	}
	act("Done", ops.FocusDone, 0)
	act("Cancel", ops.FocusCancel, 0)
	row.Actions = acts
	return row
}

// FocusSignature changes only when the Focus items' shape does, not as the clocks tick.
func FocusSignature(f *FocusRow, steps []time.Duration) string {
	var b strings.Builder
	b.WriteString("focus:")
	if f == nil {
		for _, a := range FocusStarts(steps) {
			b.WriteString("|" + a.Label)
		}
		return b.String()
	}
	for _, a := range f.Actions {
		b.WriteString("|" + a.Label)
	}
	return b.String()
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
			r.Actions = []Action{{"Restart", ops.Restart}, {"Done", ops.Done}, {"Cancel", ops.Cancel}}
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
