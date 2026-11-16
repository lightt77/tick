# tick architecture

This is for whoever works on tick next, human or LLM, with no earlier context. The
[README](README.md) covers usage. This file covers why tick is built this way, how
the parts fit, the rules the code relies on, and what is still open.

## Purpose and scope

tick is a personal, macOS-only tool for one user. It has:
- countdowns, stopwatches, and a repeating "ticker" (`tick 15m start`)
- scheduled reminders and habits ("guitar 15m every day at 18:30"), created mostly
  by an LLM through the `/ticker` command
- a **Focus cube** session: a focus stopwatch plus a repeating lap timer on a ladder
  of lengths (1m, 5m, ... 1h), with breaks and a logged summary
- a live terminal view with milliseconds, a menu bar item with controls, and
  custom sounds
- alerts that fire with no terminal open, by way of a daemon started by launchd

It replaced the user's older project `veranda` (`../productivity/veranda`, which was
cross-platform, used an HTTP daemon, SQLite and a TUI, and was heavier).

**Non-goals, decided on purpose:**
- **Linux and Windows.** Dropped for now; macOS is the only target.
- **Phone notifications.** The daemon only reaches the Mac. Apple Reminders was
  considered and set aside.
- **An MCP server.** Possible later as a thin wrapper over the CLI. `--json` and
  `--dry-run` already make the CLI LLM-friendly.
- **Notification action buttons.** `osascript` notifications can't have them;
  controls live in the tray menu instead.
- **A separate "habit" entity.** A habit is just a scheduled timer.
- **Cron.** It can't express "every 2 days" or "the last day of the month"; RRULE
  replaced it.
- **Daylight saving handling.** The user said it doesn't matter. Times are local
  wall-clock time.
- **Laps for stopwatches.**

## Shape

One binary, `cmd/tick`, has two roles:

```
tick <command>  (CLI)  ─┐                         ┌─ afplay (sound)
                        ├─ internal/ops ─ store ──┤
tray menu clicks ───────┘        │                │   osascript (notification)
                                 ▼                │
                         state.json (flock)  ◀── daemon loop (every 250 ms)
                         runs.jsonl                  ├─ timer.Advance(now)
                         config.toml                 ├─ alert.Ringer.Reconcile
                                                     └─ tray.Update
```

- **There is no IPC.** The CLI and the daemon both read and write
  `state.json`. Every read-modify-write holds an exclusive `flock` on `state.lock`,
  and every write goes through a temp file plus rename (`internal/store`). The
  daemon re-reads the file each tick, so a CLI change shows up within 250 ms. The
  CLI also works while the daemon is down; the daemon catches up when it starts,
  through the grace logic below.
- **There is a single daemon instance:** `daemon.lock` is held with
  `LOCK_EX|LOCK_NB` and holds the pid. `tick status` probes it with a shared
  non-blocking lock.
- **The CLI and the tray do the same things.** Both call `internal/ops`
  (`Create`, `Apply(name, kind, verb, until)`, `SilenceAll`). Nothing shells out to
  `tick` itself.

## Packages

| Package | Role | Notes |
|---|---|---|
| `timer` | Timer model and state machine | Pure functions of `(timer, now)`. The core of tick. |
| `focus` | The Focus cube session and its state machine | Pure, like `timer`; see its own section. Imports only `timer` (for `Duration` and `Ringing`). |
| `schedule` | RRULE subset, multi-rule merge, skips | Wraps `teambition/rrule-go` v1.8.2. |
| `store` | `state.json` + flock + atomic write | `Load` (shared lock), `Update(fn)` (exclusive; saves only if `fn` succeeds and the bytes changed). Holds `Timers` and the optional `Focus` session. |
| `ops` | Verbs shared by the CLI and the tray | One `store.Update` per call, then run-log lines. `focus.go` has `FocusStart` and `FocusDo`. |
| `runlog` | `runs.jsonl` append | Its fields are the future event-ledger schema. An optional `detail` object carries a focus session's breakdown. |
| `daemon` | The loop | `Step()` is one tick and is exported for tests. |
| `alert` | Sounds, notifications, ring loops | The `Player` interface is swapped by tests and `TICK_ALERT=log`. |
| `tray` | Menu bar | `view.go` is pure and tested; `tray.go` draws with `fyne.io/systray`; `policy_darwin.go` is cgo. |
| `config` | `config.toml` | `Load` → `(Config, timer.Settings)`; `SetSound` rewrites a single line. Also parses `focus_steps` and `focus_breaks` into `Config.Steps` and `Config.Breaks`. |
| `dashboard` | Data and writer for the static dashboard page | `Build` is pure; `Writer.Update` is called by the daemon each tick; `page.html` is embedded. |
| `launchd` | LaunchAgent plist and `launchctl` | Label `local.tick.daemon`. |
| `cli` | cobra commands, `--json` views, live view | `View` is the JSON contract for the LLM. `route.go` is the grammar, `overview.go` is `tick` / `t` / `s` / `-w`, `schedule_prompt.go` is `tick schedule`, `dashboard.go` is `tick dashboard`, `focus.go` is `tick focus`. |
| `paths` | File locations | `TICK_HOME` moves everything. |

