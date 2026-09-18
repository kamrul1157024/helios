# Session Forking: A Tree of Conversations, Rooted in One

## Status

Proposed.

## Problem

A session is a straight line. To try a second approach to the same problem the
user starts a new session and retypes everything the first one already knows —
the file tour, the constraint that ruled out the obvious fix, the three dead
ends. The context that took twenty minutes to build is not portable.

What is wanted is a branch: keep everything said so far, then diverge. Two
sessions that share a past and disagree about the future. And because the
second attempt often spawns a third, a fork must be forkable, which makes the
shape a tree and not a pair.

Helios is the right place for this. Both agents it drives can already continue
a conversation under a new identity, and a third will be able to or it will not
be a candidate. What none of them can do is remember that the new conversation
came from the old one, put it in an isolated worktree with the parent's
uncommitted work carried across, and draw the result. That is Helios's job.

## Non-Goals

- **Forking from an earlier message.** Every fork starts from the tip of the
  parent's conversation. Rewinding means Helios writes a truncated transcript
  itself, in a different format per provider, and that is a separate spec.
- **Cross-provider forks.** A Claude session forks into a Claude session. The
  conversation is in the agent's own format and does not translate.
- **Carrying uncommitted work into the fork.** The fork starts at the parent's
  `HEAD`. Replaying a dirty tree means patching across submodules, symlinks,
  file modes and three-way conflicts, and it fails in ways a user cannot act
  on. Committing first is the answer. §2 says what the fork owes the user
  instead.
- **Merging.** Branches never rejoin. Git merges the code; nothing merges the
  two conversations.
- **Cold forks.** A fork starts its agent immediately. See §5.
- **Detaching a fork.** A fork belongs to its parent for life. See §3.

---

## 1. The Model

Three new columns on `sessions`, and one rule.

They go in `columnMigrations` (`internal/store/store.go:153`), each with an id,
because that is how every column since `last_user_message` has arrived. Note
what that loop does: it discards the error and records the migration as done
either way (`store.go:307-316`). A statement that cannot fail on a fresh schema
is the only kind that belongs in it, and three `ADD COLUMN`s qualify.

```go
{"add_sessions_forked_from", `ALTER TABLE sessions ADD COLUMN forked_from TEXT NOT NULL DEFAULT ''`},
{"add_sessions_forked_at",   `ALTER TABLE sessions ADD COLUMN forked_at TEXT`},
{"add_sessions_fork_workspace", `ALTER TABLE sessions ADD COLUMN fork_workspace TEXT NOT NULL DEFAULT ''`},
{"create_sessions_forked_from_index",
    `CREATE INDEX IF NOT EXISTS idx_sessions_forked_from ON sessions(forked_from)`},
```

**`forked_from`, not `parent_session_id`.** That name is taken: the `subagents`
table has a `parent_session_id` (`store.go:86`) and so does `store.Subagent`
(`sessions.go:95`), meaning a different relationship entirely — the Task tool's
children, not a branch. One name for two hierarchies is a trap for whoever
greps next.

`forked_from` is the whole hierarchy. A fork of a fork points at the fork, so
depth costs nothing and needs no new concept. An empty value means a root
session, which is every session that exists today.

`forked_at` is when the branch was taken, and orders siblings. `fork_workspace`
records which of the two modes in §2 was used, because the UI must explain
why two sessions do or do not share a directory.

The rule: **a fork is not an independent citizen of the list.** It has no group
of its own and no sort position of its own. It renders beneath its parent, it
moves when its parent moves, and there is no gesture that separates them. §3
spells out what that costs.

### Session fields

`store.Session` (`internal/store/sessions.go:11`) gains:

```go
ForkedFrom    string  `json:"forked_from,omitempty"`
ForkedAt      *string `json:"forked_at,omitempty"`
ForkWorkspace string  `json:"fork_workspace,omitempty"`
ForkCount     int     `json:"fork_count"`      // direct children, computed
RootSessionID string  `json:"root_session_id"` // self when a root
```

`ForkCount` and `RootSessionID` are computed on read, not stored. A parent needs
the count to draw a badge without fetching its children; every client needs the
root to know which family a row belongs to.

