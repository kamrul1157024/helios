# Groups on the Phone

## The claim

The phone should show the same tree of sessions the desktop shows, off the same
daemon, and let a session be filed into a group without reaching for a laptop.

The daemon has held groups for a while and the desktop renders them. The Flutter
app renders one flat list. A machine with forty sessions reads the same on the
phone whether the user has filed them or not, which means the filing only pays
off on the device it was done on.

**Scope: `mobile/` only.** No route changes shape, no wire format moves, and
`desktop/` is not touched. The daemon already serves everything this needs.

## Where we are

| | Daemon | Desktop | Mobile |
|---|---|---|---|
| Group catalogue | `GET /api/groups` | `store.groups` | — |
| Tree build | — | `renderer/components/grouping.ts` (pure, 291 lines) | — |
| Tree UI | — | `components/sidebar.tsx` | — |
| Mode choice | — | `components/group-picker.tsx`, `localStorage helios.grouping` | — |
| Group on a session | `group_key`, and `group_path` under `?grouped=1` | read from both | not parsed |
| Order by hand | `sort_order`, `POST /api/sessions/order` | per-host `sortMode` | `manualOrderProvider` (`daemon_providers.dart:489`) |

Every write already exists: `POST /api/groups`, `PATCH /api/groups/{key}` for a
rename or a re-parent, `DELETE /api/groups/{key}`, `POST /api/groups/order`, and
`PATCH /api/sessions/{id}` with a `group` field, where `""` unfiles.

`internal/server/groups.go:14` broadcasts `session_updated` after every one of
them. The phone already listens to that event and already invalidates its
session list on it (`cache_invalidator.dart:41`), so a group made on the desktop
reaches the phone through a path that is built.

## The change

### The tree is ported, not rewritten

`grouping.ts` is pure and has no React in it: `rankOf`, `byRank`, `depthOf`,
`buildTree`, `buildCwdTree`, `orderGroups`, `lastActivityOf`, `cwdLabel`,
`tintOf`. It becomes `mobile/lib/utils/grouping.dart`, function for function.

The reason is the ordering model rather than the line count. A session's rank is
the vector of its groups' positions with its own `sort_order` last
(`grouping.ts:20`), and comparing those element by element is what makes a group
move carry its sessions with it. A second implementation of that rule would
drift from the first, and then two clients reading one daemon would disagree
about what the same numbers mean.

Two things do not come across unchanged. `tintOf` returns a colour, so it lives
in the widget file and leaves `utils/grouping.dart` free of any Flutter import —
that is what lets it be unit-tested. And `flattenSessions(List<GroupNode>)` is
added, because the phone's reorder has to post a whole-host order back.

### Three modes, as on the desktop

`off` is today's flat list, unchanged, down the same two builders
(`sessions_screen.dart:446` and `:464`). `manual` is the stored tree: any depth,
empty groups still rendered, `Ungrouped` last, a cumulative count on each header.
`directory` is one node per `cwd`, one level, ordered by activity, name, or by
hand.

The mode is a device preference, not a daemon setting — the desktop keeps it in
`localStorage` under `helios.grouping` and the phone keeps it in
`shared_preferences` under the same name. Directory order and the `PathOrder` map
go with it, for the reason `grouping.ts:145` gives: a derived group has no row to
hang a number on.

Grouping is independent of sorting. The phone already has the sort switch
(`manualOrderProvider`), it is per host, and it stays exactly as it is.

### The folds survive a restart

The desktop holds its folds in React state, so a reload opens everything. On the
phone that is the wrong default: the OS kills a backgrounded app routinely, and
a tree that re-opens itself every time is a tree nobody collapses twice.

Folded keys are `"$hostId:$path"` — the same key the desktop computes at
`sidebar.tsx:651` — and the set is written to `shared_preferences`.

### One sliver list per node, not one list of mixed rows

A grouped list is a `CustomScrollView`: a `SliverToBoxAdapter` header per node,
then that node's children, then a `SliverReorderableList` of the sessions that
stop there.

