# Vim Mode: Running the Desktop Without a Mouse

## The goal

One sentence, and every decision below answers to it:

> A person who never moves their hand to the mouse can do everything the desktop
> app can do.

Not "can navigate quickly". Can do *everything*. Approve a tool call, rename a
session, split a pane, read scrollback from four minutes ago, copy a path out of
it, pause a schedule, pair a host. If one action needs a pointer, the hand
leaves the keyboard, and the feature has failed at the thing it was for.

Vim keybindings are the means. The mouse-free guarantee is the end. Where the
two disagree — where being faithful to vim would leave something unreachable —
reachability wins.

## Why the obvious approach does not get there

The obvious approach is to bind `j` and `k` to the session list, call it vim
mode, and ship. The desktop has around 170 click handlers across 26 components.
Binding the fifteen most common leaves 155 reasons to reach for the mouse, and a
user who has to reach anyway keeps their hand there.

Neovim does not solve this by binding everything. Most of Neovim has no default
key at all. It solves it by making every action an *Ex command*, so `:` can
reach what the keymap does not. The keymap is an accelerator over a surface that
is already complete.

That is the structure to copy, and it gives the invariant this spec is built on:

**Every action registers as a command. `:` runs any command. Frequent commands
also get a key.**

Registration is the guarantee. Keys are the ergonomics. An action that someone
forgets to register is not merely unbound — it is invisible in `:` and in the
hint overlay, which is how the omission gets noticed.

## Modality, as Neovim means it

Web apps treat DOM focus as the truth and infer a "mode" from it. That inference
is where vim modes in browsers break: focus lands somewhere unexpected, the app
believes it is in normal mode, and `d` deletes something instead of typing a
letter.

Neovim has the dependency the other way round. The mode is the truth, and the
cursor is a consequence. You cannot be "in a text box" while the editor thinks
otherwise, because being in insert mode *is* what makes keys become text.

So:

> **The mode owns DOM focus. Entering INSERT focuses the zone's text surface.
> Leaving INSERT blurs it. A mouse click into a text field is the single reverse
> edge, and it sets the mode to INSERT so the two can never disagree.**

Everything below follows from that rule.

### The modes

| Mode | When | Keys do |
| --- | --- | --- |
| `NORMAL` | Default | Commands |
| `INSERT` | A text surface holds focus | Type |
| `VISUAL` | A range is being selected | Motions extend the range |
| `COMMAND` | `:` is open | Type into the command line |
| `TERMINAL` | An xterm holds focus | Everything reaches the pty |
| `SCROLL` | Reading terminal scrollback | Motions scroll the viewport |

`OPERATOR-PENDING` is out of scope. There is no `d{motion}` grammar: outside a
text buffer there are no text objects for an operator to act on.

`SCROLL` is tmux's copy-mode with the copying removed. It exists because
scrollback is the one surface a mouse currently owns outright, and because
*reading* past output is a thing you do every few minutes. Selecting and yanking
out of it is not — that stays a mouse job on purpose. See "The four gaps".

### Entering and leaving INSERT

Neovim enters insert with `i a I A o O c s`. Only three of those mean anything
outside a text buffer:

- `i` — focus the zone's text surface
- `a` — focus it, positioned at the end (in the transcript, append to the composer)
- `o` — open a new thing: a session in the sidebar, a shell in the terminal zone

Leaving is `Esc` or `Ctrl-[`. LazyVim adds no `jk` escape by default and neither
does this. Anyone who wants one can bind it, which is the point of the keymap
file.

### TERMINAL mode, and the one chord that leaves it

This is the part most likely to trap someone, so it gets the most conservative
answer available.

In `TERMINAL` mode the app intercepts **exactly one** sequence: `Ctrl-\ Ctrl-n`,
copied verbatim from Neovim. Everything else reaches the pty untouched.

`<Esc><Esc>` — which LazyVim uses for its floating terminal — is **not**
available, and the reason is specific. Claude Code binds single `Esc` to
interrupt the current turn and double `Esc` to edit the previous message. Both
must arrive intact. Stealing them would break the agent this app exists to
drive.

`Ctrl-\ Ctrl-n` is safe for the same reason Neovim chose it: no interactive
program sends it.

