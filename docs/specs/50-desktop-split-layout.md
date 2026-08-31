# Editor Groups for the Desktop: Two Panels Side by Side, Remembered Per Session

## The claim

A session's panels should be arrangeable, not just switchable. The user should
be able to drag the Files tab against the right edge of the transcript, get two
groups side by side with a sash between them, and find that arrangement again
the next time the session is opened — and the time after the app is restarted.

Today the detail view shows exactly one panel. `detail.tsx` renders a single tab
strip over a single body, and `state.panels[sessionKey]` holds the one name of
the one panel in front. Reading the transcript while browsing the tree means
alternating between two tabs, and the pairing the app is most used for —
transcript or terminal on one side, Files on the other — cannot be expressed at
all.

This spec adds VS Code's editor-group mechanism: several groups along one axis,
each with its own tab strip, tabs dragged between groups, a drop on a group's
edge splitting off a new one, draggable sashes, and the whole arrangement stored
per session.

**Scope: `desktop/` only.** No daemon route changes shape, no wire format moves,
and `mobile/` and `internal/tui` are untouched. A phone renders one panel at a
time and should keep doing so; this is a change to how one renderer arranges
what it already has.

## Where we are

| | Today |
|---|---|
| Tabs | one strip, `PANELS` = `chat \| terminal \| approvals \| git \| files` (`detail.tsx:23`) plus one per open shell (`detail.tsx:182`) |
| Which is in front | `state.panels: Record<sessionKey, RightPanel>` (`store.ts:154`), read by `currentPanel` (`store.ts:1321`), written by `showPanel` (`store.ts:1332`) |
| Which terminal is in front | `state.activeTabs: Record<sessionKey, string>` (`store.ts:160`), `currentTab` / `showTab` (`store.ts:1329,1337`) |
| Persistence | none. Both maps are in memory; a restart forgets every session's panel |
| Panel mounting | mount once, hide with `hidden` (`detail.tsx:219`), `.panel-keep[hidden]{display:none}` (`styles.css:1372`) |
| Panel unmounting | an idle sweep after `PANEL_TTL` of 5 minutes (`detail.tsx:46,106-120`), except `NO_TTL = ['git','files']` (`detail.tsx:55`) |
| Terminals | all mounted at once by `TerminalPanes` (`terminal.tsx:31`), one `.pane` each, `position:absolute; inset:0; display:none` and `.pane.active{display:block}` (`styles.css:3252-3261`) |
| The one existing split | `.agent-split` — approvals docked beside the transcript, hard-coded, folding under it below 1240px (`styles.css:1586-1614`) |

Two of those rows are the reason this is tractable. **Panels already stay
mounted**, hidden by CSS rather than unmounted, and the comments say why
(`detail.tsx:36-42`): a panel holds work as much as it displays it, and a tab
switch that unmounts throws away the open file, the scrolled diff, the scrolled
transcript. **Terminals already stay mounted** for a harder reason
(`terminal.tsx:26-29`): an xterm that unmounts loses its scrollback, and neither
the daemon nor the main process can replay it a second time.

So the app is already rendering everything at once and choosing what to show.
Showing two things is a layout change, not a lifecycle change — provided nothing
about the arrangement causes a remount.

## What it looks like

Today. One strip, one body, and the only way to see the tree is to stop seeing
the transcript:

```
┌───────────────────────────────────────────────────────────────────┐
│ opal-app — rewrite the ingest worker            ● idle   default  │
├───────────────────────────────────────────────────────────────────┤
│ agent │ ●terminal ⟳ ⏻ │ approvals │ git │ files │ ●zsh ⟳ × │  +   │
├───────────────────────────────────────────────────────────────────┤
│                                                                   │
│   > the worker retries on a 429 but not on a 503 — want me to     │
│     fold both into the same backoff?                              │
│                                                                   │
│                                                                   │
├───────────────────────────────────────────────────────────────────┤
│  ▌ask anything                                                    │
└───────────────────────────────────────────────────────────────────┘
```

After. Two groups, each with its own strip, a sash between them:

