# Scheduled Runs: A Prompt With a Clock

## The claim

Some work is a habit rather than a request. Triage the overnight PRs at nine. Sweep the
dependency updates on Monday. Write the standup note at eight forty. Today each of those is a
person remembering to type `helios new` — and the machine that would have run it was sitting
there awake the whole time.

A schedule is a saved prompt with a cron expression. The daemon fires it, and the run is an
ordinary session: same transcript, same notifications, same terminal, visible in every client
that was already watching. Nothing about a scheduled session is a second kind of session.

Some habits are not "at nine" but "when this happens", and some work is a queue rather than a
habit. So a schedule has one of four triggers:

| | Fires |
|---|---|
| **timer** | on a cron expression |
| **once** | at one moment, then it is done |
| **monitor** | when a check matches; the cron only says how often to look |
| **after** | when another job finishes |

The last two are what make this more than cron. A monitor's command decides whether there is
anything to do and hands its output to the agent — the difference between "check the build" and
"the build broke like this, fix it". And **after** is how a night's work gets ordered: line up
one-shot jobs, chain them, and go to bed.

**Scope: the daemon and all four clients.** New: `internal/schedule` (a cron parser),
`internal/store/schedules.go`, a firing loop in `internal/daemon`, REST routes, `helios
schedule`, a TUI view, a desktop sidebar list with its panel, a mobile tab, and
`skills/helios/SKILL.md` — the manual an agent reads to drive the CLI, installed during agent
setup. No provider changes: a schedule launches through the same registry everything else does.

## Where we are

| | Today |
|---|---|
| Creating a session | `handleCreateSession` (`internal/server/api.go:1832`) and `handleInternalCreateSession` (`api.go:1136`): `provider.Get` → `prov.Launch(SessionSpec{…})` → `startTerminal` → `UpsertSession` |
| Prompting a live session | `handleSessionSend` (`api.go:518-597`): `provider.QueuerFor(...).QueuePrompt()`, or `Waker.Wake(sessionID, cwd)` first when the session is cold |
| Persistence | SQLite at `~/.helios/helios.db`, one file per resource (`store/devices.go`, `store/groups.go`), `migrate()` with an append-only `columnMigrations` list keyed in `_migrations` (`store/store.go:150-205`) |
| Background work | Tickers in goroutines selecting on `ctx.Done()` — pairing tokens every 5 min, the reaper every 20, memory eviction every 2 (`internal/daemon/daemon.go:246-315`) |
| Telling clients | `shared.SSE.Broadcast(SSEEvent{Type, Data})` (`internal/server/sse.go`) |
| Asking the user something | `Manager.CreateNotification`, answered at `POST /api/notifications/{id}/action`, dispatched by `provider.ActionHandlerFor` (`internal/provider/registry.go:314-330`) |
| Cron | Nothing. `go.mod` has fourteen direct dependencies and no scheduler among them |

## The cron parser is ours

`internal/schedule` parses the five standard fields — minute, hour, day-of-month, month,
day-of-week — with ranges, steps, lists, three-letter names, and `@daily`-style shorthands.
`Next(after time.Time) time.Time` is the whole interface the rest of the system needs.

Written rather than imported, because the repo is stdlib-first and because the two rules that
actually bite are rules we want tests for in our own tree:

- **Day-of-month and day-of-week are OR, not AND**, when both are restricted. `0 9 13 * 5` is
  the thirteenth *and* every Friday, which surprises everyone once.
- **DST.** Schedules are local time, which is what a person means by "nine". On the spring
  forward `0 2 * * *` does not exist and must not fire twice the next day; on the autumn back
  it exists twice and must fire once. `Next` walks minutes in local time and asks the clock,
  rather than doing arithmetic on a duration.

An expression that can never match — `0 0 30 2 *`, February the thirtieth — is rejected when
the schedule is created, not discovered at midnight by a loop that never fires.

## What a schedule is

```
schedules
  id              TEXT PRIMARY KEY      -- uuid
  name            TEXT NOT NULL         -- what the list shows
  cron            TEXT                  -- timer and monitor; how often, or how often to look
  run_at          TEXT                  -- 'once': the one moment, RFC3339
  after_id        TEXT                  -- 'after': the job this one follows
  after_when      TEXT                  -- 'after': 'success' | 'any'
  after_session   TEXT                  -- 'after': the parent run already acted on, so once only
  tz              TEXT NOT NULL         -- IANA name, captured when the schedule is saved
  enabled         INTEGER NOT NULL      -- paused schedules keep their next_run_at honest
  kind            TEXT NOT NULL         -- 'timer' | 'once' | 'monitor' | 'after'
  done_at         TEXT                  -- 'once': when it ran, which is what "done" means
  mode            TEXT NOT NULL         -- 'new' | 'resume'
  prompt          TEXT NOT NULL         -- may contain {{output}} in a monitor
  check_cmd       TEXT                  -- monitor: run through sh -c, in cwd
  check_file      TEXT                  -- monitor: or a script on disk, run directly
  check_args      TEXT                  -- monitor: JSON array, arguments for that script
  check_match     TEXT                  -- monitor: optional regex over stdout
  last_check_at   TEXT                  -- monitor: when it last looked
  last_check_exit INTEGER               -- monitor: and what it saw
  last_check_out  TEXT                  -- monitor: first 4 KB of that, for the UI
  cwd             TEXT                  -- optional; empty means the home directory
  provider        TEXT                  -- new mode; empty means the daemon's default
  model           TEXT                  -- new mode
  permission_mode TEXT                  -- new mode
  target_session  TEXT                  -- resume mode
  next_run_at     TEXT                  -- UTC RFC3339, recomputed after every fire
  last_fired_at   TEXT
  last_session_id TEXT                  -- what the last fire produced, so clients can link to it
  last_status     TEXT                  -- 'running' | 'ok' | 'failed' | 'missed'
  last_error      TEXT
  fail_streak     INTEGER NOT NULL      -- consecutive failures; zeroed by a good run
  failing_since   TEXT                  -- when the streak started
  fires_today     INTEGER NOT NULL      -- how many times it has fired on fires_day
  fires_day       TEXT                  -- the local date that count belongs to
  created_at      TEXT NOT NULL
```

Indexed on `(enabled, next_run_at)`, which is the only query the loop makes. Added as a
`CREATE TABLE IF NOT EXISTS` entry in `columnMigrations`, as `create_groups_tree` was.

