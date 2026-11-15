package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/lightt77/tick/internal/schedule"
	"github.com/lightt77/tick/internal/timer"
)

// `tick schedule` asks for a reminder question by question and builds the same
// thing `tick timer <name> start --rrule ...` does. It needs a person at a terminal;
// scripts and the /ticker command use the flags.

func (a *app) scheduleCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "schedule",
		Short: "Set up a reminder by answering questions",
		Long: `Set up a one-time or repeating reminder by answering questions.
It ends with a preview of the next occurrences and asks before creating anything.

For scripts and LLMs, which cannot answer questions:
  tick timer <name> start --rrule "FREQ=DAILY;BYHOUR=18;BYMINUTE=30" --label "Daily 18:30"
  tick timer <name> start --at "2026-11-20 09:00"`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !isTerminal(os.Stdin) {
				return fmt.Errorf(`tick schedule asks questions, so it needs a terminal; from a script use: tick timer <name> start --rrule "..." (or --at "YYYY-MM-DD HH:MM")`)
			}
			return a.runSchedule(os.Stdin, time.Now())
		},
	}
}

var errInput = errors.New("input ended; nothing was created")

type prompter struct {
	in  *bufio.Reader
	out io.Writer
}

func newPrompter(in io.Reader, out io.Writer) *prompter {
	return &prompter{in: bufio.NewReader(in), out: out}
}

// line asks one question. An empty answer returns def.
func (p *prompter) line(q, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(p.out, "%s [%s]: ", q, def)
	} else {
		fmt.Fprintf(p.out, "%s: ", q)
	}
	s, err := p.in.ReadString('\n')
	if err != nil && s == "" {
		return "", errInput
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return def, nil
	}
	return s, nil
}

// ask repeats a question until parse accepts the answer.
func ask[T any](p *prompter, q, def string, parse func(string) (T, error)) (T, error) {
	var zero T
	for {
		s, err := p.line(q, def)
		if err != nil {
			return zero, err
		}
		v, err := parse(s)
		if err == nil {
			return v, nil
		}
		fmt.Fprintln(p.out, "  "+err.Error())
	}
}

// choose shows a numbered menu and returns the 0-based index.
func (p *prompter) choose(q string, options []string, def int) (int, error) {
	fmt.Fprintln(p.out, q)
	for i, o := range options {
		fmt.Fprintf(p.out, "  %d) %s\n", i+1, o)
	}
	return ask(p, "Choose", strconv.Itoa(def+1), func(s string) (int, error) {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > len(options) {
			return 0, fmt.Errorf("enter a number from 1 to %d", len(options))
		}
		return n - 1, nil
	})
}

func (p *prompter) yesNo(q string, def bool) (bool, error) {
	d := "n"
	if def {
		d = "y"
	}
	return ask(p, q+" (y/n)", d, func(s string) (bool, error) {
		switch strings.ToLower(s) {
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		return false, fmt.Errorf("answer y or n")
	})
}

// runSchedule is the whole conversation. now is a parameter so tests can fix it.
func (a *app) runSchedule(in io.Reader, now time.Time) error {
	o, err := a.ops()
	if err != nil {
		return err
	}
	p := newPrompter(in, a.out)

	name, err := ask(p, "Name", "", func(s string) (string, error) {
		if err := checkName(s); err != nil {
			return "", err
		}
		st, err := o.Store.Load()
		if err != nil {
			return "", err
		}
		if ex := st.Find(s); ex != nil {
			return "", fmt.Errorf("%s already exists (%s, %s); pick another name", s, ex.Kind, ex.State)
		}
		return s, nil
	})
	if err != nil {
		return err
	}

	a.f.rules, a.f.at, a.f.label = nil, "", ""
	var label string
	kind, err := p.choose("When?", []string{"Once", "Repeating"}, 1)
	if err != nil {
		return err
	}
	if kind == 0 {
		date, err := ask(p, "Date (YYYY-MM-DD)", now.Format("2006-01-02"), func(s string) (string, error) {
			if _, err := time.ParseInLocation("2006-01-02", s, time.Local); err != nil {
				return "", fmt.Errorf("use a date like 2026-11-20")
			}
			return s, nil
		})
		if err != nil {
			return err
		}
		at, err := ask(p, "Time (HH:MM)", "", func(s string) (time.Time, error) {
			hm, err := parseClock(s)
			if err != nil {
				return time.Time{}, err
			}
			t, _ := time.ParseInLocation("2006-01-02 15:04", fmt.Sprintf("%s %02d:%02d", date, hm.h, hm.m), time.Local)
			if !t.After(now) {
				return time.Time{}, fmt.Errorf("that time has already passed")
			}
			return t, nil
		})
		if err != nil {
			return err
		}
		a.f.at = at.Format("2006-01-02 15:04")
		label = "Once " + a.f.at
	} else {
		rules, lbl, err := askRecurrence(p)
		if err != nil {
			return err
		}
		a.f.rules, label = rules, lbl
	}

	more, err := p.yesNo("More options (sound, label, grace)?", false)
	if err != nil {
		return err
	}
	a.f.sound, a.f.grace = "", ""
	if more {
		if a.f.sound, err = p.line("Sound name or file (tick sounds lists names; Enter for the default)", ""); err != nil {
			return err
		}
		if label, err = p.line("Label", label); err != nil {
			return err
		}
		if a.f.grace, err = p.line("Grace after sleep, e.g. 2h (Enter for the default)", ""); err != nil {
			return err
		}
	}
	a.f.label = label

	fmt.Fprintln(a.out)
	a.f.dryRun = true
	err = a.create(o, timer.KindTimer, name, nil, now)
	a.f.dryRun = false
	if err != nil {
		return err
	}
	ok, err := p.yesNo("Create it?", true)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(a.out, "Nothing created.")
		return nil
	}
	return a.create(o, timer.KindTimer, name, nil, now)
}

