package tray

import (
	"sync"
	"time"

	"fyne.io/systray"

	"github.com/lightt77/tick/internal/store"
)

// Actions are what menu clicks do.
type Actions struct {
	Apply       func(name, verb string)
	SilenceAll  func()
	Quit        func()
	SetSound    func(value string) // a sound name or file path for config.toml
	ChooseSound func()             // ask for a file, then set it
	Sounds      func() (yours, others []string)
	SoundsStamp func() string // changes when the sound folders do
}

// Tray draws the menu bar item. Update may be called from any goroutine; systray
// hands the drawing to the main thread.
type Tray struct {
	acts Actions

	mu    sync.Mutex
	title string
	sig   string
	items map[string]*systray.MenuItem
	texts map[string]string
	stop  chan struct{}
}

// New returns a tray; call it from systray's onReady.
func New(acts Actions) *Tray {
	accessory() // no Dock icon, no app switcher entry
	systray.SetTitle(Idle)
	systray.SetTooltip("tick")
	return &Tray{acts: acts}
}

// Update redraws the title every call and the menu when its shape (or the default
// sound) changed.
func (t *Tray) Update(st *store.State, now time.Time, sound string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if title := Title(st, now); title != t.title {
		systray.SetTitle(title)
		t.title = title
	}
	rows := Rows(st, now)
	stamp := ""
	if t.acts.SoundsStamp != nil {
		stamp = t.acts.SoundsStamp()
	}
	if sig := Signature(rows, sound+"|"+stamp); sig != t.sig {
		t.rebuild(rows, sound)
		t.sig = sig
		return
	}
	for _, r := range rows {
		if it := t.items[r.Name]; it != nil && t.texts[r.Name] != r.Text {
			it.SetTitle(r.Text)
			t.texts[r.Name] = r.Text
		}
	}
}

func (t *Tray) rebuild(rows []Row, sound string) {
	if t.stop != nil {
		close(t.stop)
	}
	t.stop = make(chan struct{})
	t.items = map[string]*systray.MenuItem{}
	t.texts = map[string]string{}
	systray.ResetMenu()

	section := ""
	for _, r := range rows {
		if r.Section != section {
			if section != "" {
				systray.AddSeparator()
			}
			h := systray.AddMenuItem(r.Section, "")
			h.Disable()
			section = r.Section
		}
		it := systray.AddMenuItem(r.Text, "")
		t.items[r.Name], t.texts[r.Name] = it, r.Text
		for _, a := range r.Actions {
			sub := it.AddSubMenuItem(a.Label, "")
			name, verb := r.Name, a.Verb
			t.onClick(sub, func() { t.acts.Apply(name, verb) })
		}
	}
	if len(rows) == 0 {
		systray.AddMenuItem("No timers", "").Disable()
	}
	systray.AddSeparator()
	sm := systray.AddMenuItem(SoundTitle(sound), "Default sound for alerts")
	var yours, others []string
	if t.acts.Sounds != nil {
		yours, others = t.acts.Sounds()
	}
	for _, s := range SoundItems(sound, yours, others) {
		if s.Separator {
			sm.AddSeparator()
			continue
		}
		value := s.Value
		t.onClick(sm.AddSubMenuItemCheckbox(s.Label, "", s.Checked), func() { t.acts.SetSound(value) })
	}
	sm.AddSeparator()
	t.onClick(sm.AddSubMenuItem("Choose file…", "Use your own sound file"), t.acts.ChooseSound)
	t.onClick(systray.AddMenuItem("Silence all", ""), t.acts.SilenceAll)
	t.onClick(systray.AddMenuItem("Quit tick", "Stops the daemon until next login"), t.acts.Quit)
}

func (t *Tray) onClick(it *systray.MenuItem, fn func()) {
	stop := t.stop
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-it.ClickedCh:
				go fn()
			}
		}
	}()
}
