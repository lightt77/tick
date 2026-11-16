package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/lightt77/tick/internal/ops"
	"github.com/lightt77/tick/internal/timer"
)

func TestParse(t *testing.T) {
	m := time.Minute
	cases := []struct {
		args []string
		want Action
	}{
		{nil, Action{Kind: actOverview}},
		{[]string{"t"}, Action{Kind: actList, Of: timer.KindTimer}},
		{[]string{"s"}, Action{Kind: actList, Of: timer.KindStopwatch}},
		{[]string{"pasta"}, Action{Kind: actShow, Name: "pasta"}},
		{[]string{"deep work"}, Action{Kind: actShow, Name: "deep work"}},
		{[]string{"t", "pasta"}, Action{Kind: actShow, Of: timer.KindTimer, Name: "pasta"}},
		{[]string{"pasta", "10m"}, Action{Kind: actCreateTimer, Of: timer.KindTimer, Name: "pasta", Duration: 10 * m}},
		{[]string{"pasta", "1h30m"}, Action{Kind: actCreateTimer, Of: timer.KindTimer, Name: "pasta", Duration: 90 * m}},
		{[]string{"t", "pasta", "10m"}, Action{Kind: actCreateTimer, Of: timer.KindTimer, Name: "pasta", Duration: 10 * m}},
		{[]string{"10m"}, Action{Kind: actCreateTimer, Of: timer.KindTimer, Duration: 10 * m}},
		{[]string{"s", "work"}, Action{Kind: actCreateStopwatch, Of: timer.KindStopwatch, Name: "work"}},
		{[]string{"pasta", "pause"}, Action{Kind: actVerb, Name: "pasta", Verb: ops.Pause}},
		{[]string{"pasta", "resume"}, Action{Kind: actVerb, Name: "pasta", Verb: ops.Start}},
		{[]string{"pasta", "start"}, Action{Kind: actVerb, Name: "pasta", Verb: ops.Start}},
		{[]string{"pasta", "restart"}, Action{Kind: actVerb, Name: "pasta", Verb: ops.Restart}},
		{[]string{"guitar", "done"}, Action{Kind: actVerb, Name: "guitar", Verb: ops.Done}},
		{[]string{"guitar", "cancel"}, Action{Kind: actVerb, Name: "guitar", Verb: ops.Cancel}},
		{[]string{"guitar", "rm"}, Action{Kind: actVerb, Name: "guitar", Verb: ops.Rm}},
		{[]string{"guitar", "skip"}, Action{Kind: actVerb, Name: "guitar", Verb: ops.Skip}},
		{[]string{"s", "work", "done"}, Action{Kind: actVerb, Of: timer.KindStopwatch, Name: "work", Verb: ops.Done}},
		{[]string{"t", "pasta", "pause"}, Action{Kind: actVerb, Of: timer.KindTimer, Name: "pasta", Verb: ops.Pause}},
		{[]string{"10m", "pause"}, Action{Kind: actVerb, Name: "10m", Verb: ops.Pause}},
		// the old forms
		{[]string{"15m", "start"}, Action{Kind: actTicker, Duration: 15 * m, Verb: ops.Start}},
		{[]string{"pause"}, Action{Kind: actTickerVerb, Verb: ops.Pause}},
		{[]string{"cancel"}, Action{Kind: actTickerVerb, Verb: ops.Cancel}},
	}
	for _, c := range cases {
		got, err := parse(c.args)
		if err != nil || got != c.want {
			t.Errorf("parse(%q) = %+v, %v; want %+v", c.args, got, err, c.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		args []string
		msg  string
	}{
		{[]string{"a", "b", "c"}, "too many"},
		{[]string{"pasta", "soon"}, "neither a duration"},
		{[]string{"pasta", "0s"}, "above zero"},
		{[]string{"0s"}, "above zero"},
		{[]string{"-5m"}, "above zero"},
		{[]string{"pause", "pasta"}, "tick pasta pause"},
		{[]string{"status"}, "command or verb"},
		{[]string{"s", "t"}, "can't be a name"},
		{[]string{"s", "10m"}, "looks like a duration"},
		{[]string{"s", "work", "10m"}, "stopwatch has no duration"},
		{[]string{"10m", "soon"}, "not a verb"},
		{[]string{"t", "list", "10m"}, "is a command"},
		{[]string{"pasta", "1.5"}, "neither a duration"},
	}
	for _, c := range cases {
		_, err := parse(c.args)
		if err == nil || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("parse(%q): error %v, want one containing %q", c.args, err, c.msg)
		}
	}
}

func TestCheckName(t *testing.T) {
	for _, ok := range []string{"pasta", "deep work", "tick", "écrire", "5", "ms"} {
		if err := checkName(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "t", "s", "schedule", "status", "pause", "done", "rm", "10m", "1h30m", "0s", "list", "focus"} {
		if err := checkName(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
