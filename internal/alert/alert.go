// Package alert plays sounds with afplay and posts notifications with osascript.
// The Ringer keeps one ring loop per ringing timer and is reconciled against the
// state on every daemon tick, so a CLI `cancel` stops the sound within a tick.
package alert

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Player is the side-effecting part, swappable for tests and TICK_ALERT=log.
type Player interface {
	Play(path string, volume float64) error
	Notify(title, message string) error
}

// Mac uses the tools that ship with macOS.
type Mac struct{}

// Play blocks until the sound has played once.
func (Mac) Play(path string, volume float64) error {
	return exec.Command("afplay", "-v", strconv.FormatFloat(volume, 'f', 2, 64), path).Run()
}

// Notify passes title and message as argv, so neither is ever parsed as AppleScript.
func (Mac) Notify(title, message string) error {
	return exec.Command("osascript",
		"-e", "on run argv",
		"-e", "display notification (item 2 of argv) with title (item 1 of argv)",
		"-e", "end run",
		title, message).Run()
}

// Logger writes what would have happened to the log instead (TICK_ALERT=log).
type Logger struct{}

func (Logger) Play(path string, volume float64) error {
	log.Printf("alert: play %s at %.2f", filepath.Base(path), volume)
	return nil
}

func (Logger) Notify(title, message string) error {
	log.Printf("alert: notify %q %q", title, message)
	return nil
}

// FromEnv picks the player: TICK_ALERT=log logs instead of making noise.
func FromEnv() Player {
	if os.Getenv("TICK_ALERT") == "log" {
		return Logger{}
	}
	return Mac{}
}

var soundDirs = func() []string {
	home, _ := os.UserHomeDir()
	return []string{filepath.Join(home, "Library", "Sounds"), "/System/Library/Sounds"}
}

// The sounds_dir from config.toml: your own sounds, listed and matched first.
var custom struct {
	sync.RWMutex
	dir string
}

// SetCustomDir sets (or with "" clears) the folder of your own sounds.
func SetCustomDir(dir string) {
	custom.Lock()
	custom.dir = dir
	custom.Unlock()
}

// CustomDir is the folder of your own sounds, or "".
func CustomDir() string {
	custom.RLock()
	defer custom.RUnlock()
	return custom.dir
}

// allDirs is the lookup order: your folder, ~/Library/Sounds, the macOS sounds.
func allDirs() []string {
	if d := CustomDir(); d != "" {
		return append([]string{d}, soundDirs()...)
	}
	return soundDirs()
}

// ResolveSound turns "Glass" (a system or user sound, any case) or a file path
// into a playable file path.
func ResolveSound(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("no sound given")
	}
	if IsFile(name) {
		p := name
		if strings.HasPrefix(p, "~/") {
			home, _ := os.UserHomeDir()
			p = filepath.Join(home, p[2:])
		}
		if info, err := os.Stat(p); err != nil || info.IsDir() {
			return "", fmt.Errorf("sound file %s not found", name)
		}
		return p, nil
	}
	for _, dir := range allDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			base := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
			if strings.EqualFold(base, name) {
				return filepath.Join(dir, e.Name()), nil
			}
		}
	}
	return "", fmt.Errorf("sound %q not found in sounds_dir, ~/Library/Sounds or /System/Library/Sounds (try: tick sounds)", name)
}

// SoundGroups lists sound names in two sorted groups: yours (from sounds_dir) and
// the rest (~/Library/Sounds and macOS). Each name appears once; the first folder
// in lookup order wins, as in ResolveSound.
func SoundGroups() (yours, others []string) {
	seen := map[string]bool{}
	list := func(dir string) []string {
		var out []string
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
			if key := strings.ToLower(name); !seen[key] {
				seen[key] = true
				out = append(out, name)
			}
		}
		return out
	}
	byName := func(xs []string) {
		sort.Slice(xs, func(i, j int) bool { return strings.ToLower(xs[i]) < strings.ToLower(xs[j]) })
	}
	if d := CustomDir(); d != "" {
		yours = list(d)
		byName(yours)
	}
	for _, dir := range soundDirs() {
		others = append(others, list(dir)...)
	}
	byName(others)
	return yours, others
}

// Stamp changes when a file is added to or removed from a sound folder (a
// folder's modification time moves when its entries change).
func Stamp() string {
	var b strings.Builder
	for _, dir := range allDirs() {
		if info, err := os.Stat(dir); err == nil {
			fmt.Fprintf(&b, "%s=%d;", dir, info.ModTime().UnixNano())
		}
	}
	return b.String()
}

// Sounds lists every sound name, yours first.
func Sounds() []string {
	yours, others := SoundGroups()
	return append(yours, others...)
}

// IsFile tells a sound file path from a sound name.
func IsFile(sound string) bool {
	return strings.ContainsRune(sound, '/') || strings.HasPrefix(sound, "~")
}

// ChooseFile shows the macOS file dialog for an audio file and returns its path,
// or "" when the user cancels.
func ChooseFile() (string, error) {
	out, err := exec.Command("osascript",
		"-e", `POSIX path of (choose file of type {"public.audio"} with prompt "Default sound for tick")`).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && strings.Contains(string(ee.Stderr), "-128") {
			return "", nil // cancelled
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Ring describes one ringing timer as the state has it.
type Ring struct {
	Sound  string // resolved path
	Volume float64
	Since  time.Time
	Until  time.Time
}

type loop struct {
	since  time.Time
	until  time.Time
	cancel context.CancelFunc
}

// Ringer owns the ring loops.
type Ringer struct {
	Player   Player
	Interval time.Duration

	mu    sync.Mutex
	loops map[string]*loop
}

// NewRinger returns a ringer that repeats a sound every interval.
func NewRinger(p Player, interval time.Duration) *Ringer {
	return &Ringer{Player: p, Interval: interval, loops: map[string]*loop{}}
}

// Reconcile starts loops for new rings, restarts changed ones and stops the rest.
func (r *Ringer) Reconcile(want map[string]Ring) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for name, l := range r.loops {
		w, ok := want[name]
		if !ok || !w.Since.Equal(l.since) {
			l.cancel()
			delete(r.loops, name)
		} else {
			l.until = w.Until
		}
	}
	for name, w := range want {
		if _, ok := r.loops[name]; ok {
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		l := &loop{since: w.Since, until: w.Until, cancel: cancel}
		r.loops[name] = l
		go r.run(ctx, name, l, w)
	}
}

// Stop silences everything.
func (r *Ringer) Stop() { r.Reconcile(nil) }

func (r *Ringer) run(ctx context.Context, name string, l *loop, w Ring) {
	for {
		if err := r.Player.Play(w.Sound, w.Volume); err != nil {
			log.Printf("ring %s: %v", name, err)
		}
		r.mu.Lock()
		until := l.until
		r.mu.Unlock()
		next := time.Now().Add(r.Interval)
		if ctx.Err() != nil || next.After(until.Add(r.Interval/2)) {
			r.finish(name, l)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		}
	}
}

func (r *Ringer) finish(name string, l *loop) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loops[name] == l {
		// Keep the entry so the same ring (same Since) is not replayed by the next
		// Reconcile; it is dropped once the state stops listing it.
		l.cancel()
	}
}
