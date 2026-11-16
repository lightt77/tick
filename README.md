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
tick                         # everything, printed once (-w keeps it live)
tick t                       # running timers          tick s   running stopwatches
tick pasta 10m               # countdown named pasta; rings when done, which counts as done
tick pasta                   # live view to the millisecond (Ctrl+C to leave)
tick pasta 10m -r            # starts again each time it finishes (counts the laps)
tick 10m                     # an unnamed countdown, named 10m (then 10m-2 ...)
tick s work                  # stopwatch, counts up
tick pasta pause | resume | restart
tick pasta done              # conclude it: logged as done with the time it ran (and laps)
tick pasta cancel            # throw it away: logged as cancelled
tick schedule                # set up a reminder by answering questions
tick focus start             # Focus cube: a focus stopwatch with repeating laps (below)
tick guitar done | cancel    # a reminder that rang: mark it done, or skip it
tick guitar rm               # delete a whole schedule
tick silence                 # stop all ringing
tick sounds                  # sound names for --sound and config (yours first)
tick status                  # daemon alive?, next due, what is ringing
tick daemon install | uninstall | start
tick daemon --no-tray        # run in the foreground without the menu bar
```

- A name is unique across timers and stopwatches. `tick pasta 10m` on a name that
  exists is an error; look at it with `tick pasta`.
- Words that are commands or verbs (`t s schedule status silence sounds dashboard
  daemon help completion timer stopwatch list focus start pause resume restart done
  cancel rm skip`) and anything shaped like a duration (`10m`) can't be names. `tick t pasta
  pause` and `tick s work done` spell the kind out and check it.
- `tick 10m pause` acts on the unnamed countdown called `10m`; a plain `tick 10m`
  always makes a new one.
- `--sound` and `--label` go with a new countdown: `tick pasta 10m --sound Hero`.
- `--json` works on `tick`, `tick t`, `tick s` and `tick <name>`, for scripts and LLMs.
- The long forms still work: `tick timer pasta start 10m`, `tick stopwatch work start`,
  `tick timer list`, `tick 15m start` (the ticker, a repeating timer named `tick`) and
  `tick pause | start | restart | cancel` for it.

### Menu bar

- **Title:** whatever needs you first (`⏰ guitar`, or `⏰ break over`), else a
  running Focus cube (`◉ 5m 03:12`), else the soonest-finishing countdown
  (`pasta 04:12`), else a running stopwatch (`work ↑12:03`), else `◷`.
- **Menu:** the Focus cube first (a **Focus cube ▸** submenu to start one, or the
  running session with its verbs), then every timer with the verbs valid right now.
- **Sound ▸** picks the default alert sound. Your own sounds come first: the files
  in `sounds_dir` (this repo's `sounds/` folder; drop new files in and they appear
  in the menu right away). Then `~/Library/Sounds` and the macOS sounds, or
  **Choose file…** for any other file. A name in `sounds_dir` wins over a macOS
  sound with the same name. Picking one plays it once and saves it as `sound` in
  `config.toml`, keeping the rest of the file. A timer's own `--sound` still wins.
- **Silence all** and **Quit tick** are at the bottom.

### Focus cube

A Pomodoro-style session: a **focus stopwatch** that runs the whole time, and a **lap
timer** that chimes once and starts again each time it finishes. The laps sit on a
ladder (1m, 5m, 10m, 15m, 30m, 1h), and you move up or down it as your attention
allows. Only one session exists at a time.

```sh
tick focus start [5m]        # begin on the first rung, or on the one you name
tick focus                   # where it is (--json for scripts and LLMs)
tick focus up | down         # a longer or shorter lap; the lap restarts
tick focus reset             # throw this lap away
tick focus pause | resume
tick focus break [10m]       # pause everything; again = restart the break
tick focus done | cancel     # end it; both log one line
tick focus silence
```

Or use the menu bar: **Focus cube ▸ Start at …**, then Pause, Longer lap, Shorter lap,
Reset lap, Break …, Done and Cancel.

- **Laps.** A lap end plays the sound once and posts no banner. The focus stopwatch keeps
  going across laps. The session counts, per lap length, laps finished and laps reset.
- **Up and down** keep the time you already spent as focus time, but the unfinished
  lap is not counted. **Reset** is for "I got distracted": the lap restarts, the part
  already run comes off the focus time (never below zero), and the lap length's reset
  counter goes up.
- **Break.** It pauses the focus stopwatch and the lap. When it ends tick rings every
  `ring_interval` and posts a banner until you answer: **Resume** (continues the paused
  lap), **Restart break** (with any length) or **Done**. **Silence** stops the sound
  only. Break time is the real time until you answer, so an overrun counts.
- **Sleep.** If tick has not seen a running session for more than 2 minutes (the Mac
  slept, or the daemon was not running), the session pauses at the last moment it was
  seen, and a banner says so. Time nobody saw is never counted as focus.
- **Done** (or **Cancel**) writes one line to `runs.jsonl`: `tag: "focus"`,
  `kind: "focus"`, `amount` in focus minutes, a `note` like `16 laps, 2 breaks`, and a
  `detail` object with the per-lap-length counts. The dashboard lists these as
  **Focus sessions**.
- The ladder and the break choices are `focus_steps` and `focus_breaks` in `config.toml`.
  A session keeps the lists it started with.

### Dashboard

```sh
tick dashboard             # open the read-only page in your browser
tick dashboard --no-open   # just write the files and print the path
```

A detailed, read-only view: what needs you, the Focus cube with its live clocks and
lap counts, recent focus sessions, running and paused timers with live clocks, every schedule with its next time, the next 14 days in one agenda, and the
last 14 days of done, missed and skipped runs as a strip per habit. It is a plain
HTML file with no server. A `file://` page cannot read `state.json`, so the daemon
keeps `dashboard-data.js` next to it (rewritten when something changes, plus every
30 s), and the page reloads that file every 2 s and ticks the clocks itself. With no
daemon running, `tick dashboard` writes one snapshot and the page says so. It
follows the macOS light and dark setting.

