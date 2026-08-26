# Shared Resource Leases: One Docker Stack, Many Agents

## Problem

A project has one shared thing that only one agent can use at a time. The case
that prompted this is the `opal-app` docker stack. To test a change, an agent
must check out its branch, bring the stack up, and wait for it to be healthy.

Two agents that do this at once destroy each other's test run. The second
checkout moves the working tree under the first agent's feet. Neither agent can
see the other, so neither can wait for it.

Today the only fix is the human. The human remembers who is testing, and tells
the other sessions to hold. That does not scale past two sessions, and it fails
completely when the human is not watching.

The daemon is the only process that sees every session. It must own this.

## What already exists

This spec adds a scheduler. Most of the parts are already built.

| Piece | Where |
|-------|-------|
| Per-session MCP tools, caller identity included | `internal/mcp/tools.go`, `call(sessionID, args)` |
| Block an agent until something answers | `notifications.Manager.Register` + `WaitForDecision` |
| Ask a human on the terminal, phone and desktop at once | `internal/hitl`, `notify.Show` |
| Push state to every client | `server.SSEBroadcaster` |
| Detect a dead terminal | `daemon.reapStaleSessions` |
| Wake a cold session with a prompt | `POST /api/sessions/{id}/resume`; spec 42 |
| Run a command in a watchable terminal | `internal/terminal`, `internal/backend` |

Nothing here needs a new transport, a new database, or a new UI shell.

## Design

### Shape

```
   agent A            agent B            agent C            human
      │                  │                  │                 │
  helios_lease      helios_lease       helios_lease      desktop queue
      │                  │                  │                 │
      └──────────────────┴───────┬──────────┴─────────────────┘
                                 │
                        ┌────────▼─────────┐
                        │  lease manager   │   ordering, clocks, grants
                        │  internal/lease  │
                        └────┬────────┬────┘
                             │        │
             ┌───────────────┘        └────────────────┐
             │                                         │
    ┌────────▼─────────┐                     ┌─────────▼────────┐
    │ resource runner  │                     │  SSE + HITL      │
    │ git switch       │                     │  queue to every  │
    │ docker up        │                     │  client, prompts │
    │ health wait      │                     │  to the holder   │
    └────────┬─────────┘                     └──────────────────┘
             │
    ┌────────▼─────────┐
    │  opal-app stack  │
    └──────────────────┘
```

The agent never runs `git checkout` or `docker compose up`. The daemon runs
them. There are two reasons. The switch must be idempotent across agents, and
the second agent that wants the branch already up must skip it. An agent cannot
know that; the daemon can.

### Capacity, not a lock

A resource has a **capacity**: how many branches it can host at once. A lock is
the case where capacity is 1.

Capacity in v1 is 1. The schema still carries a `slot` column. This is the only
forward-looking piece in this spec, and it is here because the grant query is
the hard part to retrofit. Adding a column later is cheap. Rewriting the
scheduler later is not.

```
resource opal-docker  (capacity 2, illustration only — v1 ships capacity 1)

  slot 0   branch feat/checkout-fix    holders: sess-a (write)
  slot 1   branch main                 holders: sess-b (read), sess-c (read)
```

### Two levels of sharing

Do not confuse them.

- **Slots** are separate stacks. Each slot runs one branch. Capacity bounds them.
- **Sharers** are agents inside one slot. They all use the same branch and the
  same containers.

A lease is granted onto slot S when either condition holds:

1. Slot S has no holders. The runner switches it to the requested branch.
2. Slot S already runs the requested branch, the new lease is `mode=read`, and
   every current holder of S is `mode=read`.

A writer never shares. A writer runs migrations, resets fixtures, or seeds data,
and a reader that sees that mid-run gets a false failure.

Rule 2 is the largest win in practice. Several agents often test the same
branch. Today the second one waits for a rebuild it does not need.

### The daemon owns the switch

A resource is configuration, not code:

```yaml
resources:
  opal-docker:
    root: ~/workspace/opal-app
    setup: make test-stack-up BRANCH=${branch}
    health: curl -fsS localhost:3000/healthz
    teardown: make test-stack-down
    capacity: 1
    hold_ttl: 20m
    idle_ttl: 5m
    setup_deadline: 5m
```

The runner executes `setup` in a Helios-owned terminal, not a bare `exec.Cmd`.
A stack build takes minutes and it fails often. The human must be able to watch
it, and the desktop app can already show a terminal.

### Four clocks, four different failures

Each clock exists for one failure. Do not merge them.

