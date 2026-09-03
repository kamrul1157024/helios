# Files Without a Root: Folders, an Index, and a Path You Can Type

## The claim

Three questions a person asks the file panel, and the answer each one gets today:

> *"Open this exact file."* Paste `~/workspace/helios/docs/specs/57-vim-mode.md`
> into ⌘P. **No matching file under /Users/md.kamrul.hassan.** The file is there.
> The daemon can already read it. The search rejected it on the first character.
>
> *"Find this file, I forget where it is."* It has to be under the one root the
> panel is pointed at. If it is not, nothing.
>
> *"Look over there instead."* Replace the root. The whole panel moves, and the
> place you were is gone.

All three are the same mistake: **the panel has one root, and everything is
addressed relative to it.** A single current directory is a shell's model, not an
editor's. VS Code abandoned it years ago and the difference is not cosmetic —
with a folder *set* there is no root to be wrong about, no root to replace, and a
path that names a real file is just a file.

This spec adopts that model, and adds the index that makes it affordable.

**Scope:** `internal/server` (a path-aware search, an index), `desktop/`
(quick-open, root picker, files panel), `mobile/` (which gains search it has
never had). No new Go dependency. The endpoints keep their shapes; two gain
fields.

## Where we are

| | Today |
|---|---|
| Root | exactly one, per session, in `localStorage` under `helios.files.${hostId}.${sessionId}` |
| Changing it | replaces the tree wholesale (`root-picker.tsx:36`) |
| Quick-open query | sent raw to the daemon (`quick-open.tsx:49`); no path awareness |
| Root-picker query | *is* path aware — `query.startsWith('/')` browses the parent (`root-picker.tsx:43-45`) |
| Tilde | handled by `resolveSafePath` (`files.go:185`) and by `resolveFilePath` (`markdown.ts:312`); handled by neither picker |
| No-match help | offers the parent directory (`root-picker.tsx:180`) |
| Chip Open | `inRoot` remaps the path into the current root, then falls back (`files.tsx:478-484`) |
| Chip Find | throws the directory away — `setQuickOpenQuery(basename(path))` (`files.tsx:473`) |
| Candidate list | rebuilt on every keystroke (`filesearch.go:286`). No cache |
| Non-git root | full walk, capped at 100k files (`filesearch.go:28`) or 10s (`filesearch.go:26`) |
| Mobile search | none. It browses (`file_browser_screen.dart:19`) and views (`:458`), and cannot search |
| Path parsers | two regexes that must agree — `markdown.ts:286` and `message_card.dart:84` — plus the picker's own `/` test |

## The reported failure, in full

Worth walking, because each layer fails differently and all three need fixing.

The root was `/Users/md.kamrul.hassan`. The query was
`~/workspace/helios/docs/specs/57-vim-mode.md`.

1. `rankPaths` lowercases and strips whitespace (`filesearch.go:353`), then hands
   the string to `fuzzyScore`, which walks it as a subsequence
   (`filesearch.go:385`). Candidates are **relative** to the root, so none
   contains `~`. The first character fails and every file is rejected. Typing
   `/Users/…` fails on the second: no relative path contains `Users`.
2. Before failing, it walked all of `$HOME` — not a git repository, so
   `walkCandidates` (`filesearch.go:313`) rather than `git ls-files`. Once per
   keystroke, to 100k files or ten seconds.
3. The empty state offered **↑ Users**. Going up doubles the walk and cannot
   contain the answer any more than the current root already did. The file was
   four levels *down*.

The chip route fails for a different reason. `markdown.ts:286` parses the tilde
path correctly, `chat.tsx:611` keeps it whole, and then **Find** discards
everything but `57-vim-mode.md` (`files.tsx:473`) and asks the same broken search
the same broken question. The parser was right; the consumer threw its work away.

## Folders, not a root

A session owns a **folder set**:

```ts
interface Folder {
  path: string
  /** Why it is here. Decides ordering and whether it can be removed. */
  origin: 'cwd' | 'worktree' | 'added'
}
```

- The session's cwd is always folder zero and cannot be removed.
- Every worktree of its repository joins automatically — they are already
  fetched (`git worktree list`), and they are already where the agent's other
  checkouts live.
- Anything else the user adds.

The explorer draws the set as top-level nodes. Search covers the set. Opening
takes an absolute path and does not consult a root at all.