The ⌘ accelerators (`⌘N`, `⌘W`, `⌘,`) keep working in `TERMINAL` mode, because
xterm never sees them.

### which-key timing

LazyVim sets `timeoutlen = 300`. The hint overlay appears only after a pending
prefix has waited that long, which is what stops it flashing during fluent
typing. Same default here, stored in `keymap.json` as `timeoutlen` so it can be
tuned.

## Zones

Vim modes are global; vim *meaning* is per-window. `j` is "next line" in a
buffer and "next entry" in the file explorer. The equivalent here is a zone.

Half of this exists already, which the first draft of this spec missed.
`Layout.focused` in `layout.ts:23` is documented as "which group a reveal lands
in, and which one owns the keyboard", and `store.focusGroup` at `store.ts:1212`
moves it. So focus *within* the detail panel is solved. What is missing is a
level above it: nothing says whether the rail, the sidebar or the detail panel
has the keyboard.

```ts
export type Zone =
  | 'rail'
  | 'sidebar'
  | 'transcript'
  | 'terminal'
  | 'approvals'
  | 'git'
  | 'files'
  | 'editor'
  | 'diff'
  | 'schedules'
  | 'settings'
```

The zone is stored in the renderer store, drawn with a focus ring, and used as
the `when` condition for every binding. Zone movement follows LazyVim rather
than stock vim: plain `Ctrl-h/j/k/l` between zones, not `Ctrl-w h`.

`Ctrl-h` and `Ctrl-l` cross the three columns — rail, sidebar, detail — and once
inside the detail panel they hand over to `Layout.focused`, moving group to
group. The zone of the detail panel is then whatever `panelOf(group.active)`
names, so the zone list above is derived rather than separately tracked.

Zones use real DOM focus with roving `tabindex`, not a parallel virtual
selection. The app already carries `aria-label` and `aria-pressed` throughout;
a virtual cursor would leave a screen reader describing a different row from the
one the ring is on.

## The layout API is already a window manager

The single largest finding of the audit. `renderer/components/layout.ts` is a
tiling window manager with groups, sashes and pure reducers, and `store.ts`
already exposes every one of them as a method. Only drag-and-drop calls them
today.

| Key | Store method |
| --- | --- |
| `Ctrl-w s` | `splitItem(target, item, atGroup, edge)` |
| `Ctrl-w q` | `dropItem(target, item)` |
| `Ctrl-w =` | `evenGroups(target)` |
| `Ctrl-w <` `>` | `resizeGroups(target, sash, delta)` |
| `Ctrl-w x` | `moveItem(target, item, toGroup, index)` |
| `Ctrl-w r` | `setLayoutAxis(target, axis)` |
| `Ctrl-h/l` | `focusGroup(target, groupId)` |
| `g c` … `g f` | `setPanel(panel)` |

So the window commands are a keymap over methods that already exist, already
persist, and are already covered by `test/layout.test.ts`. There is no new
state and no new reducer.

Four facts about the model change what the keys can mean, and the first draft of
this spec got the first two wrong.

**The layout is flat and single-axis.** `Layout` is `{ axis: 'row' | 'column',
groups: Group[] }` — a row of groups, not a nested tree. So `Ctrl-w s` and
`Ctrl-w v` cannot mean horizontal and vertical: there is only one axis, and a
split always adds a group along it. One split key, plus `Ctrl-w r` to rotate the
whole panel between row and column. `detail.tsx:174` already forces `column` on
a narrow window, so rotation is a path the layout code walks today.

**Directional focus needs no geometry.** A flat row means `Ctrl-h` and `Ctrl-l`
are previous and next group in `layout.groups`, and `Ctrl-j` / `Ctrl-k` are the
same when the axis is `column`. No DOM rectangles, no hit-testing.

**`splitInto` refuses a pointless split.** `layout.ts:163` returns the layout
unchanged when the item is alone in its group, because dissolving and rebuilding
one group reads as nothing having happened. `Ctrl-w s` on a single-pane group is
therefore a no-op, and the mode line should say so rather than appear broken.

**`Ctrl-w q` cannot empty the panel.** `collapse` at `layout.ts:213` falls the
last group back to the transcript. Closing the final pane returns you to chat.
That is the right behaviour and it is already written.

## The command registry

