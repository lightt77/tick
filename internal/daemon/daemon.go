// Package daemon runs the loop: every tick it advances all timers to now, logs and
// announces what happened, keeps the right sounds ringing and refreshes the tray.
package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/lightt77/tick/internal/alert"
	"github.com/lightt77/tick/internal/config"
	"github.com/lightt77/tick/internal/focus"
	"github.com/lightt77/tick/internal/runlog"
	"github.com/lightt77/tick/internal/store"
	"github.com/lightt77/tick/internal/timer"
)

// Tick is how often the loop runs.
const Tick = 250 * time.Millisecond

// Tray is the menu bar, when there is one.
type Tray interface {
	Update(st *store.State, now time.Time, sound string)
}

// Dash keeps the dashboard page's data file fresh. It is called every tick and
// decides for itself whether anything needs writing.
type Dash interface {
	Update(st *store.State, now time.Time) error
}

// Daemon holds the loop's dependencies.
type Daemon struct {
	Store      *store.Store
	RunLog     string
	ConfigPath string
	Player     alert.Player
	Tray       Tray
	Dash       Dash // optional
	Now        func() time.Time

	cfg      config.Config
	settings timer.Settings
	cfgMod   time.Time
	cfgSeen  bool
	ringer   *alert.Ringer
	sounds   map[string]string
}

// Settings returns the current alert settings (after the last config load).
func (d *Daemon) Settings() timer.Settings { return d.settings }

func (d *Daemon) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *Daemon) loadConfig() {
	info, err := os.Stat(d.ConfigPath)
	var mod time.Time
	if err == nil {
		mod = info.ModTime()
	}
	if d.cfgSeen && mod.Equal(d.cfgMod) {
		return
	}
	d.cfgSeen, d.cfgMod = true, mod
	c, s, err := config.Load(d.ConfigPath)
	if err != nil {
		log.Printf("config: %v (keeping the previous settings)", err)
		if d.ringer != nil {
			return
		}
		c, s = config.Defaults, timer.DefaultSettings
	}
	d.cfg, d.settings = c, s
	alert.SetCustomDir(c.SoundsDir)
	if c.SoundsDir != "" {
		if info, err := os.Stat(c.SoundsDir); err != nil || !info.IsDir() {
			log.Printf("config: sounds_dir %s is not a folder", c.SoundsDir)
		}
	}
	d.sounds = map[string]string{}
	if d.ringer != nil {
		d.ringer.Stop()
	}
	d.ringer = alert.NewRinger(d.Player, s.RingInterval)
	if ft, ok := d.Tray.(interface{ SetFocusSteps([]time.Duration) }); ok {
		ft.SetFocusSteps(c.Steps)
	}
}

func clone(t *timer.Timer) *timer.Timer {
	b, _ := json.Marshal(t)
	var c timer.Timer
	json.Unmarshal(b, &c)
	return &c
}

func cloneFocus(s *focus.Session) *focus.Session {
	b, _ := json.Marshal(s)
	var c focus.Session
	json.Unmarshal(b, &c)
	return &c
}

// focusRing is the ring key of the Focus cube session. It starts with a NUL byte,
// which no timer name can, so it never collides with a timer.
const focusRing = "\x00focus"

// Step runs one tick.
func (d *Daemon) Step() error {
	d.loadConfig()
	now := d.now()
	st, err := d.Store.Load()
	if err != nil {
		return err
	}
	// Look first without the write lock; most ticks change nothing.
	needWrite := false
	for _, t := range st.Timers {
		if _, changed, remove := clone(t).Advance(now, d.settings); changed || remove {
			needWrite = true
			break
		}
	}
	if st.Focus != nil {
		if _, changed := cloneFocus(st.Focus).Advance(now); changed {
			needWrite = true
		}
	}
	type batch struct {
		kind timer.Kind
		evs  []timer.Event
		t    *timer.Timer
	}
	var batches []batch
	var focusEvs []focus.Event
	if needWrite {
		err := d.Store.Update(func(s *store.State) error {
			batches = nil
			focusEvs = nil
			if s.Focus != nil {
				focusEvs, _ = s.Focus.Advance(now)
			}
			var keep []*timer.Timer
			for _, t := range s.Timers {
				evs, _, remove := t.Advance(now, d.settings)
				if len(evs) > 0 {
					batches = append(batches, batch{t.Kind, evs, clone(t)})
				}
				if !remove {
					keep = append(keep, t)
				}
			}
			s.Timers = keep
			st = s
			return nil
		})
		if err != nil {
			return err
		}
	}
	for _, b := range batches {
		if err := runlog.Events(d.RunLog, b.kind, b.evs); err != nil {
			log.Printf("run log: %v", err)
		}
		for _, ev := range b.evs {
			if title, msg, ok := Announcement(ev, b.t); ok {
				go func() {
					if err := d.Player.Notify(title, msg); err != nil {
						log.Printf("notify: %v", err)
					}
				}()
			}
		}
	}
	for _, ev := range focusEvs {
		if title, msg, ok := FocusAnnouncement(ev); ok {
			go func() {
				if err := d.Player.Notify(title, msg); err != nil {
					log.Printf("notify: %v", err)
				}
			}()
		}
	}
	d.ringer.Reconcile(d.rings(st, now))
	if d.Tray != nil {
		d.Tray.Update(st, now, d.cfg.Sound)
	}
	if d.Dash != nil {
		if err := d.Dash.Update(st, now); err != nil {
			log.Printf("dashboard: %v", err)
		}
	}
	return nil
}