**`inRoot` is deleted** (`files.tsx:1244-1250`). It exists to guess which
checkout a transcript path meant when the panel is pointed at a different one —
a guess that silently rewrites `/…/service-a/src/main.go` into
`/…/service-b/src/main.go` and opens the wrong file with no indication. Once
every worktree is a folder, the path from the transcript is already inside the
set and there is nothing to guess. Deleting a heuristic is better than testing
it.

The persisted `root` migrates to a one-element `added` folder, so nobody loses
their place.

### What replaces the root picker

Removing the root leaves two jobs behind, and they were always different jobs
wearing one dialog:

- **Add a folder** — the path-completing browser `root-picker.tsx` already is.
  It keeps its behaviour and gains `~` (below). It appends rather than replaces.
- **Focus a folder** — narrow the explorer and the search to one member of the
  set, reversibly, the way VS Code's explorer collapses to one node. This is what
  people actually wanted when they replaced the root, and it does not throw the
  other folders away.

## A path is a path, wherever it is typed

One rule, and it lives in the daemon so that every client gets it at once:

> **A query resolves as a path when its directory part names a real directory.
> Otherwise it is a fuzzy query.**

Concretely, in `handleSearchFiles`:

1. If `q` starts with `/` or `~/`, expand and split it into directory and tail.
2. Otherwise, if `q` contains `/`, split it the same way and expand — but only
   accept the split when the directory part is absolute or tilde-rooted.
3. `stat` the directory part. Not a directory → fall through to fuzzy.
4. A directory → search *there* for the tail, and return absolute paths.
5. An empty tail (`~/workspace/helios/`) lists that directory.

The stat is one syscall and only runs on queries containing `/`, so the fuzzy
path is unchanged. Multi-segment relative queries like `internal/terminal/host`
stay fuzzy, which is right: they are how people fuzzy-match a path today, and
`fuzzyScore` already rewards the `/` matching in order.

The response gains one field so a client can say what it did:

```jsonc
{
  "root": "/Users/md.kamrul.hassan/workspace/helios/docs/specs",
  "resolved_from": "path",      // "path" | "query"
  "matches": [ /* … */ ],
  "scanned": 41,
  "truncated": false,
  "indexing": false             // see below
}
```

Putting this in the daemon rather than in `quick-open.tsx` is deliberate. There
are already two path regexes that must agree (`markdown.ts:286`,
`message_card.dart:84`) and a third rule in the root picker
(`root-picker.tsx:43`). Adding a fourth client-side would guarantee they drift.
Mobile gets path-aware search without shipping any parsing.

`resolveSafePath` (`files.go:185`) already expands `~` and resolves symlinks, and
there is no root jail — the daemon can read anything the user can. So this adds
no reach that the API did not have; it only lets the search reach what the reader
already could.

## The index

Re-walking a tree per keystroke is the reason a large folder is unusable, and no
amount of debouncing fixes it — `DEBOUNCE_MS` is already 90ms
(`quick-open.tsx:21`) and the walk is seconds.

An index, keyed by resolved absolute path, shared by every session and client:

```go
type index struct {
    root    string
    files   []string   // relative, as candidateFiles returns today
    built   time.Time
    git     bool       // built from git ls-files rather than a walk
    partial bool       // hit maxCandidates or the timeout
}
```

- Built by the existing `candidateFiles` (`filesearch.go:286`). No new
  enumeration code, and `skipDirs` (`filesearch.go:40`) keeps applying.
- A query on a **warm** index scores only. That is `rankPaths` over a slice —
  microseconds, and the 10-second timeout stops being reachable.
- A query on a **cold** index starts the build, waits briefly for it, and returns
  whatever exists with `indexing: true`. The client shows results as they firm up
  rather than a blank pane for ten seconds. A cold root should feel like a slow
  answer, not a hang.
- Staleness is a TTL, plus an immediate rebuild when a *git* root's `HEAD` or
  index mtime moves, since that is when the file list actually changes in bulk.
- Concurrent queries on one cold root share a single build.
- The cache is bounded by count and evicts least-recently-queried. An index of
  100k paths is a few megabytes; a handful of roots is not a memory problem, and
  an unbounded map of every directory anyone ever typed would be.

### Why not fsnotify

Spec 54 rejects a recursive watcher, and the argument holds here. A watcher
reports every write under the tree; `npm install` is tens of thousands of them.
Spec 54's concern is that those become SSE broadcasts that evict `session_status`
from a client's 64 slots (`sse.go:36-42`). An index would not broadcast, so it
escapes that specific harm — but it still pays a watch on every folder anyone has
open, on a daemon that today watches none, for a list that is only wrong between
a file's creation and the next TTL.

