// Package cli is tick's command line. Every read command takes --json, and every
// change goes through internal/ops (the same code the tray uses).
package cli

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"fyne.io/systray"
	"github.com/spf13/cobra"

	"github.com/lightt77/tick/internal/alert"
	"github.com/lightt77/tick/internal/config"
	"github.com/lightt77/tick/internal/daemon"
	"github.com/lightt77/tick/internal/dashboard"
	"github.com/lightt77/tick/internal/launchd"
	"github.com/lightt77/tick/internal/ops"
	"github.com/lightt77/tick/internal/paths"
	"github.com/lightt77/tick/internal/schedule"
	"github.com/lightt77/tick/internal/store"
	"github.com/lightt77/tick/internal/timer"
	"github.com/lightt77/tick/internal/tray"
)

// TickerName is the timer behind `tick 15m start`.
const TickerName = "tick"

type flags struct {
	json, dryRun, repeat bool
	sound, grace, at     string
	label, until         string
	rules                []string
}

type app struct {
	out, errOut io.Writer
	f           flags
}

func (a *app) store() (*store.Store, error) {
	if err := paths.Ensure(); err != nil {
		return nil, err
	}
	return store.New(paths.State(), paths.StateLock()), nil
}

func (a *app) ops() (ops.Ops, error) {
	s, err := a.store()
	if err != nil {
		return ops.Ops{}, err
	}
	return ops.Ops{Store: s, RunLog: paths.RunLog()}, nil
}

// Execute runs the CLI and returns the exit code.
func Execute() int {
	a := &app{out: os.Stdout, errOut: os.Stderr}
	root := a.rootCmd()
	err := root.Execute()
	switch {
	case err == nil:
		return 0
	case timer.IsNoop(err):
		fmt.Fprintln(a.out, err)
		return 0
	default:
		fmt.Fprintln(a.errOut, "tick:", err)
		return 1
	}
}

func (a *app) rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "tick",
		Short: "Timers, stopwatches and scheduled reminders for the macOS menu bar",
		Long: `tick keeps countdowns, stopwatches, a repeating ticker and scheduled reminders.
A background daemon (tick daemon install) rings, notifies and shows them in the menu bar.

  tick timer pasta start 10m        countdown
  tick timer pasta                  live view
  tick stopwatch work start         stopwatch
  tick 15m start                    chime every 15 minutes (the timer "tick")
  tick timer guitar start 15m --rrule "FREQ=DAILY;BYHOUR=18;BYMINUTE=30" --label "Daily 18:30"
  tick timer dentist start --at "2026-11-20 09:00"

Verbs: start (also resumes), pause, restart, cancel, skip (scheduled timers).`,
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          a.runShortcut,
	}
	root.AddCommand(a.timerCmd(timer.KindTimer), a.timerCmd(timer.KindStopwatch),
		a.statusCmd(), a.silenceCmd(), a.soundsCmd(), a.daemonCmd(), a.dashboardCmd())
	return root
}

// tick 15m start | tick start | tick pause | tick restart | tick cancel
func (a *app) runShortcut(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	if d, err := time.ParseDuration(args[0]); err == nil {
		if len(args) != 2 || args[1] != ops.Start {
			return fmt.Errorf("usage: tick %s start", args[0])
		}
		a.f.repeat = true
		return a.timerVerb(timer.KindTimer, TickerName, ops.Start, &d)
	}
	if len(args) == 1 && isVerb(args[0]) {
		return a.timerVerb(timer.KindTimer, TickerName, args[0], nil)
	}
	return fmt.Errorf("unknown command %q (see tick --help)", strings.Join(args, " "))
}

func isVerb(s string) bool {
	switch s {
	case ops.Start, ops.Pause, ops.Restart, ops.Cancel, ops.Skip:
		return true
	}
	return false
}