The alternative — flattening the tree into a single `ReorderableListView` of
header and session rows — has to police a drag that crosses a header. The daemon
holds one flat order per host, and the desktop's rule is that a card only drops
inside the node it started in (`sidebar.tsx:612-617`). A sliver per node gives
each node its own index space, so that rule holds by construction rather than by
arithmetic inside `onReorder`.

After a drop, the whole tree is flattened in render order and posted through the
existing `SessionsNotifier.reorder` (`daemon_providers.dart:183`), which already
paints optimistically and rolls back. Posting only the node's own ids would
renumber that node from zero and scatter the rest.

### A header moves by menu; a session moves by either

The desktop re-parents a group by dropping its header on another and reading the
pointer's height in the target: top quarter before, middle half inside, bottom
quarter after. That is three drop targets inside a 22-pixel row, and on a phone
it is a coin toss.

So a group header's long-press menu carries *Rename*, *New subgroup*, *Move to…*,
*Move up*, *Move down* and *Delete*, and there is no header drag. *Move to…*
opens a tree picker that excludes the moving group's own subtree — the cycle
guard `sidebar.tsx:293` writes as a check, stated instead as a list the user
cannot choose from.

A session is filed either way: *Move to group…* in the card's existing long-press
sheet (`sessions_screen.dart:1105`), or by dragging the card onto a header. The
menu is the reliable path and lands first; the drag lands last, because it is the
one that can fail. Both end at `PATCH /api/sessions/{id}`, and both paint the row
before the daemon answers, the way `patch` already does for a pin
(`daemon_providers.dart:158`).

Nothing is offered on `Ungrouped` or on a directory node. Ungrouped is synthetic
and a directory node's key is a path nobody stored, so a rename would have
nowhere to go — the same reason the desktop hides those actions rather than
greying them out.

### `grouped=1` on every session read

`listSessions` (`daemon_api_service.dart:691`) always asks for it. It is not a
field on `SessionQuery`, because the query is the Riverpod family key and
`allSessionsKey` is written out at eight call sites; a second key variant would
leave every one of their optimistic writes painting a cache entry the screen is
not reading. An older daemon ignores the parameter.

### A daemon without groups

`GET /api/groups` answers 404 on an old daemon. That is not an error the list
should show: the sessions are fine, only the filing is missing. The catalogue
carries an `unsupported` flag, the tree falls back to flat, and the mode sheet
says why — as the desktop does with `groupsUnsupported`.

## What we are not doing

- **Channels.** Groups are over sessions. The channels tab is unchanged.
- **The dashboard.** No group column, no per-group counts.
- **The daemon.** If this ever seems to need a new route, it has drifted.
- **Header drag.** Stated above, and stated here because it is the one place
  this deliberately parts company with the desktop.
- **Syncing the mode between devices.** The desktop keeps grouping local, and a
  phone that silently changed the laptop's sidebar would be a surprise.

## Tests

`mobile/test/grouping_test.dart`, against the port:

- A group with nothing in it still yields a node — the catalogue decides what
  exists, not the sessions (`grouping.ts:69-79`).
- `Ungrouped` sorts after every stored group, and is absent when every session
  is filed.
- `total` counts the subtree, not the level.
- `byRank` puts a subgroup above a loose session at the same level.
- `buildCwdTree` yields one node per `cwd`, and `orderGroups` honours activity,
  name, and a `PathOrder` with an unplaced directory falling to the end.
- A group cannot be moved into its own descendant.
- `flattenSessions` returns render order, so a reorder in one node leaves every
  other node's relative order alone.

`mobile/test/group_catalog_test.dart`, against the client, with the `MockClient`
helpers already in `optimistic_write_test.dart`:

- A 404 from `GET /api/groups` reads as unsupported rather than throwing, and a
  500 reads as an error.
- `patchSession(group: '')` sends `{"group":""}` — an unfile is a value, not an
  omission.
- A refused file puts the row back where it was.

Manual, on a device (`make apk`, then `adb install -r`): make a nested group and
file a session into it; confirm the desktop shows the same tree off the same
daemon; delete a parent and watch its children promote rather than vanish; switch
all three modes; kill the app and confirm the folds come back.