// askRecurrence asks how often and at what time, and returns rules and a label.
func askRecurrence(p *prompter) ([]string, string, error) {
	freqs := []string{
		"Every day", "Weekdays (Mon-Fri)", "Weekends (Sat, Sun)", "Certain days of the week",
		"A day of the month", "Every few hours", "Every few minutes", "Custom RRULE",
	}
	i, err := p.choose("How often?", freqs, 0)
	if err != nil {
		return nil, "", err
	}
	var r recurrence
	r.Freq = freqKinds[i]
	switch r.Freq {
	case freqWeekly:
		r.Days, err = ask(p, "Days (e.g. mon,wed,fri)", "", parseDays)
	case freqMonthly:
		r.MonthDay, err = ask(p, "Day of the month (1-31, or -1 for the last day)", "", parseMonthDay)
	case freqHours:
		if r.N, err = ask(p, "Every how many hours", "1", parsePositive); err == nil {
			r.Minute, err = ask(p, "At which minute past the hour", "0", parseMinute)
		}
	case freqMinutes:
		r.N, err = ask(p, "Every how many minutes", "20", parsePositive)
	case freqCustom:
		fmt.Fprintln(p.out, `RRULE bodies, e.g. FREQ=DAILY;BYHOUR=18;BYMINUTE=30 (an empty line ends the list)`)
		for {
			s, e := p.line("Rule", "")
			if e != nil {
				return nil, "", e
			}
			if s == "" && len(r.Custom) > 0 {
				break
			}
			if s == "" {
				continue
			}
			rule, e := schedule.Validate(s)
			if e != nil {
				fmt.Fprintln(p.out, "  "+e.Error())
				continue
			}
			r.Custom = append(r.Custom, rule)
		}
	}
	if err != nil {
		return nil, "", err
	}
	if r.Freq == freqDaily || r.Freq == freqWeekdays || r.Freq == freqWeekends || r.Freq == freqWeekly || r.Freq == freqMonthly {
		r.Times, err = ask(p, "Time(s) of day, e.g. 18:30 or 09:00,21:00", "", parseClocks)
		if err != nil {
			return nil, "", err
		}
	}
	return buildRules(r)
}

type freqKind int

const (
	freqDaily freqKind = iota
	freqWeekdays
	freqWeekends
	freqWeekly
	freqMonthly
	freqHours
	freqMinutes
	freqCustom
)

// freqKinds lines up with the menu in askRecurrence.
var freqKinds = []freqKind{freqDaily, freqWeekdays, freqWeekends, freqWeekly, freqMonthly, freqHours, freqMinutes, freqCustom}

type clock struct{ h, m int }

func (c clock) String() string { return fmt.Sprintf("%02d:%02d", c.h, c.m) }

// recurrence is what the questions collect.
type recurrence struct {
	Freq     freqKind
	Times    []clock  // daily, weekdays, weekends, weekly, monthly
	Days     []string // weekly: MO..SU
	MonthDay int      // monthly: 1..31 or -1
	N        int      // hours, minutes: the interval
	Minute   int      // hours: minute past the hour
	Custom   []string // custom: validated rules
}

var dayNames = []struct{ code, short string }{
	{"MO", "Mon"}, {"TU", "Tue"}, {"WE", "Wed"}, {"TH", "Thu"}, {"FR", "Fri"}, {"SA", "Sat"}, {"SU", "Sun"},
}