func (a *app) timerCmd(kind timer.Kind) *cobra.Command {
	use, short := "timer <name> [start|pause|restart|cancel|skip] [duration]", "Countdowns, the ticker and scheduled reminders"
	if kind == timer.KindStopwatch {
		use, short = "stopwatch <name> [start|pause|restart|cancel]", "Stopwatches"
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Long: short + `.

  tick ` + string(kind) + ` list              all of them
  tick ` + string(kind) + ` <name>            live view (one line with --json or when piped)
  tick ` + string(kind) + ` <name> <verb>     start creates it if it does not exist`,
		Args: cobra.RangeArgs(1, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] == "list" && len(args) == 1 {
				return a.list(kind)
			}
			name := args[0]
			if err := validName(name); err != nil {
				return err
			}
			if len(args) == 1 {
				return a.show(kind, name)
			}
			verb := args[1]
			var d *time.Duration
			if len(args) == 3 {
				if verb != ops.Start || kind == timer.KindStopwatch {
					return fmt.Errorf("a duration only goes with: tick timer <name> start <duration>")
				}
				v, err := time.ParseDuration(args[2])
				if err != nil || v <= 0 {
					return fmt.Errorf("%q is not a duration like 10m, 1h30m or 90s", args[2])
				}
				d = &v
			}
			return a.timerVerb(kind, name, verb, d)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&a.f.json, "json", false, "print JSON")
	if kind == timer.KindTimer {
		f.BoolVar(&a.f.repeat, "repeat", false, "start again each time it finishes (a ticker)")
		f.StringVar(&a.f.sound, "sound", "", "sound name (tick sounds) or file path")
		f.StringVar(&a.f.grace, "grace", "", "how late a missed occurrence may still fire after sleep (default from config, 2h)")
		f.StringArrayVar(&a.f.rules, "rrule", nil, "RRULE body, e.g. FREQ=DAILY;BYHOUR=18;BYMINUTE=30 (repeatable)")
		f.StringVar(&a.f.at, "at", "", `one-time reminder, "YYYY-MM-DD HH:MM"`)
		f.StringVar(&a.f.label, "label", "", "plain description shown in lists and the menu bar")
		f.BoolVar(&a.f.dryRun, "dry-run", false, "validate and show the next 5 occurrences without creating")
		f.StringVar(&a.f.until, "until", "", `with pause: resume the schedule automatically, "YYYY-MM-DD [HH:MM]"`)
	}
	return cmd
}

func validName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return fmt.Errorf("a name cannot be empty")
	case name == "list":
		return fmt.Errorf(`"list" is a command, not a name`)
	case len(name) > 40:
		return fmt.Errorf("names are at most 40 characters")
	case strings.ContainsAny(name, "\n\t"):
		return fmt.Errorf("names cannot contain tabs or newlines")
	}
	return nil
}

// parseWhen reads a local date/time: "2006-01-02 15:04", "2006-01-02", "15:04"
// (the next such time), or RFC 3339.
func parseWhen(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("15:04", s, time.Local); err == nil {
		y, m, d := now.Date()
		t = time.Date(y, m, d, t.Hour(), t.Minute(), 0, 0, time.Local)
		if !t.After(now) {
			t = t.AddDate(0, 0, 1)
		}
		return t, nil
	}
	return time.Time{}, fmt.Errorf(`%q is not a time like "2026-11-20 09:00", "2026-11-20" or "18:30"`, s)
}

func (a *app) creationFlagsUsed() bool {
	return a.f.repeat || a.f.sound != "" || a.f.grace != "" || len(a.f.rules) > 0 || a.f.at != "" || a.f.label != "" || a.f.dryRun
}

func (a *app) timerVerb(kind timer.Kind, name, verb string, d *time.Duration) error {
	o, err := a.ops()
	if err != nil {
		return err
	}
	st, err := o.Store.Load()
	if err != nil {
		return err
	}
	now := time.Now()
	existing := st.Find(name)
	if existing == nil {
		if verb != ops.Start {
			return fmt.Errorf("no %s named %q (create it with: tick %s %s start ...)", kind, name, kind, name)
		}
		return a.create(o, kind, name, d, now)
	}
	if d != nil || a.creationFlagsUsed() {
		return fmt.Errorf("%s already exists (%s); cancel it first to create it again with new settings", name, existing.State)
	}
	var until *time.Time
	if a.f.until != "" {
		if verb != ops.Pause {
			return fmt.Errorf("--until only goes with pause")
		}
		u, err := parseWhen(a.f.until, now)
		if err != nil {
			return err
		}
		until = &u
	}
	res, err := o.Apply(name, kind, verb, until)
	if err != nil {
		return err
	}
	return a.report(res, verb)
}