Both `GetSession` (`sessions.go:228`) and `SearchSessions` (`sessions.go:338`)
name their columns explicitly, so both `SELECT` lists and both `Scan` calls
grow. There is no `SELECT *` to ride along on.

### Inheritance must be skipped for a fork

`UpsertSession` files every new row under the group of the newest session in
the same directory (`sessions.go:117`). That is right for a session and wrong
for a fork, whose group comes from its root and whose own `group_key` §3 says
is unused. Worse, spec 61 §2 makes `groupForCWD` repo-aware, so a fork's fresh
worktree would resolve to the repo's group and write a key that every reader is
then told to ignore.

`UpsertSession` skips `groupForCWD` when `ForkedFrom` is set. One branch, and
it keeps the column honestly empty.

### The 1000-row limit can split a family

`SearchSessions` ends `ORDER BY COALESCE(last_event_at, created_at) DESC LIMIT
1000` (`sessions.go:346`). Order by activity and a fork can land inside the
window while its parent falls outside it, or the reverse. Either way a client
receives a row whose `forked_from` names a session it does not have.

Clients must already handle this, because the same happens under a search or a
status filter. The rule: **a row whose parent is absent from the response
renders as a root.** It is the only answer that cannot produce an invisible
session, and it costs one lookup in the client's own map.

---

## 2. What a Fork Copies

The conversation always, and a worktree of its own by default.

```
"worktree"   a new git worktree on a new branch, at the parent's HEAD  (default)
"same"       both sessions share the parent's working tree
```

A fork exists to try a second answer to the same question. Two agents editing
one checkout is not a second answer, it is a race — so the default gives the
fork ground of its own. `same` remains, because a directory that is not a git
repository has no other option, and because a fork that is a follow-up rather
than a rival does not need the isolation.

This makes spec 61's `POST /api/git/worktrees` a prerequisite, not a later
phase. Fork cannot ship before it.

### The branch

Callers may name the branch. Most will not, and a fork that demands a text field
before it happens is a fork nobody takes. When `branch` is absent Helios derives
one from the parent's title — the same slug rule as spec 61 — and appends
`-2`, `-3` and so on until the name is free. A forked-from-a-fork inherits the
chain, so the names stay readable rather than nesting.

The worktree path follows spec 61's convention exactly: a `<repo>-worktrees/`
sibling, one directory per branch.

### What the fork does not get

The parent's uncommitted work stays with the parent. The fork starts at the
parent's `HEAD`.

This is a real edge, and the spec chooses to live with it rather than replay a
diff: **a fork inherits a memory of edits that are not on its disk.** The agent
remembers rewriting `internal/server/api.go`; in the new worktree that rewrite
is not there. The agent will usually discover this the first time it reads the
file, and Helios does not need to pretend otherwise.

What Helios does owe the user is a warning before the fact, not a surprise
after it. When the parent's tree is dirty, the fork sheet says so and names the
count:

> The parent has 7 uncommitted files. The fork starts from the last commit and
> will not see them.

Committing first is the answer, and that is the user's call to make.

### When the parent is not in a repository

`worktree` is impossible. The fork falls back to `same`, records
`fork_workspace: "same"`, and returns a warning saying why. Refusing would be
worse: a session in `/tmp` is still worth forking.

---

## 3. Lineage: Forks Move With Their Parent

A fork is drawn as a child of its parent and cannot be dragged out. This is a
deliberate loss of freedom, and it buys a list that never lies about where a
conversation came from.

The consequences, each of which is a behaviour to implement:

**Grouping is inherited, never assigned.** `group_key` on a fork row is unused
and unwritten. The effective group is the root's group, resolved by walking
`forked_from`. The refusal goes in `SetSessionGroup` (`internal/store/groups.go:286`),
which already returns an error for a key naming no group; `handlePatchSession`
calls it before anything else is written (`internal/server/api.go:877`) for
exactly this reason, and surfaces the message. It maps to `409` rather than the
`400` that handler gives today — the request is well-formed, the target is not
eligible.

**Ordering is inherited too.** `sort_order` on a fork row is unused. A family
sorts as a unit by the root's `sort_order`; within a family, forks follow their
parent, ordered by `forked_at` ascending.