**`cwd` is optional, and that is not a detail.** Plenty of useful schedules are about nothing on
this disk: watch the pull requests waiting on my review, watch the on-call queue, watch an
inbox. Those want an agent with its tools, not a checkout. An empty `cwd` means the home
directory, which is exactly what `handleCreateSession` already does with an empty `cwd`
(`api.go:1857-1866`) — so this is the existing default, not a new rule.

The timezone is stored rather than assumed. "Nine" means nine where the person was when they
typed it, and a laptop that crosses the Atlantic should not quietly move the standup note.

No runs-history table, in this or any version. The sessions *are* the history: every fire
produces one, tagged with `schedule_id`, and asking "what has this schedule been doing" is a
query against a table that already holds the transcripts. The columns below are only the summary
a list needs without a join.

`fail_streak` and `failing_since` exist because `last_error` alone is a liar by omission: it is
overwritten every fire, so a schedule that has failed every night for a week looks exactly like
one that failed once, last night. Two columns make "failing for 6 nights, since Tuesday" a thing
the list can say. `fires_today` does the same job for the opposite failure — a monitor whose
condition never clears — and is what turns a runaway from a mystery into a number.

**Two modes, because two habits.** A nightly triage wants a **new** session: a fresh agent, its
own transcript, no memory of last night's argument. A recurring chore against a long-lived
session wants **resume**: the prompt goes into a session that remembers, through the same path
the phone's prompt box uses.

**Overlap is allowed.** If the previous run is still working when the next fire comes, the new
one starts anyway. A daily job that occasionally takes twenty-five hours is the user's problem
to notice, and the alternative — silently skipping — is the failure mode nobody sees.

## When it fires

A ticker in `startDaemon`, alongside the reaper, every 30 seconds — and one sweep immediately at
start, because a `time.Ticker` yields nothing for its first period and a daemon that has just
come back is exactly when the missed fires are waiting. Each tick takes the enabled schedules
whose `next_run_at` is in the past and sorts them by how late they are:

- **Within five minutes: fire.** The window covers a 30-second tick, a loaded machine, and an
  ordinary daemon restart.
- **Later than that: it was missed.** Do not run it. Record `last_status = 'missed'` and ask
  (below).

Either way `next_run_at` is recomputed from *now*, never advanced by adding an interval to the
stored value. A daemon that was off for a week comes back to one question per schedule, not to
a week of backfired agents.

**Claiming a fire is a conditional update, not a lock:**

```sql
UPDATE schedules SET next_run_at = ?, last_fired_at = ? WHERE id = ? AND next_run_at = ?
```

Fire only when that affects one row. Two overlapping ticks, or a restart in the middle of a
fire, then cost nothing — SQLite already gives us the atomicity, and a mutex would only protect
the process against itself.

**One rule covers both ways of missing a fire.** A stopped daemon is obvious. A sleeping laptop
is the subtler one: the process is alive, Go's timers do not fire while the machine sleeps, and
`Ticker` drops the missed ticks rather than bursting — so the tick lands late on wake, which is
exactly the "later than that" branch. There is nothing macOS-specific to write.

The work is pure functions taking `now` as a parameter — `dueSchedules(db, now)`, `fireDue(...)`
— with the ticker the only thing in `daemon.go`. That is how the reaper and the evictor are
already tested, and it means no clock abstraction and no sleeping in tests.

## Asking about a missed run

The missed fire asks: **Run now** or **Skip**, on the phone and on the desktop, because every
notification goes to both.

**The clients need no work at all**, which is worth explaining. Both dispatch on the part of the
type *after* the first dot — `kindOf` in `desktop/src/shared/notifications.ts:19`,
`notification.kind` in `mobile/lib/providers/card_registry.dart:29` — precisely so that a second
provider's permission request gets the first provider's card. So a notification of type
`helios.question`, carrying the payload the question card already reads:

```json
{"questions": [{"question": "…", "options": [{"label": "Run now"}, {"label": "Skip"}]}]}
```

renders as the question card everywhere, and is answered with the body those cards already send:
`{"action":"answer","selections":[{"question_index":0,"option_index":0}]}`.

What is missing is only the routing. Notification types resolve through a *registered provider's*
`Actor` (`internal/provider/registry.go:314-330`), a `Provider` must implement `Launch`, and
`Info` has no "hidden" flag — so registering a fake provider to own one question would put
"helios" in the new-session picker. Instead, `provider.RegisterSystemActor(id, routes)`: a
package-level map consulted in `ActionHandlerFor` when the provider lookup misses, and appended
in `NotificationTypes`. One edit covers the action dispatch at `api.go:68` and `api.go:148` and
the catalogue at `api.go:1766` together, and any future daemon-level question gets it free.

One trap, and it would have been found at runtime: `notifications.source_session` is `NOT NULL`
(`store/store.go:48`) and every sweep keys on it. A scheduled question stores the **schedule id**
there, never an empty string.

## Monitors: a check that decides

A monitor's cron says how often to look, not when to run. On each due tick the daemon runs the
check in the schedule's `cwd`, and the result decides whether an agent starts at all.

**The check is a command or a file, and the schedule says which.** One line of shell covers most
of them — `make -q test`, `curl -fsS localhost:8080/health` — and goes in `check_cmd`, run
through `sh -c`. Anything with real logic in it belongs in a file the user can edit, test and
keep in git, so `check_file` names a script on the machine and `check_args` its arguments; it is
executed directly, by its own shebang, with no shell in between. Exactly one of the two is set,
and which one is a radio button rather than a guess: a bare word is a perfectly good command
*and* a plausible filename, and quietly picking wrong is worse than asking.

A file check is validated when it is saved — it must exist, be a regular file, and be executable
— because "chmod +x" discovered at 3am is not a good way to find out. It is validated again when
it runs, since a file is a thing that can be deleted or edited between saving and firing, and
the daemon runs whatever is in it *then*.

**What counts as a match**, in two rules with no third:

- **With `check_match` set, the regex over stdout decides.** The exit code is ignored, because
  the commands people reach for here report absence by failing: `grep ERROR app.log` exits 1
  when the log is clean, and that is the good case, not a broken check.
- **Without it, a non-zero exit is the match.** This is the `test` convention: the command
  asserts that things are fine, and failing is the news. `curl -fsS localhost:8080/health`,
  `test $(df --output=pcent / | tail -1 | tr -dc 0-9) -lt 90`.

**A check does not need a repository, or even this machine's files.** The two rules above are
about an exit code and some text, and plenty of the interesting ones are a network call:

```
check   gh pr list --search "review-requested:@me draft:false" --json number,title
match   "number"
cwd     (empty — nothing here is about a directory)
prompt  Pull requests are waiting on my review:

        {{output}}

        Review each one and leave comments.
```

`gh pr list` prints `[]` and exits 0 when there is nothing, so the exit code says nothing useful
and the pattern does the work. The agent that then wakes up has the MCP servers and the tools it
would have had if a person had started it — see below.

**The output reaches the agent through `{{output}}`.** The prompt is a template with exactly one
placeholder, substituted with the command's stdout — capped at 32 KB, the tail kept rather than
the head, because the interesting part of a log is the end. A prompt with no placeholder simply
does not get the output; nothing is appended behind the user's back.

```
check   make test 2>&1
prompt  The test suite is failing. Here is the output:

        {{output}}

        Find the cause and fix it. Do not change the tests to pass.
```

**A matching check fires every time it matches.** That is the decision, and it is the sharp
edge of this feature: a condition that stays true — a disk that is still full at midnight — will
start an agent on every tick until something changes. The check is expected to be
self-clearing, and the design leans on that. Three things keep it from being a machine-killer,
and none of them is a hidden cooldown:

- The cron is the rate limit. `*/30 * * * *` is a decision the user typed.
- Every trigger writes a log line and updates the row, so a runaway is visible in
  `helios logs --daemon` and in the list, not silent.
- The memory budget (`docs/specs/42-cold-sessions.md`) already evicts cold sessions, so a
  runaway costs sessions rather than the machine.

**The check is not an agent, and gets a shorter leash**: a 30-second timeout, output captured to
32 KB, killed by process group so a shell that spawns children does not leave them behind. A
check that times out is a failed check, not a match — a monitor that fires because its own
health probe hung would be exactly backwards.

The check runs as the daemon's user with the daemon's environment. That is the same trust
boundary as every session Helios already launches, and worth saying out loud rather than
discovering: **a schedule is code execution, and whoever can reach the API can write one.** The
API is already device-paired and the daemon already spawns agents that run anything; a monitor
adds no new privilege, only a new way to spend it.

## Once, and after: a night that runs itself

A **once** job has a moment instead of a cron. It fires, its session goes idle, and it is done —
the row stays, marked, with what it produced and a **Run again** button. A list that deletes what
happened last night is a list that cannot answer "did it work".

`last_status` follows the same rule as everything else here: `running` from the launch until the
session goes idle, then `ok`; `failed` if it errored or died first.

An **after** job has no clock at all. Its trigger is another job finishing, and dragging it under
a parent is what says so. Chain a few one-shots and the night has an order: migrate, then test
feature one, then feature two, then write up what broke.

**A job is done when its session goes idle.** That is the definition, everywhere in this spec:
the chain's trigger, the one-shot's "done", and what `last_status` records.

It is also the one the code already gives us. An agent that finishes its turn goes **idle** —
the Stop hook writes it (`internal/provider/claude/hooks.go:573`). It does *not* go to
`terminated`; that is `SessionEnd`, the process going away (`hooks.go:838`), which for a warm
session may not happen for hours. A chain waiting on `terminated` would mostly never run.

| Parent's session reaches | The link reads it as |
|---|---|
| `idle` | **success** — the agent did the work and stopped |
| `error` (`StopFailure`, `hooks.go:630`) | **failure** |
| `terminated` without ever reaching `idle` | **failure** — killed or died before finishing |

**And then the run is closed.** A finished job holds a whole agent process for as long as it
stays warm, and one that fires hourly holds a new one every hour — nobody is going to type into
them, and the transcript stays readable either way. So the tick that settles a run also
terminates its session (`Shared.EndSession`, shared with the terminate route). A **resume**
schedule is the exception: the conversation it keeps going is the point of it.

**And a run that never starts is a failure, on a deadline.** A session's status is written by
the agent's own hooks, so an agent that dies before its first one leaves the row at `starting`
and nothing ever moves it again — the run reads as still working and the chain behind it waits
for ever. The reaper is no help: a dead terminal is a cold session by design
(`internal/daemon/reaper.go:16-30`). So a run still at `starting` `BootGrace` (3 minutes) after
it fired is recorded as failed, said so in the schedule's log, and closed. Minutes rather than
the 25 seconds a resumed session gets, because a cold agent loads a transcript, its MCP servers
and the user's settings before it says anything, and nobody is waiting at a keyboard for this
one.

That is why a chain reads the parent's *recorded* outcome — `last_status`, written by
`settleRunning` earlier in the same tick — rather than the parent session's status. The table
above still decides what gets recorded; reading the session directly would now find every
finished parent `terminated`, and call every one of them a failure.

**Children are noticed on the same tick as everything else.** There is no central place where a
session's status changes — `UpdateSessionStatus` is called from inside each provider's hooks
(`claude/hooks.go:573`, `:630`, `:838`) — so an event hook would mean either a new observer on
the broadcaster or a callback threaded through the providers. Neither earns its keep for this:
the loop already runs every 30 seconds, so it reads the parent's session status there and starts
the children it finds ready. A chain step begins within half a minute of the last one ending,
which is not a number anyone will notice at 3am.

The child records `after_session_id` — the parent session it already acted on — so a parent that
sits idle for hours starts its children exactly once.

**Each link carries its own rule.** `after_when` is `success` or `any`, chosen on the link, so
"only if the tests passed" and "clean up either way" can both exist in one chain. A stopped
chain is not an error — the children are marked `blocked` with the reason, which is what the
list shows.

**Siblings run together.** Three jobs dragged under one parent all start when it finishes. They
are three sessions, and if they share a working tree they will fight over it — that is the
user's arrangement to make, and the list shows them side by side so it is visible.

**Cycles are refused at save**, walking the parent chain before writing: A after B after A is a
400, not a hang. **Deleting a parent deletes the branch** — children, grandchildren, and the
schedule logs that went with them. A job that follows another has no clock of its own, so an
orphan can never fire again whether it is paused or not, and a list of jobs that will never run
is a list nobody can read. The clients say which ones before they ask: "Delete nightly-sweep and
the 2 chained under it — greet-hello, greet-hola?". The runs are released either way, and stay
as ordinary sessions.

A schedule has exactly one trigger. Dragging a timer under a parent clears its cron and makes it
an `after`; dragging it back out leaves it paused with no trigger until one is chosen. Two ways
for the same job to start is a race nobody asked for.

## What a fire does: the existing flow, with the prompt filled in

