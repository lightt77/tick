package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/lightt77/tick/internal/ops"
	"github.com/lightt77/tick/internal/timer"
)

// The short grammar. parse is pure: it turns the words after `tick` (flags already
// taken out by cobra) into an Action and never looks at the store, so every form
// and every error is table-tested.
//
//	tick                      overview
//	tick t | tick s           running timers | running stopwatches
//	tick <name>               live view
//	tick <name> 10m           countdown       tick 10m   auto-named countdown
//	tick s <name>             stopwatch
//	tick <name> <verb>        pause resume restart done cancel rm skip
//	tick t|s <name> ...       the same with the kind spelled out
//
// Old forms (`tick timer ...`, `tick 15m start`, `tick pause`) are handled by the
// timer/stopwatch commands and by legacy() below.

type actionKind int

const (
	actOverview    actionKind = iota
	actList                   // running timers or stopwatches
	actShow                   // live view of one
	actCreateTimer            // countdown, named or auto-named
	actCreateStopwatch
	actVerb
	actTicker     // legacy: tick 15m start
	actTickerVerb // legacy: tick pause
)

// Action is what the words mean.
type Action struct {
	Kind     actionKind
	Of       timer.Kind // t / s given or implied, else ""
	Name     string     // empty for an auto-named countdown
	Duration time.Duration
	Verb     string
}

// verbs are the words that act on an existing timer. resume is another name for start.
var verbs = map[string]string{
	"start": ops.Start, "resume": ops.Start, "pause": ops.Pause, "restart": ops.Restart,
	"done": ops.Done, "cancel": ops.Cancel, "rm": ops.Rm, "skip": ops.Skip,
}

// reserved words can't be names, because they are commands, kinds or verbs.
var reserved = map[string]bool{
	"t": true, "s": true, "schedule": true, "status": true, "silence": true, "sounds": true,
	"dashboard": true, "daemon": true, "help": true, "completion": true, "timer": true,
	"stopwatch": true, "list": true, "focus": true,
}

func isReserved(name string) bool {
	_, verb := verbs[name]
	return reserved[name] || verb
}

// parseDuration reads 10m, 1h30m, 90s; zero and negative are not durations here.
func parseDuration(s string) (time.Duration, bool) {
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, false
	}
	return d, true
}

// durationShaped is true for anything time.ParseDuration reads, even 0s, so a name
// like "10m" can't be confused with a countdown.
func durationShaped(s string) bool {
	_, err := time.ParseDuration(s)
	return err == nil
}

func parse(args []string) (Action, error) {
	if len(args) == 0 {
		return Action{Kind: actOverview}, nil
	}
	first := args[0]

	// Legacy: tick 15m start, and a bare verb for the timer named "tick".
	if len(args) == 2 && args[1] == ops.Start {
		if d, ok := parseDuration(first); ok {
			return Action{Kind: actTicker, Duration: d, Verb: ops.Start}, nil
		}
	}
	if len(args) == 1 {
		if v, ok := verbs[first]; ok {
			return Action{Kind: actTickerVerb, Verb: v}, nil
		}
	}

	switch first {
	case "t":
		return parseKind(timer.KindTimer, args[1:])
	case "s":
		return parseKind(timer.KindStopwatch, args[1:])
	}
	return parseRest("", args)
}

// parseKind handles tick t ... and tick s ...
func parseKind(kind timer.Kind, rest []string) (Action, error) {
	if len(rest) == 0 {
		return Action{Kind: actList, Of: kind}, nil
	}
	if kind == timer.KindStopwatch && len(rest) == 1 {
		if err := checkName(rest[0]); err != nil {
			return Action{}, err
		}
		return Action{Kind: actCreateStopwatch, Of: kind, Name: rest[0]}, nil
	}
	return parseRest(kind, rest)
}

// parseRest handles <name>, <name> <duration>, <name> <verb> and <duration>
// (alone, or followed by a verb for an auto-named countdown).
func parseRest(kind timer.Kind, args []string) (Action, error) {
	first := args[0]
	if len(args) > 2 {
		return Action{}, fmt.Errorf("too many words; see: tick --help")
	}
	if _, isVerb := verbs[first]; isVerb && len(args) == 2 {
		return Action{}, fmt.Errorf("the verb goes after the name: tick %s %s", args[1], first)
	}
	if durationShaped(first) {
		if _, ok := parseDuration(first); !ok {
			return Action{}, fmt.Errorf("%q is not a duration above zero, like 10m, 1h30m or 90s", first)
		}
	}
	if d, ok := parseDuration(first); ok {
		if kind == timer.KindStopwatch {
			return Action{}, fmt.Errorf("a stopwatch has no duration")
		}
		if len(args) == 1 {
			return Action{Kind: actCreateTimer, Of: timer.KindTimer, Duration: d}, nil
		}
		// tick 10m pause: the auto-named countdown "10m"
		v, ok := verbs[args[1]]
		if !ok {
			return Action{}, fmt.Errorf("%q is not a verb (%s)", args[1], verbList)
		}
		return Action{Kind: actVerb, Of: kind, Name: first, Verb: v}, nil
	}

	if len(args) == 1 {
		if isReserved(first) {
			return Action{}, fmt.Errorf("%q is a command or verb, not a timer name", first)
		}
		return Action{Kind: actShow, Of: kind, Name: first}, nil
	}

	if d, ok := parseDuration(args[1]); ok {
		if kind == timer.KindStopwatch {
			return Action{}, fmt.Errorf("a stopwatch has no duration")
		}
		if err := checkName(first); err != nil {
			return Action{}, err
		}
		return Action{Kind: actCreateTimer, Of: timer.KindTimer, Name: first, Duration: d}, nil
	}
	if v, ok := verbs[args[1]]; ok {
		return Action{Kind: actVerb, Of: kind, Name: first, Verb: v}, nil
	}
	if durationShaped(args[1]) {
		return Action{}, fmt.Errorf("%q is not a duration above zero, like 10m, 1h30m or 90s", args[1])
	}
	return Action{}, fmt.Errorf("%q is neither a duration (10m) nor a verb (%s)", args[1], verbList)
}

const verbList = "pause, resume, restart, done, cancel, rm, skip"

// checkName is validName plus the first-word rules: commands, kinds and verbs
// can't be names, and neither can anything shaped like a duration.
func checkName(name string) error {
	if err := validName(name); err != nil {
		return err
	}
	if isReserved(name) {
		return fmt.Errorf("%q is a command or verb, so it can't be a name", name)
	}
	if durationShaped(strings.TrimSpace(name)) {
		return fmt.Errorf("%q looks like a duration, so it can't be a name", name)
	}
	return nil
}