Dependencies are deliberately few: `cobra`, `fyne.io/systray` (needs cgo),
`teambition/rrule-go`, `BurntSushi/toml`.

## The timer state machine (`internal/timer`)

Everything is a `Timer`. `Kind` is either `timer` or `stopwatch`. Optional fields
turn a timer into a ticker (`Repeat`) or a scheduled reminder (`Schedule`).

States: `scheduled`, `due`, `running`, `paused`, `done`.
- `done` is only reached by a **one-off** countdown. Scheduled timers go back to
  `scheduled`, and repeating timers keep running.
- **Ringing is an attribute (`Ringing{Since, Until}`), not a state.** That lets a
  scheduled timer go straight back to waiting while its sound still plays. Ringing
  stops at `Until` (the cap), on `tick silence` or the tray's Silence, or on any
  verb applied to that timer.
- **The clock math:** `Banked` is the remaining time (countdown) or elapsed time
  (stopwatch) at the moment `Since` was set. Nothing ticks inside the state. Every
  reading is computed from `now`, so the state is never written once per second.

| Verb | Effect |
|---|---|
| `start` / `resume` | Name unknown: the long CLI form creates it (the short form errors). On paused: resume. On done: run again. On due: begin the countdown (only for schedules that have a duration; others use `done`). On a schedule paused with `pause`: resume the schedule. On scheduled with a duration: an early, off-schedule run now. On scheduled without a duration: a no-op. On running: a no-op. |
| `pause` | Running → paused (banks the time). Scheduled or due → schedule suspended (`Suspended`, optional `SuspendedUntil` for auto-resume); a due occurrence is logged as skipped. |
| `restart` | Countdown back to its full duration and running; stopwatch zeroed and running. A no-op on scheduled or due. |
| `done` | Concludes it (`Timer.Done`). Stopwatch or countdown: logs `done` with the time it ran and deletes it; a repeating timer logs one `done` with the lap count in `note` ("7 laps"). A finished (rung) countdown was already logged, so it is only cleared. A schedule in `due`: logs `done` and goes back to `scheduled` (deleted if it was the last occurrence). A schedule with nothing pending: a no-op. The event has `User` set, so no notification. |
| `cancel` | On a stopwatch or timer: deletes it and logs `cancelled` with the time it ran (and laps), except a rung countdown (already logged). On a schedule: drops the due or next occurrence (the `skip` behaviour, logged `skipped`), or aborts a countdown the schedule started (`AbortRun`, logged `cancelled`); the schedule stays. |
| `rm` | Deletes it, whatever it is: a whole schedule goes, logged `cancelled`. For a timer or stopwatch it is the same as `cancel`. |
| `skip` | Scheduled timers only; same as `cancel` on a schedule. Skips the due occurrence, or else the next one (added to `Schedule.Skipped`). Removes the timer if nothing is left. |
| `silence` | Clears `Ringing` only (tray, and `tick silence` for all timers). |

No-ops return `timer.ErrNoop`. The CLI prints them and exits 0; `Update` saves
nothing.

**`Advance(now, settings)`** is the only place where time moves. It returns events,
`changed` and `remove`, and runs in this order:
1. A ring past its `Until` is cleared.
2. A running countdown at zero:
   - repeating: chimes once (`Ringing.Until == Since`, so exactly one play), and the
     next lap starts at the previous end; after a sleep longer than a lap, it starts
     from now, with no replay
   - scheduled: back to `scheduled` and rings for `ring_cap`
   - otherwise: `done` and rings