A fire creates a session exactly the way pressing **New session** creates one — same
`SessionSpec`, same provider, same permission mode, same hooks, same MCP servers, same
notifications, same terminal. **The only difference is that the prompt was written earlier.**
Nothing downstream of the launch knows or cares that a clock started it.

That is the whole design of the fire, and it is what makes the GitHub case work with no new
machinery: an agent launched by a schedule has whatever tools an agent launched by hand has, so
"go through the PRs waiting on my review" is a scheduled session doing what that agent already
does. The daemon does not learn about GitHub, and it does not become an MCP client.

The same is true in the other direction, and is the reason to say it out loud: a scheduled
session is **not** sandboxed, quieter, or in any way lesser. It can ask for permission at 4am,
and that notification will reach the phone like any other.

**It is labelled, though, and that is the one difference that reaches the clients.** A session
created by a fire carries the schedule that made it:

```
ALTER TABLE sessions ADD COLUMN schedule_id TEXT
```

Nothing in the session's behaviour reads that column. It exists so the lists can:

- **The sidebar leaves them out.** Six agents you started and forty the clock started is a
  sidebar that has stopped being a list of your work. The filter goes in `SearchSessions`
  (`internal/store/sessions.go:282-290`, beside `pinned` and `terminated`) as an explicit
  three-way `Jobs` field, and `handleListSessions` (`api.go:311-320`) defaults it to *exclude*.

  **The default lives in the handler, not in the store, and that is not a style preference.**
  `ListSessions()` — the no-argument one — is what the reaper (`daemon/reaper.go:34`), the
  memory evictor (`daemon/evict.go:175`) and the MCP tools (`mcp/tools.go:249`) call. A store
  that quietly hid scheduled sessions would mean scheduled sessions are never reaped when their
  terminal dies and never evicted when memory runs short: forty invisible agents holding RAM.
  The store stays neutral; only the client-facing handler has an opinion.
- **The runs list puts them back, in the same UI.** `GET /api/sessions?filter=jobs` is the
  night's work; `?schedule_id=<id>` is one schedule's history. Both return ordinary sessions, so
  every client renders them with the session list it already has — same rows, same status dots,
  same tap-through to the transcript and the terminal. No second list component anywhere.

This is also where run history comes from, and why the schema needs no runs table: **the runs
are the sessions**. "What has build-watch been doing this week" is a query that already exists,
against rows that already carry their own transcript.

**The refactor this asks for first.** If the fire must be the same work, it must be the same
code — and today it is not. The launch sequence (`provider.Get`, `Launch(SessionSpec{…})`,
`startTerminal`, `UpsertSession`) is written twice, at `api.go:1176-1221` and again at
`api.go:1870-1905`, and the wake-and-type path for a cold session is inline in the send handler
at `api.go:576-632`. Extract both before the scheduler exists, into functions the handlers and
the loop all call. A third copy living in a background goroutine is how the scheduled path
quietly stops matching the interactive one.

## Failure, stated in advance

| | |
|---|---|
| `cwd` no longer exists | Checked with `resolveCWD` before launching. The fire is skipped, `next_run_at` advances, and one question asks whether to disable the schedule or leave it to try again. |
| Resume target was terminated | Never wake a dead conversation. The question offers a new session instead, or skip. |
| Cron that can never match | Rejected at save — the parser reports "never" when `Next` finds nothing within four years — so it is a 400 and is never stored. |
| A check that times out, or whose command is missing | A failed check, never a match. Recorded on the row and logged; the streak counts it, so a monitor with a typo in its command disables itself after three rather than looking healthy for ever. |
| A check file that was deleted, or lost its `+x` | Same: a failed check with the reason on the row — "no such file", "not executable" — not a silent quiet result that reads as healthy. |
| A check that matches on every single tick | Fires every time, by design — but it is on the row, in the log, and the fail-streak's sibling is visible: the list shows "triggered 40× today", which is how a runaway is noticed at a glance. |
| `{{output}}` in a timer's prompt | Rejected at save. There is no output to put there, and silently leaving the literal text in the prompt is worse than saying so. |
| Daemon shutting down mid-tick | `ctx.Err()` is checked before the claiming update. If shutdown lands after the claim, the row is left as it is and the next start's sweep asks about it. A session already spawned is independent of the daemon and keeps running. |

## When it goes wrong, where you look

A scheduler runs while nobody is watching, so "what happened at 4am" has to be answerable at
9am. There are three places to look, and they answer different questions.

**1. The daemon log — what the loop decided.** Every fire writes exactly one line, whatever the
outcome, through the same `log` the reaper uses (`internal/daemon/daemon.go:135`, written to
`~/.helios/logs/`, tailed with `helios logs --daemon` and served over `GET /internal/logs`):

```
schedule morning-triage (7f3a…): fired → session a3f1c2e8
schedule standup-note (91b2…): missed by 7h12m, asked
schedule nightly-bench (c04e…): failed — cwd /Users/kim/work/bench does not exist
schedule dep-sweep (2d81…): skipped — disabled
```

One line per decision, never per tick: a loop that logs "nothing to do" every 30 seconds buries
the four lines a week that matter. The schedule's name *and* id are both on the line, because
the name is what a person searches for and the id is what the API takes.

**2. The schedule's own log — what its checks printed.** The daemon log is one line per
decision, which is the wrong grain for "what has this monitor been seeing all week". So each
schedule appends to `~/.helios/logs/schedules/<id>.log`: a timestamped block per check and per
fire, holding the exit code, the verdict, and the output — the full 32 KB, not the 4 KB the row
keeps for the UI. Capped at 5 MB and rotated once, because a five-minute monitor writes 288
blocks a day and nobody wants to discover this feature through a full disk.

**It is tailable, live, from anywhere:**

```
$ helios schedule logs build-watch --follow

11:15:02  check  exit 0    quiet
11:30:02  check  exit 0    quiet
11:45:02  check  exit 2    MATCH → firing
                 --- ok internal/schedule  0.312s
                 --- FAIL internal/tunnel  1.204s
                 ---     zrok_test.go:118: want https://, got bare hostname
11:45:03  fire   session 8c1f2ad9 · new · ~/work/helios
12:00:02  check  exit 2    MATCH → firing
```

`GET /api/schedules/{id}/log?tail=200` returns the tail, in the shape `GET /internal/logs`
already uses (`internal/server/logs.go:87-146`).

