# Grouping: Manual Groups, One JSON Column, One Rank Vector

## The claim

The session list should be groupable, up to three levels deep, in an order the
person chooses, and it should stay flat until they ask for anything else.

Desktop [PR #104](https://github.com/kamrul1157024/helios/pull/104) groups the
sidebar by project and derives each group's position from the sessions inside
it — "projects take the position of their best session rather than an order of
their own". That gives a group no order of its own, hard-codes one grouping, and
turns it on for everybody.

This spec replaces it with manual groups, a depth the picker caps at three, and
a rank vector that makes a group's position one row rather than a rewrite of
every session inside it.

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

### Groups are manual, and a session holds an ordered list of them

A group is something a person makes. It has a key, a name, and a position.
Nothing is derived, nothing is guessed, and there is no folding rule to get
wrong.

A session carries an ordered list of group keys. **Position in the list is the
nesting level** — first is outermost — and which key goes where is the person's
choice at the moment they assign it. A group is not born at a level and is not
pinned to one.

```
  session          groups
  ───────────      ─────────────────────────────────
  fix ingest       ["work", "opal-app"]
  schema bump      ["work", "opal-app", "backend"]
  wire group-by    ["work", "helios"]
  personal spike   NULL                                ← ungrouped
```

Two invariants, both enforced in the handler rather than by a constraint:

- **The list is dense.** No holes and no nulls inside it; assignment fills the
  next free position. `json_array_length` is therefore the session's depth,
  which makes "render only when it splits" a length check.
- **The keys are distinct.** A session belongs to a group once.

### Levels

How deep the sidebar nests is a client-side choice, and **it is none by
default** — the list is flat and looks exactly as it does today. Three is the
limit the picker enforces, and it is a UI rule, not a storage one: nothing in
the schema caps the list, so a fourth level would be a frontend change and no
migration.

A session with one key nests one deep, one with three nests three. The list is
ragged by construction, and the rendering rule below handles it.

### The rank vector

A session's position is a vector: the position of each group it holds, its own
order last, padded to the depth being rendered. Compare element by element; the
first difference wins.

```
  session          vector                 rendered
  ───────────      ─────────────────      ────────
  schema bump      (0, 1, 3, 0)           Work › opal-app › backend
  fix ingest       (0, 1, ∞, 0)           Work › opal-app
  retry backoff    (0, 1, ∞, 1)           Work › opal-app
  wire group-by    (0, 2, ∞, 0)           Work › helios
  tidy zshrc       (4, ∞, ∞, 0)           Side
  poke at sqlite   (∞, ∞, ∞, 3)           Ungrouped, last

  grouping off  →  (0) (0) (1) (0) (0) (3)   ← sort_order alone
```

`∞` is a level the session does not reach. It sorts last, which puts subgroups
above loose sessions inside the same parent — folders before files — and puts
ungrouped sessions at the bottom of the list. That ordering is a consequence of
the padding rather than a separate decision, and it is the conventional one.

```ts
const UNRANKED = Number.MAX_SAFE_INTEGER

// session.groups arrives as [{key, name, position}], outermost first.
function rankOf(session: Session, depth: number): number[] {
  const path = (session.groups ?? []).map((g) => g.position)
  while (path.length < depth) path.push(UNRANKED)
  return [...path, session.sort_order]
}

function byRank(a: number[], b: number[]): number {
  for (let i = 0; i < a.length; i += 1) {
    if (a[i] !== b[i]) return (a[i] as number) - (b[i] as number)
  }
  return 0
}
```

### One group, two depths

Because the level is per session, the same group can sit at different depths for
different sessions, and then it renders in two places with one position:

```
  session A    ["work", "opal-app"]
  session B    ["opal-app"]

  ▾ work
    ▾ opal-app        ← A
  ▾ opal-app          ← B
```

This is allowed. It takes a deliberately odd pair of assignments to produce, the
result is visible on screen, and preventing it would mean pinning a group to a
level — which is exactly the thing this design does not store.

## Storage

One table and one column. Migration entries go in the existing
`columnMigrations` list in `internal/store/store.go:146`.

```sql
CREATE TABLE IF NOT EXISTS groups (
  key      TEXT PRIMARY KEY,
  name     TEXT NOT NULL,
  position INTEGER NOT NULL
);

ALTER TABLE sessions ADD COLUMN groups TEXT;   -- '["g_work","g_opal"]'
```

A JSON array rather than `group1`/`group2`/`group3`, because **three is a
frontend rule, not a storage fact**. Fixed columns would put a UI decision in
the schema, and the day the picker allows four it is a migration on the busiest
table.

JSON1 is available — verified against the driver this project uses,
`modernc.org/sqlite v1.48.2`, SQLite 3.51.3. All three of the operations this
needs work:

```sql
-- one join per rendered level, generated from the requested depth
LEFT JOIN groups g1 ON g1.key = json_extract(s.groups, '$[0]')

-- membership at any depth
WHERE EXISTS (SELECT 1 FROM json_each(s.groups) WHERE value = ?)

-- indexable if the 1000-row limit ever stops being enough
CREATE INDEX idx_sessions_group0 ON sessions(json_extract(groups, '$[0]'))
```

Not a comma-separated string: that answers "what is in this group" with
`',' || groups || ',' LIKE '%,g2,%'`, which is unindexable and is the same
delimiter trap that makes naive path-prefix matching wrong.

**Position lives on the group, never on the session.** One row moves when a
group moves. Storing it per session would mean updating every session in the
group, giving them all a chance to disagree, and leaving a quiet group with
nowhere to keep its place.

No host column. The daemon is per machine, so all of this is scoped to that
machine already, which is why `sessions` has none either.

`sessions.sort_order` stays as it is. It becomes a position *within* the
innermost group rather than across the host — see **What this costs**.

**Membership is a snapshot, not a rule.** A session keeps the groups it was
assigned even if the person later reorganises. That is deliberate: a session's
history should not be rewritten under it.

**New sessions inherit.** On create, a session copies `groups` from the most
recent session with the same `cwd`. Assign a directory's first session and every
later agent started there joins on its own — including every worktree session,
which is the case that makes manual grouping bearable at all. Exact `cwd` match,
one query, no surprises.

### Sample data

Everything below was run against SQLite, not written by hand.

```
  groups                              sessions
  key        name      position       id    sort_order  groups
  ─────────  ────────  ────────       ────  ──────────  ──────────────────────────────
  g_work     Work      0              7f3a  0           ["g_work","g_opal"]
  g_opal     opal-app  1              91bc  1           ["g_work","g_opal"]
  g_helios   helios    2              a4d1  0           ["g_work","g_opal","g_backend"]
  g_backend  backend   3              c2e9  0           ["g_work","g_helios"]
  g_side     Side      4              d8f0  1           ["g_work","g_helios"]
                                      e551  0           ["g_side"]
                                      f7aa  3           NULL
```

The join, and what it sorts to:

```
  id    title             lvl1  lvl2      lvl3     vector
  ────  ────────────────  ────  ────────  ───────  ────────────────
  a4d1  schema bump       Work  opal-app  backend  (0,1,3,0)
  7f3a  fix ingest retry  Work  opal-app  —        (0,1,999,0)
  91bc  retry backoff     Work  opal-app  —        (0,1,999,1)
  c2e9  wire group-by     Work  helios    —        (0,2,999,0)
  d8f0  ptyhost race      Work  helios    —        (0,2,999,1)
  e551  tidy zshrc        Side  —         —        (4,999,999,0)
  f7aa  poke at sqlite    —     —         —        (999,999,999,3)
```

Two things to read off it:

- `a4d1` and `7f3a` both have `sort_order = 0` and do not collide, because
  `sort_order` is now a position *within* the innermost group.
- Dragging `helios` above `opal-app` is `UPDATE groups SET position` on two
  rows. Five sessions move, and none of them is written.

## API

### Reading

Ranks ride on the session list, so a client makes one request and does no join:

```
GET /api/sessions?grouped=1
GET /api/sessions?grouped=1&group_key=g_work
```

```json
{"sessions": [
  {"session_id": "7f3a", "sort_order": 0,
   "groups": [
     {"key": "g_work", "name": "Work",     "position": 0},
     {"key": "g_opal", "name": "opal-app", "position": 1}
   ]}
]}
```

One list of objects, outermost first — not three parallel arrays that have to
stay index-aligned. Normalized in storage, denormalized on the wire: the client
gets `(key, name, position)` together and needs no lookup table of its own, and
`rankOf` reads straight off it.

Omit `grouped` and the field is absent — the default, and every existing caller
is untouched. `group_key` filters to sessions holding that key at any depth, via
`json_each`, alongside the existing `cwd` filter on
`SearchSessions(query, status, filter, cwd)` (`internal/store/sessions.go:222`).

Implementation is one `LEFT JOIN groups ON key = json_extract(s.groups,'$[N]')`
per rendered level, generated from the requested depth rather than hard-coded.
`SearchSessions` is a plain WHERE-list builder with no existing joins and a
fixed SELECT (`sessions.go:222-284`), so this is additive.

`name` is served rather than derived: only the daemon knows a group's name, and
the clients should not each keep a copy.

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
POST   /api/groups              {"name":"Work"}          → {"key":"g_01H8…"}
PATCH  /api/groups/{key}        {"name":"Client work"}
DELETE /api/groups/{key}
POST   /api/groups/order        {"order":["g_work","g_opal","g_helios"]}
PATCH  /api/sessions/{id}       {"groups":["g_work","g_opal"]}
```

`POST /api/groups/order` writes the whole list in one transaction and renumbers
it `0..n-1`, mirroring `SetSessionOrder`. The daemon then appends any group the
client did not mention — one hidden behind the terminated filter would otherwise
be missing from the client's list and land unranked.

Session assignment rides on the existing `PATCH /api/sessions/{id}`, which
already takes `pinned` and `title`. `groups` is the whole ordered list; the
handler rejects duplicates, unknown keys, nulls inside the array, and anything
longer than the UI's limit. Passing `[]` or `null` clears the session's groups.

Deleting a group clears it from every session that holds it, in the same
transaction.

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
| Group membership | daemon, `sessions.groups` | yes |
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
    │    sessions.groups           │                │
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

- `internal/store/store.go` — `create_groups_table`, `add_sessions_groups`.
- `internal/store/groups.go` (new) — `CreateGroup`, `RenameGroup`, `DeleteGroup`,
  `SetGroupOrder`, `ListGroups`.
- `internal/store/sessions.go` — `SearchSessions` grows `grouped bool` and
  `groupKey string`, three `LEFT JOIN groups`; `Groups`, `GroupPositions`,
  `GroupNames` on `Session`, all `omitempty`; `SetSessionGroups`; inheritance in
  `UpsertSession`.
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
