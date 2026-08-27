# One Archival State: Terminated Replaces Archived

## The claim

A session has two ways to leave the working list. A person can archive it, or a
person can terminate it. The two mean the same thing to the person who does it:
put this away, I am not working on it.

The code already says so. Three comments name terminated as the archival state:

| Where | What it says |
|---|---|
| `internal/backend/backend.go:109` | "Terminated is the archival state" |
| `internal/daemon/evict.go:196` | "Terminated is the archival state a person chooses" |
| `internal/provider/claude/hooks.go:829` | "Terminated stamps ended_at and reads as archived" |

Each client then carries the cost of the duplicate. Mobile shows an "Archived"
chip at `sessions_screen.dart:470` **and** a separate show-terminated toggle at
line 352. Desktop shows a "Show archived" checkbox at `sidebar.tsx:314` beside a
"Show terminated" one. Two controls, one question.

The TUI has no archive at all. It has never needed one.

And nobody uses it. This developer's own database, the busiest one there is:

| `archived` | `status` | rows |
|---|---|---|
| 0 | terminated | 241 |
| 0 | idle | 7 |
| 0 | active | 1 |

249 sessions, 241 of them put away, and not one by the archive.

## What actually differs today

| | `archived` | `terminated` |
|---|---|---|
| Who sets it | a person, `PATCH archived` | a person via `/terminate`, or the agent's SessionEnd hook |
| Effect on the agent | none | kills the terminal, `Backend.Kill` |
| Undo | `PATCH archived: false` | `POST /resume` |
| `ended_at` | untouched | stamped, `store/sessions.go:136` |
| Eviction | exempt, `evict.go:49` | already cold |

Two rows matter. The rest is bookkeeping.

## What this costs, stated plainly

**You can no longer archive a running agent.** Today you can put a working
session out of sight and let it work. After this, putting it away stops it.

This spec accepts that. A session hidden from the list while its agent still
runs is invisible work, and the sort already drops a quiet session to the
bottom. Anyone who wants a running session out of the way can pin the ones they
want instead.

**Resume is not the same as unarchive.** Unarchive gives back the same live
session. Resume starts a new terminal in the same directory, and the agent picks
the conversation up from the transcript. What is lost is the host's scrollback
ring, which is 1 MiB and already truncating. See
[42-cold-sessions.md](42-cold-sessions.md), which measures that trade.

The UI must therefore say "Resume". It must not say "Restore".

## The change

### Store

Two migrations, appended to `columnMigrations` in `internal/store/store.go` in
this order:

```sql
-- promote_archived_to_terminated
UPDATE sessions
   SET status = 'terminated',
       ended_at = COALESCE(ended_at, datetime('now'))
 WHERE archived = 1;

-- drop_sessions_archived
ALTER TABLE sessions DROP COLUMN archived;
```

`DROP COLUMN` is proven in this codebase. `drop_sessions_tmux_pane` and
`drop_sessions_managed` already use it, on the same `modernc.org/sqlite` driver.

**The promote step must not use the ignore-errors loop.** `store.go:166-175`
throws away the result of every `Exec`, which is right for an `ADD COLUMN` that
may already exist and wrong here. If the `UPDATE` fails, the loop records it as
done and the `DROP` on the next pass destroys the rows it was meant to save.

Run the promote before the loop, or inside it with its error checked and
returned. Only the `DROP` may be ignored.

Drop `archived` from the base `CREATE TABLE sessions` at `store.go:72` as well.
A fresh database then fails both migrations, the runner ignores the errors, and
it records them as done. That is how the two existing drops already behave.

Remove `Session.Archived` (`store/sessions.go:32`) and the column from every
`SELECT` and `UpdateSessionFlags`. `UpdateSessionFlags` becomes
`UpdateSessionPinned`.

### List filter

`ListSessionsFiltered` takes `"all"`, `"pinned"`, and `"archived"`. The third
becomes `"terminated"` and selects `status = 'terminated'`. The default stops
excluding anything.

One visible consequence: sessions that were archived were hidden from the
default list, so the TUI never saw them. They are terminated now, and the TUI
shows terminated sessions in grey. They will appear there. That is correct —
the TUI shows what ended, and these ended.

### API

Remove `archived` from the `PATCH /api/sessions/{id}` body (`api.go:914-932`),
from the SSE payload (`api.go:962`) and from the response (`api.go:969`).
`terminate` and `resume` already exist and do not change.

An older client against a newer daemon reads `archived` as absent, so false. It
then shows a session it used to hide. It shows more, never less. That is the
safe direction to fail.

### MCP

`mcp/tools.go:261` drops to one condition:

```go
if !all && sess.Status == "terminated" {
```

The `all` description at line 100 loses the word "archived".