**Following it is polling, not streaming, and deliberately so.** The existing log route reads the
file and returns lines — there is no streaming log anywhere in the daemon today, and adding one
means a second long-lived connection kind next to the SSE stream and the terminal websocket. A
check that runs every five minutes does not need sub-second delivery. `--follow` re-requests the
tail every two seconds and prints what is new; the desktop and mobile panels do the same while
they are open, and stop when they are closed. If someone later wants a true stream, the route is
the place to add it and nothing above it changes.

**3. The schedule row — the current state.** `last_status`, `last_error`, `fail_streak` and
`failing_since`, on every client, in the list. This answers "is this thing healthy" without
anyone opening a log.

**4. The session — what the agent actually did.** A fire that launched successfully is an
ordinary session with an ordinary transcript, and `last_session_id` is the link to it. Nothing
about a scheduled run is hidden from the transcript, the terminal or the notifications.

The gap worth naming: a fire that fails *before* a session exists — bad cwd, provider missing,
launch error — has no transcript, so the log line and `last_error` are the only record. That is
why the log line is not optional, and why the error is stored rather than only printed.

**Three failures in a row disables the schedule** and asks, once, with the same `helios.question`
card. A schedule that cannot work should stop trying and say so, rather than writing the same
line into the log every night for a month.

## The API

Registered on both muxes — the public one for the apps, the internal one for the CLI:

```
GET    /api/schedules
POST   /api/schedules            create; validates the cron and returns next_run_at
PATCH  /api/schedules/{id}       edit, enable, disable
DELETE /api/schedules/{id}
POST   /api/schedules/{id}/run   fire now, and answer "Run now" on a missed one
POST   /api/schedules/{id}/check run the check once and report — the "Test now" button
GET    /api/schedules/{id}/log   the tail of its own log; polled to follow it
```

And two filters on a route that already exists, which is the whole of the runs list:

```
GET    /api/sessions?filter=jobs        every session a schedule started
GET    /api/sessions?schedule_id=<id>   one schedule's runs, newest first
```

The default list — the sidebar's — gains one condition: `schedule_id IS NULL`.

New SSE events: `schedule_created`, `schedule_updated`, `schedule_deleted`, `schedule_fired`.
`schedule_fired` carries the session id, which is what lets a client jump straight to the
transcript of a run that just started.

## The four clients

Each one already has a shape for this; none of them invents anything. Every surface shows the
same four facts about a schedule — **when it next fires**, **what it will do**, **whether it is
on**, and **how the last run went** — because those are the questions a person opens this list
to answer.

### CLI

`helios schedule list | add | rm | enable | disable | run | check | logs`, dispatched from the
switch in `cmd/helios/main.go:37-76`, talking to the internal port the way `handleDevices` does
(`main.go:184-230`). Columns are `fmt.Printf` with fixed widths, as everywhere else.

This is the load-bearing surface, not a convenience: it is what the desktop's agent-driven
creation calls, so `add` has to express everything a schedule can be, and its `--help` has to be
good enough for an agent to read once and get right.

```
$ helios schedule list

NAME              WHEN               NEXT/CHECK        LAST      DOES
morning-triage    0 9 * * 1-5        today 09:00       ✓ 14h     new · ~/work/opal-app
dep-sweep         0 4 * * 1          Mon 04:00         ✓ 3d      new · ~/work/helios
standup-note      40 8 * * 1-5       today 08:40       ! missed  resume · a3f1c2e8
nightly-bench     0 2 * * *          paused            ✗ failed  new · ~/work/bench
build-watch       */15 * * * *       checks in 4m      ✓ 2h      monitor · make -q test
pr-review         0 */2 * * 1-5      checks in 51m     ✓ 4h      monitor · gh pr list …

6 schedules · 1 paused · 1 missed while the daemon was down
```

```
$ helios schedule add "triage the overnight PRs" \
    --cron "0 9 * * 1-5" --name morning-triage --cwd ~/work/opal-app

morning-triage — every weekday at 09:00, first run today 09:00 (2h 14m from now)
```

A monitor is the same command with `--check`, or `--check-file` for a script, and `--match` when
the exit code is not the answer:

```
$ helios schedule add "The tests are failing:\n\n{{output}}\n\nFind the cause and fix it." \
    --cron "*/15 * * * *" --name build-watch --cwd ~/work/helios \
    --check "make test 2>&1"

build-watch — checks every 15 minutes, fires when `make test 2>&1` exits non-zero
              first check in 4m

$ helios schedule add "Queue is backing up:\n\n{{output}}\n\nFind out why." \
    --cron "*/5 * * * *" --name queue-watch --cwd ~/work/opal-app \
    --check-file ~/checks/queue_depth.py --check-arg --threshold --check-arg 5000

queue-watch — checks every 5 minutes, runs ~/checks/queue_depth.py --threshold 5000
              fires when it exits non-zero · first check in 2m
```

### TUI

`internal/tui/schedules.go`, modelled on `devices.go`: a list, a detail screen, a
confirm-before-delete screen, data loaded through a `tea.Cmd`. Reachable with `c` from
`screenMain` (`start.go:551-604`, where `t`, `s`, `a` and `m` already live).

```
┌─ helios · schedules ──────────────────────────────────────────────┐
│                                                                   │
│  ▸ morning-triage    ⏰ 0 9 * * 1-5    today 09:00      ● on      │
│    dep-sweep         ⏰ 0 4 * * 1      Mon 04:00        ● on      │
│    standup-note      ⏰ 40 8 * * 1-5   today 08:40      ! missed  │
│    nightly-bench     ⏰ 0 2 * * *      —                ○ paused  │
│    build-watch       ◉ */15 * * * *   checks in 4m     ● on      │
│                                                                   │
├───────────────────────────────────────────────────────────────────┤
│  triage the overnight PRs                                         │
│  new session · ~/work/opal-app · claude · last run ✓ 14h ago      │
├───────────────────────────────────────────────────────────────────┤
│  ↑↓ move   enter run now   space pause   e edit   d delete   q    │
└───────────────────────────────────────────────────────────────────┘
```

The selected row expands into the strip beneath it rather than replacing the list, so pausing
the thing you are reading about does not move it out from under you. The glyph carries the kind:
a clock fires on time, a ring watches. On a monitor the strip shows the check and what it last
saw, which is the thing you came to look at:

```
├───────────────────────────────────────────────────────────────────┤
│  make test 2>&1  ·  fires when it exits non-zero                  │
│  last check 4m ago: exit 0 · quiet · fired 0× today               │
├───────────────────────────────────────────────────────────────────┤
```

### Desktop

