// Package schedule says when a scheduled timer fires.
//
// A schedule is either one or more RRULE bodies (RFC 5545, a tested subset) anchored
// at a start time, or a single one-time instant. Skipped occurrences are excluded
// dates. rrule-go holds one rule per set, so several rules are merged here.
package schedule

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/teambition/rrule-go"
)

// Schedule is stored inside a timer. Exactly one of Rules or At is set.
type Schedule struct {
	Rules   []string    `json:"rules,omitempty"`   // e.g. "FREQ=DAILY;BYHOUR=18;BYMINUTE=30"
	At      *time.Time  `json:"at,omitempty"`      // one-time occurrence
	Anchor  time.Time   `json:"anchor"`            // DTSTART for every rule
	Skipped []time.Time `json:"skipped,omitempty"` // excluded occurrences
}

// Keys a rule may use. Everything else is rejected, so an LLM cannot hand us a rule
// shape nobody tested.
var allowedKeys = map[string]bool{
	"FREQ": true, "INTERVAL": true, "COUNT": true, "UNTIL": true,
	"BYDAY": true, "BYMONTHDAY": true, "BYMONTH": true, "BYSETPOS": true,
	"BYHOUR": true, "BYMINUTE": true,
}

var allowedFreq = map[string]bool{
	"MINUTELY": true, "HOURLY": true, "DAILY": true, "WEEKLY": true, "MONTHLY": true, "YEARLY": true,
}

// New validates the rules (or the one-time instant) and returns a schedule anchored
// at anchor. The anchor is cut to the whole minute, or to the whole local hour when a
// rule repeats by the minute or hour, so "every 20 minutes" means :00, :20 and :40.
func New(rules []string, at *time.Time, anchor time.Time) (Schedule, error) {
	anchor = wholeMinute(anchor)
	switch {
	case len(rules) > 0 && at != nil:
		return Schedule{}, fmt.Errorf("use either rules or a one-time time, not both")
	case len(rules) == 0 && at == nil:
		return Schedule{}, fmt.Errorf("a schedule needs a rule or a one-time time")
	}
	if at != nil {
		t := wholeMinute(*at)
		return Schedule{At: &t, Anchor: anchor}, nil
	}
	clean := make([]string, 0, len(rules))
	for _, r := range rules {
		c, err := Validate(r)
		if err != nil {
			return Schedule{}, err
		}
		if strings.Contains(c, "FREQ=MINUTELY") || strings.Contains(c, "FREQ=HOURLY") {
			anchor = time.Date(anchor.Year(), anchor.Month(), anchor.Day(), anchor.Hour(), 0, 0, 0, time.Local)
		}
		clean = append(clean, c)
	}
	s := Schedule{Rules: clean, Anchor: anchor}
	if _, err := s.compile(); err != nil {
		return Schedule{}, err
	}
	return s, nil
}

// Validate checks one rule body against the supported subset and returns it
// normalised (upper case, no "RRULE:" prefix).
func Validate(rule string) (string, error) {
	r := strings.ToUpper(strings.TrimSpace(rule))
	r = strings.TrimPrefix(r, "RRULE:")
	if r == "" {
		return "", fmt.Errorf("empty rule")
	}
	keys := map[string]string{}
	for _, part := range strings.Split(r, ";") {
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok || v == "" {
			return "", fmt.Errorf("rule %q: %q is not KEY=VALUE", rule, part)
		}
		if !allowedKeys[k] {
			return "", fmt.Errorf("rule %q: %s is not supported (allowed: FREQ INTERVAL COUNT UNTIL BYDAY BYMONTHDAY BYMONTH BYSETPOS BYHOUR BYMINUTE)", rule, k)
		}
		if _, dup := keys[k]; dup {
			return "", fmt.Errorf("rule %q: %s appears twice", rule, k)
		}
		keys[k] = v
	}
	freq, ok := keys["FREQ"]
	if !ok {
		return "", fmt.Errorf("rule %q: FREQ is required", rule)
	}
	if !allowedFreq[freq] {
		return "", fmt.Errorf("rule %q: FREQ=%s is not supported", rule, freq)
	}
	if _, c := keys["COUNT"]; c {
		if _, u := keys["UNTIL"]; u {
			return "", fmt.Errorf("rule %q: use COUNT or UNTIL, not both", rule)
		}
	}
	// Without explicit times a daily-or-slower rule would fire at the creation time
	// of day, which nobody means. Make the time part of the rule.
	switch freq {
	case "DAILY", "WEEKLY", "MONTHLY", "YEARLY":
		if keys["BYHOUR"] == "" || keys["BYMINUTE"] == "" {
			return "", fmt.Errorf("rule %q: FREQ=%s needs BYHOUR and BYMINUTE", rule, freq)
		}
	case "HOURLY":
		if keys["BYMINUTE"] == "" {
			return "", fmt.Errorf("rule %q: FREQ=HOURLY needs BYMINUTE", rule)
		}
	}
	// rrule-go does not range-check numbers (it accepts BYHOUR=25).
	ranges := map[string][2]int{
		"INTERVAL": {1, 1000}, "COUNT": {1, 100000}, "BYHOUR": {0, 23}, "BYMINUTE": {0, 59},
		"BYMONTH": {1, 12}, "BYMONTHDAY": {-31, 31}, "BYSETPOS": {-366, 366},
	}
	for k, lim := range ranges {
		v, ok := keys[k]
		if !ok {
			continue
		}
		for _, item := range strings.Split(v, ",") {
			n, err := strconv.Atoi(item)
			if err != nil || n < lim[0] || n > lim[1] || (n == 0 && (k == "BYMONTHDAY" || k == "BYSETPOS")) {
				return "", fmt.Errorf("rule %q: %s=%s is out of range", rule, k, v)
			}
		}
	}
	if _, err := rrule.StrToROptionInLocation(r, time.Local); err != nil {
		return "", fmt.Errorf("rule %q: %v", rule, err)
	}
	return r, nil
}

