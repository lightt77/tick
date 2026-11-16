package runlog

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSinceFiltersAndSkipsJunk(t *testing.T) {
	p := filepath.Join(t.TempDir(), "runs.jsonl")
	day := func(d int) time.Time { return time.Date(2026, 11, d, 9, 0, 0, 0, time.UTC) }
	if err := Append(p, Entry{TS: day(1), Tag: "old", Outcome: "done", Kind: "timer"},
		Entry{TS: day(10), Tag: "a", Outcome: "missed", Kind: "timer"}); err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("not json\n\n")
	f.Close()
	Append(p, Entry{TS: day(12), Tag: "b", Outcome: "done", Kind: "stopwatch", Amount: 12.5, Unit: "min"})

	got, err := Since(p, day(5))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Tag != "a" || got[1].Tag != "b" || got[1].Amount != 12.5 {
		t.Fatalf("got %+v", got)
	}
}

func TestSinceMissingFileIsEmpty(t *testing.T) {
	got, err := Since(filepath.Join(t.TempDir(), "none.jsonl"), time.Time{})
	if err != nil || len(got) != 0 {
		t.Fatalf("%v %v", got, err)
	}
}

func TestDetailRoundTripsAndOldLinesStillParse(t *testing.T) {
	p := filepath.Join(t.TempDir(), "runs.jsonl")
	day := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	d := &Detail{Laps: map[string]LapCount{"1m": {Done: 12}, "5m": {Done: 4, Reset: 1}}, Breaks: 2, BreakMin: 18}
	if err := Append(p, Entry{TS: day, Tag: "pasta", Outcome: "done", Kind: "timer", Amount: 10, Unit: "min"},
		Entry{TS: day, Tag: "focus", Outcome: "done", Kind: "focus", Amount: 84.5, Unit: "min", Detail: d}); err != nil {
		t.Fatal(err)
	}
	got, err := Since(p, time.Time{})
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	if got[0].Detail != nil {
		t.Fatalf("a timer line gained a detail: %+v", got[0].Detail)
	}
	if !reflect.DeepEqual(got[1].Detail, d) {
		t.Fatalf("detail %+v", got[1].Detail)
	}
	b, _ := os.ReadFile(p)
	if lines := strings.Split(strings.TrimSpace(string(b)), "\n"); strings.Contains(lines[0], "detail") {
		t.Fatalf("detail must be left out when absent: %s", lines[0])
	}
}