### Eviction

`evict.go:49` drops `|| sess.Archived`. Nothing becomes evictable that was not
already safe: the line reads `!evictableStatuses[sess.Status] || sess.Archived`,
and `terminated` is not an evictable status. An archived session was exempt, and
as a terminated session it stays exempt by the first half of the same test.

`TestCandidates_SkipsPinnedArchivedAndCold` (`evict_test.go:57`) loses its
archived fixture and becomes `TestCandidates_SkipsPinnedAndCold`.

### Mobile

- `SessionFilter.archived` becomes `SessionFilter.terminated`, and the chip is
  labelled "Terminated".
- The show-terminated toggle above the list goes. The chip answers the same
  question, and one control is enough.
- `_statusOrder` loses its archived rung.
- The batch bar action becomes Terminate. The context menu item becomes
  Terminate or Resume.
- `Session.archived` and `patchSession(archived:)` go.

### Mobile swipe

Right-swipe keeps two states, as it does now:

| Session state | Action | Colour |
|---|---|---|
| terminated | Resume | green |
| anything else | Terminate | teal |

The gesture never dismisses the card. `confirmDismiss` returns false at
`sessions_screen.dart:623`, and that shape stays.

**Terminate asks first only while the agent is mid-turn** — that is, when
`session.isActive`: `active`, `starting`, `compacting`, or
`waiting_permission`. An idle session terminates on the swipe, because nothing
is in flight and Resume brings it back. An agent in the middle of a turn loses
that turn, so it is worth one tap.

Resume never asks.

### Desktop

Desktop is the cheapest of the three, because its header already does what this
spec asks for. `detail.tsx:455` shows Resume when the session is terminated, and
`detail.tsx:465` shows Terminate with a confirm when it is not. Both are already
one click, and the comment at line 463 already argues that ending a session is
common enough to deserve that.

So the Archive item in the overflow menu (`detail.tsx:509-517`) is **deleted,
not replaced**. Putting a Terminate there would be the second Terminate in the
same header.

The sidebar stacks two filters that ask one question:

| Filter | Scope | Lifetime | Where |
|---|---|---|---|
| `showArchived` | all hosts | app session | checkbox, `sidebar.tsx:314` |
| `showTerminated` | one host | app session | link, `sidebar.tsx:221-230` |

Delete the first. `sidebar.tsx:123` goes, `showArchived` leaves the store
(`store.ts:147`, `185`, `711-713`), and the checkbox goes with it. It is held in
memory only and never written to disk, so nothing stale is left behind.

Previously-archived sessions then fall to the per-host control at line 135, get
counted into `hidden` at line 139, and appear behind "Show N terminated". That
is the better of the two controls: it is per host, and it names a count.

Drop `archived` from `shared/models.ts:24` and from the fixture at
`test/session-state.test.ts:18`. `patchSession` takes `Record<string, unknown>`
(`main/api.ts:298`), so no signature changes.

`canResume` and `isTerminated` in `shared/models.ts` already carry the whole
idea, and `models.ts:60` already calls terminated "the one final state". This
spec makes the data match a claim the desktop code has been making for a while.

### Desktop keeps its confirm; mobile does not

Desktop confirms every terminate. Mobile will confirm only mid-turn. That is on
purpose, not an oversight.

The two gestures differ. On desktop the person clicks a button labelled
"Terminate", under a tooltip that says only Resume brings it back. On mobile the
person swipes a card, and the swipe is the same gesture whether it terminates or
resumes. Mobile's rule spends the confirm where the loss is real — a turn in
flight — and stays out of the way for the idle sessions that make up most of the
list.

## Older specs

`session-search-and-group-by-directory.md` documents the `archived` filter, and
`41-helios-mcp-tools.md:107` and `42-cold-sessions.md:149` mention it. Leave
them. They record what was true when they were written. This spec supersedes
them, and a spec edited to match today's code stops being a record.

## Tests

- A store test: a row with `archived = 1` comes out terminated, with `ended_at`
  set, and the column is gone.
- A store test: a row already terminated keeps its original `ended_at`.
- A filter test: `"terminated"` returns the ended sessions, and the default
  returns everything.
- A mobile test: the swipe asks before terminating a mid-turn session, and does
  not ask for an idle one.

  Written against `needsTerminateConfirm` in `models/session.dart` rather than
  as a widget test. `SessionsScreen` needs a populated `HostManager` and a
  stubbed daemon to pump, and a test of that size would be measuring the
  scaffolding. The rule is one predicate, so it is a predicate.
- `evict_test.go`: rename and drop the archived fixture.
- `desktop/test/session-state.test.ts`: drop `archived` from the fixture. Its
  four terminated tests already pass and must keep passing untouched.