**No dialog anywhere.** The sidebar switches between two lists of the same shape — sessions and
schedules, grouped by host, one row per thing — and whatever it selects fills the main panel.
The switch sits above the search and the arrange control, because those belong to whichever
list is showing: schedules have their own search, over the name, the prompt and the check.

Touches `store.ts` (a sidebar mode, a schedule selection, a schedule query), `sidebar.tsx`,
`detail.tsx`, a new `components/schedules.tsx`, a key in `keys.ts`, queries in `queries.ts`, the
methods on `HostApi` in `bridge.ts`, and their names in the `API_METHODS` whitelist in
`main/ipc.ts`.

```
┌────────────────────────┬──────────────────────────────────────────┐
│ ( sessions │SCHEDULES) │  build-watch                    ● on     │
│ 🔍 Search schedules  + │  every 15 min · make test 2>&1           │
├────────────────────────┼──────────────────────────────────────────┤
│ ▾ mdkamrulhassan       │  Overview │ Runs │ Log                   │
│   ⏰ morning-triage     ├──────────────────────────────────────────┤
│      weekdays 09:00    │  Check   make test 2>&1                  │
│   ◉ build-watch  in 4m │          fires when it exits non-zero    │
│      every 15 min · …  │          last check 4m ago · exit 0      │
│   ⧗ nightly-migrate    │  Runs    a new session in ~/work/helios  │
│        done            │          claude                          │
│     ⇢ test-one running │  Prompt  The tests are failing:          │
│     ⇢ test-two waiting │          {{output}}                      │
│        ⇢ write-up      │          Find the cause and fix it.      │
│           waiting      │                                          │
│                        │  ─────────────────────────────────────── │
│                        │  next check in 4m · fired 0× today       │
│                        │  [Run now] [Test check] [Edit] [Delete]  │
└────────────────────────┴──────────────────────────────────────────┘
```

The tabs are `.panel-tabs`, the strip every session panel already uses. The cron expression is
written out in words — "weekdays at 09:00" — with the expression itself kept for the editor.
Nobody reads `0 9 * * 1-5` at a glance, and a list that cannot be skimmed is one where a wrong
schedule hides in plain sight.

A host with nothing scheduled renders nothing at all, heading included. One machine's empty
list is not news when another has six, so the empty state is shown once and only when nobody
has any.

### Chains, by dragging

A job dragged onto another becomes its child. The list is the tree, indented one level per link,
and the drag is the edit. Dropping asks what the link means — in the main panel, not over the
top of the list — because whether a failed parent still releases the child is half the decision
and guessing it is how a chain surprises someone at 3am.

```
┌────────────────────────┬──────────────────────────────────────────┐
│   ⧗ nightly-migrate    │  Link                                    │
│     ⇢ test-one         ├──────────────────────────────────────────┤
│  ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌  │  Run test-three after test-two…          │
│  ▸ run it after        │                                          │
│    test-two            │   (•) only if it succeeds                │
│  ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌  │   ( ) either way                         │
│   ⏰ morning-triage     │                                          │
│                        │  A job with a parent has no clock of its │
│                        │  own: the parent finishing starts it.    │
│                        │                       [Cancel]  [Link]   │
└────────────────────────┴──────────────────────────────────────────┘
```

The dragged id rides on the drag event rather than in React state — state is a render behind,
and a drop that lands fast would read the render from before the drag began.

While a chain is running the same list is the progress view, because "where has it got to" is
the question it already answers:

```
│   ⧗ nightly-migrate                                    done      │
│     ⇢ test-one                                         running   │
│     ⇢ test-two                                         waiting   │
│   ⧗ nightly-bench                                      paused    │
│     ⇢ report          ⊘ the job it follows failed                │
```

### The runs are sessions, so the runs list is the session list

`▸ open runs` on any schedule, and the night's work as a whole, open the session list every
client already has — filtered, not rebuilt. Same rows, same status dots, same tap-through to the
transcript and the terminal:

```
┌─ Runs · build-watch ──────────────────────────────────────────  × ┐
│                                                                   │
│  ● 12:00  the tests are failing…            active   1m   claude  │
│  ✓ 11:45  the tests are failing…            idle    14m   claude  │
│  ✓ 09:15  the tests are failing…            idle    31m   claude  │
│  ✗ 04:00  the tests are failing…            error    2m   claude  │
│                                                                   │
│  4 runs · today                                                   │
└───────────────────────────────────────────────────────────────────┘
```

The sidebar, meanwhile, keeps them in a second section under the host, folded:

```
  ▾ mac-studio                                        6 sessions
      ● refactor the store                     active   2m
      ✓ mermaid in the renderer                idle    14m
      …
  ▸ Automated runs                                            9
```

Folded is the default, for the same reason the filter is: forty sessions the clock started
would bury the six you started yourself. Open it and the runs are ordinary session rows —
same status dots, same panel, same context menu — including the terminated ones, which is most
of them, since a run is closed as soon as it finishes. Clicking a run in the **Runs** tab opens
it here and unfolds the section on the way, rather than leaving it behind a header the reader
would have to know to open.

### Making one: ask an agent, or write it yourself

**+ New schedule** does not open the form. It opens a fork, because nobody wants to fill in a
cron field and most people would rather say what they mean:

```
┌────────────────────────┬──────────────────────────────────────────┐
│ ( sessions │SCHEDULES) │  New schedule                            │
│ 🔍 Search schedules  + ├──────────────────────────────────────────┤
├────────────────────────┤                                          │
│   ⏰ morning-triage     │  On          [ mdkamrulhassan        ▾ ] │
│   ◉ build-watch        │                                          │
│                        │  Describe it ┌────────────────────────┐  │
│                        │              │ every 15 minutes, run  │  │
│                        │              │ the go tests in ~/work │  │
│                        │              │ /helios and if they    │  │
│                        │              │ fail, start an agent   │  │
│                        │              │ to fix them            │  │
│                        │              └────────────────────────┘  │
│                        │              An agent reads this, works  │
│                        │              out the schedule, and       │
│                        │              creates it with the CLI.    │
│                        │                                          │
│                        │  In          [ ~/work/helios           ] │
│                        │  Agent       [ claude                ▾ ] │
│                        │                                          │
│                        │  [Set it up manually]  [Cancel] [Ask an  │
│                        │                                 agent]   │
└────────────────────────┴──────────────────────────────────────────┘
```