// buildRules turns the answers into RRULE bodies (one per time of day) and a short label.
func buildRules(r recurrence) (rules []string, label string, err error) {
	each := func(prefix string) {
		for _, c := range r.Times {
			rules = append(rules, fmt.Sprintf("%sBYHOUR=%d;BYMINUTE=%d", prefix, c.h, c.m))
		}
	}
	times := func() string {
		var s []string
		for _, c := range r.Times {
			s = append(s, c.String())
		}
		return strings.Join(s, ", ")
	}
	needTimes := func() error {
		if len(r.Times) == 0 {
			return fmt.Errorf("at least one time of day is needed")
		}
		return nil
	}
	switch r.Freq {
	case freqDaily:
		if err = needTimes(); err != nil {
			return
		}
		each("FREQ=DAILY;")
		label = "Daily " + times()
	case freqWeekdays:
		if err = needTimes(); err != nil {
			return
		}
		each("FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR;")
		label = "Weekdays " + times()
	case freqWeekends:
		if err = needTimes(); err != nil {
			return
		}
		each("FREQ=WEEKLY;BYDAY=SA,SU;")
		label = "Weekends " + times()
	case freqWeekly:
		if err = needTimes(); err != nil {
			return
		}
		if len(r.Days) == 0 {
			return nil, "", fmt.Errorf("at least one day is needed")
		}
		each("FREQ=WEEKLY;BYDAY=" + strings.Join(r.Days, ",") + ";")
		var names []string
		for _, code := range r.Days {
			for _, d := range dayNames {
				if d.code == code {
					names = append(names, d.short)
				}
			}
		}
		label = strings.Join(names, ", ") + " " + times()
	case freqMonthly:
		if err = needTimes(); err != nil {
			return
		}
		if r.MonthDay == 0 || r.MonthDay < -31 || r.MonthDay > 31 {
			return nil, "", fmt.Errorf("the day of the month must be 1 to 31, or -1 for the last day")
		}
		each(fmt.Sprintf("FREQ=MONTHLY;BYMONTHDAY=%d;", r.MonthDay))
		if r.MonthDay == -1 {
			label = "Last day of the month " + times()
		} else {
			label = fmt.Sprintf("Monthly on day %d %s", r.MonthDay, times())
		}
	case freqHours:
		if r.N < 1 {
			return nil, "", fmt.Errorf("the interval must be at least 1")
		}
		rules = []string{fmt.Sprintf("FREQ=HOURLY;INTERVAL=%d;BYMINUTE=%d", r.N, r.Minute)}
		label = fmt.Sprintf("Every %d h at :%02d", r.N, r.Minute)
		if r.N == 1 {
			label = fmt.Sprintf("Hourly at :%02d", r.Minute)
		}
	case freqMinutes:
		if r.N < 1 {
			return nil, "", fmt.Errorf("the interval must be at least 1")
		}
		rules = []string{fmt.Sprintf("FREQ=MINUTELY;INTERVAL=%d", r.N)}
		label = fmt.Sprintf("Every %d min", r.N)
	case freqCustom:
		if len(r.Custom) == 0 {
			return nil, "", fmt.Errorf("at least one rule is needed")
		}
		rules = r.Custom
	default:
		return nil, "", fmt.Errorf("unknown frequency")
	}
	for _, rule := range rules {
		if _, err := schedule.Validate(rule); err != nil {
			return nil, "", err
		}
	}
	return rules, label, nil
}

// parseClock reads 18:30, 9:05 (24-hour).
func parseClock(s string) (clock, error) {
	hs, ms, ok := strings.Cut(strings.TrimSpace(s), ":")
	h, e1 := strconv.Atoi(hs)
	m, e2 := strconv.Atoi(ms)
	if !ok || e1 != nil || e2 != nil || len(ms) != 2 || h < 0 || h > 23 || m < 0 || m > 59 {
		return clock{}, fmt.Errorf("use a 24-hour time like 18:30 or 09:00")
	}
	return clock{h, m}, nil
}

// parseClocks reads one or more times separated by commas or spaces, without repeats.
func parseClocks(s string) ([]clock, error) {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' })
	if len(fields) == 0 {
		return nil, fmt.Errorf("enter a time like 18:30")
	}
	var out []clock
	seen := map[clock]bool{}
	for _, f := range fields {
		c, err := parseClock(f)
		if err != nil {
			return nil, err
		}
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out, nil
}

// parseDays reads mon,wed,fri (or full names) into MO,WE,FR in week order.
func parseDays(s string) ([]string, error) {
	want := map[string]bool{}
	for _, f := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return r == ',' || r == ' ' }) {
		found := false
		for _, d := range dayNames {
			if f == strings.ToLower(d.short) || (len(f) > 3 && strings.HasPrefix(dayFull(d.code), f)) {
				want[d.code] = true
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("%q is not a day; use mon,tue,wed,thu,fri,sat,sun", f)
		}
	}
	var out []string
	for _, d := range dayNames {
		if want[d.code] {
			out = append(out, d.code)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("enter at least one day, like mon,wed,fri")
	}
	return out, nil
}

func dayFull(code string) string {
	return map[string]string{"MO": "monday", "TU": "tuesday", "WE": "wednesday", "TH": "thursday",
		"FR": "friday", "SA": "saturday", "SU": "sunday"}[code]
}

func parseMonthDay(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n == 0 || n < -31 || n > 31 {
		return 0, fmt.Errorf("enter 1 to 31, or -1 for the last day")
	}
	return n, nil
}

func parsePositive(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 1000 {
		return 0, fmt.Errorf("enter a whole number from 1 to 1000")
	}
	return n, nil
}

func parseMinute(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > 59 {
		return 0, fmt.Errorf("enter a minute from 0 to 59")
	}
	return n, nil
}