| Clock | The failure it catches | On expiry |
|---|---|---|
| Setup deadline | A branch that will never build blocks the whole queue | Fail the grant. Requeue that lease at the back. Grant the next waiter. |
| Liveness | The holder's session died | Release at once |
| Idle | The agent took the lease, then went to do something else | Warn once, then release |
| Hold TTL | The agent tests forever | Escalate. See below. |

```
  acquire        granted                                     TTL
    │               │                                         │
    ├── queued ─────┼──────────── held ───────────────────────┼──────►
    │               │                                         │
    │◄─ setup ─────►│◄── idle clock resets on holder activity ┤
    │   deadline    │                                         │
    │               │                              queue empty ──► extend, silent
    │               │                              queue busy  ──► prompt holder
    └── liveness: the reaper releases at any point in this line
```

### Pressure exists only when someone waits

This is the rule that makes the TTL humane, and it is the one to preserve if the
rest of this spec changes.

When the hold TTL expires and the queue is empty, the lease extends itself.
Nothing is announced. Nobody is waiting, so the expiry means nothing.

When the hold TTL expires and someone is queued, the daemon asks. It raises a
HITL prompt on the holder's terminal and a notification on the phone:

```
┌─ opal-docker ─────────────────────────────────┐
│ Held 20m on feat/checkout-fix. 2 sessions     │
│ are waiting.                                  │
│                                               │
│   > Extend 10 minutes                         │
│     Release now                               │
│                                               │
│ ↑↓ select · enter confirm · esc cancel        │
└───────────────────────────────────────────────┘
```

An unanswered prompt releases the lease after 2 minutes. An agent that is
really testing will answer. An agent that cannot answer is the case the release
is for.

Never kill a test run without asking first.

### Ordering

Order is `(priority ASC, enqueued_at ASC)`. Position is computed by that query.
It is never stored.

Human reordering writes `priority` and nothing else. This keeps drag-and-drop
from fighting the scheduler, and it makes the reorder a single-row update that
any surface can perform.

There is no automatic aging in v1. A human who can reorder can also starve
someone by accident. The fix is to show the wait time in the UI, not to add a
second scheduler that argues with the first.

### A human is a holder too

The human can take a lease with no session behind it. It is released by hand.

Without this, an agent takes the stack while you are testing in a browser and
your session dies mid-click. This is a small feature and it is not optional.

### A holder must not be evicted

This meets spec 42 directly.

`evictionCandidates` in `internal/daemon/evict.go` treats every `idle` session as
a candidate. A session that holds a lease is often `idle` — the agent is
waiting while a human clicks through the app. Evicting it kills `claude`, which
releases the lease, which tears the stack down under the human.

Exclude lease **holders** from eviction.

Do not exclude **queued** sessions. A queued session is the best eviction victim
in the pool: it is doing nothing, it will do nothing for minutes, and the grant
already wakes it. Waiting on a queue should make a session *more* likely to go
cold, not less.

### Waiting without spending tokens

An agent must never poll. Polling costs a turn per check and fills the context
with nothing.

There are two waits, and the length decides which one runs.

**Short wait — block inside the tool call.** `helios_lease` does not return until
the stack is up. The agent's turn is suspended. It spends no tokens. This is the
same mechanism a blocking permission hook already uses, and that one routinely
waits minutes for a phone tap.

**Long wait — go cold and get woken.** MCP clients time out a tool call. So
`acquire` takes `wait_seconds`, defaulting below that timeout. If the grant has
not landed by then, the call returns `queued` with a position, and the turn ends.
The session is then a normal idle session; the pool may evict it. On grant, the
daemon sends the session a prompt, which wakes it if cold.

```
 agent A                daemon                      agent B
    │                     │                            │
    │ acquire(feat/x)     │                            │
    ├────────────────────►│ slot free → starting       │
    │                     ├── git switch, docker up    │
    │                     │   health ok → held         │
    │◄── granted ─────────┤                            │
    │                     │        acquire(feat/y)     │
    │  ...testing...      │◄───────────────────────────┤
    │                     │ queued, pos 1              │
    │                     ├───────────────────────────►│
    │                     │            (turn ends, B may go cold)
    │ release             │                            │
    ├────────────────────►│ slot free → starting       │
    │                     ├── git switch, docker up    │
    │                     │   health ok → held         │
    │                     │  prompt: "opal-docker is   │
    │                     │  ready on feat/y"          │
    │                     ├───────────────────────────►│ wakes, continues
```

A third agent that asks for `feat/x` while A holds it is granted immediately as
a sharer, and the middle of this diagram does not run.

### Lease states

