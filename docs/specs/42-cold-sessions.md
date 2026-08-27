# Cold Sessions: Enforce the Memory Budget

## Read this first: eviction was removed twice, on purpose

Do not treat this as a missing feature. It existed, and two commits took it out:

| Commit | What it removed | Stated reason |
|--------|-----------------|---------------|
| `00acdc9` *stop evicting warm sessions on a timer* | the idle TTL | "age is not evidence that nobody wants it, and an eviction costs the host's scrollback ring, which `claude --resume` does not bring back" |
| `a87a893` / #86 *report what sessions cost instead of evicting them* | the LRU at twenty hosts or a quarter of memory | "an eviction costs the host's scrollback, which no `--resume` brings back, and it happened to whichever session the user had not looked at lately" |

The `budget` in `hostStats()` is not an unenforced intention. It is what is left
of the removed feature, kept so clients can display a number.

This spec reverses those two commits. It does so because of one measurement
neither of them states, not because they were forgotten.

### Why the objection no longer holds

Both removals rest on the cost of losing the host's scrollback ring. That ring
is **1 MiB** — `internal/terminal/ring.go:8`, `DefaultRingSize = 1 << 20` — and
it already discards its oldest bytes; `host_test.go` has a case named for it.

Against that, an evicted session frees the whole `claude` process. On the
machine this was written on, the two live sessions were **786 MB** and
**744 MB**.

So the trade is roughly **1 MiB of already-truncating terminal output against
800 MB of resident memory**, and the megabyte is a *rendering of data that
survives anyway*:

```
what a cold session actually loses

  transcript (.jsonl)   survives   →  the agent panel has the whole history
  permission mode       survives   →  ResumeArgs replays it
  conversation state    survives   →  claude --resume reloads it
  terminal scrollback   lost       →  a redraw of the above, ≤1 MiB, already lossy
```

Waking reattaches, and `claude --resume` draws its own UI from the conversation.
The terminal is not left blank; it is left without the previous rendering for
the moment before the agent redraws.

### Two things a future change should not do

**Do not snapshot the ring to disk to "solve" this.** An earlier draft of this
spec proposed writing the 1 MiB out on eviction and replaying it on wake. It is
unnecessary — the scrollback duplicates the transcript — and it invites an
argument about whether the replay is faithful. The honest position is that the
scrollback was never worth the process.

**Do not mark an evicted session `terminated`.** The tmux-era reaper did exactly
that — `db.UpdateSessionStatus(sessionID, "terminated", "StaleReaper")` — which
was a dead end for the session. `ed20e16` fixed it, and `reaper.go` states the
model that must hold:

> Losing a terminal does not end a session. Under the warm pool cold is a normal
> state… Only claude itself, through the SessionEnd hook, terminates a session.

That fix is **not** why eviction was removed. Reinstating eviction must not
reinstate the bug.

## Problem

A session holds a `helios ptyhost`, which holds a running `claude`. Nothing
bounds how many are resident. A machine used for a day holds every agent it has
started, most untouched for hours.

## What already exists

This spec adds a policy. Nearly all the machinery is in place.

| Piece | Where |
|-------|-------|
| Kill a terminal, keep the session | `backend.Backend.Kill` → `dropMirror` + `Registry.Evict` |
| Cold is a normal state | `internal/daemon/reaper.go:18` |
| Bring one back | `POST /api/sessions/{id}/resume`; any prompt wakes a cold session |
| Per-session memory | `Host.Usage() map[string]int64` |
| Pool total and budget | `Host.Status()`, `hostStats()` |
| Resume in the same mode | `Session.PermissionMode` + `ResumeArgs` |
| A 20-minute pass to hang this on | `daemon.go:254` |
| Activity timestamps on the registry | `Registry.Touch`, `entry.lastActive` — kept when `InUse` and `MaxWarm` were deleted |

## Design

### Pressure, not age

Eviction runs only when warm memory exceeds the budget.