**Ask an agent** starts an ordinary session with a short prompt. Short because the manual it
needs is a skill rather than a wall of text: `skills/helios/SKILL.md`, embedded in the binary
and installed to `~/.claude/skills/helios/` during agent setup, beside the hooks. It documents
the four kinds, the two rules a monitor follows, chains, and what the daemon refuses at save.
An agent asked to write a schedule has to know the CLI *before* it is asked, and a manual
nobody installed is a manual nobody reads.

The prompt says only: here is what they asked for, work out which kind it is, create it with
`helios schedule add`, then tell me what you made. Everything else is in the skill, so there is
one manual rather than two that can disagree.

Then the reader watches an ordinary session do it. There is nothing to invent for that — it is
the transcript, and the schedule appears in the list when the agent creates it, ready to be
read before it ever fires.

**Which machine and which agent are asked, not inferred.** The host is part of what a schedule
is, and the agent is the same choice the new-session dialog offers.

**Set it up manually** is the form below, and it is also where an agent's work gets corrected.

### The form

Reached by **Edit…**, by a click on a card, and by the fallback above:

```
┌─ New schedule ────────────────────────────────────────────────  × ┐
│                                                                   │
│  Name       [ morning-triage                                   ]  │
│                                                                   │
│  When       [ 0 9 * * 1-5                                      ]  │
│             weekdays at 09:00 — next Mon 3 Mar, 09:00             │
│                                                                   │
│  Fires      (•) on the clock    ( ) when a check matches          │
│                                                                   │
│  Run        (•) a new session   ( ) into an existing session      │
│                                                                   │
│  Where      [ ~/work/opal-app                              ▾   ]  │
│             optional — leave it empty for work that is not       │
│             about a directory                                     │
│  Agent      [ claude ▾ ]   [ default model ▾ ]   [ auto ▾ ]       │
│                                                                   │
│  Prompt     ┌─────────────────────────────────────────────────┐   │
│             │ triage the overnight PRs and summarise what     │   │
│             │ needs a human                                   │   │
│             └─────────────────────────────────────────────────┘   │
│                                                                   │
│                                          Cancel      Save         │
└───────────────────────────────────────────────────────────────────┘
```

The line under the expression is the whole validation story: it reads back what was understood
and names the next fire. A cron that cannot be parsed says so there, and Save is refused.

Choosing "when a check matches" turns the same dialog into a monitor: `When` becomes how often
to look, and the check appears above the prompt.

```
│  When       [ */15 * * * *                                     ]  │
│             every 15 minutes — next check in 4m                   │
│                                                                   │
│  Fires      ( ) on the clock    (•) when a check matches          │
│                                                                   │
│  Check      (•) a command   ( ) a script on this machine          │
│             [ make test 2>&1                                   ]  │
│             fires when this exits non-zero                        │
│             ── or, with the second option chosen ──               │
│             [ ~/checks/queue_depth.py                      ▾   ]  │
│             [ --threshold 5000                                 ]  │
│             ✓ executable · runs directly, by its shebang          │
│                                                                   │
│  Match      [ optional — a pattern in the output               ]  │
│                                                                   │
│  Prompt     ┌─────────────────────────────────────────────────┐   │
│             │ The tests are failing:                          │   │
│             │                                                 │   │
│             │ {{output}}                                      │   │
│             │                                                 │   │
│             │ Find the cause and fix it.                      │   │
│             └─────────────────────────────────────────────────┘   │
│             {{output}} is replaced with what the check printed    │
│                                                     [ Test now ]  │
```

**Test now** runs the check once, there and then, and reports what it did — exit code, whether
that counts as a match, and the first lines of output. A monitor whose first real check is at
3am is a monitor nobody can debug, and this is one button against that.

### Mobile

A third tab beside Sessions and Notifications, a list, and a full screen per schedule with the
same three tabs the desktop has. `models/schedule.dart`, calls in `services/daemon_api_service.dart`,
providers in `providers/daemon_providers.dart`, a `CacheTarget.schedules` with its `schedule_*`
cases in `providers/cache_effects.dart`, and `screens/schedules_screen.dart`,
`schedule_detail_screen.dart`, `schedule_editor_screen.dart`, `new_schedule_sheet.dart`.

```
┌───────────────────────────────┐   ┌───────────────────────────────┐
│ ←  Schedules              +   │   │ ←  build-watch          ● on  │
├───────────────────────────────┤   ├───────────────────────────────┤
│  ⏰ morning-triage      in 2h  │   │ overview │ runs │ log         │
│  weekdays 09:00 · ~/work/app  │   ├───────────────────────────────┤
│ ───────────────────────────── │   │ Check                         │
│  ◉ build-watch         in 4m  │   │ make test 2>&1                │
│  every 15 min · make test     │   │ fires when it exits non-zero  │
│ ───────────────────────────── │   │ last check 4m ago · exit 0    │
│  ⧗ nightly-migrate     done   │   │                               │
│  once · ~/work/app            │   │ Runs                          │
│    ⇢ test-one       running   │   │ a new session in ~/work/app   │
│    ⇢ test-two       waiting   │   │                               │
│ ───────────────────────────── │   │ Prompt                        │
│  ! standup-note      missed   │   │ The tests are failing:        │
│    [ Run now ]                │   │ {{output}} — fix them.        │
├───────────────────────────────┤   ├───────────────────────────────┤
│  Sessions  Schedules  Notifs  │   │ [Run now] [Test] [Edit] [🗑]  │
└───────────────────────────────┘   └───────────────────────────────┘
```

The **+** opens the same fork the desktop does — describe it and an agent builds it, or set it
up manually — with the same host and agent pickers.

**Mobile does not drag.** Nesting a tree by touch is a fight, and the phone is where a chain is
watched rather than built: the list renders the same indented tree, and a link is made in the
editor's "runs after" picker.

### The missed run, as it arrives

No new card: `helios.question` renders in the question card both apps already have
(`card_registry.dart:29`), which is why the clients need no work for this at all.

```
┌───────────────────────────────┐
│ ⏰ helios                 now │
│ standup-note did not run at   │
│ 08:40 — the daemon was down.  │
│                               │
│  [ Run now ]      [ Skip ]    │
└───────────────────────────────┘
```

## What we are not doing

- **No seconds field, no `@reboot`.** Five fields, minute resolution.
- **No timezone picker.** The zone is captured from the machine when the schedule is saved and
  stored, so DST is handled and a moved laptop does not move the standup note. Choosing a
  *different* zone than the one you are in is a feature with a user behind it.
- **No chaining, no dependencies, no retries.** A failed run is recorded, not retried; the next
  fire is the retry.