func (a *app) create(o ops.Ops, kind timer.Kind, name string, d *time.Duration, now time.Time) error {
	var t *timer.Timer
	var err error
	if kind == timer.KindStopwatch {
		t = timer.NewStopwatch(name, now)
	} else {
		dur := time.Duration(0)
		if d != nil {
			dur = *d
		}
		if len(a.f.rules) > 0 || a.f.at != "" {
			if a.f.repeat {
				return fmt.Errorf("--repeat is for countdowns; a schedule already repeats")
			}
			var at *time.Time
			if a.f.at != "" {
				v, err := parseWhen(a.f.at, now)
				if err != nil {
					return err
				}
				at = &v
			}
			s, err := schedule.New(a.f.rules, at, now)
			if err != nil {
				return err
			}
			if t, err = timer.NewScheduled(name, dur, s, now); err != nil {
				return err
			}
		} else {
			if d == nil {
				return fmt.Errorf("give a duration (tick timer %s start 10m) or a schedule (--rrule / --at)", name)
			}
			if t, err = timer.NewCountdown(name, dur, a.f.repeat, now); err != nil {
				return err
			}
		}
		t.Label = a.f.label
		if a.f.sound != "" {
			loadSoundsDir()
			if _, err := alert.ResolveSound(a.f.sound); err != nil {
				return err
			}
			t.Sound = a.f.sound
		}
		if a.f.grace != "" {
			g, err := time.ParseDuration(a.f.grace)
			if err != nil || g <= 0 {
				return fmt.Errorf("--grace %q is not a duration like 2h", a.f.grace)
			}
			t.Grace = timer.Duration(g)
		}
	}
	if a.f.dryRun {
		v := viewOf(t, now, 5)
		if a.f.json {
			return printJSON(a.out, struct {
				DryRun bool `json:"dry_run"`
				View
			}{true, v})
		}
		fmt.Fprintf(a.out, "Would create %s %s: %s\n", kind, name, statusText(t, now, false))
		if t.Label != "" {
			fmt.Fprintf(a.out, "Label: %s\n", t.Label)
		}
		for _, u := range v.Upcoming {
			fmt.Fprintf(a.out, "  %s\n", u.Local().Format("Mon 2006-01-02 15:04"))
		}
		return nil
	}
	if err := o.Create(t); err != nil {
		return err
	}
	a.warnIfNoDaemon()
	if a.f.json {
		return printJSON(a.out, viewOf(t, now, 5))
	}
	fmt.Fprintf(a.out, "%s: %s\n", name, statusText(t, now, false))
	return nil
}

func (a *app) report(res ops.Result, verb string) error {
	now := time.Now()
	if a.f.json {
		if res.Deleted {
			return printJSON(a.out, map[string]any{"name": res.Name, "deleted": true})
		}
		return printJSON(a.out, viewOf(res.Timer, now, 5))
	}
	for _, ev := range res.Events {
		if ev.Kind == timer.EvSkipped {
			fmt.Fprintf(a.out, "skipped %s\n", ev.At.Local().Format("Mon 2006-01-02 15:04"))
		}
	}
	if res.Deleted {
		if verb == ops.Cancel {
			fmt.Fprintf(a.out, "%s cancelled\n", res.Name)
		} else {
			fmt.Fprintf(a.out, "%s has no more occurrences and was removed\n", res.Name)
		}
		return nil
	}
	fmt.Fprintf(a.out, "%s: %s\n", res.Timer.Name, statusText(res.Timer, now, false))
	return nil
}

func (a *app) warnIfNoDaemon() {
	if ok, _ := daemon.Running(paths.DaemonLock()); !ok {
		fmt.Fprintln(a.errOut, "note: the tick daemon is not running, so nothing will ring. Start it with: tick daemon install")
	}
}