3. Auto-resume when `SuspendedUntil` has passed. Nothing missed meanwhile is caught
   up.
4. Occurrences that have arrived (`NextAt <= now`):
   - all except the latest are logged as missed (up to 50, through `Between`)
   - the latest becomes **due** if it's within the grace window (`timer.Grace`, else
     `settings.Grace`, default 2h), with `Late` set when more than 90 s late, and a
     ring of `due_ring` (20 s)
   - an occurrence that arrives while a previous run is still running or paused is
     missed, and a new one arriving while still due replaces it (the old one is
     missed)
   - `NextAt` is then recomputed
5. There is no nagging. A due occurrence rings for `due_ring`, then **waits**
   (state stays `due`, shown as pending) until the user resolves it with `done`,
   `cancel`/`skip`, or `pause`. If the next occurrence arrives first, the old one is
   logged missed ("next occurrence arrived") and the new one takes its place.
6. `remove = true` when the timer is scheduled, not suspended, has no `NextAt` and
   isn't ringing. One-time reminders and exhausted `COUNT`/`UNTIL` rules are cleaned
   up this way.

`NextAt` is cached in the state and recomputed only when it passes, or on skip and
resume. That keeps RRULE work to about one call per occurrence.

## Schedules (`internal/schedule`)

- `Schedule{Rules []string, At *time.Time, Anchor time.Time, Skipped []time.Time}`.
  Exactly one of `Rules` or `At` is set.