```ts
export interface Command {
  /** Stable, dotted, never shown raw to the user. `sidebar.rename`. */
  id: string
  /** What `:` and the overlay display. Imperative. "Rename session". */
  title: string
  /** Groups the overlay's columns. "Session", "Window", "Git". */
  group: string
  /** Zones and modes this applies in. Absent means everywhere in NORMAL. */
  when?: { zones?: Zone[]; modes?: Mode[] }
  /** Asks before running. Used for anything destructive or agent-visible. */
  confirm?: string
  run(ctx: CommandContext, count?: number): void | Promise<void>
}
```

`CommandContext` carries the current zone, the selection, and the store. It does
**not** carry a React component instance: commands must be runnable from `:`
when the relevant component is not mounted. A command whose target is not
present is disabled rather than absent, so the palette does not change shape as
panes come and go.

Commands register at module scope beside the component that owns the action.
Registration is not optional — see "Enforcement".

## Moving a cursor over what is already drawn

Lists are the exception to the rule above, and finding that out cut the largest
risk in this spec roughly in half.

A list's rows come from React Query and live inside the component that renders
them. A command holds no component, so it cannot ask what is on screen — which
looked like a reason to lift every list into the store.

It is not. The command can ask the *document*, and the document is the better
source: the order the elements are in **is** the order the eye reads, with
filters, folds and sort already applied. So a zone marks itself with
`data-vim-zone` and its rows with `data-vim-item`, and `renderer/vim/items.ts`
moves a real DOM focus over them.

```
j / k          move focus by one         G / gg   first and last
Enter          click the focused row     Ctrl-h/l cross columns
```

Five commands — `list.next`, `list.prev`, `list.first`, `list.last`,
`list.activate` — then serve the sidebar, the rail, the schedules, the file
tree, the approvals and the git panel at once. Adding a zone is two attributes,
not a store refactor.

Three things fall out of it:

- **`Enter` runs the row's own `onClick`.** A row's meaning belongs to the
  component that drew it, and half of them are already buttons.
- **Folded rows are skipped**, by testing `offsetParent`. They are in the
  document and not on the screen, and landing on one moves a cursor nobody can
  see.
- **The cursor is remembered per zone.** Vim keeps a cursor per window and puts
  you back on it; without that, crossing to the rail and back lands on the first
  row, so two motions that should cancel out lose your place instead.

## The state that still has to move

What remains is real, but it is smaller than the coverage tables suggest: the
list state is handled above, so this is only the state a *command* must set.

Most click handlers do not call the store. They call a `useState` setter that
lives in the component. A command run from `:` has no way to reach one.

The audit sorts them into two piles, and the distinction matters because the
second pile looks like work and is not.

**Must move to the store.** These are view state a keyboard user has to reach:

| File | State |
| --- | --- |
| `files.tsx:115,116` | `tabs`, `activePath` — the Files panel keeps its own tab list |
| `files.tsx:105,125,128` | `rootOverride`, `expanded`, `side` |
| `files.tsx:130,132` | `quickOpen`, `rootPicker` |
| `sidebar.tsx:204,208` | `collapsed`, `folded` |
| `sidebar.tsx:215,220` | `showTerminated`, `menu` |
| `sidebar.tsx:231,233,234` | `creatingIn`, `renaming`, `picker` |
| `schedules.tsx:499` | `tab` |
| `commits.tsx:157,328` | `all`, `picked` |
| `git.tsx:51,52,222` | `worktree`, `worktrees`, `selected` |

`files.tsx` is the awkward one. The Files panel's open tabs are component state,
so they die on unmount and no command can address them. `files.tab` and
`files.closeTab` in the coverage table both depend on this move.

**Must not move.** Drag state and INSERT-mode drafts:

`dragging`, `groupDrag`, `groupDrop`, `drag`, `over`, `edge` are pointer
machinery. A keyboard has no drag, so these have no keyboard counterpart and
need none. `draft`, `sending`, `comment`, `anchor`, `head` belong to a text
surface that INSERT mode owns while it is focused.

`store.ts` is already 1692 lines. This move is a refactor with no user-visible
change, which makes it exactly the kind of thing that should land on its own,
before the registration sweep and not tangled into it.

## Two traps in the existing keyboard code

