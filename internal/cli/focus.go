package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/lightt77/tick/internal/config"
	"github.com/lightt77/tick/internal/focus"
	"github.com/lightt77/tick/internal/ops"
	"github.com/lightt77/tick/internal/paths"
	"github.com/lightt77/tick/internal/tray"
)

// focusCmd is `tick focus ...`: the Focus cube session (see package focus).
func (a *app) focusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "focus",
		Short: "Focus cube: a focus stopwatch with a repeating lap on a ladder of lengths",
		Long: `Focus cube keeps one session: a stopwatch of focus time that runs the whole time,
and a lap timer (1m, 5m, 10m, 15m, 30m, 1h by default) that chimes once and starts
again each time it finishes. Move up or down the ladder, reset a lap, take a break.
The ladder and the break choices are focus_steps and focus_breaks in config.toml.

  tick focus                    where the session is
  tick focus start [5m]         begin, on the 1m lap or on the rung you name
  tick focus up | down          a longer or shorter lap (the lap restarts)
  tick focus reset              throw this lap away (its time comes off the focus time)
  tick focus pause | resume     resume also ends a break and continues the lap
  tick focus break [10m]        pause everything for a break (again: restart the break)
  tick focus done | cancel      end it; either logs one line to the run log
  tick focus silence            stop a ringing sound`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return a.focusShow() },
	}
	cmd.Flags().BoolVar(&a.f.json, "json", false, "print JSON")

	verb := func(name, short string, args cobra.PositionalArgs, run func(args []string) error) {
		c := &cobra.Command{Use: name, Short: short, Args: args,
			RunE: func(cmd *cobra.Command, args []string) error { return run(args) }}
		c.Flags().BoolVar(&a.f.json, "json", false, "print JSON")
		cmd.AddCommand(c)
	}
	verb("start [rung]", "Begin a session, on the first lap length or on a rung of the ladder (like 5m)", cobra.MaximumNArgs(1), a.focusStart)
	for _, v := range []struct{ name, short, op string }{
		{"pause", "Pause the focus time and the lap", ops.FocusPause},
		{"resume", "Continue after a pause, or end a break and continue the lap", ops.FocusResume},
		{"up", "Move to the next longer lap and restart it", ops.FocusUp},
		{"down", "Move to the next shorter lap and restart it", ops.FocusDown},
		{"reset", "Throw this lap away: restart it and take its time off the focus time", ops.FocusReset},
		{"done", "End the session and log it", ops.FocusDone},
		{"cancel", "End the session and log it as cancelled", ops.FocusCancel},
		{"silence", "Stop a ringing sound (the session stays as it is)", ops.FocusSilence},
	} {
		op := v.op
		verb(v.name, v.short, cobra.NoArgs, func([]string) error { return a.focusDo(op, 0) })
	}
	verb("break [length]", "Pause everything for a break (default: the first focus_breaks choice)", cobra.MaximumNArgs(1), func(args []string) error {
		var d time.Duration
		if len(args) == 1 {
			var ok bool
			if d, ok = parseDuration(args[0]); !ok {
				return fmt.Errorf("%q is not a break length above zero, like 5m or 1h", args[0])
			}
		}
		return a.focusDo(ops.FocusBreak, d)
	})
	return cmd
}

func (a *app) focusStart(args []string) error {
	c, _, err := config.Load(paths.Config())
	if err != nil {
		return err
	}
	rung := 0
	if len(args) == 1 {
		d, ok := parseDuration(args[0])
		if !ok {
			return fmt.Errorf("%q is not a duration above zero, like 5m", args[0])
		}
		if rung, ok = focus.FindRung(c.Steps, d); !ok {
			return fmt.Errorf("no %s lap on the ladder (%s); change focus_steps in config.toml to add one", args[0], joinShort(c.Steps))
		}
	}
	o, err := a.ops()
	if err != nil {
		return err
	}
	s, err := o.FocusStart(c.Steps, c.Breaks, rung)
	if err != nil {
		return err
	}
	a.warnIfNoDaemon()
	return a.focusPrint(s)
}

func (a *app) focusDo(verb string, d time.Duration) error {
	o, err := a.ops()
	if err != nil {
		return err
	}
	res, err := o.FocusDo(verb, d)
	if err != nil {
		return err
	}
	if res.Ended != nil {
		outcome := "done"
		if verb == ops.FocusCancel {
			outcome = "cancelled"
		}
		e := ops.FocusEntry(*res.Ended, outcome)
		if a.f.json {
			return printJSON(a.out, map[string]any{"active": false, "outcome": outcome, "focus_ms": res.Ended.Focus.Milliseconds(), "note": e.Note})
		}
		fmt.Fprintf(a.out, "focus %s · %s focused · %s\n", outcome, tray.Clock(res.Ended.Focus), e.Note)
		return nil
	}
	return a.focusPrint(res.Session)
}

func (a *app) focusShow() error {
	s, err := a.store()
	if err != nil {
		return err
	}
	st, err := s.Load()
	if err != nil {
		return err
	}
	return a.focusPrint(st.Focus)
}