func (a *app) list(kind timer.Kind) error {
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
			if t.Kind == kind {
				views = append(views, viewOf(t, now, 0))
			}
		}
		return printJSON(a.out, views)
	}
	printList(a.out, st, kind, now)
	return nil
}

func (a *app) show(kind timer.Kind, name string) error {
	s, err := a.store()
	if err != nil {
		return err
	}
	st, err := s.Load()
	if err != nil {
		return err
	}
	t := st.Find(name)
	if t == nil {
		return fmt.Errorf("no %s named %q", kind, name)
	}
	if t.Kind != kind {
		return fmt.Errorf("%s is a %s; use tick %s %s", name, t.Kind, t.Kind, name)
	}
	now := time.Now()
	if a.f.json {
		return printJSON(a.out, viewOf(t, now, 5))
	}
	if !isTerminal(os.Stdout) {
		fmt.Fprintf(a.out, "%s  %s\n", name, statusText(t, now, true))
		return nil
	}
	return live(s, name, a.out)
}

func (a *app) statusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Is the daemon running, what is due next, what is ringing",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.store()
			if err != nil {
				return err
			}
			st, err := s.Load()
			if err != nil {
				return err
			}
			running, pid := daemon.Running(paths.DaemonLock())
			installed, loaded := launchd.Status()
			var ringing []string
			var next *timer.Timer
			for _, t := range st.Timers {
				if t.Ringing != nil && !t.Repeat {
					ringing = append(ringing, t.Name)
				}
				if t.NextAt != nil && !t.Suspended && (next == nil || t.NextAt.Before(*next.NextAt)) {
					next = t
				}
			}
			if a.f.json {
				out := map[string]any{"daemon_running": running, "agent_installed": installed, "agent_loaded": loaded,
					"timers": len(st.Timers), "ringing": ringing}
				if running {
					out["pid"] = pid
				}
				if next != nil {
					out["next"] = map[string]any{"name": next.Name, "at": next.NextAt, "label": next.Label}
				}
				return printJSON(a.out, out)
			}
			switch {
			case running:
				fmt.Fprintf(a.out, "daemon: running (pid %d)\n", pid)
			case loaded:
				fmt.Fprintln(a.out, "daemon: not running (agent loaded; it was quit from the menu? run: tick daemon start)")
			case installed:
				fmt.Fprintln(a.out, "daemon: not running (agent installed but not loaded; run: tick daemon install)")
			default:
				fmt.Fprintln(a.out, "daemon: not running (run: tick daemon install)")
			}
			fmt.Fprintf(a.out, "timers: %d\n", len(st.Timers))
			if next != nil {
				fmt.Fprintf(a.out, "next: %s %s\n", next.Name, whenText(*next.NextAt))
			}
			if len(ringing) > 0 {
				fmt.Fprintf(a.out, "ringing: %s (tick silence)\n", strings.Join(ringing, ", "))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&a.f.json, "json", false, "print JSON")
	return cmd
}

func (a *app) silenceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "silence",
		Short: "Stop every ringing timer (they keep their state)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			o, err := a.ops()
			if err != nil {
				return err
			}
			n, err := o.SilenceAll()
			if err != nil {
				return err
			}
			fmt.Fprintf(a.out, "silenced %d\n", n)
			return nil
		},
	}
}

func (a *app) soundsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sounds",
		Short: "List sound names usable with --sound and in config.toml",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			loadSoundsDir()
			yours, others := alert.SoundGroups()
			if len(yours) > 0 {
				fmt.Fprintf(a.out, "# yours (%s)\n%s\n\n# macOS and ~/Library/Sounds\n", alert.CustomDir(), joinLines(yours))
			}
			fmt.Fprintln(a.out, joinLines(others))
			return nil
		},
	}
}