Both would produce data loss rather than a missing feature, so they are called
out here rather than left to implementation.

**Blur commits, so a mode-driven blur commits too.** The inline rename fields at
`sidebar.tsx:1066` and `detail.tsx:845` commit on `onBlur` and cancel on
`Escape`. The rule that "leaving INSERT blurs the text surface" would therefore
*commit* a rename the user meant to abandon. `Esc` must run the field's own
cancel path and only then blur, so the two orderings cannot disagree.

**IME composition.** `chat.tsx:467` already refuses to send while
`event.nativeEvent.isComposing`, because Enter accepts a candidate mid-word. The
same guard has to cover the `Esc` that leaves INSERT: an IME uses `Esc` to
dismiss its candidate window, and stealing it would strand the composition. The
composer has no `Escape` handler at all today, so this is new code, not a patch.

## `keymap.json`

Device-local, in Electron's `userData`, beside `notification-prefs.json`. It
follows the `PrefsStore` precedent at `main/prefs.ts`: what a keyboard does is a
property of this machine, and a user with three paired hosts should not answer
the question three times.

```jsonc
{
  "version": 1,
  "enabled": true,
  "timeoutlen": 300,
  "leader": "<space>",
  "bindings": [
    { "keys": "g c",      "command": "panel.chat" },
    { "keys": "<C-\\><C-n>", "command": "terminal.normal", "when": "terminal" },
    { "keys": "d d",      "command": null,        "when": "sidebar" }
  ]
}
```

- A `bindings` entry **overrides** the default of the same `keys` and `when`.
  The file holds overrides only, so a default added in a later version arrives
  bound rather than missing. This is the same reasoning `PrefsStore.load`
  already applies to `alerts`.
- `"command": null` unbinds. Without it there is no way to remove a default you
  dislike.
- The file is hand-editable and hot-reloaded on write. Anyone who wants vim
  keybindings wants to edit a file.
- A parse failure falls back to defaults and raises a toast naming the line. It
  never leaves the app unbound: an unusable keymap plus a mouse-free user is a
  person who cannot recover.

Settings gains a **Keys** pane listing every command with its binding, a capture
field that records a pressed sequence, a conflict warning, and a reset. The pane
writes the same file.

## Resolution

A pure module, `renderer/vim/resolve.ts`, with no DOM import — the same shape as
`renderer/keys.ts`, and testable the same way under `node --test`.

```ts
type Resolution =
  | { kind: 'pending'; keys: string; candidates: Command[] }
  | { kind: 'run'; command: Command; count?: number }
  | { kind: 'none' }
```

It holds a trie of sequences, a pending buffer, and a count prefix. A prefix
that no longer matches resolves to `none` and clears rather than swallowing the
key. The `timeoutlen` timer is the caller's business, not the resolver's, so the
resolver stays a pure function of keys and keymap.

`Esc` abandons a half-typed sequence **and only that**. With nothing pending it
is an ordinary key, which is what lets it be bound to leaving insert mode. An
earlier draft short-circuited on `Esc` unconditionally, which made the binding
that leaves insert impossible to reach.

The `candidates` list is what the overlay draws, which is why it comes out of
the resolver rather than being computed twice.

## The overlay

Two parts, both at the bottom edge, both reading the registry so neither can
drift from the real bindings.

**The mode segments** ride the existing `status-line.tsx` rather than a bar of
their own. The first draft of this spec proposed a second bar below it; that was
wrong. Two bars at the foot of the window is one more than the window can spare,
and the mode belongs next to the branch because both answer "where am I".

```
 NORMAL  sidebar  ~/workspace/helios  main  claude-opus-4-7  bypass        3g
```

Mode first and coloured, as lualine does. Zone second. The pending keys go hard
right with `margin-left: auto`, so a half-typed sequence appears in the same
place whatever the session segments in front of it are doing.

One consequence worth naming: the bar renders only when a session is selected
(`detail.tsx:372`), so with nothing selected there is no mode indicator. The
sidebar is still navigable, and selecting anything brings the bar back.

**The hint panel** slides up when a prefix has been pending for `timeoutlen`. It
lists only the candidates the resolver returned, grouped by `Command.group`, in
columns:

```
 <space>
 ┌──────────────┬──────────────┬──────────────┐
 │ f  +find     │ g  +git      │ s  +session  │
 │ w  +window   │ h  +host     │ q  quit      │
 └──────────────┴──────────────┴──────────────┘
```

A `+` marks a prefix, a bare label marks a leaf — which-key's own convention.

## Coverage

The audit below is the working list. Each row is an action reachable by mouse
today. A row is done when a command id exists and a key or `:` reaches it.
**Rows without a key are not gaps** — `:` covers them. Rows without a command id
*are* gaps.

### Sidebar — `sidebar.tsx`

| Action | Command | Key |
| --- | --- | --- |
| Select session | `sidebar.select` | `Enter` |
| Next / previous row | `sidebar.next` / `.prev` | `j` / `k` |
| First / last row | `sidebar.first` / `.last` | `gg` / `G` |
| Filter the list | `sidebar.search` | `/` |
| Clear the filter | `sidebar.clearSearch` | `Esc` |
| New session | `session.new` | `o`, `⌘N` |
| New session in this project | `session.newHere` | `O` |
| New group | `group.new` | `<leader>gn` |
| New group at top level | `group.newRoot` | — |
| Rename session | `session.rename` | `<leader>r` |
| Regenerate title | `session.retitle` | — |
| Resume session | `session.resume` | `<leader>R` |
| Open terminal | `session.terminal` | `t` |
| Pin / unpin | `session.pin` | `<leader>p` |
| Permission mode | `session.mode` | `<leader>m` |
| Move to group | `session.group` | `<leader>g` |
| Terminate | `session.terminate` | `<leader>x` (confirm) |
| Delete | `session.delete` | `dd` (confirm) |
| Row actions menu | `sidebar.menu` | `<leader><leader>` |
| Fold / unfold group | `sidebar.fold` | `za` |
| Collapse host | `sidebar.foldHost` | `zc` |
| Arrange menu | `sidebar.arrange` | `<leader>a` |
| Toggle automated runs | `sidebar.autoRuns` | — |
| Reorder within group | `sidebar.moveUp` / `.moveDown` | `<C-k>` / `<C-j>` |
| Add host | `host.add` | — |
| Quit | `app.quit` | `<leader>qq` |

Reordering is drag-only today and has no keyboard route at all.

### Detail and panels — `detail.tsx`, `layout.ts`

| Action | Command | Key |
| --- | --- | --- |
| Chat / Terminal / Approvals / Git / Files | `panel.chat` … `panel.files` | `gc gt ga gd gf` |
| Next / previous panel | `panel.next` / `.prev` | `Shift-l` / `Shift-h` |
| New shell | `terminal.new` | `<leader>tn` |
| Reload shell | `terminal.reload` | — |
| Close shell | `terminal.close` | `⌘W`, `Ctrl-w q` |
| Reconnect / disconnect | `session.reconnect` / `.disconnect` | — |
| Select terminal tab | `terminal.tab` | `1`…`9` |
| Dismiss the show-note | `note.dismiss` | `Esc` |
| Split / close / equalise / resize / move | `window.*` | `Ctrl-w` family |

### Transcript — `chat.tsx`

| Action | Command | Key |
| --- | --- | --- |
| Scroll down / up | `transcript.down` / `.up` | `j` / `k` |
| Half page | `transcript.halfDown` / `.halfUp` | `Ctrl-d` / `Ctrl-u` |
| Top / bottom | `transcript.top` / `.bottom` | `gg` / `G` |
| Load older | `transcript.older` | `<leader>o` |
| Focus composer | `composer.focus` | `i` |
| Send | `composer.send` | `Enter` in INSERT |
| Stop the agent | `session.stop` | `<leader>s` |
| Attach files | `composer.attach` | `<leader>A` |
| Keep or file a paste | `paste.keep` / `paste.file` | `y` / `f` |
| Remove an attachment | `composer.dropAttachment` | `x` |
| Next / previous message | `transcript.nextMsg` / `.prevMsg` | `]m` / `[m` |
| Copy message | `transcript.copy` | `yy` |
| Expand a tool block | `transcript.toggleTool` | `za` |
| Open / find a file chip | `chip.open` / `chip.find` | `Enter` / `gf` |
| Copy a chip's path | `chip.copyPath` | `yp` |
| Select lines to quote | `transcript.visual` | `v`, `V` |

