package tray

import (
	"strings"
	"testing"
	"time"

	"github.com/lightt77/tick/internal/schedule"
	"github.com/lightt77/tick/internal/store"
	"github.com/lightt77/tick/internal/timer"
)

var t0 = time.Date(2026, 11, 16, 8, 0, 0, 0, time.Local)

func countdown(name string, d time.Duration, started time.Time) *timer.Timer {
	t, _ := timer.NewCountdown(name, d, false, started)
	return t
}

func TestClock(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0: "00:00", 754 * time.Second: "12:34", 3754 * time.Second: "1:02:34", -time.Second: "00:00",
	} {
		if got := Clock(d); got != want {
			t.Errorf("Clock(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestTitlePriority(t *testing.T) {
	st := &store.State{}
	if got := Title(st, t0); got != Idle {
		t.Fatalf("empty: %q", got)
	}
	st.Timers = append(st.Timers, timer.NewStopwatch("work", t0))
	if got := Title(st, t0.Add(65*time.Second)); got != "work ↑01:05" {
		t.Fatalf("stopwatch only: %q", got)
	}
	st.Timers = append(st.Timers, countdown("tea", 10*time.Minute, t0), countdown("pasta", 5*time.Minute, t0))
	if got := Title(st, t0.Add(time.Minute)); got != "pasta 04:00" {
		t.Fatalf("soonest-finishing: %q", got)
	}
	ringing := countdown("bread", time.Minute, t0)
	ringing.Advance(t0.Add(time.Minute), timer.DefaultSettings)
	st.Timers = append(st.Timers, ringing)
	if got := Title(st, t0.Add(time.Minute)); got != "⏰ bread" {
		t.Fatalf("ringing first: %q", got)
	}
}

func TestTickerChimeDoesNotTakeTheTitle(t *testing.T) {
	tk, _ := timer.NewCountdown("tick", 15*time.Minute, true, t0)
	tk.Advance(t0.Add(15*time.Minute), timer.DefaultSettings)
	st := &store.State{Timers: []*timer.Timer{tk}}
	if got := Title(st, t0.Add(15*time.Minute)); got != "tick 15:00" {
		t.Fatalf("ticker lap: %q", got)
	}
}

func TestRowsAndActions(t *testing.T) {
	s, _ := schedule.New([]string{"FREQ=DAILY;BYHOUR=18;BYMINUTE=30"}, nil, t0)
	g, _ := timer.NewScheduled("guitar", 15*time.Minute, s, t0)
	g.Label = "Daily 18:30"
	paused := countdown("tea", 10*time.Minute, t0)
	paused.Pause(t0.Add(time.Minute), nil)
	st := &store.State{Timers: []*timer.Timer{g, paused, countdown("pasta", 5*time.Minute, t0)}}

	rows := Rows(st, t0.Add(time.Minute))
	if len(rows) != 3 {
		t.Fatalf("%d rows", len(rows))
	}
	want := []struct{ section, text, actions string }{
		{"Running", "pasta  04:00", "Pause,Restart,Done,Cancel"},
		{"Paused", "tea  09:00 left", "Resume,Restart,Done,Cancel"},
		{"Scheduled", "guitar  today 18:30 · Daily 18:30", "Start now,Skip next,Pause schedule,Remove schedule"},
	}
	for i, w := range want {
		var labels []string
		for _, a := range rows[i].Actions {
			labels = append(labels, a.Label)
		}
		if rows[i].Section != w.section || rows[i].Text != w.text || strings.Join(labels, ",") != w.actions {
			t.Errorf("row %d: %s %q %v, want %s %q %s", i, rows[i].Section, rows[i].Text, labels, w.section, w.text, w.actions)
		}
	}

	// Due: start with the duration, and the signature changes (menu rebuild).
	before := Signature(rows, "Glass")
	g.Advance(time.Date(2026, 11, 16, 18, 30, 0, 0, time.Local), timer.DefaultSettings)
	rows = Rows(st, time.Date(2026, 11, 16, 18, 30, 0, 0, time.Local))
	if rows[0].Section != "Due" || rows[0].Actions[0].Label != "Silence" || rows[0].Actions[1].Label != "Start 15:00" {
		t.Fatalf("due row %+v", rows[0])
	}
	if Signature(rows, "Glass") == before {
		t.Fatal("signature did not change with the menu shape")
	}
}

func TestDueReminderIsMarkedDone(t *testing.T) {
	s, _ := schedule.New([]string{"FREQ=DAILY;BYHOUR=9;BYMINUTE=0"}, nil, t0)
	g, _ := timer.NewScheduled("meds", 0, s, t0)
	when := time.Date(2026, 11, 17, 9, 0, 0, 0, time.Local)
	g.Advance(when, timer.DefaultSettings)
	g.Advance(when.Add(time.Minute), timer.DefaultSettings) // the ring is over, still pending
	rows := Rows(&store.State{Timers: []*timer.Timer{g}}, when.Add(time.Minute))
	var labels []string
	for _, a := range rows[0].Actions {
		labels = append(labels, a.Label)
	}
	if rows[0].Section != "Due" || strings.Join(labels, ",") != "Done,Skip,Pause schedule,Remove schedule" {
		t.Fatalf("due reminder row %s %v", rows[0].Section, labels)
	}
}

func TestSignatureIgnoresTicking(t *testing.T) {
	st := &store.State{Timers: []*timer.Timer{countdown("pasta", 5*time.Minute, t0)}}
	a := Signature(Rows(st, t0.Add(time.Second)), "Glass")
	b := Signature(Rows(st, t0.Add(2*time.Second)), "Glass")
	if a != b {
		t.Fatal("the menu would be rebuilt every second")
	}
}

func labels(items []SoundItem) string {
	var out []string
	for _, it := range items {
		l := it.Label
		if it.Separator {
			l = "---"
		}
		if it.Checked {
			l += "*"
		}
		out = append(out, l)
	}
	return strings.Join(out, ",")
}

func TestSoundItems(t *testing.T) {
	yours, others := []string{"elevator-ding"}, []string{"Glass", "Hero"}
	if got := labels(SoundItems("hero", yours, others)); got != "elevator-ding,---,Glass,Hero*" {
		t.Fatalf("named default: %s", got)
	}
	if got := labels(SoundItems("elevator-ding", yours, others)); got != "elevator-ding*,---,Glass,Hero" {
		t.Fatalf("your sound as default: %s", got)
	}
	if got := labels(SoundItems("/Users/me/Music/bell.mp3", yours, others)); got != "Custom: bell.mp3*,---,elevator-ding,---,Glass,Hero" {
		t.Fatalf("file default: %s", got)
	}
	if got := labels(SoundItems("Glass", nil, others)); got != "Glass*,Hero" {
		t.Fatalf("no folder of your own: %s", got)
	}
	items := SoundItems("/Users/me/Music/bell.mp3", nil, others)
	if items[0].Value != "/Users/me/Music/bell.mp3" {
		t.Fatalf("custom value %q", items[0].Value)
	}
	if SoundTitle("/x/y/bell.mp3") != "Sound: bell.mp3" || SoundTitle("Glass") != "Sound: Glass" {
		t.Fatal("sound title")
	}
}

func TestSignatureFollowsSound(t *testing.T) {
	rows := Rows(&store.State{}, t0)
	if Signature(rows, "Glass") == Signature(rows, "Hero") {
		t.Fatal("changing the sound must rebuild the menu")
	}
}