- **`rrule-go` holds one RRULE per `Set`.** So several rules ("weekdays 18:30 + Sat
  10:00") are compiled separately and merged in `Next` and `Between`. Skips are
  applied by tick itself, not with EXDATE. As a result, **skipped occurrences still
  count towards COUNT**, which the user wanted.
- **The allowed subset** (`Validate`):
  - keys: FREQ (MINUTELY, HOURLY, DAILY, WEEKLY, MONTHLY, YEARLY), INTERVAL, COUNT,
    UNTIL, BYDAY, BYMONTHDAY, BYMONTH, BYSETPOS, BYHOUR, BYMINUTE
  - DAILY and slower rules require BYHOUR and BYMINUTE, otherwise they would fire at
    the creation time of day; HOURLY requires BYMINUTE
  - COUNT and UNTIL together are rejected, as is DTSTART
  - **`rrule-go` does not range-check numbers** (it accepts `BYHOUR=25`), so
    `Validate` does
- **Anchor** (DTSTART) is the creation time cut to the whole minute, or to the whole
  local hour when a rule is MINUTELY or HOURLY, so "every 20 minutes" lands on :00,
  :20 and :40. Cutting goes through `time.Date` in local time, never
  `time.Truncate`, which would be wrong for zones with a :30 offset (the tests use
  +05:30 on purpose).
- **Speed:** `Next` on a year-old every-20-minutes rule takes about 1 ms. No
  re-anchoring is needed.
- **Verified rule shapes:** `schedule_test.go` checks daily, Mon/Wed/Fri, 1st and
  15th, last day of the month, last Friday, last weekday (BYSETPOS), yearly,
  quarterly, twice a day, every 20 minutes in work hours (including over a weekend),
  every 2 days, a two-rule set, COUNT, UNTIL, and skips counting towards COUNT.
- **Not expressible:** holidays, "N times a week, any day", randomness, triggers
  other than time, completion-anchored repeats, fixed time zones.

## Focus cube (`internal/focus`)

One session, stored in `state.json` beside the timers (`State.Focus`, `omitempty`).
It is a composite object on purpose, not a stopwatch plus a ticker linked by a name:
pause, reset, break and done have to change both clocks atomically, a half-cancelled
session cannot exist, and the run log gets one line.

- **Model.** `Session{State, Ladder, Breaks, Step, Focus/FocusSince, Lap/LapSince, Laps,
  BreakCount, BreakTotal, BreakStart/BreakEnds, Seen, Ringing, ...}`. The two clocks use
  the same trick as `Timer`: a banked value plus a start time, read against `now`.
  `Focus` is the focus stopwatch (elapsed), `Lap` is the time left in the current lap.
  The ladder and the break choices are copied from the config when the session starts.
- **States:** `running`, `paused`, `break`, `breakover`. A finished session is simply
  removed. A pause that tick made itself has `PausedBy: "sleep"`.

| Verb | Effect |
|---|---|
| `pause` / `resume` | Bank or restart both clocks. `resume` also ends a break (the lap continues where it stopped) and answers a finished break. |
| `up` / `down` | Next or previous rung; the lap restarts. The partial lap stays in the focus time and is **not** counted as a lap. At either end it is a no-op. |
| `reset` | The lap restarts, its elapsed part comes off the focus time (never below 0), and that rung's `reset` counter goes up. Works while paused (stays paused). |
| `break [d]` | Bank both clocks, `State = break`, `BreakEnds = now + d`. During a break it restarts the break; time already spent is added to `BreakTotal`, `BreakCount` stays. |
| `done` / `cancel` | `Summary(now)` becomes one run-log line (`ops.FocusEntry`) and the session is removed. A partial lap counts as focus time, not as a lap. |
| `silence` | Clears `Ringing` only. |

`up`, `down`, `reset` and `pause` are refused on a break (`resume` first).

**`Advance(now)`** is the only place where time moves, in this order:
1. A ring past its `Until` is cleared.
2. **Sleep rule.** A running session not seen for more than `SleepGap` (2 min) is
   paused at `Seen` (`PausedBy: sleep`, one `EvSleep` event). This covers a sleeping Mac
   and a daemon that was down; time nobody saw is never focus time.
3. A lap at zero: all laps that fit are counted (`n = 1 + overshoot / lap`, normally 1),
   the next lap starts where the last one ended, and one chime is queued
   (`Ringing.Until == Since`, the same single-play trick as a ticker lap).
4. A break at its end becomes `breakover` and rings for `AckRing` (12 h) until the user
   answers. This is the one ring in tick that does not stop by itself after `ring_cap`.
5. `Seen` is refreshed at most every `SeenEvery` (30 s) while running.

Because of step 5 a running session writes `state.json` about twice a minute; quiet
ticks in between still write nothing (a test checks it). The dashboard data file
follows the same writes.

- **Daemon.** `Step` advances the session in the dry run and in the real update.
  Events: a lap chime is sound only (no banner, a 1m lap would be noise); break over and
  sleep-pause post a banner (`FocusAnnouncement`). The ring key is `"\x00focus"`, which no
  timer name can collide with.
- **Run log.** One line on done or cancel: `tag: "focus"`, `kind: "focus"`, `amount` in
  focus minutes, `note` ("16 laps, 2 breaks") and `detail` (`laps` per rung with `done` and
  `reset`, `breaks`, `break_min`). Laps are not logged one by one.
- **Surfaces.** CLI `tick focus ...` and a flat tray submenu (no nested menus, because
  nesting in `fyne.io/systray` was not verified on macOS). `focus` is a reserved name.
  The tray learns the ladder through `SetFocusSteps`, which the daemon calls when it
  loads the config, so no goroutine reads the daemon's config.
- **Dashboard.** `Data.Focus` (`FocusView`, the same fixed-point clock fields as
  `TimerView`) feeds a live card; focus runs in `history` feed a "Focus sessions" table.
- **State format.** The `focus` field is additive and the file version stays 1. An older
  daemon would drop it on its next write, so restart the daemon after installing
  (`tick daemon start`).

## Daemon (`internal/daemon`) and tray

`Step()` runs every 250 ms:
1. Reload `config.toml` if its mtime changed. A bad config keeps the previous
   settings. `alert.SetCustomDir(sounds_dir)` runs here.
2. Load the state and dry-run `Advance` on clones (the timers and the Focus session).
   Most ticks change nothing and **write nothing**, which a test checks.
3. If anything changed: inside `store.Update`, advance for real, collect events and
   drop removed timers.
4. Events go to `runs.jsonl` (done, missed, skipped, cancelled; ticker laps and
   due alerts are not logged) and to notifications through `Announcement`.
5. `Ringer.Reconcile(rings)` keeps one goroutine per ringing timer. A ring is
   identified by `Ringing.Since`: the same `Since` is never replayed, and a new
   `Since` restarts the loop.
6. `Tray.Update(state, now, defaultSound)`.

**Tray (`internal/tray`):**
- **Title:** ringing, due or a finished focus break first (`⏰ name`, `⏰ break over`,
  `+N`), else the Focus cube (`◉ 5m 03:12`, `⏸ 5m 03:12`, `☕ 04:10`), else the
  soonest-finishing countdown, else a running stopwatch (`↑`), else `◷`. A ticker's chime
  doesn't take the title.
- **Menu:** the Focus cube item first (`FocusMenu` for a session, a **Focus cube ▸ Start
  at …** submenu when idle), then sections Due, Running, Paused, Done, Stopwatches,
  Scheduled. Each timer
  has a submenu with only the verbs valid in its state (`Rows`). A finished countdown
  offers Restart, Done and Cancel; Done and Cancel both just clear it, because it was
  already logged as `done` when it rang. Then
  **Sound ▸**, **Silence all** and **Quit tick**.
- **Rebuild only on a shape change.** `Signature(rows, sound+stamp)` excludes the
  ticking texts, so the menu is rebuilt only when its shape, the default sound or a
  sound folder's mtime (`alert.Stamp`) changes. Otherwise item titles are updated in
  place. Each rebuild closes the previous click goroutines through a stop channel.
- **Sound ▸** lists `sounds_dir` first, a separator, then `~/Library/Sounds` and
  `/System/Library/Sounds` (deduplicated, case-insensitive, and the first folder
  wins). **Choose file…** runs `osascript choose file of type {"public.audio"}`,
  where error -128 means cancel. Choosing a sound calls `config.SetSound`, which
  replaces or adds the single `sound =` line and validates the result before the
  rename, then previews the sound once.
- **No Dock icon, without a `.app`.** `policy_darwin.go` calls
  `[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory]` (cgo).
  Verified: `lsappinfo` reports `ApplicationType = UIElement`.
- **Threads.** `systray.Run` must own the main thread; `fyne.io/systray` locks it
  in its `init`. The daemon loop runs in a goroutine started from `onReady`.
  SIGTERM (launchd stop) leads to `systray.Quit()`, which leads to a clean exit.
  `--no-tray` skips systray entirely, for headless runs and tests.

## Dashboard (`internal/dashboard`)

A read-only page the user opens with `tick dashboard`. There is **no server**, by
choice: no port, no auth, nothing to keep running.

- **The constraint.** A page opened from `file://` can load a `<script src>` but
  cannot `fetch` a file, and it cannot evaluate RRULEs. So the daemon writes
  everything into `dashboard-data.js` (`window.TICK = {...};`) next to
  `dashboard.html`, both in the data folder (outside the vault, 0600).
- **The page** (`page.html`, vanilla JS/CSS, no network) re-injects a
  `<script src="dashboard-data.js?t=…">` every 2 s and re-renders when
  `generated_ms` changes. It draws the clocks itself every 250 ms from `ends_ms`
  (running countdown), `started_ms` (running stopwatch: now minus elapsed) or the
  fixed `remaining_ms`/`elapsed_ms` (paused). All data reaches the DOM through
  `textContent`; a test fails if the page uses `innerHTML`.
- **Everything the page can't compute is computed in Go** by `Build`: the next 14
  days of occurrences (one `Schedule.Between` per timer, grouped by local day with
  at most 6 times listed per timer per day, so an "every 20 minutes" rule stays
  small) and the last 30 days of `runs.jsonl` (`runlog.Since`).
- **`Writer.Update`** runs every 250 ms tick but starts with a cheap check (mtime
  and size of `state.json` and `runs.jsonl`, and that the data file exists), so
  quiet ticks write nothing. It writes on a change, as a 30 s heartbeat (which is
  how the page tells "daemon stopped" from "quiet"), and when the file was deleted.
  A failed write waits for the next heartbeat instead of retrying every tick.
  Writes are temp file plus rename. Errors are logged, never returned from `Step`.
- **Snapshot mode.** `tick dashboard` writes one snapshot (`source: "snapshot"`)
  when no daemon is running, or the data file is missing or older than 90 s (an old
  daemon binary never writes one). The page then says it is not updating.
- **Read-only on purpose.** The tray and the CLI call `ops`; a page without a server
  can't. Adding buttons or an `ask` box would need a local server (with an Origin
  check and a token, since it would accept POSTs).