### Files and editor — `files.tsx`, `file-tree.tsx`, `editor.tsx`

| Action | Command | Key |
| --- | --- | --- |
| Toggle explorer | `files.explorer` | `<leader>e`, `⌘B` |
| Go to file | `files.quickOpen` | `<leader>ff`, `⌘P` |
| Find in files | `files.grep` | `<leader>fg`, `⇧⌘F` |
| Tree down / up | `tree.next` / `.prev` | `j` / `k` |
| Collapse / expand | `tree.collapse` / `.expand` | `h` / `l` |
| Open | `tree.open` | `Enter` |
| Breadcrumb up | `files.up` | `-` |
| Choose worktree or folder | `files.root` | `<leader>fw` |
| Back to session folder | `files.rootReset` | — |
| Select / close tab | `files.tab` / `.closeTab` | `1`…`9` / `<leader>bd` |
| Save | `editor.save` | `⌘S`, `:w` |
| Reload from disk | `editor.reload` | `:e!` |
| Keep my version | `editor.keepMine` | — |
| Preview / edit | `editor.preview` | `<leader>v` |
| Copy contents | `editor.copyAll` | `<leader>y` |
| Fold a section | `files.foldSection` | `za` |
| Motions, visual, search, undo | CodeMirror vim | full vim |

The editor uses `@replit/codemirror-vim`, added to the `extensions` array in
`editor.tsx:116`. It brings its own `:` line; the app's `:` is suppressed while
the editor holds focus, so there is one command line and it is the one whose
buffer you are in.

### Git, diff, review — `git.tsx`, `commits.tsx`, `review.tsx`, `diff-view.tsx`

| Action | Command | Key |
| --- | --- | --- |
| Next / previous hunk | `diff.nextHunk` / `.prevHunk` | `]c` / `[c` |
| Next / previous file | `diff.nextFile` / `.prevFile` | `]f` / `[f` |
| Split / unified | `diff.layout` | `<leader>dl` |
| Select a changed file | `git.selectFile` | `Enter` |
| Scope menu | `git.scope` | `<leader>gs` |
| Working tree / all branches | `git.allBranches` | — |
| Pick a commit | `git.pickCommit` | `Enter` |
| Compare two commits | `git.compare` | `Ctrl-Enter` |
| Load more commits | `git.moreCommits` | — |
| Worktrees | `git.worktrees` | `<leader>gw` |
| Jump to a review file | `review.jumpFile` | `Enter` |
| Mark reviewed | `review.mark` | `<leader>gr` |
| Comment | `review.comment` | `c` |
| Send comment | `review.send` | `⌘Enter` |

### Approvals and notifications — `approvals.tsx`, `notification-card.tsx`

Every action here changes what an agent is allowed to do, so every one carries
`confirm`. A single letter must never approve a tool call by accident.

| Action | Command | Key |
| --- | --- | --- |
| Next / previous card | `approvals.next` / `.prev` | `j` / `k` |
| Approve | `approvals.approve` | `<leader>y` (confirm) |
| Deny | `approvals.deny` | `<leader>n` (confirm) |
| Trust folder | `approvals.trust` | — (confirm) |
| Edit before approving | `approvals.edit` | `i` |
| Answer a question | `approvals.pick` | `1`…`9` |
| Submit answers | `approvals.submit` | `⌘Enter` |
| Skip | `approvals.skip` | — |
| Accept / decline | `approvals.accept` / `.decline` | — (confirm) |
| Retry | `approvals.retry` | — |
| Dismiss | `approvals.dismiss` | `x` |

### Schedules — `schedules.tsx`

| Action | Command | Key |
| --- | --- | --- |
| Next / previous | `schedules.next` / `.prev` | `j` / `k` |
| Select | `schedules.select` | `Enter` |
| New | `schedule.new` | `o` |
| Edit | `schedule.edit` | `i` |
| Save | `schedule.save` | `⌘S` |
| Pause / resume | `schedule.toggle` | `<leader>p` |
| Run now | `schedule.runNow` | `<leader>x` (confirm) |
| Delete | `schedule.delete` | `dd` (confirm) |
| Describe with AI | `schedule.describe` | — |
| Test the check | `schedule.test` | — |
| Switch tab | `schedule.tab` | `1`…`9` |
| Open a run | `schedule.openRun` | `Enter` |
| Link a child | `schedule.link` | — |
| Clear selection | `schedules.clear` | `Esc` |

