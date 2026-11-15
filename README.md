# tick

Timers, stopwatches, a repeating ticker and scheduled reminders for the macOS menu
bar. One Go binary is both the CLI and a small background daemon. The daemon rings,
notifies and shows the soonest-finishing timer in the menu bar, with controls in its
menu. macOS only.

Working on the code? Read [ARCHITECTURE.md](ARCHITECTURE.md) first: it has the
design decisions, the state machine, the invariants and what is still open.

## Install

```sh
go install ./cmd/tick      # puts tick in ~/go/bin (on PATH)
tick daemon install        # start the daemon now and at every login
tick status
```

After changing the code, run `go install ./cmd/tick` again, then `tick daemon start`.
The running daemon keeps the old binary until it is restarted.

`tick daemon install` writes `~/Library/LaunchAgents/local.tick.daemon.plist`. That
agent starts the daemon at login and restarts it after a crash, but not after you
choose **Quit tick** from the menu (`tick daemon start` brings it back).
`tick daemon uninstall` removes the agent and keeps your timers.

## Use

```sh
tick timer pasta start 10m          # countdown; rings when done
tick timer pasta                    # live view to the millisecond (Ctrl+C to leave)
tick timer pasta pause | start | restart | cancel
tick stopwatch work start           # counts up
tick stopwatch work pause | start | restart | cancel
tick 15m start                      # chime every 15 minutes: the timer named "tick"
tick pause | start | restart | cancel   # ...act on it
tick timer list                     # everything; add --json for scripts and LLMs
tick silence                        # stop all ringing
tick sounds                         # sound names for --sound and config (yours first)
tick status                         # daemon alive?, next due, what is ringing
tick daemon install | uninstall | start
tick daemon --no-tray               # run in the foreground without the menu bar
```

### Menu bar

- **Title:** whatever needs you first (`⏰ guitar`), else the soonest-finishing
  countdown (`pasta 04:12`), else a running stopwatch (`work ↑12:03`), else `◷`.
- **Menu:** every timer with the verbs valid right now.
- **Sound ▸** picks the default alert sound. Your own sounds come first: the files
  in `sounds_dir` (this repo's `sounds/` folder; drop new files in and they appear
  in the menu right away). Then `~/Library/Sounds` and the macOS sounds, or
  **Choose file…** for any other file. A name in `sounds_dir` wins over a macOS
  sound with the same name. Picking one plays it once and saves it as `sound` in
  `config.toml`, keeping the rest of the file. A timer's own `--sound` still wins.
- **Silence all** and **Quit tick** are at the bottom.

### Scheduled reminders and habits

A scheduled timer waits. When an occurrence arrives it becomes **due**: it rings
three times, notifies, and reminds again every 10 minutes (3 reminders in all).
`start` begins its countdown, or acknowledges a reminder without a duration. If
nobody starts it, the occurrence is logged as missed.

```sh
tick timer guitar start 15m --rrule "FREQ=DAILY;BYHOUR=18;BYMINUTE=30" --label "Daily 18:30"
tick timer meds start --rrule "FREQ=DAILY;BYHOUR=9,21;BYMINUTE=0" --label "9:00 and 21:00"
tick timer dentist start --at "2026-11-20 09:00"
tick timer guitar start 15m --rrule "..." --dry-run    # check: prints the next 5 dates

tick timer guitar skip                 # skip the due or next occurrence
tick timer guitar pause --until 2026-12-03   # pause the schedule, resume then
tick timer guitar start                # resume a paused schedule, or start early
```

- Schedules are RFC 5545 RRULE bodies restricted to FREQ (MINUTELY to YEARLY),
  INTERVAL, COUNT, UNTIL, BYDAY, BYMONTHDAY, BYMONTH, BYSETPOS, BYHOUR and BYMINUTE.
  Daily and slower rules need BYHOUR and BYMINUTE.
- Repeat `--rrule` for different times on different days.
- A skipped occurrence still counts towards COUNT.
- **After sleep:** an occurrence that passed while the Mac was asleep fires on wake
  if it is within the grace window (2h, or `--grace`). Only the latest one fires;
  older ones are logged as missed.
- Times are local wall-clock time. Daylight saving is not handled specially.

The `/ticker` command (in the productivity repo's `customizations/ai/commands/`)
lets an LLM create and change these from plain language. It uses `--dry-run` and
asks before creating anything.

## Files

All files are in `~/Library/Application Support/tick/`, outside the vault, readable
only by you:

| File | What |
|---|---|
| `state.json` | all timers (CLI and daemon share it under an flock) |
| `runs.jsonl` | one line per finished, skipped, missed or cancelled run: `ts, tag, amount, unit, note, outcome, kind` |
| `config.toml` | optional settings, below |

The daemon log is `~/Library/Logs/tick/daemon.log`. `TICK_HOME=<dir>` moves
everything, which is useful for trials, and `TICK_ALERT=log` logs alerts instead of
playing them.

```toml
# config.toml: every key optional; these are the defaults
sound = "Glass"          # a name from `tick sounds`, or a file path
sounds_dir = ""          # a folder of your own sounds, e.g. "~/…/tick/sounds"
volume = 0.8
ring_interval = "5s"     # between repeats of the sound
ring_cap = "2m"          # a finished timer stops ringing after this
nag_interval = "10m"     # between reminders of a due occurrence
nag_count = 3
grace = "2h"
```

Per timer, `--sound` overrides `sound`. Notifications are posted by `osascript`, so
macOS shows them as coming from Script Editor, and you may have to allow them once
in System Settings → Notifications.

## Layout

```
cmd/tick            main
internal/timer      the timer model and state machine (pure, tested)
internal/schedule   RRULE subset on top of teambition/rrule-go, multi-rule merge
internal/store      state.json under flock, atomic writes
internal/runlog     runs.jsonl
internal/daemon     the 250 ms loop: advance, log, notify, ring, tray
internal/alert      afplay ring loops, osascript notifications
internal/tray       menu bar (fyne.io/systray); view.go is the pure, tested part
internal/ops        verbs shared by the CLI and the menu
internal/launchd    the LaunchAgent
internal/cli        commands (cobra)
internal/config     config.toml: load, defaults, SetSound
internal/paths      where files live (TICK_HOME override)
sounds/             your own alert sounds (sounds_dir points here)
```

`go test ./...` runs everything without sound or GUI (add `-race` before
committing).