- **Tested** without a browser: `dashboard_test.go` (states, agenda window and
  grouping, writer skip/heartbeat/missing-file/failure, file format and mode, page
  safety) and a daemon test that quiet ticks leave the data file alone. The page's
  look and its 2 s reload were checked by hand in headless Chrome with synthetic data.

## Alerts (`internal/alert`)

- **Sound:** `afplay -v <volume> <file>`, which blocks for one play. A ring loop
  plays, waits `ring_interval`, and stops when the next play would fall past
  `Until + interval/2`. That gives: a chime plays once, a due occurrence for `due_ring`
  (20 s: five plays at the default 5 s interval), and a
  finished timer until `ring_cap`.
- **Notifications:** `osascript` with title and message passed as **argv** (`on run
  argv`), never interpolated into the script, so timer names cannot inject
  AppleScript. macOS attributes them to Script Editor.
- **Sound lookup** (`ResolveSound`): a file path if the value contains `/` or starts
  with `~`; otherwise a case-insensitive base-name match in `sounds_dir`, then
  `~/Library/Sounds`, then `/System/Library/Sounds`. An unresolvable sound in the
  daemon falls back to Glass and is logged. The CLI rejects it when the timer is
  created.

## Files and environment

Data: `~/Library/Application Support/tick/` (0700 dir, 0600 files).
- `state.json`: `{"version":1,"timers":[…],"focus":{…}}` (`focus` only while a Focus cube
  session exists), timers sorted by name. Durations are strings
  like `"15m0s"` (`timer.Duration`). A newer `version` is refused.
