package runlog

import (
	"os"
	"path/filepath"
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