```
                  ┌──────────┐
      acquire ───►│  queued  │
                  └────┬─────┘
       cancelled ◄─────┤ (human, or caller gone)
                       │ scheduler picks it
                  ┌────▼─────┐
                  │ starting │  runner: switch, up, health
                  └────┬─────┘
            failed ◄───┤ setup deadline, or health never passes
                       │           (requeued at the back, once)
                  ┌────▼─────┐
                  │   held   │◄── extend
                  └────┬─────┘
                       │ release, idle expiry, TTL escalation, session death
                  ┌────▼─────┐
                  │ released │
                  └──────────┘
```

## The tool surface

One tool, not four. The registry has three tools today and each one earns its
place. Follow the `helios_show` pattern: one discriminator, and errors that
correct the agent so it can retry without a human.

```
helios_lease(
  action:    acquire | release | extend | status
  resource:  "opal-docker"
  branch:    "feat/checkout-fix"      # acquire only
  mode:      read | write             # default read
  reason:    "testing the new cart"   # shown to the human in the queue
  wait_seconds: 25                    # acquire only
)
```

The caller's `sessionID` is implicit, exactly as it is for `helios_show`. This
gives ownership for free. It also means `release` is not load-bearing: the
reaper releases on session death, and the `SessionEnd` hook releases on a clean
exit. A forgotten `release` costs the idle timeout, not the day.

`status` returns the queue as the human sees it, so an agent can say "I am
second, behind the payments session" instead of going quiet.

## Desktop

One panel per resource. It reads the same SSE events every other client reads.

```
┌─ opal-docker ──────────────────────── open ── [drain] [hold for me] ─┐
│                                                                       │
│  HOLDING   feat/checkout-fix                                          │
│    sess-a  "cart regression"       write   14m held · 6m left  [end]  │
│                                                                       │
│  WAITING                                                              │
│  1 sess-d  main                    read     8m waited  [top] [drop]   │
│  2 sess-b  feat/search-rank        write    3m waited  [top] [drop]   │
│                                                                       │
│  stack:  building ──────────────►  healthy      [watch terminal]      │
└───────────────────────────────────────────────────────────────────────┘
```

`drain` stops new grants without disturbing the holder. Use it before you take
the machine back. `hold for me` is the human lease.

Mobile gets the read-only view and the TTL prompt. It already receives the
notification.

## Data model

```sql
CREATE TABLE resources (
  name              TEXT PRIMARY KEY,
  root              TEXT NOT NULL,
  setup_cmd         TEXT NOT NULL,
  health_cmd        TEXT NOT NULL DEFAULT '',
  teardown_cmd      TEXT NOT NULL DEFAULT '',
  capacity          INTEGER NOT NULL DEFAULT 1,
  hold_ttl_s        INTEGER NOT NULL DEFAULT 1200,
  idle_ttl_s        INTEGER NOT NULL DEFAULT 300,
  setup_deadline_s  INTEGER NOT NULL DEFAULT 300,
  state             TEXT NOT NULL DEFAULT 'open',   -- open | draining
  created_at        TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE resource_slots (
  resource   TEXT NOT NULL,
  slot       INTEGER NOT NULL,
  branch     TEXT NOT NULL DEFAULT '',
  state      TEXT NOT NULL DEFAULT 'empty',  -- empty | starting | up | failed
  PRIMARY KEY (resource, slot)
);

CREATE TABLE leases (
  id             TEXT PRIMARY KEY,
  resource       TEXT NOT NULL,
  slot           INTEGER,                    -- null until granted
  session_id     TEXT NOT NULL DEFAULT '',   -- empty means a human hold
  holder_label   TEXT NOT NULL DEFAULT '',
  branch         TEXT NOT NULL,
  mode           TEXT NOT NULL DEFAULT 'read',
  state          TEXT NOT NULL,              -- queued|starting|held|released|failed|cancelled
  priority       INTEGER NOT NULL DEFAULT 0,
  reason         TEXT NOT NULL DEFAULT '',
  enqueued_at    TEXT NOT NULL,
  granted_at     TEXT,
  expires_at     TEXT,
  touched_at     TEXT,
  released_at    TEXT,
  release_source TEXT
);

CREATE INDEX leases_queue ON leases(resource, state, priority, enqueued_at);
```

`touched_at` is written by the hook that already fires on tool use
(`internal/daemon/hooks.go`). The idle clock needs no new plumbing.

## HTTP API