- `state.lock`, `daemon.lock` (pid).
- `runs.jsonl`: `{"ts","tag","amount","unit":"min","note","outcome","kind"}`.
  These fields were chosen to become the future event ledger, so it can be reused
  as is. A focus session adds an optional `detail` object (`kind: "focus"`).
- `config.toml`: see the README. Keys: `sound`, `sounds_dir`, `volume`,
  `ring_interval`, `ring_cap`, `due_ring`, `grace`, `focus_steps` (ascending),
  `focus_breaks`. Old `nag_interval` and
  `nag_count` keys are ignored.
- `dashboard.html`, `dashboard-data.js`: generated (see Dashboard). Safe to delete;
  they come back with `tick dashboard` and the daemon.

Logs: `~/Library/Logs/tick/daemon.log` (launchd's stdout and stderr).

Environment:
- `TICK_HOME=<dir>` moves data and logs, for trials and end-to-end tests.
- `TICK_ALERT=log` logs plays and notifications instead of making them.

LaunchAgent: `~/Library/LaunchAgents/local.tick.daemon.plist`
- runs `<binary> daemon` with `RunAtLoad`
- `KeepAlive {SuccessfulExit: false}`: restarted after a crash, not after "Quit tick"
- `ProcessType Interactive`
- `launchd.Executable()` refuses a `go run` temp binary, because the plist records
  the absolute binary path

## CLI contract (`internal/cli`)

- **Grammar** (`route.go`, pure `parse(args) (Action, error)`, table-tested):
  - `tick` overview; `tick t` / `tick s` running timers / stopwatches (`-w` redraws
    four times a second; piped or without a terminal it prints once)
  - `tick <name>` live view; `tick <name> <duration> [-r]` countdown;
    `tick <duration> [-r]` unnamed countdown, auto-named `10m`, `10m-2`, ...;
    `tick s <name>` stopwatch; `tick <name> <verb>`; `tick t|s <name> ...` states the
    kind and checks it; `tick <duration> <verb>` acts on an auto-named countdown
  - verbs: `start` `resume` `pause` `restart` `done` `cancel` `rm` `skip`
  - first-word reserved: `t s schedule status silence sounds dashboard daemon help
    completion timer stopwatch list focus`, every verb, and anything `time.ParseDuration`
    reads (so a name can't be `10m`; `checkName`). Auto-names are the one exception.
  - **Names are one namespace** across timers and stopwatches (`store.Find`), so there
    is no "both kinds" ambiguity; the `t`/`s` prefix is only a check.
  - **Old forms** stay: `tick timer <name> [verb] [duration]` and `tick stopwatch ...`
    (the cobra commands), `tick <dur> start` (the ticker, `actTicker`) and a bare
    verb for the timer named `tick` (`actTickerVerb`).
  - `tick focus [--json]` and `tick focus start [rung] | pause | resume | up | down | reset
    | break [length] | done | cancel | silence` (`focus.go`); `--json` prints `FocusView`.
    A no-op (`resume` on a running session, `down` on the shortest lap) exits 0.
  - Also `status`, `silence`, `sounds`, `dashboard [--no-open]`,
    `daemon [install|uninstall|start] [--no-tray]`, and `schedule` (below).
- **Root flags:** `-r/--repeat`, `-w/--watch`, `--json`, `--sound`, `--label`, `--until`.
  Each is refused on a form it doesn't fit ("-r goes with a duration").
- **`tick schedule`** (`schedule_prompt.go`): a conversation on stdin (numbered menus,
  standard library only). Name, once or repeating, how often, time(s) of day, more
  options, a dry-run preview, and a y/n. `buildRules` is pure and its output is
  checked against `schedule.Validate`; one RRULE per time of day. It refuses a
  non-terminal stdin and points to the flags, because an LLM can't answer prompts.
  It reuses `app.create` (set `a.f.rules`/`a.f.at`/`a.f.dryRun`), so it makes exactly
  what `tick timer <name> start --rrule ...` makes. New reminders have no duration.
- **Creation flags** of the long form (`--rrule` (repeatable), `--at`, `--label`, `--sound`,
  `--grace`, `--repeat`, `--dry-run`) apply only when `start` creates a new name. On an
  existing name they are an error: "cancel it first".
- **`--json`** prints `View`, the stable shape the LLM reads:
  - `remaining` / `remaining_ms` for countdowns, `elapsed` / `elapsed_ms` for
    stopwatches
  - `next`, `upcoming` (the next 5, single view and dry run only), `due_at`, `late`,
    `paused_schedule`, `paused_until`, `ringing`, `rules`, `at`, `label`
- **Exit codes:** an error exits 1 with `tick: …` on stderr; a no-op exits 0.
- **`parseWhen`** accepts `YYYY-MM-DD HH:MM`, `YYYY-MM-DDTHH:MM`, `YYYY-MM-DD`
  (midnight), RFC 3339, and `HH:MM` (the next such time).
- **Live view:** redraws every 33 ms, re-reads the state every 250 ms, and exits on
  Ctrl+C, when the countdown finishes, or when the timer is ended (done, cancelled or
  deleted). When stdout is not a terminal it prints one line instead. It is view-only.

## The `/ticker` command

The source is `../productivity/customizations/ai/commands/ticker.md`.
`refreshAICommands` (the `sync-commands.sh` script) installs it as the Claude Code
skill `~/.claude/skills/ticker/SKILL.md`, and for OpenCode. It is user-invoked only.

- **Quick things:** countdowns, the ticker, stopwatches and silence run at once.
- **Schedules:** it asks for what's missing, builds the RRULE from a table of
  verified shapes, runs `--dry-run --json`, shows the next 5 dates, and creates
  nothing without an explicit yes.
- **Cancels:** always confirmed first.
- **Daemon:** it never runs `tick daemon install` itself.

If the CLI's flags or the RRULE subset change, update `ticker.md` and re-check
its rule table. Every rule in it was dry-run against the real binary.

## Testing

- `go test -race ./...`: everything runs without sound, GUI or real waiting. Time is
  injected (`Now func() time.Time` in `ops` and `daemon`; an explicit `now` in
  `timer`).
- **Tests to know about:**
  - `timer_test`: the state machine, one test per scenario (pause and resume, ticker
    laps, stopwatch, due and grace, done, cancel and skip on schedules, pause-until,
    reminders without a duration)
  - `schedule_test`: rule shapes, validation, speed
  - `store_test`: 40 concurrent writers with nothing lost, corrupt-file refusal
  - `runlog_test`: `Since` filters by time, skips junk lines, and treats a missing file
    as empty
  - `daemon_test`: end-to-end countdown (including cancel on a finished one logging
    nothing more), a late-after-sleep reminder, a pending reminder that never nags,
    quiet ticks writing nothing, dashboard data following changes only, the
    single-instance lock; for the Focus cube: lap chimes with no banner, quiet ticks until
    the `seen` refresh, auto-pause with one banner after a sleep, break over ringing until
    answered then done logging one line, and the ring key not colliding with a timer named
    `focus`
  - `focus_test`: the Focus state machine (every verb in every state, lap counting and the
    single chime, a stall counting every lap, partial-lap rules, reset floor, break restart
    accumulating time, resume continuing the lap, the sleep rule, `seen` throttling and quiet
    ticks, the JSON round trip, and counting after a load, where `Laps` comes back nil)
  - `ops/focus_test`: exclusive start, verbs without a session, one log line on done with
    its `detail`, cancel, no-ops saving nothing
  - `cli/route_test`: the grammar table (`parse`, its errors, `checkName`)
  - `cli/e2e_test`: runs `tick ...` in-process against a throwaway `TICK_HOME` with
    `TICK_ALERT=log`: the short grammar, auto-named countdowns, refused flag misuse,
    the old forms, verbs on a pending reminder, and a whole `tick focus` session including
    the configured ladder
  - `cli/schedule_prompt_test`: the `tick schedule` conversation and `buildRules`
  - `dashboard_test`: see Dashboard (also the Focus view per state and `detail` on history)
  - `tray/view_test`: title priority, rows and verbs (including a finished countdown's
    Silence, Restart, Done, Cancel), signature stability, sound ordering; for the Focus
    cube: the menu per state, the title rules and the signature
  - `alert_test`: ring timing, deduplication and ordering, `sounds_dir` lookup
    priority, stamp
  - `config_test`: `SetSound` keeps the rest of the file, escapes paths, refuses an
    invalid result; the focus lists' defaults, override (without leaking into `Defaults`:
    the TOML decoder writes into an existing slice in place) and validation
  - `launchd_test`: `plutil -lint` and XML escaping
- **A manual end-to-end run** without touching real data:
  `TICK_HOME=$(mktemp -d) TICK_ALERT=log tick daemon --no-tray &`, then drive the
  CLI against it.
- **What tests can't cover:** the tray's visual behaviour (the Focus submenu included), real sound and
  notification delivery, and the file dialog. Those were checked by hand on the
  user's Mac.

## Working conventions

- **Build and install:** `go install ./cmd/tick` puts it in `~/go/bin`, which is on
  PATH. Then `tick daemon start`, **because the running daemon keeps the old binary
  until restarted.** There is no Makefile, on purpose.
- **Repo:** this is a nested Git repo in the user's Obsidian vault at
  `01tech/projects/tick`. Its remote is `origin`, `git@github.com:lightt77/tick.git`
  (private), and `main` tracks it. The vault's `pushToObsidian` script commits and
  pushes nested repos that have a remote.
- **Commits and pushes are the user's.** Don't commit, push, install the
  LaunchAgent or change their shell config without an explicit request.
- **`~/.zshrc` is the real shell config.** The copy in
  `../productivity/customizations/shell/.zshrc` is a backup that `pushToObsidian`
  overwrites from it. Edit `~/.zshrc`.
- **`sounds/`** in this repo holds the user's own alert sounds; `sounds_dir` in
  their `config.toml` points at it. Its files are committed with the repo.

## Open and parked work

- **Retire veranda.** Waiting for the user's go-ahead. In `~/.zshrc`, `timer()`
  should call `tick timer "$2" start "$1"` and the `veranda` alias should go. The
  veranda repo and `~/.veranda` stay untouched.
- **Event ledger (parked).** Free-form "what happened" events ("exercised", "worked
  2h") that an LLM can query.
  - Agreed so far: a light structure (`ts`, `tag`, optional `amount` + `unit`,
    `note`), with the LLM normalising tags at write time.
  - Timers would write to the same ledger, which is why `runs.jsonl` already has
    that shape.
  - A logged event should **not** auto-close a due timer.
  - Undecided: inside tick or a separate binary. The suggestion was one repo with
    the ledger as its own package, shipped inside tick at first.
  - The user's older `../productivity/timeledger` (SQLite) is related.
- **Possible later:**
  - `tick ask "…"`: a one-file agent loop over an API LLM (OpenRouter) that turns
    plain language into `tick` commands. Parked on purpose; the user wants to use
    tick for a few days first. Design notes: reuse `ticker.md` as the system prompt,
    allow only `timer`, `stopwatch`, `silence`, `status`, `sounds`, enforce the
    dry-run and the y/N for create and cancel in code rather than trusting the
    model, and keep the API key in `~/Library/Application Support/tick/.env`
    (outside the vault).
  - an MCP server wrapping the CLI
  - notification buttons (would need `alerter` or a signed native helper)
  - per-timer volume, or per-timer sound from the tray
  - streaks and stats from `runs.jsonl`
  - completion-anchored schedules ("2 days after I last did it")
- **Known limits:**
  - daylight-saving transitions are not handled specially
  - notifications come from Script Editor
  - `teambition/rrule-go` is stagnant (last commit 2023) but adequate; keep it
    behind `internal/schedule`, where `Elojah/rrule` is the noted fallback