func (a *app) daemonCmd() *cobra.Command {
	var noTray bool
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run the daemon in the foreground (normally launchd does this)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runDaemon(noTray)
		},
	}
	cmd.Flags().BoolVar(&noTray, "no-tray", false, "run without the menu bar item")
	cmd.AddCommand(
		&cobra.Command{
			Use: "install", Short: "Start the daemon now and at every login (LaunchAgent)", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				exe, err := launchd.Executable()
				if err != nil {
					return err
				}
				if err := launchd.Install(exe); err != nil {
					return err
				}
				fmt.Fprintf(a.out, "installed %s\nrunning %s daemon\n", launchd.PlistPath(), exe)
				return nil
			},
		},
		&cobra.Command{
			Use: "uninstall", Short: "Stop the daemon and remove the LaunchAgent", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				if err := launchd.Uninstall(); err != nil {
					return err
				}
				fmt.Fprintln(a.out, "uninstalled; timers are kept in", paths.Data())
				return nil
			},
		},
		&cobra.Command{
			Use: "start", Short: "Start (or restart) the installed daemon, e.g. after Quit", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				if err := launchd.Kickstart(); err != nil {
					return err
				}
				fmt.Fprintln(a.out, "started")
				return nil
			},
		},
	)
	return cmd
}

func (a *app) runDaemon(noTray bool) error {
	if err := paths.Ensure(); err != nil {
		return err
	}
	lock, err := daemon.Lock(paths.DaemonLock())
	if err != nil {
		return err
	}
	defer lock.Close()
	log.SetFlags(log.LstdFlags)
	s := store.New(paths.State(), paths.StateLock())
	o := ops.Ops{Store: s, RunLog: paths.RunLog()}
	d := &daemon.Daemon{Store: s, RunLog: paths.RunLog(), ConfigPath: paths.Config(), Player: alert.FromEnv(),
		Dash: &dashboard.Writer{StatePath: paths.State(), RunLogPath: paths.RunLog(), Out: paths.DashboardData(), PID: os.Getpid()}}
	log.Printf("tick daemon started (pid %d, data %s)", os.Getpid(), paths.Data())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	if noTray {
		go func() { <-sig; cancel() }()
		d.Run(ctx)
		log.Print("tick daemon stopped")
		return nil
	}

	done := make(chan struct{})
	systray.Run(func() {
		d.Tray = tray.New(tray.Actions{
			Apply: func(name, verb string) {
				if _, err := o.Apply(name, "", verb, nil); err != nil && !timer.IsNoop(err) {
					log.Printf("menu %s %s: %v", verb, name, err)
				}
			},
			SilenceAll: func() {
				if _, err := o.SilenceAll(); err != nil {
					log.Printf("silence: %v", err)
				}
			},
			Quit:        systray.Quit,
			Sounds:      alert.SoundGroups,
			SoundsStamp: alert.Stamp,
			SetSound: func(value string) {
				setDefaultSound(d, value)
			},
			ChooseSound: func() {
				path, err := alert.ChooseFile()
				if err != nil {
					log.Printf("choose sound: %v", err)
				}
				if path != "" {
					setDefaultSound(d, path)
				}
			},
		})
		go func() { d.Run(ctx); close(done) }()
		go func() { <-sig; systray.Quit() }()
	}, func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	})
	log.Print("tick daemon stopped")
	return nil
}

// setDefaultSound saves the default sound to config.toml (the daemon picks the
// change up on its next tick) and plays it once as a preview.
func setDefaultSound(d *daemon.Daemon, value string) {
	path, err := alert.ResolveSound(value)
	if err != nil {
		log.Printf("sound: %v", err)
		return
	}
	if err := config.SetSound(paths.Config(), value); err != nil {
		log.Printf("sound: %v", err)
		return
	}
	log.Printf("default sound is now %s", value)
	c, _, _ := config.Load(paths.Config()) // just saved, so it loads
	if err := d.Player.Play(path, c.Volume); err != nil {
		log.Printf("sound preview: %v", err)
	}
}

// loadSoundsDir makes the CLI see sounds_dir like the daemon does.
func loadSoundsDir() {
	if c, _, err := config.Load(paths.Config()); err == nil {
		alert.SetCustomDir(c.SoundsDir)
	}
}