`SetSessionOrder` (`sessions.go:480`) numbers every id it is handed, in one
transaction, so it cannot be the place that filters. `handleSessionOrder`
(`internal/server/api.go:1014`) drops fork ids from `req.Order` before the call.
A client that posts a family's worth of ids then gets the roots numbered and
the forks left alone, rather than a silent renumbering of rows the renderer
ignores.

**Moving the root moves everything.** Dragging a parent into a group carries
every descendant. There is no partial move and no prompt asking about children.

**Deleting a parent reparents, it does not orphan.** A deleted session's
children take its `forked_from` — its own parent, or empty if it was a root.
The family keeps its shape, one level shorter. This is the same rule
`DeleteGroup` already applies to the group tree (`groups.go:129-191`), and it
belongs inside the transaction `DeleteSession` already opens
(`sessions.go:529`), beside the subagent and notification cleanup.

Terminating a parent does nothing to its forks; a terminated session still
holds its place in the tree.

**Cycles are impossible and must still be checked.** Forking can only append a
leaf, so a cycle needs a bug. The reparent path above is where that bug would
live. Every walk up the chain carries a `seen` set and stops, the way
`pathOf` and `ancestorsOf` already do (`groups.go:305`, `groups.go:327`) — a
walk that cannot terminate is worse than one that stops early.

---

## 4. The Provider Verb

Helios owns the tree, the worktree, the lineage and the UI. It asks the provider
for exactly one thing: the argv that continues a given conversation under a new
identity.

```go
// Forker starts a new session that begins with another session's history.
//
// The new conversation must be independent from the first message on: nothing
// said in the fork may reach the parent, and the parent must remain resumable
// under its own id.
//
// Four ids, because unlike Resume this names two sessions. sessionID is the
// new one Helios has just minted. parentSessionID and parentResumeID are the
// source's, and a provider must read them the way Resumer describes: the
// resume id when the agent mints its own, the session id when it accepts
// Helios's. parentResumeID is empty for the second kind.
//
// Returning an empty Argv means this session cannot be forked right now, which
// is not the same as the provider being unable to fork at all.
type Forker interface {
    Fork(sessionID, parentSessionID, parentResumeID, mode string) (Launch, error)
}
```

It sits beside `Resumer` in `internal/provider/provider.go:101`, and like every
capability there it is optional. `Info()` gains `CanFork bool` so a client can
grey the menu item out rather than discover the failure by pressing it. A
provider that does not implement `Forker` answers `501` with the provider name.

**Why four arguments and not three.** `Resume(sessionID, resumeID, mode)` gets
away with two ids because they name the same session — `resumeID` is "this
session, as the agent knows it". A fork names two different sessions, and
collapsing them loses the one thing the provider needs. The naming follows
`Resumer`'s own rule and no new concept is introduced.

The conformance test (`internal/provider/conformance_test.go:142`, which already
covers `Resume`) gains the matching case: a provider claiming `CanFork` must
return non-empty argv when given a parent it could have resumed.

### `resume_id` is empty for Claude, and that is not an error

This is the trap. `Session.ResumeID` is documented as nil whenever the agent
takes Helios's id: *"For Claude they are equal and this is nil"*
(`sessions.go:37-42`). A fork handler that refuses on an empty `resume_id`
would reject every healthy Claude session and accept only Codex ones.

The existing code already models this correctly and should be copied rather
than reasoned about afresh: `claude.Provider.Resume` ignores its `resumeID`
argument entirely and resumes by `sessionID` (`claude/provider.go:68-69`),
while `codex.Provider.Resume` returns empty argv when `resumeID == ""`
because for Codex that genuinely means "never reported in"
(`codex/codex.go:162-171`).

So the handler passes both ids through unresolved and asks the provider. The
`409` in §5 fires on an empty `Launch.Argv` coming back, not on an empty
column.

### The invariant a `Fork` must hold

Helios correlates hooks to session rows through the id it minted. For Claude
that works because launch passes `--session-id` (`register.go:143`). For Codex
it works because Codex mints the id, reports it through the session-start hook,
and `SessionByResumeID` (`sessions.go:621`) finds the row.