A stale index costs a user one missing row for a few seconds. A watcher costs a
dependency, a descriptor budget, and a per-platform failure mode. Take the stale
row. If it proves annoying, the narrower fix is to invalidate on the
`PostToolUse` hook spec 54 already touches — the agent creating a file is the
case that matters, and the daemon is told about it there.

## Suggestions that point at the answer

The empty state must never again offer only the one direction that cannot help.
In order:

1. **The query is a path that exists.** Show it as a single row — *Open
   `57-vim-mode.md` · `~/workspace/helios/docs/specs`*. This alone resolves the
   reported bug.
2. **Another folder in the set matches.** Show those matches, badged with the
   folder. Cheap once indexed, and it is usually the real answer when several
   worktrees are in flight.
3. **Nothing matches anywhere.** Offer *Add a folder…*, not the parent
   directory. Climbing is a bigger walk and a worse guess.

The parent pill (`root-picker.tsx:180`) is removed from the search empty state.
It stays in the folder browser, where climbing is the point.

## The clients

### Desktop

- `quick-open.tsx` renders `resolved_from: "path"` as the single Open row, and
  drops `RootSuggestions` for the ordered list above.
- Results carry a folder badge when the set has more than one member. One
  member, no badge — a badge that is always the same word is noise.
- `files.tsx:473` stops calling `basename`. Chip **Find** passes the resolved
  path, which the daemon now understands. Chip **Open** loses the `inRoot` hop
  and opens the absolute path.
- The explorer draws folders as top-level nodes; per-folder expansion state joins
  the existing per-session `localStorage` record.

### Mobile

Mobile has the browser (`file_browser_screen.dart:19`), the viewer (`:458`), the
worktree picker (`:102`) and the chips (`message_card.dart:240`). It has no
search of any kind. It gets one, against the same endpoint, because the index is
what made it affordable and the daemon is what made it correct.

`_rootPath` becomes the folder set. Tapping a chip whose path sits outside every
folder opens the file anyway — it already does (`message_card.dart:122-153`), and
that is the behaviour to keep, not the restriction to spread.

## What we are not doing

- **No content-search change.** `handleGrepFiles` (`filesearch.go:108`) keeps its
  ripgrep-then-native shape and its limits. It gains the folder set as its scope
  and nothing else.
- **No machine-wide search.** The set is explicit. A file outside it is reachable
  by typing its path, which is the escape hatch and does not need a second one.
- **No frecency or recently-opened ordering.** Worth having, needs a store to
  hold it, and it would hide whether the ranking underneath is right.
- **No persistent on-disk index.** In-memory, per daemon lifetime. A cold start
  after a restart is one walk.
- **No third path regex.** If `markdown.ts:286` and `message_card.dart:84` should
  converge, that is its own change.

## Tests

Go, beside the existing `filesearch_test.go`:

- A `q` of `~/dir/name` searches `dir` and returns absolute paths, with
  `resolved_from: "path"`.
- The same for `/abs/dir/name`.
- A directory part that does not exist falls back to fuzzy — `foo/bar/baz` still
  matches `foo/bar/bazqux.go` under the root.
- `internal/terminal/host` stays fuzzy and still ranks
  `internal/terminal/host.go` first, proving the split did not capture ordinary
  queries.
- A trailing slash lists the directory.
- The index: a second query does not re-walk; a git root rebuilds when `HEAD`
  moves; two concurrent cold queries produce one build; eviction is by
  least-recently-queried.
- `indexing: true` on the first cold query, `false` once built.

Desktop, under `node --test` beside `paths.test.ts`:

- Chip **Find** sends the full resolved path, not the basename. This is the
  regression that produced the report.
- The empty state offers Open-this-path and other folders, never the parent.
- Folder-set reducers: add, remove, focus, unfocus; cwd cannot be removed;
  worktrees join without duplicating an added folder at the same path.
- Migration: a persisted `root` becomes a one-element added set.

`inRoot` gets no test, because it is being deleted.

## Shape of the work

Reviewable in this order. The first two are the whole reported bug and are worth
landing before the model change.

1. Path-aware `handleSearchFiles`, with its tests. Fixes the paste-a-path case on
   both clients at once, with no UI change.
2. `files.tsx:473` stops discarding the directory; the empty state is reordered.
3. The index, behind the same endpoint. Pure performance, no behaviour change.
4. The folder set in the desktop store, with migration. `inRoot` deleted.
5. The explorer, the badge, and the split of the root picker into add and focus.
6. Mobile: the folder set, then search.
