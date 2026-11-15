// Package config reads the optional config.toml.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/lightt77/tick/internal/timer"
)

// Config mirrors config.toml. Every key is optional.
type Config struct {
	Sound        string  `toml:"sound"`
	Volume       float64 `toml:"volume"`
	RingInterval string  `toml:"ring_interval"`
	RingCap      string  `toml:"ring_cap"`
	DueRing      string  `toml:"due_ring"` // how long a due occurrence rings
	Grace        string  `toml:"grace"`
	SoundsDir    string  `toml:"sounds_dir"` // your own sounds, listed first
}

// Defaults used for any key the file leaves out.
var Defaults = Config{
	Sound: "Glass", Volume: 0.8, RingInterval: "5s", RingCap: "2m",
	DueRing: "20s", Grace: "2h",
}

// Load reads path (a missing file means all defaults) and returns the config and
// the timer settings derived from it.
func Load(path string) (Config, timer.Settings, error) {
	c := Defaults
	if _, err := toml.DecodeFile(path, &c); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Defaults, timer.DefaultSettings, fmt.Errorf("%s: %v", path, err)
	}
	if strings.HasPrefix(c.SoundsDir, "~/") {
		home, _ := os.UserHomeDir()
		c.SoundsDir = filepath.Join(home, c.SoundsDir[2:])
	}
	if c.Volume <= 0 || c.Volume > 1 {
		return Defaults, timer.DefaultSettings, fmt.Errorf("%s: volume must be between 0 and 1", path)
	}
	s := timer.Settings{}
	for _, f := range []struct {
		key string
		val string
		dst *time.Duration
	}{
		{"ring_interval", c.RingInterval, &s.RingInterval},
		{"ring_cap", c.RingCap, &s.RingCap},
		{"due_ring", c.DueRing, &s.DueRing},
		{"grace", c.Grace, &s.Grace},
	} {
		d, err := time.ParseDuration(f.val)
		if err != nil || d <= 0 {
			return Defaults, timer.DefaultSettings, fmt.Errorf("%s: %s=%q is not a duration like 5s or 2m", path, f.key, f.val)
		}
		*f.dst = d
	}
	return c, s, nil
}

var soundLine = regexp.MustCompile(`^\s*sound\s*=`)
var tableLine = regexp.MustCompile(`^\s*\[`)

// SetSound sets the default sound in the config file at path, keeping every other
// line and comment. The new file is validated before it replaces the old one.
func SetSound(path, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("empty sound")
	}
	enc, err := toml.Marshal(map[string]string{"sound": value})
	if err != nil {
		return err
	}
	line := strings.TrimRight(string(enc), "\n")

	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	lines := strings.Split(strings.TrimRight(string(old), "\n"), "\n")
	if len(old) == 0 {
		lines = nil
	}
	replaced := false
	for i, l := range lines {
		if tableLine.MatchString(l) {
			break // only top-level keys; tick's config has no tables
		}
		if soundLine.MatchString(l) {
			lines[i] = line
			replaced = true
			break
		}
	}
	if !replaced {
		lines = append([]string{line}, lines...)
	}
	data := []byte(strings.Join(lines, "\n") + "\n")

	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if _, _, err := Load(tmp.Name()); err != nil {
		return fmt.Errorf("not saved: %v", err)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
