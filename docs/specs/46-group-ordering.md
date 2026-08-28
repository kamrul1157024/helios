# Grouping: A Tree of Manual Groups

## The claim

The session list should be groupable, up to three levels deep, in an order the
person chooses, and it should stay flat until they ask for anything else.

Desktop [PR #104](https://github.com/kamrul1157024/helios/pull/104) groups the
sidebar by project and derives each group's position from the sessions inside
it — "projects take the position of their best session rather than an order of
their own". That gives a group no order of its own, hard-codes one grouping, and
turns it on for everybody.

This spec replaces it with a tree of groups a person makes. A group knows its
own parent, a session hangs off one node, and moving a group is one row written
rather than a rewrite of every session inside it.

## Where we are

| | Today |
|---|---|
| `sessions.sort_order` | one integer per session per daemon, written whole-list |
| Group order | derived, desktop only, not stored |
| Grouping | always on, project only, in PR #104 |
| Mobile | flat list, no grouping — `mobile/lib/screens/sessions_screen.dart` |
| TUI | no grouping |

`POST /api/sessions/order` takes the full list and renumbers it `0..n-1`
(`internal/store/sessions.go:355-374`), then broadcasts `session_updated` so
every other client refetches (`internal/server/api.go:1066-1070`).

## Relationship to PR #104

This lands on top of `feat/sidebar-project-groups` and is merged after it. Most
of that branch is kept — it did the row, the menu and the chrome, and only its
grouping model is replaced.

**Kept as-is.** `SessionRow` and its two-line layout
(`sidebar.tsx:586-745`), the density toggle, the search field and its clear
button, `SelectionMenu`, `sessionActions`, `HostMeter`, `byHand` and
`compareRows` (`sidebar.tsx:754-771`), the new icons, and the detail-header
cleanup that moved a session's actions onto its row.

**Changed.**

| What | From | To |
|---|---|---|
| `grouped` memo (`sidebar.tsx:145-190`) | flat `Map<string, Project>` per host | a tree built from rank vectors |
| Rendering | `grouped.map` → `projects.map` → `rows.map` | one recursive node component |
| `Drag` (`sidebar.tsx:77-81`) | `{hostId, projectKey, sessionId}` | carries the whole group path |
| `accepts` (`sidebar.tsx:607`) | same project key | same full path |
| `folded` (`sidebar.tsx:107`) | `${hostId}:${projectKey}` | keyed by path, and its comment fixed — it says "path" today but holds the folded name |
| `.project-rows` indent | `padding-left: 8px`, one level | computed from depth |
| Sort toggle | a two-state button | the picker popover |

**Removed.** `desktop/src/renderer/components/projects.ts` — `placeOf` derives a
group from `session.project` or the basename of `cwd`, which is the model this
spec replaces. `tintOf` survives, re-keyed on the group key, because a manual
group wants a badge colour just as much.

**Resolved by the replacement**, rather than fixed on #104 itself — the three
issues raised in review:

1. Two directories sharing a basename merged into one group, and the group's
   `cwd` was then whichever session sorted first. Groups are now explicit, so
   nothing folds by accident.
2. The `folded` comment described a key the code did not build. The key changes
   here anyway.
3. There was no way to turn grouping off. It is now off by default.

**The project `+` button stays, with a defined seed.** #104 hands the dialog
`project.cwd`, which was ambiguous because a project could span directories.
Here it seeds from **the most recent session's `cwd` in that group**. That is
deterministic, and it composes with the inheritance rule: a session started in
that directory inherits that directory's groups, so `+` on a group puts the new
session in the group. The `seed` plumbing through `app.tsx` and the `seeded` ref
in `newsession.tsx` is kept unchanged.

## The model

### Groups are a tree, and a session hangs off one node

A group is something a person makes, and it knows its own parent. Nesting is a
property of the group, not of the session: a session attaches to exactly one
node, and its path is whatever walking that node's parents gives.

```
  groups                                  sessions
  key        name      parent    pos      id    group_key
  ─────────  ────────  ────────  ───      ────  ─────────
  g_work     Work      —         0        7f3a  g_opal
  g_opal     opal-app  g_work    0        91bc  g_opal
  g_be1      backend   g_opal    0        a4d1  g_be1
  g_helios   helios    g_work    1        c2e9  g_be2
  g_be2      backend   g_helios  0        f7aa  NULL
```

**A name may repeat.** Identity is the key, so `backend` under `opal-app` and
`backend` under `helios` are two nodes that happen to share a label. An earlier
draft keyed nesting off an ordered list on each session, which forced those two
into one group and let a single group render at two different depths. Both
problems were the same mistake: nesting stored in the wrong place.

**Position is among siblings**, not global. A group's place is a number
alongside the others under its parent, which is what makes moving one a single
row write.

### Creating and moving

Creating is one insert with a parent — "New group under Work" needs no
rearranging of anything that already exists. Moving a subtree is one
`UPDATE parent_key`, and everything beneath it follows because nothing beneath
it recorded its own depth.

### Deleting reparents

Deleting a node never orphans anything and never deletes work:

```
  before                     delete opal-app          delete Work
  ▾ Work                     ▾ Work                   ▾ opal-app
    ▾ opal-app                 ▾ backend                ▾ backend
      ▾ backend                · 7f3a                     · a4d1
        · a4d1                 · 91bc                 ▾ helios
      · 7f3a                   ▾ helios                 …
      · 91bc                     …                     · 7f3a  ← unassigned
    ▾ helios                                           · 91bc  ← unassigned
```

- **Child groups take the deleted node's parent.** Remove a parent and its
  subgroups become roots, which is what "the level above is gone" should mean.
- **Sessions on the deleted node take its parent too**, and fall to unassigned
  when there was no parent.

Nothing is destroyed but the node itself, so a delete is recoverable by hand
rather than being a decision about someone's sessions.

### Levels

How deep the sidebar nests is a client-side choice, and **it is none by
default** — the list is flat and looks exactly as it does today. Three is the
depth the picker offers; the tree itself has no limit, because a cap on how deep
you may build is a UI opinion and not a fact about the data.

### Directory

`Directory` is a grouping level like any other, and it lives in the same ordered
list in the picker. Its index there is the depth it nests at: first and it
gathers, last and it splits. The only thing setting it apart is where its value
comes from — the session already carries its `cwd`, so the daemon never stores
this one, and it is client-side configuration rather than a row.

### Ordering

A session's place is a vector: its group's position at each level from the root
down, then its own order last. Compare element by element; the first difference
wins.

```
  session   path                        vector
  ───────   ─────────────────────       ────────────
  a4d1      Work › opal-app › backend   (0, 0, 0, 0)
  7f3a      Work › opal-app             (0, 0, ∞, 0)
  c2e9      Work › helios › backend     (0, 1, 0, 0)
  f7aa      —                           (∞, ∞, ∞, 3)
```

`∞` is a level the session does not reach: it sorts last, so subgroups sit above
loose sessions and unassigned sessions fall to the bottom. Turning grouping off
drops the leading elements and leaves `sort_order`, which is what the list
sorted on before any of this.

Dragging `helios` above `opal-app` writes one row — their positions swap under
`Work`. Every session beneath both moves, and none of them is written.

## Storage

Two columns added, one table. Migration entries go in the existing
`columnMigrations` list in `internal/store/store.go:146`.

```sql
CREATE TABLE IF NOT EXISTS groups (
  key        TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  parent_key TEXT REFERENCES groups(key) ON DELETE SET NULL,
  position   INTEGER NOT NULL
);

ALTER TABLE sessions ADD COLUMN group_key TEXT;
```

`ON DELETE SET NULL` is the floor, not the rule: the reparenting above is done
explicitly in one transaction, because a child should rise to its grandparent
rather than to the root. The constraint only guarantees that a crash between
statements cannot leave a row pointing at a group that is gone.

No host column. The daemon is per machine, so all of this is scoped to that
machine already, which is why `sessions` has none either.

`sessions.sort_order` stays as it is. It becomes a position *within* a group
rather than across the host — see **What this costs**.

**Membership is a snapshot.** A session keeps the group it was filed under even
after the tree around it is rearranged, and a delete moves it exactly one level
up. A session's history should not be rewritten under it.

**New sessions inherit.** On create, a session copies `group_key` from the most
recent session with the same `cwd`. File a directory once and every later agent
started there joins on its own — including every worktree session, which is what
keeps manual grouping from needing an action per session.

### Cycles

Moving a node under its own descendant would make a loop that no walk
terminates. The move is refused: a parent chain is walked before the write, and
a `parent_key` that appears in it is rejected.

## API

### Reading

Ranks ride on the session list, so a client makes one request and does no join:

```
GET /api/sessions?grouped=1
GET /api/sessions?grouped=1&group_key=g_work
```

```json
{"sessions": [
  {"session_id": "7f3a", "sort_order": 0, "group_key": "g_opal",
   "group_path": [
     {"key": "g_work", "name": "Work",     "position": 0},
     {"key": "g_opal", "name": "opal-app", "position": 0}
   ]}
]}
```

`group_key` is what the session stores; `group_path` is that node and its
ancestors, outermost first, resolved by the daemon. Normalized in storage,
denormalized on the wire — the client walks no parents of its own and `rankOf`
reads straight off the path.

Omit `grouped` and the field is absent — the default, and every existing caller
is untouched. `group_key` as a query parameter filters to sessions in that group **or any
group beneath it**, since asking for a branch means asking for what is under it.
It sits alongside the existing `cwd` filter on
`SearchSessions(query, status, filter, cwd)` (`internal/store/sessions.go:222`).

Implementation reads the whole `groups` table once — it has a handful of rows —
and resolves each session's path in Go. A recursive CTE would answer the same
question in SQL and cost a join per query for a table that fits in a map.

Names and positions are served rather than derived: only the daemon knows the
tree, and no client should keep its own copy of it.

**The server does not order the result.** It keeps
`ORDER BY COALESCE(last_event_at, created_at) DESC` (`sessions.go:264`), which
every client already re-sorts over. Three reasons the final sort stays client
side:

1. **Optimistic drag.** `reorderSessions` applies locally and posts behind it,
   because "a card that snaps back while the daemon answers reads as a drag that
   failed" (`desktop/src/renderer/store.ts:387-397`).
2. **The server does not know how many levels the client is showing.**
3. **Sort mode is per host**, already applied client side.

### Writing

```
POST   /api/groups              {"name":"backend","parent":"g_opal"}  → {"key":"g_…"}
PATCH  /api/groups/{key}        {"name":"Client work"}
PATCH  /api/groups/{key}        {"parent":"g_helios"}    # moves the whole subtree
DELETE /api/groups/{key}                                 # reparents, never orphans
POST   /api/groups/order        {"parent":"g_work","order":["g_helios","g_opal"]}
PATCH  /api/sessions/{id}       {"group":"g_opal"}       # null clears it
```

`POST /api/groups/order` orders **one parent's children**, since position is
among siblings. It writes them in one transaction and renumbers `0..n-1`,
mirroring `SetSessionOrder`, then appends any sibling the client did not mention
— one hidden behind the terminated filter would otherwise land unranked.

A `PATCH` that sets `parent` walks the target's ancestors first and refuses a
key it finds there: a node moved under its own descendant is a cycle no walk
terminates.

Session assignment rides on the existing `PATCH /api/sessions/{id}`, which
already takes `pinned` and `title`. `group` is a single key; the handler rejects
one that names no group, and `null` clears the session.

Deleting a group reparents its children and its sessions to its own parent, in
the same transaction — see **Deleting reparents**.

Every write broadcasts, for the reason the session route already gives — every
client is looking at the same list:

```go
sh.SSE.Broadcast(SSEEvent{Type: "session_updated", Data: map[string]interface{}{"groups": true}})
```

`session_updated` rather than a new type, because the ranks arrive on the
session list: a client that refetches sessions has already picked them up.

Routes register beside the existing ones in `internal/server/server.go:192`.

## Desktop

### The list

```
  ▾ mac-studio                                          14
    ▾ Work                                               9
      ▾ opal-app                                         6
        ┌────────────────────────────────────────────┐
        │▌● Active   fix ingest retry            2m  │
        └────────────────────────────────────────────┘
        ▸ backend                                      3
      ▾ helios                                          3
        ┌────────────────────────────────────────────┐
        │▌● Active   wire group-by               2m  │
        └────────────────────────────────────────────┘
    ▾ Ungrouped                                          5
```

**A level renders only when it splits something.** A group holding one child
group shows no header for it — the row would repeat what the row above already
said. The same rule answers the host header when only one host is configured.

**Ungrouped is synthetic**, computed rather than stored, hidden when empty, and
sorts last because its slots are `∞`.

Indentation is computed from depth. Today the CSS hard-codes one level —
`.project-rows { padding-left: 8px }` — which becomes `depth * 8`.

### The picker

One popover off the `⇅` toolbar button. Default says nothing is grouped:

```
  not grouped                          grouping on
  ┌──────────────────────────┐         ┌──────────────────────────────┐
  │ ☐ Group sessions         │         │ ☑ Group sessions             │
  │ ──────────────────────── │         │ ──────────────────────────── │
  │ ORDER SESSIONS BY        │         │ ORDER GROUPS BY              │
  │   ● Activity             │         │    ● Activity                │
  │   ○ Manual — drag        │         │    ○ Name A→Z                │
  └──────────────────────────┘         │    ○ Manual — drag to move   │
                                       │ ──────────────────────────── │
                                       │ ORDER SESSIONS BY            │
                                       │    ● Activity                │
                                       │    ○ Manual — drag to move   │
                                       │ ──────────────────────────── │
                                       │ Manage groups…               │
                                       └──────────────────────────────┘
```

- **A block is shown only when it applies.** Turning grouping off removes the
  groups block rather than greying it: a disabled control advertises something
  the user cannot reach.
- **Every list is named** — `ORDER GROUPS BY`, not a bare `GROUPS` — so the
  repeated `Activity` rows are never ambiguous.
- **`Name A→Z` is not offered for sessions.** A session's `title` is null until
  a person or the model supplies one, so the list would reshuffle as titles
  arrive.
- **One ordering for all three slots.** Groups share a single position column,
  so there is one `ORDER GROUPS BY`, not one per level.

### Interaction

- **Assigning** comes off the row's context menu, which PR #104 has just built:
  *Groups ▸* with the existing groups, a checkmark on the ones this session
  holds, and *New group…*. Picking one puts it in the first free slot; the
  submenu greys a group the session already holds.
- **Group drag** is offered only under `Manual`, and posts
  `POST /api/groups/order`. Under Activity or Name the header is not draggable,
  because a drag whose result is immediately overwritten by a sort is a drag
  that did nothing.
- **Session drag** stays confined to the innermost group — a drop is legal only
  when the target shares the dragged session's whole group list.
- **Optimistic writes** everywhere, as `reorderSessions` already does.
- **Materialising ranks.** Choosing `Manual` posts the arrangement currently on
  screen, freezing the derived order into positions. Every later drag is then a
  permutation of positions that already exist — which is what `setManualOrder`
  already does for sessions.

### Threading `grouped` through

Three files, not one. Missing the middle one fails silently:

| | |
|---|---|
| `desktop/src/renderer/bridge.ts:180` | `listSessions(params)` — add the flag |
| `desktop/src/main/ipc.ts:21` | `listSessions` is in an allowlist of permitted methods |
| `desktop/src/main/api.ts:136` | builds the query via `queryString` at `api.ts:499-503` |

### What persists where

| | Lives in | Shared across clients |
|---|---|---|
| Session order | daemon, `sessions.sort_order` | yes |
| Group order | daemon, `groups.position` | yes |
| Group membership | daemon, `sessions.group_key` | yes |
| Sort mode | daemon, `setSortModeEverywhere` | yes |
| Grouping on/off, density | client | no |

The arrangement belongs to the daemon; the way you look at it belongs to the
client. A phone and a 27-inch monitor want different densities and plausibly
different depths.

## Mobile

Nothing is required. Mobile omits `grouped`, the three fields never appear, the
vector is one element long, and it sorts on `sort_order` exactly as it does now.

When grouping lands there it adds the flag to the call it already makes and
inherits everything — groups, membership and order are daemon state, so a group
made on the desktop is a group on the phone.

```
        desktop                                   mobile
           │  drag "Work" above "Personal"          │
           │  POST /api/groups/order                │
           ▼                                        │
    ┌──────────────────────────────┐                │
    │  daemon (one per machine)    │                │
    │    groups                    │                │
    │    sessions.group_key        │                │
    └──────────┬───────────────────┘                │
               │  SSE session_updated               │
               └────────────────────────────────────▶ GET /api/sessions?grouped=1
                                                       → same ranks, same order
```

## What this costs

**`sort_order` stops being a total order over the host.** It becomes a position
within the innermost group. Turn grouping off and the flat list is `sort_order`
alone, which will not match the arrangement the grouped view showed. Two views,
two answers — and it is what lets a group move by writing one row instead of
every session inside it.

**Groups need setup.** Nobody gets grouping for free. The default is the flat
list, and the inheritance rule means the setup cost is one assignment per
directory rather than one per session.

**A group can render at two depths** if it is assigned to different slots on
different sessions. Accepted above.

## Open decisions

1. **Deleting a group with sessions in it.** Clear the key from them silently,
   or refuse until they are reassigned?
2. **Can Ungrouped be dragged**, or is last the only sensible place for it?

## Files changed

**Backend**

- `internal/store/store.go` — `create_groups_table` with `parent_key`,
  `add_sessions_group_key`.
- `internal/store/groups.go` (new) — `CreateGroup(name, parent)`, `RenameGroup`,
  `MoveGroup` with the cycle check, `DeleteGroup` reparenting children and
  sessions, `SetGroupOrder(parent, keys)`, `ListGroups`.
- `internal/store/sessions.go` — `SearchSessions` grows `grouped bool` and
  `groupKey string`, the latter matching a group or anything beneath it;
  `GroupKey` and `GroupPath` on `Session`, both `omitempty`; `SetSessionGroup`;
  inheritance in `UpsertSession`.
- `internal/server/api.go` — the group handlers, `groups` on
  `handlePatchSession`, `grouped`/`group_key` on `handleListSessions`,
  broadcast on write.
- `internal/server/server.go` — register the group routes.

**Desktop**

- `desktop/src/renderer/components/grouping.ts` (new) — `rankOf`, `byRank`, the
  tree builder, the render-only-when-it-splits rule. Absorbs `tintOf`.
- `desktop/src/renderer/components/projects.ts` — deleted; `placeOf` is the
  model being replaced.
- `desktop/src/renderer/components/group-picker.tsx` (new) — the popover.
- `desktop/src/renderer/components/sidebar.tsx` — recursive nesting from the
  rank vector, draggable group headers, depth-computed indentation, drop
  confined to the innermost group.
- `desktop/src/renderer/components/selection-menu.tsx` — `MenuAction` gains a
  submenu, for *Groups ▸*.
- `desktop/src/renderer/store.ts` — `groups`, `grouping`, `reorderGroups`,
  `setSessionGroups`, the flag on the sessions fetch.
- `desktop/src/renderer/bridge.ts`, `src/main/ipc.ts`, `src/main/api.ts` — the
  new calls and the flag, through all three.
- `desktop/src/renderer/styles.css` — depth-computed indentation.
- `desktop/src/shared/models.ts` — `groups: {key,name,position}[]` on `Session`.

**Mobile**

Nothing. When grouping lands: the flag in
`mobile/lib/services/daemon_api_service.dart`, the three fields in
`mobile/lib/models/session.dart`, and the same comparison in
`mobile/lib/screens/sessions_screen.dart`.