| Route | Purpose |
|---|---|
| `GET /api/resources` | List resources, slots, and current holders |
| `GET /api/resources/{name}/queue` | Holders and waiters, ordered |
| `POST /api/resources/{name}/leases` | Enqueue. Used by MCP and by the CLI |
| `POST /api/resources/{name}/drain` | Stop granting. Toggle |
| `POST /api/resources/{name}/hold` | Human lease |
| `POST /api/leases/{id}/extend` | Extend the TTL |
| `POST /api/leases/{id}/priority` | Human reorder |
| `DELETE /api/leases/{id}` | Cancel if queued, release if held |

SSE: `resource_updated` and `lease_updated`. Both carry the whole queue for that
resource. The queue is small, and a client that reconstructs order from deltas
will get it wrong.

## Changes

| File | Change |
|---|---|
| `internal/lease/` (new) | Manager, scheduler, clocks, waiters |
| `internal/lease/runner.go` (new) | Switch, up, health, in a Helios terminal |
| `internal/store/leases.go` (new) | Tables and queries above |
| `internal/mcp/tools.go` | Add `helios_lease`; add it to `toolOrder` |
| `internal/server/` | Routes and SSE events above |
| `internal/daemon/evict.go` | Exclude holders from `evictionCandidates` |
| `internal/daemon/reaper.go` | Release leases for sessions that lost a terminal |
| `internal/daemon/hooks.go` | Update `touched_at`; release on `SessionEnd` |
| `desktop/src/` | Queue panel |
| `mobile/lib/` | Read-only queue, TTL prompt |

The waiter registry copies the shape of `notifications.Manager` — register the
slot before publishing, buffer of one — but not its code. A grant is not a
notification. It has no human decision in the normal path and it writes no
notification row.

## Risks

| Risk | Response |
|---|---|
| The queue deadlocks and every agent stalls | Every wait has a clock. No path waits forever. `drain` plus force-release is the manual override |
| A half-built stack is reported healthy | The health command decides, not the setup exit code. A slot with no health command is `up` on exit 0, and that is the operator's choice |
| The runner and a human both run `git checkout` | The runner refuses to switch a dirty tree, and reports which files are dirty. It does not stash |
| Coalesced readers interfere anyway | `mode=write` is the escape hatch, and it never shares |
| An agent forgets `release` | Idle timeout, TTL escalation, and session death all release it |
| Reordering starves a session | The wait time is shown in every queue view |

## Testing

Table-driven, in the style of `evict_test.go`. No docker in the unit tests: the
runner is an interface, and the tests supply a fake.

- Order: priority beats enqueue time; equal priority is FIFO.
- Grant: an empty slot grants; a busy slot with a different branch queues.
- Coalescing: a reader on the held branch is granted at once with no runner call.
- Coalescing: a writer on the held branch queues.
- Coalescing: a reader queues when the current holder is a writer.
- Setup deadline: the grant fails, the lease requeues once, the next waiter starts.
- Idle: expiry releases; a `touched_at` write inside the window does not.
- TTL, empty queue: extends, announces nothing.
- TTL, non-empty queue: prompts; no answer in 2 minutes releases.
- Death: the reaper releases the lease and grants the next waiter.
- Eviction: a holder is not a candidate; a queued session still is.
- Drain: the holder keeps the lease; no waiter is granted.
- Human hold: no session; survives a reaper pass.

## Implementation order

Each step is a PR that stands alone.

1. Store tables, plus the ordering query and its tests. No behaviour yet.
2. Lease manager with a fake runner: enqueue, grant, release, the four clocks.
   This is where the logic and most of the tests live.
3. HTTP routes and SSE. The queue is now visible with `curl`.
4. The real runner: terminal, git switch, docker, health.
5. `helios_lease` MCP tool, blocking path only.
6. Death and eviction wiring: reaper, hooks, `evict.go`.
7. Cold path: `wait_seconds` returns `queued`, and the grant wakes with a prompt.
8. Desktop queue panel, including reorder and human hold.
9. Mobile read-only view.

Steps 1 to 5 are usable on their own. A single human with two sessions gets the
whole benefit there.

## Open

1. **Do agents ever write while testing?** Migrations, seeds, fixture resets. If
   they do, `mode` matters and the default should be `write`. If testing is
   always browse-and-assert, the default is `read` and coalescing carries most
   of the value.
2. **Can the opal-app stack run twice** on separate ports and compose project
   names? If it can, capacity goes above 1 and the queue mostly disappears.
   That is a better outcome than a fair queue. Check it before step 2, because
   it decides how much of the scheduler is worth building.
3. **Should a queued lease appear on the phone?** It is not a decision, so it is
   not a notification. It may still be worth a quiet entry. Left out of v1.