```
┌───────────────────────────────────────────────────────────────────┐
│ opal-app — rewrite the ingest worker            ● idle   default  │
├────────────────────────────────────────┬─┬────────────────────────┤
│ agent │ ●terminal ⟳ ⏻ │ ●zsh ⟳ ×      │ │ files │ git │ approvals│
├────────────────────────────────────────┤ ├────────────────────────┤
│                                        │ │ ▾ internal/            │
│  > the worker retries on a 429 but     │ │   ▾ ingest/            │
│    not on a 503 — want me to fold      │ │       worker.go      ● │
│    both into the same backoff?         │ │       backoff.go       │
│                                        │ │   ▸ server/            │
│                                        │ │ ──────────────────────  │
│                                        │ │ func (w *Worker) run(  │
├────────────────────────────────────────┤ │   for {                │
│  ▌ask anything                         │ │     if err := w.step(  │
└────────────────────────────────────────┴─┴────────────────────────┘
     group A · 1.3fr                    sash    group B · 0.7fr
     active: panel:chat                         active: panel:files
     items:  panel:chat, panel:terminal,        items:  panel:files,
             term:…:zsh                                 panel:git,
                                                        panel:approvals
```

Both strips are live: clicking `git` in the right-hand group swaps that group's
body and leaves the transcript alone. Nothing about the left group knows the
right one exists.

The grid underneath. Three column tracks, two row tracks, and every item a flat
child placed into a cell — the strips into row 1, the bodies into row 2, the
sash spanning both:

```
grid-template-columns: minmax(0,1.3fr)   6px   minmax(0,.7fr)
grid-template-rows:    auto  /  1fr

                col 1                col 2            col 3
           ┌───────────────────┐    ┌─────┐    ┌──────────────────┐
   row     │  strip A          │    │     │    │  strip B         │
   auto    └───────────────────┘    │     │    └──────────────────┘
           ┌───────────────────┐    │  s  │    ┌──────────────────┐
           │  panel:chat       │    │  a  │    │  panel:files     │
   row     │ ┄panel:terminal┄  │    │  s  │    │ ┄panel:git┄      │
   1fr     │ ┄term:…:zsh┄      │    │  h  │    │ ┄panel:approvals┄│
           └───────────────────┘    └─────┘    └──────────────────┘

           ┄dashed┄ = the group's inactive items. Same cell, `hidden`,
                      so they generate no box and size no track.
```

Dragging `files` from group B to group A changes one string —
`gridColumn: 3` becomes `gridColumn: 1` — on a `<div>` that is not touched
otherwise. That is the whole move.

## The shape of the state

A flat list of groups along one axis:

```ts
type ItemId = `panel:${RightPanel}` | `term:${string}`
interface Group { id: string; items: ItemId[]; active: ItemId; size: number }
interface Layout { axis: 'row' | 'column'; groups: Group[]; focused: string }
```

`size` is an `fr` weight, not a pixel count. A pixel width taken from today's
window leaves gutters in a wider one, which is the bug `writeReadingWidth`
already works around by hand for the markdown column (`files.tsx:731-736`).
Weights need no such rule.

Flat rather than a tree. VS Code has a full pane grid, but its common case — and
the one this spec exists for — is a row of groups. A nested tree is several
times the state and the drop logic for an arrangement nobody has asked for. The
functions below take a `Layout` and return a `Layout`, so a tree can replace the
representation later without touching a call site.

Items, not panels. A shell tab and the Files panel are the same kind of thing
once they can be dragged into different groups, and the current split — panels
in one strip, terminals in a parallel `activeTabs` map — has no meaning under a
layout. `detail.tsx:137` already argues the same point about the strip: a shell
is a sibling of the transcript, not a mode of the terminal.

## Rendering: one grid, and nothing ever moves in the DOM

This is the load-bearing decision, so it is worth stating what it avoids.

Dragging a tab from one group to another moves it in the DOM. In React, a
re-parent is an unmount and a fresh mount — which runs `TerminalPane`'s cleanup
and calls `term.dispose()` (`terminal.tsx:167`), losing the scrollback that
cannot be replayed. The same move discards the CodeMirror state in Files and the
scroll position in Git.