// wholeMinute drops seconds in local wall-clock time.
func wholeMinute(t time.Time) time.Time {
	t = t.In(time.Local)
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, time.Local)
}

func (s Schedule) compile() ([]*rrule.RRule, error) {
	out := make([]*rrule.RRule, 0, len(s.Rules))
	for _, body := range s.Rules {
		opt, err := rrule.StrToROptionInLocation(body, time.Local)
		if err != nil {
			return nil, fmt.Errorf("rule %q: %v", body, err)
		}
		opt.Dtstart = s.Anchor
		r, err := rrule.NewRRule(*opt)
		if err != nil {
			return nil, fmt.Errorf("rule %q: %v", body, err)
		}
		out = append(out, r)
	}
	return out, nil
}

func (s Schedule) skipped(t time.Time) bool {
	for _, x := range s.Skipped {
		if x.Equal(t) {
			return true
		}
	}
	return false
}

// Next returns the first occurrence strictly after `after`, or false when the
// schedule has no more occurrences.
func (s Schedule) Next(after time.Time) (time.Time, bool) {
	if s.At != nil {
		if s.At.After(after) && !s.skipped(*s.At) {
			return *s.At, true
		}
		return time.Time{}, false
	}
	rules, err := s.compile()
	if err != nil {
		return time.Time{}, false
	}
	var best time.Time
	for _, r := range rules {
		cur := after
		for {
			t := r.After(cur, false)
			if t.IsZero() {
				break
			}
			if !s.skipped(t) {
				if best.IsZero() || t.Before(best) {
					best = t
				}
				break
			}
			cur = t
		}
	}
	return best, !best.IsZero()
}

// Upcoming returns up to n occurrences strictly after `after`.
func (s Schedule) Upcoming(after time.Time, n int) []time.Time {
	var out []time.Time
	cur := after
	for len(out) < n {
		t, ok := s.Next(cur)
		if !ok {
			break
		}
		out = append(out, t)
		cur = t
	}
	return out
}

// Between returns occurrences in (from, to], oldest first, at most limit of the
// newest ones (a long sleep over a minutely rule must not build a huge list).
func (s Schedule) Between(from, to time.Time, limit int) []time.Time {
	if !to.After(from) {
		return nil
	}
	var all []time.Time
	if s.At != nil {
		if s.At.After(from) && !s.At.After(to) && !s.skipped(*s.At) {
			all = append(all, *s.At)
		}
	} else {
		rules, err := s.compile()
		if err != nil {
			return nil
		}
		seen := map[int64]bool{}
		for _, r := range rules {
			for _, t := range r.Between(from, to, true) {
				if t.Equal(from) || s.skipped(t) || seen[t.Unix()] {
					continue
				}
				seen[t.Unix()] = true
				all = append(all, t)
			}
		}
		sort.Slice(all, func(i, j int) bool { return all[i].Before(all[j]) })
	}
	if limit > 0 && len(all) > limit {
		all = all[len(all)-limit:]
	}
	return all
}

// Skip excludes one occurrence. Skipped occurrences still count towards COUNT.
func (s *Schedule) Skip(t time.Time) {
	if !s.skipped(t) {
		s.Skipped = append(s.Skipped, t)
	}
}

// Describe is a compact machine description used when no label was given.
func (s Schedule) Describe() string {
	if s.At != nil {
		return "once at " + s.At.Format("2006-01-02 15:04")
	}
	return strings.Join(s.Rules, " + ")
}