`00acdc9` is right that age is not evidence. A time-to-live kills a session you
were about to return to while the machine has twenty gigabytes free, and charges
a context reload for nothing. Under pressure the question is not "is this old?"
but "which can I most afford to lose?".

It runs on a 2-minute pass of its own, not on the 20-minute stale-terminal
reaper. Sharing that tick left the machine sitting over budget for up to
nineteen minutes while the user watched it swap. The check itself is a map the
host already keeps and one query, so it is cheap enough to run often; the
reaper is slow because it probes sockets and re-reads transcripts.

Evicting more often does not make it flappy: a session must have gone unread for
`minIdleBeforeEvict` before it is a candidate at all, so one just woken is out of
reach for five minutes regardless of how often the pass runs.

### The signal is when *you* last looked

`last_event_at` records what the agent did. Nothing records what the human did,
so a session you read thirty seconds ago looks identical to one you have not
opened in a day.

A new `last_interacted_at` on `sessions` records attention:

| Written when | By |
|--------------|-----|
| a session is selected | desktop, mobile |
| its session is in front and the window is focused | desktop, on a slow heartbeat |
| a prompt is sent, or a file or diff is opened in it | any client |

Focus is what makes this better than the `Registry.InUse` check `00acdc9` added
and `a87a893` deleted. A viewer count says a socket is open, so a desktop left
running on one session pinned it warm forever. A focused heartbeat stops
counting when the app is in the background.

### Only `idle` may be evicted

| Status | Evict? | Why |
|--------|--------|-----|
| `idle` | **yes** | nothing is running and nobody is blocked |
| `active`, `starting`, `compacting` | no | the agent is mid-turn |
| `waiting_permission`, `waiting_input` | no | **you** are the blocker; killing it discards the question |
| terminated, archived | n/a | no terminal to take |

Pinned sessions are spared. Pinning already means "I am coming back to this".

A session must also have gone uninteracted for at least five minutes, so one
cannot be evicted moments after it is woken.

### Choosing a victim

```
score = rss * max(minutes_since_last_interacted, 1)
```

Multiplied, not divided. Dividing ranks a large session read a minute ago above
one nobody has opened all day, which is backwards — an earlier draft of this
spec had it that way and the tests caught it.

Highest score is evicted first: expensive to hold, and long unread. A session
with no `last_interacted_at` falls back to `last_event_at`, which makes it a
strong candidate — correctly, since no client has ever shown it.

This is cost-benefit, not recency. A 900 MB session nobody has opened for two
hours frees far more than a 200 MB session read ten minutes ago, and is the less
likely of the two to be wanted next.

Eviction stops as soon as the pool is under budget. It does not evict every
candidate it could.

### The budget is a setting

| Setting | Default | Meaning |
|---------|---------|---------|
| `memory.budget_fraction` | `0.25` | Share of total RAM the warm pool may hold |
| `memory.evict` | `false` | **Opt-in.** Nothing is evicted until this is turned on |

A fraction, not megabytes: the same install runs on a 16 GB laptop and a 64 GB
desktop.

**Eviction is off until asked for.** It kills a running agent and takes its
scrollback, and it reverses a decision made twice before. Upgrading should not
start doing that to somebody's machine — they should choose it, having read
what it costs. The budget only takes effect once the switch is on.

```
Save memory                          [ ]  off by default
  Stops the agents you have not opened lately.

Use up to
  ( ) Quarter of RAM   8.0 GB      recommended
  ( ) Half of RAM      16.0 GB
  ( ) Three quarters   24.0 GB
  ( ) No limit                     nothing is ever evicted
```

The setting is named for what it does for the user, not for the mechanism.
"Cold", "evict" and "warm pool" are how this is discussed in the code and in
this spec; the switch says **Save memory**, because that is the reason somebody
would turn it on.

The resolved size is shown beside each option. "A quarter" means nothing until
it says 8 GB.

### Mobile needs almost nothing

An evicted session already renders correctly on the phone. `a87a893` added the
reporting side to mobile when it removed eviction, and it is still there:

| Already present | Where |
|-----------------|-------|
| `needsRecovery`, and `hasTerminal` | `mobile/lib/models/session.dart:110` |
| An amber "Cold — tap to resume" chip on the card | `sessions_screen.dart:735` |
| The same state in the detail header | `session_detail_screen.dart:854` |
| Per-session memory, blank when cold | `Session.memoryLabel` |

So the only mobile work is the **setting**. The budget belongs to a daemon, not
to a device, and `settings_screen.dart` already reads and writes daemon settings
through `getSettings` / `updateSettings`. Without it you can watch a session go
cold from your phone and have to walk to the machine to change the budget.

Mobile deliberately gets **no eviction notification**. The card already flips to
cold, and a push about memory on a machine you are not sitting at is noise. The
notification is desktop and TUI only.

### Announce it

Broadcast `session_updated`, as a dying terminal already does, plus a
notification:

```
opal-app went cold — freed 840 MB
Not opened for 2h. Your next prompt wakes it.
```

A session that quietly goes cold and then takes eight seconds to answer reads as
Helios being slow. One line prevents that, and makes the first eviction on any
machine explain itself.

## Changes

| File | Change |
|------|--------|
| `internal/store/store.go` | `last_interacted_at` column on `sessions` |
| `internal/store/sessions.go` | `TouchSession`, and the column in the read path |
| `internal/server/api.go` | `POST /api/sessions/{id}/touch`; `hostStats()` uses the configured budget |
| `internal/store/settings.go` | `memory.budget_fraction`, `memory.evict` |
| `internal/daemon/evict.go` | new. Candidate filter, scoring, the loop |
| `internal/daemon/reaper.go` | call it from the existing pass |
| `desktop/.../store.ts` | touch on selection, and on a focused heartbeat |
| `desktop/.../settings.tsx`, `internal/tui/general_settings.go` | the budget control |
| `mobile/lib/screens/settings_screen.dart` | the same budget control; the cold state is already shown |

`backend.Backend` needs nothing new.

## Risks

- **Evicting something being read.** `last_interacted_at` is the mitigation. The
  failure mode is a client that never reports it, making a session a permanent
  candidate — tolerable only because the conversation survives.
- **Reversing a recent decision.** Two commits removed this. If the ratio above
  ever stops holding — a much larger ring, or scrollback that stops duplicating
  the transcript — the argument collapses and this should go again.
- **The budget becoming real on upgrade.** Nobody has felt this number, so
  nothing happens until `memory.evict` is turned on — and the eviction event is
  announced when it does.
- **A status that means "idle but mid-something".** If one is ever added and not
  put on the never-evict list, work is lost.

## Testing

- only `idle` is a candidate; `waiting_permission` is never chosen even when it
  is the largest and longest unread
- pinned sessions are skipped
- a session interacted with inside the minimum age is skipped
- eviction stops as soon as it is under budget
- equal size: the longer-unread session is chosen
- equal time: the larger session is chosen
- no `last_interacted_at` falls back to `last_event_at`, not to "just now"
- `memory.evict` unset evicts nothing under any pressure, and only the exact
  string "true" enables it
- an evicted session keeps its status; nothing writes `terminated`
- `TouchSession` moves a session out of the candidate set
- the fraction round-trips, and an absent setting resolves to `0.25`

## Implementation order

1. `last_interacted_at`, `TouchSession`, and clients reporting it. Nothing
   evicts. Let it run, and check the timestamps match what you would have
   chosen yourself.
2. `internal/daemon/evict.go` — pure functions over a candidate list. No
   backend, no database, all the policy, all the tests.
3. Settings, and `hostStats()` reporting the configured budget.
4. Wire into the reaper pass.
5. The notification.
6. Desktop, TUI and mobile controls.

Step 1 first on purpose: the signal the policy rests on should be collected and
sanity-checked before anything kills a process with it.

## Open

**Total RAM or free RAM?** A quarter of total is predictable. A share of free
adapts to whatever else is running, but the budget then moves under the user.

**Should the desktop touch on scroll?** Selection plus a focused heartbeat may
be enough. Reading a long transcript without clicking is the case that would
slip through.