The usual escape is a portal: hold a `div` created imperatively, `createPortal`
the content into it once, and `appendChild` that `div` into whichever group
should own it. The portal container object never changes, so React never
unmounts. It works, and it costs three things. Child effects run before parent
effects, so `term.open(container)` fires while the host is still detached —
xterm 5.5 logs *"Terminal.open was called on an element that was not attached to
the DOM"* and then measures character cells against a detached node, which can
throw inside `WebglAddon`. Every move then needs a `proposeDimensions()` and
`refresh()` kick to re-measure. And the browser clears focus when a node leaves
the document, so CodeMirror sees a spurious selection change on every drag.

None of that is necessary, because the DOM does not have to move.

`.panel-body` becomes a **CSS Grid with flat children**:

- `grid-template-rows: auto 1fr` — row 1 holds the tab strips, row 2 holds the
  bodies.
- `grid-template-columns` is computed inline from the group weights, interleaved
  with fixed sash tracks: `minmax(0,1.3fr) 6px minmax(0,.7fr)`.
- The children are three flat lists, rendered in an order that does not depend
  on the layout: one strip per group, **one host per item** in canonical order
  (`PANELS`, then `store.tabs`), and N−1 sashes at `grid-row: 1 / -1`.

Several items share a cell. The inactive ones keep the `hidden` attribute they
already carry (`detail.tsx:219`), generate no box, and contribute nothing to
track sizing — so a hidden Files panel cannot widen the column its group sits
in.

Moving a tab between groups is then one inline `gridColumn` string changing on a
node that stays exactly where it is. No remount, no re-parent, no re-measure, no
focus loss. `minmax(0, …)` on every track because a grid item's default
`min-width: auto` would let a wide diff refuse to shrink.

`.panes` and `.pane` lose their absolute positioning (`styles.css:3223-3261`).
That container existed to stack terminals on top of each other in one cell;
under the grid each pane is a grid item in its group's cell, stacked by the same
`hidden` rule as everything else.

**Measured, not assumed.** The claim was tested against the running app over
CDP, on a live agent terminal: `.panel-body` set to the grid above, `.panes` to
`display: contents` so each `.pane` becomes a grid item, and the pane's
`gridColumn` then moved between tracks.

| | pane width | xterm | canvases |
|---|---|---|---|
| flex + absolute, as today | 1672px | 280 cols | 2 |
| grid item, col 1 (1.3fr) | 1083px | 139 cols | 2 |
| moved to col 3 (0.7fr) | 583px | 73 cols | 2 |
| moved back to col 1 | 1083px | 139 cols | 2 |

The node was identity-checked across the moves and never replaced, both WebGL
canvases survived, and the terminal kept rendering. Changing one `gridColumn`
string re-measured the pane, the proposal reached the host, and the host's
answer resized the grid — the whole loop, with nothing re-parented.

## The layout module

`desktop/src/renderer/components/layout.ts`, pure, importing nothing from the
store — the same shape as `components/grouping.ts`, and for the same reason:
`bridge.ts` binds `window.helios` at module scope, so anything that reaches it
cannot be loaded by the type-stripping test runner. A module that reaches
nothing can be asserted directly.

| Function | Does |
|---|---|
| `defaultLayout()` | one group, `['panel:chat']`, focused |
| `reveal(layout, item)` | if the item is in a group, activate it there and focus that group; otherwise append it to the focused group and activate it |
| `moveItem(layout, item, toGroup, index)` | move between strips, or reorder within one |
| `splitInto(layout, item, atGroup, edge)` | new group before or after `atGroup`, taking half its weight |
| `removeItem(layout, item)` | drop it, pick the neighbour as active, collapse an emptied group and give its weight back |
| `resize(layout, sashIndex, delta)` | move weight across one sash, clamped |
| `reconcile(layout, liveTermIds)` | **append-only** — add terminals that are not placed yet |
| `sweep(layout, lastSeen, now)` | which items the idle sweep may unmount |
| `parseLayout(unknown)` | a stored value, or the default |

`removeItem` replaces the neighbour-picking written by hand inside `closeTab`
(`store.ts:1256-1268`) — the rule it encodes, that closing the middle of three
shells should land on the one before it rather than fall back to the agent's
terminal, moves into the model and gets a test.

## Seven things that will break if they are not designed for