func (a *app) focusPrint(s *focus.Session) error {
	now := time.Now()
	if a.f.json {
		return printJSON(a.out, focusViewOf(s, now))
	}
	fmt.Fprintln(a.out, strings.Join(focusLines(s, now), "\n"))
	return nil
}

func joinShort(ds []time.Duration) string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = focus.Short(d)
	}
	return strings.Join(out, ", ")
}

// FocusView is the stable JSON shape of the session for an LLM or a script.
type FocusView struct {
	Active           bool                    `json:"active"`
	State            string                  `json:"state,omitempty"`
	PausedBy         string                  `json:"paused_by,omitempty"`
	Lap              string                  `json:"lap,omitempty"`
	LapRemaining     string                  `json:"lap_remaining,omitempty"`
	LapRemainingMs   int64                   `json:"lap_remaining_ms,omitempty"`
	Focus            string                  `json:"focus,omitempty"`
	FocusMs          int64                   `json:"focus_ms,omitempty"`
	Ladder           []string                `json:"ladder,omitempty"`
	Laps             map[string]focus.Counts `json:"laps,omitempty"`
	Breaks           int                     `json:"breaks,omitempty"`
	BreakTotal       string                  `json:"break_total,omitempty"`
	BreakRemaining   string                  `json:"break_remaining,omitempty"`
	BreakRemainingMs int64                   `json:"break_remaining_ms,omitempty"`
	BreakOverBy      string                  `json:"break_over_by,omitempty"`
	Ringing          bool                    `json:"ringing,omitempty"`
	Started          string                  `json:"started,omitempty"`
}

func focusViewOf(s *focus.Session, now time.Time) FocusView {
	if s == nil {
		return FocusView{}
	}
	sum := s.Summary(now)
	v := FocusView{Active: true, State: string(s.State), PausedBy: s.PausedBy,
		Lap: focus.Short(s.LapLen()), Focus: tray.Clock(sum.Focus), FocusMs: sum.Focus.Milliseconds(),
		Laps: sum.Laps, Breaks: sum.Breaks, Ringing: s.Ringing != nil, Started: s.Started.Format(time.RFC3339)}
	for _, d := range s.Rungs() {
		v.Ladder = append(v.Ladder, focus.Short(d))
	}
	if sum.BreakTotal > 0 {
		v.BreakTotal = focus.Short(sum.BreakTotal)
	}
	if s.State == focus.Running || s.State == focus.Paused {
		r := s.LapRemaining(now)
		v.LapRemaining, v.LapRemainingMs = tray.Clock(r), r.Milliseconds()
	}
	if r := s.BreakRemaining(now); s.State == focus.Break {
		v.BreakRemaining, v.BreakRemainingMs = tray.Clock(r), r.Milliseconds()
	}
	if s.State == focus.BreakOver {
		v.BreakOverBy = tray.Clock(s.BreakOverBy(now))
	}
	return v
}

// focusLines is the text of `tick focus` and the line in the overview.
func focusLines(s *focus.Session, now time.Time) []string {
	if s == nil {
		return []string{"No focus session. Start one: tick focus start"}
	}
	sum := s.Summary(now)
	var head string
	switch s.State {
	case focus.Running:
		head = fmt.Sprintf("running · %s lap, %s left", focus.Short(s.LapLen()), tray.Clock(s.LapRemaining(now)))
	case focus.Paused:
		head = fmt.Sprintf("paused · %s lap, %s left", focus.Short(s.LapLen()), tray.Clock(s.LapRemaining(now)))
		if s.PausedBy == focus.PausedBySleep {
			head += " (paused because tick did not see it; the Mac slept?)"
		}
	case focus.Break:
		head = fmt.Sprintf("on a break · %s left", tray.Clock(s.BreakRemaining(now)))
	case focus.BreakOver:
		head = fmt.Sprintf("break over %s ago · resume, break again, or done", tray.Clock(s.BreakOverBy(now)))
	}
	lines := []string{fmt.Sprintf("focus: %s · %s focused", head, tray.Clock(sum.Focus))}
	if parts := lapParts(sum.Laps); len(parts) > 0 {
		lines = append(lines, "  laps: "+strings.Join(parts, ", "))
	}
	if sum.Breaks > 0 {
		lines = append(lines, fmt.Sprintf("  breaks: %d (%s)", sum.Breaks, focus.Short(sum.BreakTotal)))
	}
	return lines
}

// lapParts is "1m ×12, 5m ×4 (1 reset)", shortest lap first.
func lapParts(laps map[string]focus.Counts) []string {
	type kv struct {
		key string
		d   time.Duration
		c   focus.Counts
	}
	var rows []kv
	for k, c := range laps {
		if c.Done == 0 && c.Reset == 0 {
			continue
		}
		d, _ := time.ParseDuration(k)
		rows = append(rows, kv{k, d, c})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].d < rows[j].d })
	var out []string
	for _, r := range rows {
		p := fmt.Sprintf("%s ×%d", r.key, r.c.Done)
		if r.c.Reset > 0 {
			p += fmt.Sprintf(" (%d reset)", r.c.Reset)
		}
		out = append(out, p)
	}
	return out
}