### Rail, settings, hosts, dialogs

| Action | Command | Key |
| --- | --- | --- |
| Sessions / Schedules / Settings | `mode.sessions` / `.schedules` / `.settings` | `<leader>1` `2` `3` |
| Settings section | `settings.section` | `j` / `k` |
| Open settings | `settings.open` | `⌘,` |
| Reload themes | `settings.reloadThemes` | — |
| Reset notification prefs | `settings.resetPrefs` | — |
| Pair this machine | `host.pairLocal` | — |
| Pair a remote host | `host.pairRemote` | — |
| Every dialog: confirm / cancel | `dialog.confirm` / `.cancel` | `Enter` / `Esc` |
| Dialog list movement | `dialog.next` / `.prev` | `Ctrl-n` / `Ctrl-p` |
| Release notes | `updates.show` | — |

`Ctrl-n` and `Ctrl-p` rather than `j`/`k` inside dialogs: a dialog with a text
field is in INSERT, so plain letters must type.

This is not a new convention. `quick-open.tsx:70` and `root-picker.tsx:107`
already accept `ArrowDown`/`Ctrl-n`, `ArrowUp`/`Ctrl-p` and `Enter`. Those two
are the reference implementation; the remaining dialogs — `newsession.tsx`,
`group-picker.tsx`, `worktrees.tsx`, `updates.tsx` — should copy them rather
than invent anything.

`SECTIONS` in `settings.tsx` listed five panes. **Keys** is now the sixth,
between Notifications and Hosts: it carries the vim switch and the whole keymap
as a table, so what the keys do is readable without pressing them.

## The four gaps

Four things have no keyboard route at all today, and none is fixed by binding an
existing handler.

### 1. Terminal scrollback

Reading output above the fold is a pointer operation today. `terminal.tsx` loads
`FitAddon`, `WebLinksAddon` and `WebglAddon` — there is no search addon and no
way to move the viewport from the keyboard.

`SCROLL` mode is entered with `Ctrl-\ Ctrl-n`, the same chord that leaves
`TERMINAL` mode. Leaving the pty and being able to read what it printed are the
same intention, so they are the same key.

- Add `@xterm/addon-search`.
- `j k Ctrl-d Ctrl-u gg G` scroll, over `term.scrollLines()`.
- `/` and `?` search, `n` and `N` repeat, over the search addon.
- `Esc` returns to the live edge and to `TERMINAL`.

**Selection and yank are deliberately excluded.** A copy cursor means drawing an
overlay in cell coordinates above the WebGL canvas, and keeping it correct
across scroll, resize and reflow. No xterm addon does it, so it is hand-built
and it is the most expensive item in the whole feature.

The trade it buys is worth naming plainly. Reading scrollback happens many times
an hour, so it earns keys. Copying a line out of scrollback happens perhaps once
an hour, and a mouse does it in a second. Paying the most expensive item in the
spec for the rarest action is the wrong trade, so copying stays a mouse job.

This is the one place where the mouse-free goal is knowingly not met.

### 2. Row and context menus

Smaller than it looks. Every popover in the app is one component,
`selection-menu.tsx`, driven by a `MenuAction[]` — label, `run`, `danger`,
`disabled`, and nested `children`. `session-menu.ts` builds that array. So the
content is already data and needs no change.

Two things are missing from `SelectionMenu`:

- **Keyboard navigation.** It listens for `Escape` to dismiss
  (`selection-menu.tsx:50`) and nothing else. It needs `j`/`k` to move a
  highlight, `Enter` to run, `l`/`h` to enter and leave `children`, and it must
  skip `disabled` rows.
- **Anchoring to an element.** The `anchor` prop is `'point' | 'above'` and both
  take `x`/`y`. A third value, anchored to the focused row's bounding rect, is
  what lets a key open the menu where the pointer is not.

Both land in one file, and every caller benefits at once.

### 3. Drag-only reordering