**1. Reconcile must never prune.** The layout is read from localStorage
synchronously when a session is selected, but shells are re-attached over an
`await` in `store.syncShells`, called from an effect (`detail.tsx:81-84`). A
reconcile that dropped items with no live tab would delete every `term:` item in
that window — the restored arrangement would be gone before it could be shown.
Removal is event-driven only: `closeTab`, `killShell`, `terminal_closed`.

**2. `applyShow` targets a session that may not be selected.** The agent's
`helios_show` prepares another session's view and waits there
(`store.ts:929-931`). Its layout may never have been loaded. Every mutation goes
through a `layoutFor(state, key)` that materialises from localStorage on first
touch and never re-reads once `state.layouts[key]` exists — otherwise the read
that happens on select would clobber what the agent just did.

**3. `openTerminal` reveals a tab that does not exist yet.** It sets the panel
and the active tab, then `await`s `bridge.term.open` (`store.ts:1024-1054`).
Under a layout, revealing `term:<id>` before the tab is in `state.tabs` places an
item nothing renders. Create the tab and reveal it in the same `set`.

**4. Two visible panes both grab focus.** `TerminalPane` calls `term.focus()`
whenever it becomes active (`terminal.tsx:208`). With two groups on screen the
last one to render wins, and every layout change steals the caret. Focus only
when the item's group is `layout.focused`.

**5. The idle sweep must look at every group.** `PANEL_TTL` unmounts a panel
that has not been in front for five minutes. A panel sitting visible in a second
group has not been "in front" by today's definition and would be swept out from
under the user. The check becomes `groups.some(g => g.active === id)`.

**6. A tab in a strip is not a mounted panel.** These have to be separate, and
the first draft of this spec had them as one thing. If the layout only holds
items that have been revealed, a panel you have never opened is in no strip and
there is nothing to drag. If the layout holds everything and the layout drives
mounting, opening a session mounts Git and Files and fires their fetches.

So the default layout lists all five panels — the strip says what the session
*has* — and mounting is a separate question the view answers: an item is mounted
once it has been looked at, and terminals from the moment their tab exists,
because output arrives on one channel for every tab and is dropped for a tab
with no pane listening.

**7. The terminal placeholder is not a pane.** "No terminal attached" and the
Attach / Wake buttons live on the container today, alongside the effect that
auto-attaches when the panel is first shown (`terminal.tsx:50-99`). Under the
grid there is no container. `panel:terminal` becomes an item in its own right
that renders the placeholder and holds that effect; `reconcile` swaps it for
`term:<agentTabId>` in place once the agent's tab lands, so the split the user
built survives the attach.

**8. A pane has to outlive the session being selected.** The obvious shape —
render the items of the selected session's layout — unmounts the previous
session's terminals on every switch. That looks harmless, because switching back
builds a new pane. It is not: the connection lives in the main process and keeps
counting bytes, so the new xterm's re-attach asks the host to catch it up from a
sequence the host has already passed, and gets a delta instead of a screen. The
result is a live green dot over an empty grid with a cursor in it, until a
reconnect. `TerminalPanes` avoided this by mapping over every tab in the store
rather than the session's, and the replacement has to keep that: **one pane per
tab, for every tab, mounted for as long as the tab is.** Which cell it sits in
and whether it is hidden come from the selected session's layout; whether it
exists does not.

That splits the terminal in two: `panel:terminal` is the *slot* — the tab in the
strip, and the attach button when there is nothing attached — while the pane
itself is drawn from the deck of all tabs and placed into that slot's cell.

**9. An xterm opened against a zero-size element is stranded for good.**
`Terminal.open()` measures a character against the element it is given. A pane
that mounts behind another tab has no layout, so the cell comes out zero wide —
and nothing ever measures again: xterm carries no resize observer, and
`FitAddon.proposeDimensions()` returns `undefined` on a zero cell, so the pane
cannot even ask the host for a size. It draws an empty grid with a cursor in it
until a reconnect builds a new terminal, which is exactly the "blinking cursor,
fixed by refreshing" the shells already show today.

The fix is to open the terminal the first time its container has a size rather
than on mount. Writing before `open` is fine — the bytes land in the buffer — so
the snapshot the host replays on attach is there when the pane is finally shown.
This is a pre-existing bug that the split makes far easier to hit, because more
panes now mount behind something.