// soundPath resolves a sound name once and remembers it. A name that can't be
// resolved falls back to Glass (logged); "" means nothing can be played.
func (d *Daemon) soundPath(who, name string) string {
	if name == "" {
		name = d.cfg.Sound
	}
	if path, ok := d.sounds[name]; ok {
		return path
	}
	p, err := alert.ResolveSound(name)
	if err != nil {
		log.Printf("sound for %s: %v; using Glass", who, err)
		p, _ = alert.ResolveSound("Glass")
	}
	d.sounds[name] = p
	return p
}

func (d *Daemon) rings(st *store.State, now time.Time) map[string]alert.Ring {
	want := map[string]alert.Ring{}
	for _, t := range st.Timers {
		if t.Ringing == nil {
			continue
		}
		if path := d.soundPath(t.Name, t.Sound); path != "" {
			want[t.Name] = alert.Ring{Sound: path, Volume: d.cfg.Volume, Since: t.Ringing.Since, Until: t.Ringing.Until}
		}
	}
	if f := st.Focus; f != nil && f.Ringing != nil {
		if path := d.soundPath("focus", ""); path != "" {
			want[focusRing] = alert.Ring{Sound: path, Volume: d.cfg.Volume, Since: f.Ringing.Since, Until: f.Ringing.Until}
		}
	}
	return want
}

// FocusAnnouncement is the notification for a Focus cube event, if it gets one. A
// lap chime is sound only: a banner every minute would be noise.
func FocusAnnouncement(ev focus.Event) (title, msg string, ok bool) {
	switch ev.Kind {
	case focus.EvBreakOver:
		return "☕ Break over", "Back to it from the menu bar, or: tick focus resume (tick focus break 5m for more break)", true
	case focus.EvSleep:
		return "◉ Focus paused", "No sign of tick since " + ev.At.Local().Format("15:04") + " (the Mac slept?), so the session paused there. Resume from the menu bar or: tick focus resume", true
	}
	return "", "", false
}

// Announcement is the notification for an event, if it gets one.
func Announcement(ev timer.Event, t *timer.Timer) (title, msg string, ok bool) {
	label := t.Name
	if t.Label != "" {
		label = t.Name + " · " + t.Label
	}
	switch ev.Kind {
	case timer.EvFinished:
		if ev.Chime {
			return "tick", fmt.Sprintf("%s: another %s", t.Name, Short(time.Duration(t.Duration))), true
		}
		if ev.User || t.Duration == 0 {
			return "", "", false // the user did it, or a plain reminder was marked done
		}
		return "⏱ " + t.Name + " done", Short(time.Duration(t.Duration)) + " finished", true
	case timer.EvDue:
		msg := "Due now. Mark it done from the menu bar or: tick " + quote(t.Name) + " done"
		if t.Duration > 0 {
			msg = "Due now. Start it from the menu bar or: tick " + quote(t.Name) + " start"
		}
		if ev.Late {
			msg = "Was due at " + ev.At.Local().Format("15:04") + " while the Mac slept. " + msg
		}
		return "⏰ " + label, msg, true
	}
	return "", "", false
}

// quote wraps a name that needs it so the hint can be pasted into a shell.
func quote(name string) string {
	if strings.ContainsAny(name, " \t\"'$&;|<>()*?#~`\\") {
		return "'" + strings.ReplaceAll(name, "'", `'\''`) + "'"
	}
	return name
}

// Short formats a duration compactly: 15m, 1h30m, 45s.
func Short(d time.Duration) string {
	s := d.Round(time.Second).String()
	s = strings.TrimSuffix(s, "0s")
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	if s == "" {
		return "0s"
	}
	return s
}

// Run loops until ctx is cancelled.
func (d *Daemon) Run(ctx context.Context) {
	tk := time.NewTicker(Tick)
	defer tk.Stop()
	for {
		if err := d.Step(); err != nil {
			log.Printf("tick: %v", err)
		}
		select {
		case <-ctx.Done():
			if d.ringer != nil {
				d.ringer.Stop()
			}
			return
		case <-tk.C:
		}
	}
}

// Lock takes the single-instance lock; it fails if another daemon holds it.
// The returned file must stay open for the daemon's lifetime.
func Lock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another tick daemon is already running")
	}
	f.Truncate(0)
	fmt.Fprintf(f, "%d\n", os.Getpid())
	return f, nil
}

// Running reports whether a daemon holds the lock, and its pid.
func Running(path string) (bool, int) {
	f, err := os.Open(path)
	if err != nil {
		return false, 0
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err == nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return false, 0
	}
	var pid int
	fmt.Fscan(f, &pid)
	return true, pid
}