Manual session sort and group reparenting exist only as drags, as does pane
rearrangement. `moveItem` in `layout.ts` already covers panes. Sessions and
groups need `sidebar.moveUp` / `.moveDown` and a move-to-group command wired to
the same mutation the drop handler calls.

### 4. Confirmation

`dd` on a session and `<leader>x` on a schedule are destructive, and a mistyped
sequence must not be able to fire them. `Command.confirm` opens a prompt at the
bottom edge that takes `y` or `n`, which is Neovim's `confirm` and needs no new
concept. Approvals use the same mechanism for the opposite reason: not because
undoing is hard, but because an agent acts on the answer immediately.

## Enforcement

The invariant — every action is a command — decays silently unless something
checks it. Three mechanisms, in increasing cost:

1. **`:` is the audit.** An action missing from the palette is missing from the
   feature. This costs nothing and catches most omissions in ordinary use.
2. **A registry test.** Assert every registered command has a unique id, a
   non-empty title, a group the overlay knows, and — for anything with
   `confirm` — no single-key binding. Runs under `node --test` with the
   resolver tests.
3. **An audit test.** A test that walks the component sources, counts `onClick`
   handlers, and compares against a recorded number. When the count moves, the
   author either registers the new action or updates the number deliberately.
   Crude, but it makes the invariant visible in review, which is where it is
   actually kept.

## Testing

- `resolve.ts` — pure, and where the risk is. Sequences, prefixes, counts,
  timeouts, `Esc`, unbinding via `null`, override precedence, unknown keys.
- The keymap loader — malformed files, unknown command ids, conflicts, and the
  fallback that must never leave the app unbound.
- Mode transitions — every edge in the table above, and specifically that
  `TERMINAL` passes single and double `Esc` through untouched.
- Playwright (`npm run e2e`) — one test that starts a session, sends a prompt,
  opens a file, splits a pane and closes it, using **only** key events. That
  test is the specification of the goal, and it should fail loudly rather than
  be skipped.

Both harnesses exist and need no setup. `test/layout.test.ts` already covers the
reducers the `Ctrl-w` family binds to, so those commands need tests for the
binding, not for the behaviour. `e2e/` runs a real daemon through
`e2e/daemon.ts` and `e2e/fixtures.ts`, which is what makes a keyboard-only walk
through the app possible to write at all.

Two dependencies are new: `@xterm/addon-search` and `@replit/codemirror-vim`.
Neither is in `package.json`. `@codemirror/search` is already there and is a
different thing — it searches an editor buffer, not a terminal.

## Non-goals

- Registers, macros, marks and jumplists.
- Operator-pending grammar outside the editor.
- Vimscript or Lua configuration. `keymap.json` is data.
- Vim mode on mobile.
- A copy cursor over terminal scrollback. Selecting and yanking there stays a
  mouse job — see gap 1 for the reasoning.
- Replacing the existing ⌘ accelerators. They keep working in every mode, and
  they are what a user who has not turned vim mode on still has.

## Shape of the work

The vertical slice lands as one feature but not as one commit. Reviewable order:

1. `resolve.ts`, the registry types, and their tests. No UI.
2. Zones in the store above `Layout.focused`, the focus ring, and
   `Ctrl-h/j/k/l`.
3. The mode machine and the mode line. Vim mode becomes switchable here.
4. The `Ctrl-w` family over the store methods listed above. No new state.
5. **Lift the component state named in "The state that has to move first".** No
   user-visible change, and nothing after this point works without it.
6. Keyboard navigation and rect anchoring in `selection-menu.tsx`.
7. Registration sweep across the components in the coverage tables.
8. `:` over the registry, reusing `quick-open.tsx`'s matcher.
9. The hint overlay.
10. `keymap.json`, the loader, and the Settings **Keys** pane.
11. `@replit/codemirror-vim` in the editor.
12. `SCROLL` mode and the search addon.

Steps 1 to 4 are the foundation and are worth landing before anything else: they
are self-contained, they bind to code that already exists and is already tested,
and they are where the design is proved or found wrong.

Step 5 is the one to watch. It is a pure refactor with nothing to show for it,
which makes it the step most likely to be skipped or smuggled into step 7. It
should not be. A registration sweep that drags a state-lifting refactor along
with it is unreviewable, and the sweep is already the widest change in the
feature.