One more, adjacent rather than caused: every mounted terminal holds a WebGL
context and Chrome evicts past roughly sixteen. `WebglAddon` is loaded at
`terminal.tsx:148` and its `onContextLoss` is not handled. Two groups make more
terminals visible at once and make the ceiling easier to hit, so dispose and
reload the addon on loss. It loads after `open`, since the renderer needs the
element.

## What the split does *not* threaten

Resizing a terminal by dragging a sash is safe, and it is worth writing down
why, because it looks like the most dangerous thing in the feature.

The host owns the grid. Since #118 the renderer sends `proposeDimensions()` and
never resizes xterm itself; the only thing that moves the grid is a status
broadcast from the host (`terminal.tsx:196-204, 227-233`). The host adopts the
smallest interactive viewer (`internal/terminal/host.go:445-464`) and
`applyNegotiatedSize` returns early when the negotiated size equals the current
one (`host.go:471-474`). So a drag that moves less than one cell sends
proposals the host discards, and a drag that does move a cell gets an answer
back that both sides then agree on. The class of bug #118 fixed — a grid
rendering at a width the PTY never adopted, which is what duplicated typing
looks like — cannot come back through this door, because the renderer no longer
has a way to set its own width.

What remains is chatter: one IPC and one frame per `ResizeObserver` tick during
a drag, most of them no-ops at the host. Worth a throttle if a drag ever feels
heavy. Not a correctness risk, and not a reason to change the design.

## What happens to the store

`state.panels` and `state.activeTabs` are replaced by
`state.layouts: Record<sessionKey, Layout>`. Every existing caller collapses to
one operation — reveal an item — and `currentPanel` and `currentTab` survive as
selectors derived from the focused group, so the call sites keep their shape:

| Call site | Becomes |
|---|---|
| `setPanel` (`store.ts:897`) | `reveal('panel:' + name)` |
| `openFile`, `findFile` (`store.ts:902,914`) | `reveal('panel:files')`, unchanged target |
| `appendPrompt` (`store.ts:992`) | `reveal('panel:chat')` |
| `applyShow` (`store.ts:933`) | `reveal` against the named session's layout, through `layoutFor` |
| `openTerminal`, `showTerminal` (`store.ts:1019,1222`) | `reveal('term:' + id)`, in the same `set` as the tab |
| `selectTab` (`store.ts:1279`) | `reveal('term:' + tabId)` |
| `closeTab` (`store.ts:1242`) | `removeItem`, which now owns the neighbour rule |

`showPanel` and `showTab` go.

## Persistence

`localStorage`, keyed `helios.layout.{hostId}.{sessionId}` — beside the Files
panel's own per-session record at `helios.files.{hostId}.{sessionId}`
(`files.tsx:199`), and written in the style every other reader in the codebase
uses: a `try`/`catch` that falls back to the default, because a full or
unavailable store should cost the arrangement and not the panel
(`store.ts:206-276`, `files.tsx:940-966`).

Per machine, deliberately. The daemon has no per-session metadata column —
sessions are fixed columns in SQLite (`internal/store/sessions.go:11-60`) and the
only key-value store is global (`internal/store/settings.go`) — so syncing the
layout would mean a schema change and a new PATCH field, for a preference that a
phone cannot honour and a 27-inch monitor and a laptop would disagree about
anyway.

Shell tab ids survive a restart: they are `terminalId(hostId, info.id)` over the
daemon's own `terminals()` list, and the agent's tab keys on the session id.
Both are server-side and stable, which is what makes a saved `term:` item
meaningful on the next launch.

## The gestures

Drag and drop, following the conventions `sidebar.tsx` already sets for the
group tree (`sidebar.tsx:140-160, 517-580`): a dedicated dataTransfer type so a
drop target can refuse a payload it cannot hold, and both the payload and the
drop mode read off the event rather than out of state — a drop can land in the
same tick as the drag start, before a `setState` has committed.

- `application/x-helios-tab` carries the `ItemId`.
- Dropped on a tab strip: move into that group at the index under the pointer.
- Dropped on a group body: the leading and trailing quarters **along the
  layout's axis** split off a new group; everything else moves into the group
  without splitting. That is `dropModeFor` (`sidebar.tsx:154`) turned sideways.
  Only along the axis, because a flat list has nowhere to put a cross-axis
  split — see below.