A fork must land in one of those two camps and not between them. A provider that
forks into an id Helios never learns produces a session whose hooks land
nowhere — alive in the process table, dead in the UI. This is the one rule a
`Fork` implementation can break quietly, so it is the one the conformance test
should be built around.

Both current providers satisfy it, and neither needs new plumbing:

```
claude   claude --resume <parentSessionID> --fork-session \
                --session-id <newSessionID> --permission-mode <mode>
codex    codex fork <parentResumeID>          env HELIOS_SESSION_ID=<newSessionID>
```

Claude accepts a caller-chosen id for the forked conversation. Verified by
running it: the fork answers from the parent's history, and its transcript is
written under the id Helios supplied. So a forked Claude session keeps
`resume_id` nil like every other Claude session, and `ResumeArgs`
(`register.go:70`) — which takes `sessionID` and ignores `resumeID` — stays
correct as written. Codex is already in the second camp, and the env var its
`Resume` sets (`codex.go:174`) is what ties its report back to the row.

---

## 5. API: `POST /api/sessions/{id}/fork`

### Request

```
POST /api/sessions/4f3c.../fork
Content-Type: application/json

{
  "workspace": "worktree",
  "branch":    "try-a-queue",
  "prompt":    "Forget the mutex. Do it with a queue instead.",
  "title":     "queue variant"
}
```

Every field is optional. `workspace` defaults to `worktree`. `branch` is derived
from the parent's title when absent, per §2. `prompt` is delivered as the fork's
first message. `title` defaults to the parent's title suffixed with the branch
name — then the usual auto-title replaces it once the agent has said something.

The minimum request is an empty body, and it does the right thing.

### Behaviour

1. Load the parent with `GetSession`. `404` if unknown.
2. `501` if the provider does not implement `Forker`.
3. Resolve the workspace. For `worktree`, derive the branch if none was given
   and call spec 61's worktree creation. A failure there fails the request — a
   fork pointed at a directory that does not exist is worse than no fork. The
   one exception is a parent outside a repository, which falls back to `same`
   with a warning.
4. Mint the session id. Call
   `Fork(sessionID, parent.SessionID, deref(parent.ResumeID), parent.PermissionMode)`.
   **`409` if the returned `Argv` is empty** — that is the provider saying this
   particular session cannot be forked, which for Codex means it never reported
   its id. Not a check on the column: see §4.
5. Start the terminal and register the row, following `StartSession`
   (`internal/server/launch.go:75-136`) — `startTerminal`, then `UpsertSession`
   with `status: "starting"`, then `UpdateSessionPermissionMode` from
   `launch.Mode`, then `Pending.Add` for the workspace-trust dialog. The fork
   needs all four, and adds `forked_from`, `forked_at` and `fork_workspace`.
6. Deliver `prompt`, if given, through the existing `awaitAgent` path.
7. Broadcast `session_created` over SSE. The parent's `fork_count` changed, so
   broadcast `session_updated` for the parent too — otherwise a second client
   shows a parent with no children.

`StartSession` takes a `NewSession` struct and returns a `StartedSession`
(`launch.go:52-72`). Fork is a fourth caller of that sequence after the two API
handlers and the scheduler, and the comment at the head of that file is explicit
about what happens when a caller writes its own copy. `NewSession` gains
`ForkedFrom` and `ForkWorkspace`, and the one branch they need lives inside
`StartSession` rather than beside it.

The fork starts hot. A fork exists because someone has a question to ask it, and
a fork that must be woken before it can be asked adds a step to the only path
anyone takes. Forks of forks fan out, and the memory budget in spec 42 is what
answers that — eviction already exists and already understands cold sessions.

### Response

`200`, in the shape `handleCreateSession` already returns (`api.go:1774`) — not
a session object, and not a `201`. Creating a session answers with the handle
and the directory, and a fork is a session; a second shape for the same event
is a second thing for every client to parse.

```json
{
  "success":        true,
  "session_id":     "9b21...",
  "terminal":       "hh-9b21",
  "cwd":            "/Users/x/workspace/helios-worktrees/try-a-queue",
  "forked_from":    "4f3c...",
  "fork_workspace": "worktree",
  "warnings":       ["the parent has 7 uncommitted files; the fork starts from HEAD"]
}
```

