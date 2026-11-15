package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if content != "" {
		os.WriteFile(p, []byte(content), 0o600)
	}
	return p
}

func TestLoadDefaultsWhenMissing(t *testing.T) {
	c, s, err := Load(filepath.Join(t.TempDir(), "none.toml"))
	if err != nil || c.Sound != "Glass" || s.RingCap != 2*time.Minute || s.DueRing != 20*time.Second {
		t.Fatalf("defaults: %+v %+v %v", c, s, err)
	}
}

func TestLoadRejectsBadValues(t *testing.T) {
	for _, bad := range []string{`volume = 2`, `due_ring = "soon"`, `ring_cap = "soon"`, `grace = "-1h"`, `not toml`} {
		if _, _, err := Load(write(t, bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestSetSoundReplacesAndKeepsTheRest(t *testing.T) {
	p := write(t, "# my settings\nvolume = 0.5\nsound = \"Glass\"   # old\ndue_ring = \"30s\"\n")
	if err := SetSound(p, "Hero"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	want := "# my settings\nvolume = 0.5\nsound = \"Hero\"\ndue_ring = \"30s\"\n"
	if string(b) != want {
		t.Fatalf("file:\n%s\nwant:\n%s", b, want)
	}
	c, s, err := Load(p)
	if err != nil || c.Sound != "Hero" || c.Volume != 0.5 || s.DueRing != 30*time.Second {
		t.Fatalf("after: %+v %v", c, err)
	}
	if info, _ := os.Stat(p); info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", info.Mode().Perm())
	}
}

func TestSetSoundAddsWhenMissing(t *testing.T) {
	p := write(t, "volume = 0.4\n")
	if err := SetSound(p, "Ping"); err != nil {
		t.Fatal(err)
	}
	c, _, err := Load(p)
	if err != nil || c.Sound != "Ping" || c.Volume != 0.4 {
		t.Fatalf("after: %+v %v", c, err)
	}
}

func TestSetSoundCreatesTheFile(t *testing.T) {
	p := write(t, "")
	if err := SetSound(p, "Pop"); err != nil {
		t.Fatal(err)
	}
	if c, _, err := Load(p); err != nil || c.Sound != "Pop" {
		t.Fatalf("after: %+v %v", c, err)
	}
}

func TestSetSoundEscapesPaths(t *testing.T) {
	p := write(t, "")
	path := `/Users/me/My "Best" Sounds\bell.mp3`
	if err := SetSound(p, path); err != nil {
		t.Fatal(err)
	}
	if c, _, err := Load(p); err != nil || c.Sound != path {
		t.Fatalf("round trip: %q %v", c.Sound, err)
	}
}

func TestSetSoundLeavesABrokenFileAlone(t *testing.T) {
	p := write(t, "volume = 7\n") // invalid already: the result would be too
	if err := SetSound(p, "Hero"); err == nil {
		t.Fatal("saved a config that does not load")
	}
	if b, _ := os.ReadFile(p); string(b) != "volume = 7\n" {
		t.Fatalf("file changed to %q", b)
	}
	if err := SetSound(p, " "); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty sound: %v", err)
	}
}

func TestSoundsDirExpandsHome(t *testing.T) {
	c, _, err := Load(write(t, "sounds_dir = \"~/tick/sounds\"\n"))
	home, _ := os.UserHomeDir()
	if err != nil || c.SoundsDir != filepath.Join(home, "tick", "sounds") {
		t.Fatalf("sounds_dir %q %v", c.SoundsDir, err)
	}
}

func TestOldNagKeysAreIgnored(t *testing.T) {
	if _, _, err := Load(write(t, "nag_interval = \"10m\"\nnag_count = 3\n")); err != nil {
		t.Fatalf("an old config must still load: %v", err)
	}
}