```
 a group body, while a tab is being dragged over it (axis: row)

 ┌───────┬───────────────────────────────┬───────┐
 │       │                               │       │
 │  ←    │                               │   →   │
 │ split │      move into this group     │ split │  leading / trailing
 │ before│      (no split, just a tab)   │ after │  quarters
 │       │                               │       │
 │       │                               │       │
 └───────┴───────────────────────────────┴───────┘
   25%                  50%                 25%

 axis: column turns the same three bands ninety degrees — split above,
 move into, split below.

 the drop, previewed: dragging `files` onto the right quarter

 ┌────────────────────────────────────┬─┬────────┐
 │ agent │ ●terminal │ ●zsh           │ │        │
 ├────────────────────────────────────┤ ├────────┤
 │                                    │ │▒▒▒▒▒▒▒▒│
 │  > the worker retries on a 429…    │ │▒files▒▒│
 │                                    │ │▒▒▒▒▒▒▒▒│
 └────────────────────────────────────┴─┴────────┘
       keeps 1fr… ── shrinks to make room ──→ 1fr
```

The highlight is drawn by the group under the pointer, at the size the new group
would take — half the weight of the one it splits from.

The sash is `onPointerDown` plus `pointermove`/`pointerup` on `window`, copying
the markdown column's grip (`files.tsx:714-736`) and `.sidebar-grip`
(`sidebar.tsx:806-816`). Double-click resets the weights, as the other two grips
already do.

Below the `.agent-split` breakpoint of 1240px (`styles.css:1612`) a row of
groups is two unreadable columns. The axis flips to `column` at that width — the
same answer the approvals dock already gives, and the reason `axis` is in the
model rather than assumed.

## What we are not doing

- **A pane tree, and with it the cross-axis drop.** One axis, N groups. Dropping
  a tab on the top edge of a group in a row layout would have to nest a column
  inside that column, and there is nowhere in a flat list to put it — so those
  two quarters are not drop zones, and the gesture is unavailable rather than
  wrong. The API takes a `Layout` and returns one, so this is a representation
  change if it is ever wanted.
- **The same panel in two groups.** VS Code opens one file in two editors; here
  a panel is a singleton with a live connection behind it, and two views of one
  terminal have nothing to disagree about.
- **Syncing to mobile.** Named above.
- **Cross-session layouts.** A group holds one session's items. Two sessions
  side by side is a different feature with a different state shape.
- **Touching the daemon.** No route, payload or SSE event changes. If this spec
  ever seems to need one, that is the signal it has drifted out of scope.

## Tests

The desktop suite is `node --test --experimental-strip-types test/*.test.ts` and
does not mount React, so the tests go at the model, in
`test/layout.test.ts`, mirroring `test/grouping.test.ts`:

- A split makes a second group and halves the weight of the one it came from.
- Removing the last item of a group collapses it and returns its weight to the
  neighbour.
- Closing the middle of three shells activates the one before it; closing the
  last activates nothing and lets the fallback stand — the rule `closeTab`
  encodes today.
- `reconcile` leaves a saved `term:` item alone when its tab has not been
  re-attached yet, and appends a live tab that is not placed anywhere.
- `sweep` spares an item that is active in a group other than the focused one.
- `reveal` on an item already placed focuses its group instead of moving it.
- `parseLayout` returns the default for garbage, for a truncated object, and for
  a layout whose `focused` names a group that is not there.

Manual, against the running app over CDP, as with PRs #109 and #112: split Files
out of the transcript and confirm the transcript keeps its scroll position; put
a terminal in each group, type in both, drag the sash, and confirm neither loses
scrollback; switch sessions and restart the app and confirm each layout returns.

**A trap for whoever tests this.** A dev instance is a second interactive
viewer, and the host adopts the smallest one. Narrowing a terminal in the dev
window narrows the PTY for every other viewer, including the window the user is
working in — measured during this spec's own testing: a session sitting at 218
columns dropped to 139 when a dev pane was moved into a 0.7fr track. It is not
damage and it is not sticky. `applyNegotiatedSize` runs again when the viewer
disconnects (`internal/terminal/host.go:687-702`), so quitting the dev instance
restores the size. But do the testing in a window at least as wide as the real
one, or expect the user's terminal to reflow while you work.