### Listing

`GET /api/sessions` returns forks inline, in tree order — a parent immediately
followed by its descendants, depth-first, siblings by `forked_at`. Flat order
with the fields to rebuild the tree is enough for every client and spares each
one its own sort. `?filter=roots` returns roots only, for a caller that wants to
draw its own nesting.

---

## 6. The Tree in the UI

All three clients draw the same thing: a fork is an indented child of its parent
with a `⑂` mark, and a parent with forks carries a chevron and a count.

```
▾ helios                                    ← group, folds (exists today)
    ▾ ● refactor the launch path       2 ⑂  ← family, folds
        ⑂ queue variant
        ▾ ⑂ no-mutex variant           1 ⑂  ← a fork that folds its own forks
            ⑂ no-mutex, in-process
      ● fix the codex hook
    ▸ ● migrate the store              4 ⑂  ← folded: four rows hidden
```

Indentation is per depth and capped at four. Past that the rows stop moving
right and the mark repeats, or a deep family walks off a phone screen.

### Folding

A family folds at every level, not just at the root. The chevron sits on any row
whose `fork_count` is above zero, at the leading edge, and is its own tap target
— the rest of the row still opens the session.

The fold state reuses the machinery groups already have, in the same stores,
under a key that cannot collide with a group path:

| Client | Store | Key |
| --- | --- | --- |
| Desktop | the existing `folded` record, `sidebar.tsx:228` | `${hostId}:fork:${sessionId}` |
| Mobile | `shared_preferences`, as spec 62 | `${hostId}:fork:${sessionId}` |

Which means the phone's folds survive a restart and the desktop's do not,
exactly as spec 62 decided for groups, and for the same reason.

**Default open.** A fork exists because someone just made it, and a fork that
appears already hidden is a fork the user thinks failed. Folding is something
the user does to a family that has grown, not a state families start in.

**Selection unfolds ancestors.** Reaching a fork from search, a notification, a
deep link or the schedules tab must open every fold above it, so the selected
row is on screen instead of behind a chevron. The `automated` block already sets
this precedent (`sidebar.tsx:963`).

**Folded families still count.** A group header's cumulative count includes
hidden forks. A folded family that silently shrinks the number above it is a
number nobody trusts.

### Desktop

Fork lives in the session context menu, as an entry in `sessionActions`
(`desktop/src/renderer/components/session-menu.ts:60`, beside Rename and
Terminate), opening a small
dialog with the same content as the phone's sheet: a prefilled branch field, an
optional first prompt, the dirty-tree warning when it applies, and `same` behind
a disclosure.

A fork row is not a drag handle — `draggable={false}`, the same answer
`sidebar.tsx:990` already gives to automated runs, and for the same reason: the
host's hand-sorted order is one list and a fork is not in it. Dragging a parent
lifts the whole family as one block, with the descendant count on the drag
preview.

### Mobile

The phone needs more than an indent, because its list is built out of index
spaces that a fork row would corrupt. Four changes, in the order they have to
happen.

**1. The model carries the lineage.** `Session` (`mobile/lib/models/session.dart:36`)
gains `forkedFrom`, `forkedAt`, `forkCount` and `rootSessionId`, read in
`fromJson` beside `sort_order` and `group_key` (`session.dart:91`) and added to
`copyWith` (`session.dart:154`).

**2. `grouping.dart` hangs forks off sessions, not off nodes.** Today
`buildTree` files every session onto a node with `node.sessions.add(session)`
(`grouping.dart:163`). A fork must not land there — it belongs under its parent
session, wherever that parent hangs. So:

- `node.sessions` holds **roots only**.
- `node.total` still counts every session, forks included. A folded family that
  shrinks the header's number is a number nobody trusts.
- A new `familyRows(root, forksByParent, folded)` returns the visible rows of
  one family in render order, each with its depth. Recursive, and it stops
  descending at a folded row.

`flattenSessions` (`grouping.dart:265`) stays as it is and keeps returning roots
only. That is correct rather than convenient: the daemon holds one flat order of
roots, and §3 says a fork has no `sort_order`. Posting fork ids through
`SessionsNotifier.reorder` (`daemon_providers.dart:183`) would ask the daemon to
number rows it is about to ignore.

