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

	// Focus applies a Focus cube verb ("start" with the lap length, or an ops.Focus*
	// verb with the break length for "break").
	Focus func(verb string, d time.Duration)
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

	steps     []time.Duration // the ladder offered when no session runs
	focusItem *systray.MenuItem
	focusText string
}

// SetFocusSteps tells the tray which lap lengths the idle "Focus cube" submenu offers.
// The daemon calls it when the config is (re)loaded.
func (t *Tray) SetFocusSteps(steps []time.Duration) {
	t.mu.Lock()
	t.steps = append([]time.Duration(nil), steps...)
	t.mu.Unlock()
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
	fr := FocusMenu(st.Focus, now)
	stamp := ""
	if t.acts.SoundsStamp != nil {
		stamp = t.acts.SoundsStamp()
	}
	if sig := Signature(rows, sound+"|"+stamp+"|"+FocusSignature(fr, t.steps)); sig != t.sig {
		t.rebuild(rows, fr, sound)
		t.sig = sig
		return
	}
	if fr != nil && t.focusItem != nil && t.focusText != fr.Text {
		t.focusItem.SetTitle(fr.Text)
		t.focusText = fr.Text
	}
	for _, r := range rows {
		if it := t.items[r.Name]; it != nil && t.texts[r.Name] != r.Text {
			it.SetTitle(r.Text)
			t.texts[r.Name] = r.Text
		}
	}
}

func (t *Tray) rebuild(rows []Row, fr *FocusRow, sound string) {
	if t.stop != nil {
		close(t.stop)
	}
	t.stop = make(chan struct{})
	t.items = map[string]*systray.MenuItem{}
	t.texts = map[string]string{}
	t.focusItem, t.focusText = nil, ""
	systray.ResetMenu()

	// The Focus cube comes first: a way to start one, or the running session.
	if fr == nil {
		it := systray.AddMenuItem("Focus cube", "A focus stopwatch with repeating laps")
		for _, a := range FocusStarts(t.steps) {
			verb, d := a.Verb, a.Dur
			t.onClick(it.AddSubMenuItem(a.Label, ""), func() { t.focus(verb, d) })
		}
	} else {
		it := systray.AddMenuItem(fr.Text, "")
		t.focusItem, t.focusText = it, fr.Text
		for _, a := range fr.Actions {
			verb, d := a.Verb, a.Dur
			t.onClick(it.AddSubMenuItem(a.Label, ""), func() { t.focus(verb, d) })
		}
	}
	systray.AddSeparator()

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

func (t *Tray) focus(verb string, d time.Duration) {
	if t.acts.Focus != nil {
		t.acts.Focus(verb, d)
	}
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