- **No natural-language parsing in the daemon.** The sentence becomes a schedule because an
  agent reads it and runs `helios schedule add`. The daemon only ever sees a cron expression.
- **No second tab pattern, and no dialog.** The detail uses `.panel-tabs`, the strip every
  session panel already uses, and everything lives in the sidebar and the main panel.
- **The daemon does not become an MCP client.** A check is a command or a script. Anything that
  needs MCP is work for the agent the fire starts, which already has it.
- **No special session behaviour.** A scheduled run is the existing create-session flow with a
  seeded prompt: no sandbox, no quiet mode, no second kind of session. The one thing it carries
  is a `schedule_id`, and the only code that reads it is the list filter.
- **No second list component.** The runs list is the session list with a filter. If it needs a
  bespoke UI, the filter was the wrong idea.
- **No fan-in.** A job follows one parent. Waiting on two is a graph, and a graph needs a
  scheduler with opinions about partial failure that this one does not have.
- **No check output in the transcript beyond `{{output}}`.** The agent gets what the prompt asks
  for; the full history lives in the schedule's log, not in the session.
- **No catch-up beyond one question.** Ten missed fires ask once.
- **No cross-host schedules.** A schedule lives on the daemon that will run it, like everything
  else in Helios.

## Delivery: one PR, and what was checked before saying so

This is a large feature and it lands in one review, so the question "is that actually possible"
was answered against the code rather than by optimism. Every claim below was verified.

| Seam it needs | State of it | Verdict |
|---|---|---|
| A place to launch a session from the daemon | `provider.Get` → `Launch` → `startTerminal` → `UpsertSession`, duplicated at `api.go:1176-1221` and `api.go:1870-1905` | **Extract first.** The refactor is the first commit, and nothing else starts until the existing paths still work. |
| A background loop with lifecycle | Three already, same shape (`daemon.go:246-315`) | Copy the shape. |
| A place for a new table | `columnMigrations`, append-only, keyed in `_migrations` (`store/store.go:150-205`) | One entry. |
| A session-list filter | `SearchSessions` switch (`sessions.go:282-290`); handler builds the query at `api.go:311-320` | Fits, **but the default belongs in the handler** — the reaper, evictor and MCP call the unfiltered `ListSessions()`. |
| Notification with actions, from the daemon | Types resolve through a provider's Actor (`registry.go:314-330`); clients switch on the part after the dot (`shared/notifications.ts:19`, `card_registry.dart:29`) | `RegisterSystemActor` + `helios.question` payload. **No client work at all.** |
| Log tail over the API | `handleInternalLogs` reads the file and returns lines (`logs.go:87-146`); no streaming anywhere | Reuse the shape, **poll to follow**. Streaming was cut. |
| Session status → chain advance | No central hook; `UpdateSessionStatus` is called from provider hooks | **Poll on the existing tick.** Event plumbing was cut. |
| Drag to nest, in the desktop list | The sidebar already drags sessions into groups with `before`/`inside`/`after` drop modes (`sidebar.tsx:517-580`) | The interaction exists; the chain tree reuses it. |
| Running a command with a timeout | `exec.CommandContext` throughout (`server/git.go:370`, `filesearch.go:468`) | Standard. `Setpgid` and a negative-PID kill are the only new part. |

**Three things were cut to make one PR honest**, and each is a smaller feature that can come
later without changing anything above it: a streaming log route, an event-driven chain trigger,
and drag-to-nest on mobile.

**The order inside the PR.** Each step is runnable, and the daemon is provable before a single
client line is written:

1. Extract the launch and wake-and-type paths. Green tests, and a session still starts from the
   CLI and the desktop.
2. `internal/schedule` — the parser, with its table tests. Pure, no wiring.
3. The table, the store methods, the REST routes.
4. The loop: due, grace, missed, the claiming update. Timers only.
5. `helios schedule` — the whole CLI. **This is where the feature becomes real and testable**:
   everything after it is a client for something that already works.
6. The check runner, `{{output}}`, the per-schedule log, and `once`.
7. Chains, and `schedule_id` on sessions with the list filters.
8. Desktop: list, form, runs list, then the drag tree.
9. Mobile, then the TUI.
10. Agent-driven creation, last, because it depends on the CLI being finished and its `--help`
    being honest.

**How it is proved before it is merged.** Unit tests carry the parser and the loop, but a
scheduler is a claim about time and deserves one real run: a timer set a minute out on the live
daemon, a monitor whose check flips from quiet to matching, and a two-step chain where the first
job is watched going idle and the second starts. The desktop is driven over CDP and the phone
over `adb`, as the mermaid work was.

## Tests

- `internal/schedule`: table tests over each field — ranges, steps, lists, names, `*`, the
  shorthands — and the rejections. `Next` across both DST boundaries, the day-of-month/day-of-week
  OR rule, and February the thirtieth returning "never".
- The loop, by passing `now` rather than sleeping: fires on the boundary of the grace window,
  marks missed outside it, recomputes `next_run_at` from now rather than backfilling, fires
  nothing when disabled, raises exactly one question for a gap holding ten missed fires, launches
  once when the same `now` is swept twice, and still launches when the previous run is live.
- `internal/store`: CRUD against an in-memory database, as `store/groups_test.go` does.
- The check runner: a command that exits non-zero is a match, one that exits zero is not, a
  regex flips both of those, a timeout is a failure rather than a match, output over 32 KB keeps
  the tail, and a `check_file` that is missing or not executable fails with a reason that says
  which.
- Prompt templating: `{{output}}` is substituted, an absent placeholder does not append, and
  `{{output}}` in a timer is rejected at save.
- Chains: a parent going `idle` starts a `success` child, a parent going `error` blocks it and
  starts an `any` child, two siblings both start, a cycle is refused at save, deleting a parent
  pauses its children with a reason, and `terminated` without a preceding `idle` counts as
  failure.
- One-shot: fires at its moment, is marked done, and does not fire twice — including across a
  daemon restart, which is where a `run_at` in the past would be most tempting to re-run.
- The session flag: a fire sets `schedule_id`, the default list excludes those rows,
  `filter=jobs` returns exactly them, and `schedule_id=<id>` returns one schedule's runs.
- The routes, with `httptest`: a bad cron is a 400, `run` fires a schedule that is not due,
  `check` runs the check without firing, `log` returns a tail, and a delete takes the
  schedule's log file and its notifications with it.
- The missed-fire question end to end: a notification with both actions, answerable from the
  public API, resolving through the daemon-owned action table.