**3. One reorderable item per family, not per session.** In `_nodeSlivers`
(`sessions_screen.dart:933`) the `SliverReorderableList` keeps
`itemCount: node.sessions.length` — roots — and its `itemBuilder` returns a
`Column` of `familyRows`, not a single card. One index is one family, so
"the family moves as a unit" holds by construction and `_onNodeReorder`
(`sessions_screen.dart:168`) needs no arithmetic change.

The flat path does the same: the ungrouped `ReorderableListView.builder`
(`sessions_screen.dart:492`) iterates roots and builds families, and so does the
plain `ListView.builder` beneath it (`sessions_screen.dart:510`).

`_buildSwipeableCard` (`sessions_screen.dart:1197`) gains `depth`, `forkCount`
and `folded`. Only the root row is given a `reorderIndex`, so only the root
wraps in `ReorderableDragStartListener` (`sessions_screen.dart:1521`) — that is
the phone's version of the desktop's `draggable={false}`.

**4. Fork folds get their own set.** `GroupingPrefs.folded`
(`grouping_providers.dart:136`) is keyed `"$hostId:${path.join('/')}"` over
group paths. Forks are keyed by session id, not path, so they get a sibling set
`foldedForks` persisted under `helios.foldedForks` beside `helios.foldedGroups`
(`grouping_providers.dart:171`), with `isForkFolded(hostId, sessionId)` and
`toggleForkFold`. A separate set rather than a shared one with a prefix: one
namespace holding two kinds of key is a collision waiting for the first group
named `fork`.

The chevron on a session row reuses what `GroupHeader`
(`mobile/lib/widgets/group_header.dart`) already draws, at the leading edge and
as its own tap target. The rest of the row still opens the session
(`sessions_screen.dart:1342`).

**Fork sits in the long-press menu.** `_showContextMenu`
(`sessions_screen.dart:1591`) gains a "Fork…" item that opens a sheet in the
same shape as the group menu at `sessions_screen.dart:738`:

```
Fork "refactor the launch path"

  Branch        [ try-a-queue                    ]
  First message [                                ]

  ⚠ The parent has 7 uncommitted files. The fork
    starts from the last commit.

  ▸ Same folder instead of a new worktree

                                    [ Cancel ] [ Fork ]
```

A new worktree is the default and needs no control — the branch field is the
only thing a normal fork asks for, prefilled with the derived name. `same` is
behind a disclosure, because it is the answer to a narrow question and putting
it on the face invites a shared checkout by accident. The dirty-tree warning
appears only when the parent has uncommitted files.

The sheet stays open while the request is in flight, and reports a `409` or a
`501` inline. On success it closes and pushes the new session.

**Lineage in the session screen.** A fork's header shows `⑂ forked from
<parent title>` as a chip that navigates to the parent. The parent's header
shows the reverse as a row of chips, one per direct fork. On a narrow screen
that row scrolls rather than wraps.

### TUI

A keybind on the session list opens the same three-field prompt, and the rows
nest with the same cap. Folding is the same key the group rows already use.

---

## 7. Phases

1. **Store.** Migration, `Session` fields, `ForkSession`, `UpdateSessionParent`,
   the reparent-on-delete path, and derived group and order. Tests for the
   ordering rules in §3, which is where the subtle breakage lives.
2. **Provider.** `Forker`, `CanFork` in `Info()`, and the conformance case that
   checks the id invariant in §4.
3. **Claude and Codex.** One `Fork` each, both argv already known.
4. **API.** `POST /api/sessions/{id}/fork`, branch derivation, the worktree call
   into spec 61, tree order in `GET /api/sessions`, and the `409` on grouping a
   fork.
5. **Desktop.** Nesting, folding, the non-draggable fork row, the fork dialog.
6. **Mobile.** In the order §6 lists them: model, `grouping.dart`, the
   one-item-per-family lists, the fold set. Then the sheet and the lineage
   chips.
7. **TUI.** Keybind and nesting.

**Spec 61 phase 1 comes before phase 4 of this spec.** A fork makes a worktree
by default, so there is no useful version of this that ships ahead of the
endpoint that makes one.