### Scheduled reminders and habits

A scheduled reminder waits. When an occurrence arrives it becomes **due**: it rings
for `due_ring` (20 s), notifies once, and then **waits for you**, in the menu bar's
Due section and on the dashboard, with no nagging. Mark it with `tick guitar done`
(logged done), or `tick guitar cancel` (logged skipped). If the next occurrence
arrives first, the unresolved one is logged as missed.

`tick schedule` asks for the name, once or repeating, how often (every day, weekdays,
weekends, certain days, a day of the month, every few hours or minutes, or a custom
RRULE) and the time, then shows the next five occurrences and asks before creating
anything. It needs a terminal. Scripts and the `/ticker` command use the flags:

```sh
tick timer guitar start --rrule "FREQ=DAILY;BYHOUR=18;BYMINUTE=30" --label "Daily 18:30"
tick timer meds start --rrule "FREQ=DAILY;BYHOUR=9,21;BYMINUTE=0" --label "9:00 and 21:00"
tick timer dentist start --at "2026-11-20 09:00"
tick timer guitar start --rrule "..." --dry-run    # check: prints the next 5 dates

tick guitar cancel                     # skip the due or next occurrence (same as skip)
tick guitar pause --until 2026-12-03   # pause the schedule, resume then
tick guitar resume                     # resume a paused schedule
tick guitar rm                         # delete the whole schedule
```

A schedule created with a duration (`tick timer guitar start 15m --rrule ...`) still
works: `tick guitar start` begins the countdown when it is due. New reminders from
`tick schedule` have none.

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
| `state.json` | all timers and the Focus cube session (CLI and daemon share it under an flock) |
| `runs.jsonl` | one line per done, skipped, missed or cancelled run: `ts, tag, amount, unit, note, outcome, kind` (a repeating timer logs one `done` with `note: "7 laps"`; a focus session adds a `detail` object) |
| `config.toml` | optional settings, below |
| `dashboard.html`, `dashboard-data.js` | the dashboard page and its data (your timer names and history; generated, safe to delete) |

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
due_ring = "20s"         # how long a due occurrence rings before it waits quietly
grace = "2h"
focus_steps = ["1m", "5m", "10m", "15m", "30m", "1h"]   # Focus cube laps, shortest first
focus_breaks = ["5m", "10m", "15m", "30m"]              # Focus cube break choices
```

`nag_interval` and `nag_count` from older configs are ignored. Per timer, `--sound` overrides `sound`. Notifications are posted by `osascript`, so
macOS shows them as coming from Script Editor, and you may have to allow them once
in System Settings → Notifications.

## Layout

```
cmd/tick            main
internal/timer      the timer model and state machine (pure, tested)
internal/focus      the Focus cube session and its state machine (pure, tested)
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
internal/dashboard  data for the static dashboard page, its writer, the embedded page
internal/paths      where files live (TICK_HOME override)
sounds/             your own alert sounds (sounds_dir points here)
```

`go test ./...` runs everything without sound or GUI (add `-race` before
committing).
