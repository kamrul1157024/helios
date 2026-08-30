# React Query as the Desktop Data Layer

## The claim

Every read the desktop makes should go through one cache, every write should go
through one mutation path, and the daemon's event stream should invalidate that
cache rather than hand-patch a store.

Today the renderer has a 1321-line `Store` class that is three things at once: a
cache of server data, a set of hand-rolled refetch schedulers, and the window's
own UI state. Around it, nine components hand-roll their own fetching with
cancellation flags. Nothing dedupes, nothing shares, and every screen invents
its own loading and error handling.

This spec moves the server half into React Query and leaves the store holding
only what is genuinely this window's business.

**Scope: `desktop/` only.** Nothing in `internal/`, `cmd/` or `mobile/` is
touched, no daemon route changes shape, and no wire format moves. Every client
keeps talking to the same API it does today — this is a change to how one
renderer holds the answers, not to what it asks for.

## Where we are

| | Today |
|---|---|
| Server cache | `Store.state.sessions`, `.notifications`, `.groups`, `.stats`, `.sortMode`, `.groupsUnsupported` |
| Reads | 24 read methods on `HostApi`, 20 of them called, from 14 files |
| Writes | 26 write methods on `HostApi`, 25 of them called, from 9 files |
| Cancellation | 69 `cancelled` flags across 9 components — and two components with no guard at all |
| Refetch | `refreshHost`, `refreshSessions`, `refreshNotifications`, `refreshGroups`, `refreshSortMode`, `syncShells`, plus a debounced `scheduleRefresh` (`store.ts:789`) |
| Invalidation | nine SSE cases hand-patching state (`store.ts:718-780`) |
| Optimism | hand-written save-and-rollback in `reorderSessions` (`store.ts:519-537`), `reorderGroups` (`store.ts:653-676`) and `setSortMode` (`store.ts:483-494`) |
| Dedup | none |

`bridge.api.call` already unwraps the main process's `{ok, value}` envelope and
rethrows with the daemon's HTTP status attached (`src/preload/preload.ts:33-43`,
`bridge.ts:164-171`). So `api(hostId).readFile(path)` is already a promise that
resolves or rejects — it drops into a `queryFn` unchanged, and no adapter layer
is needed.

Five methods on `HostApi` have no caller in the renderer: `getSession`,
`subagents`, `commands`, `devices` and `wake`. They are not wrapped. If one
gains a caller later it gets a key then, not now.

## What this costs today, stated plainly

**The same resource is fetched several times over.** Three reads are asked for
with identical arguments from separate components, so today they are separate
IPC round trips for a byte-identical answer:

- `gitWorktrees(cwd)` — `worktrees.tsx:28` and `files.tsx:112`
- `providers()` — `newsession.tsx:36` and `detail.tsx:471`
- `settings()` — `settings.tsx:455`, `settings.tsx:601` and `store.ts:465`

`gitDiff`, `gitLog`, `gitChanges` and `listFiles` also have several callers
each, but with different arguments — `git.tsx:89` probes the log with one
limit while `commits.tsx:171` pages it with another. Those are distinct keys
and will not dedupe. Naming them as duplicates overstates the win; the honest
claim is that they gain caching across mounts, not sharing across panels.

**Every component reinvents the same lifecycle.** Sixty-nine `cancelled` flags
exist because each fetch has to decide for itself what to do when its inputs
change mid-flight. That is the same bug surface written nine times, and it is
where the files-panel race in PR #112 came from. Two more components —
`file-tree.tsx:52` and `find-in-files.tsx:52` — fetch in an effect with no
guard of any kind, which is the same race with nothing written against it.

**One event refetches everything.** `session_status` arrives for a single
session and `scheduleRefresh` refetches that host's whole session list. It is
debounced, which makes it cheap rather than correct: the list is refetched
because the store has no way to say "this one row changed".

**Loading and error states are ad hoc.** There is no shared answer to "is this
pending", so screens either show nothing or show a spinner someone wrote by
hand, and a failed read is a `store.fail(err)` toast rather than something the
view can render around.

## The change

### One query key per resource

Keys are namespaced by host first, because two hosts can hold the same path and
must not collide:

```ts
['host', hostId, 'sessions', { grouped }]
['host', hostId, 'groups']
['host', hostId, 'notifications']
['host', hostId, 'settings']
['host', hostId, 'directories']
['host', hostId, 'providers']
['host', hostId, 'models', provider]
['host', hostId, 'git', 'status', cwd]
['host', hostId, 'git', 'diff', cwd, file, at]
['host', hostId, 'git', 'log', cwd, opts]
['host', hostId, 'git', 'changes', cwd, to, from, mergeBase]
['host', hostId, 'git', 'worktrees', cwd]
['host', hostId, 'git', 'reviewed', cwd, base]
['host', hostId, 'files', 'dir', path]
['host', hostId, 'files', 'content', path]
['host', hostId, 'files', 'search', root, q]
['host', hostId, 'files', 'grep', root, q, opts]
['host', hostId, 'transcript', sessionId]
['host', hostId, 'terminals', sessionId]
```

The argument objects are part of the key because the callers disagree about
them: `listSessions` is asked with `grouped: '1'` only when the sidebar is in
manual mode (`store.ts:684`), and two callers of `gitLog` pass different
limits. A key that dropped them would serve one caller the other's answer.

Hierarchical on purpose: `invalidateQueries({ queryKey: ['host', id, 'git'] })`
takes out every git read for a host in one call, which is what a commit or a
branch change actually means.

The keys live in `src/renderer/keys.ts` and the reads that use them in
`src/renderer/queries.ts`. The split is not cosmetic: `bridge.ts` binds
`window.helios` at module scope and uses a TypeScript parameter property, so
anything importing it cannot be loaded by the test runner's type-stripping.
`keys.ts` reaches nothing, which is what lets the key factory, the SSE map and
the reducers be asserted directly. `errors.ts` exists for the same reason —
`statusOf` is needed by the retry policy, which must not drag in the bridge.

`src/renderer/host-data.ts` fans those keys out across every paired host, in the
shape the sidebar already reads: `Record<hostId, T>`. The views ask "all hosts
at once" because they draw one list.

### Reads

Fourteen resource-shaped reads become hooks and lose their `useEffect`:
`gitStatus`, `gitDiff`, `gitLog`, `gitChanges`, `gitWorktrees`, `reviewedFiles`,
`listFiles`, `readFile`, `searchFiles`, `grepFiles`, `listDirectories`,
`providers`, `models`, `settings`.

The lists — `listSessions`, `listGroups`, `notifications` — move too. They are
the ones the store currently caches by hand, and they are the reason
`scheduleRefresh` exists.

`searchFiles` and `grepFiles` are keyed by their query string and want
`placeholderData: keepPreviousData`, so typing does not blank the results
between keystrokes.

Three reads are not what they look like:

- **`settings` carries the sort mode.** `refreshSortMode` (`store.ts:463-471`)
  fetches the whole settings document to read one field, `sessions.sort`.
  There is no sort-mode route. It becomes a `select` off the settings query,
  and `setSortMode` becomes a mutation on the same key.
- **`listSessions` carries the host stats.** `stats` is the `host` field of the
  session-list envelope (`store.ts:684-691`), not a separate fetch. It is a
  `select`, not a key.
- **`readFile` is not a read the view renders.** It is the seed for an editable
  buffer. See below.

### `readFile` becomes a query, and the buffer syncs from it

`files.tsx` does not display what `readFile` returns. It copies the answer into
an `OpenFile` record (`files.tsx:25-38`) and then owns it: `saved` for the
dirty check and for revert, `modTime` for the write's optimistic-concurrency
check, `dirty`, and `version`, which is bumped only to force a CodeMirror
remount (`files.tsx:756`). The draft the user is typing lives in a ref
(`files.tsx:83`) so a keystroke does not re-render the tree.

The content moves into the cache and the record keeps everything else. Three
things make that safe rather than a rewrite of the panel.

**Only the active tab mounts a query.** A dynamic number of tabs cannot mount a
dynamic number of hooks, but the panel already renders exactly one file
(`files.tsx:503`). The open tabs become a list of paths and per-tab UI state;
the active path gets a child component with one `useQuery` in it, keyed on the
path so a switch remounts. An inactive tab holds no query and needs none —
its draft is already in the ref, and its dirty flag is already on the record.

**The dirty flag becomes authoritative, not derived.** Today dirty is
recomputed against `saved` on every keystroke (`files.tsx:332`), so undoing
back to the original clears it. That still works for the active tab, whose
`saved` is `query.data.content`. What must not happen is a background refetch
moving `saved` under an edited buffer, which would mark a dirty file clean.
So file content is **`staleTime: Infinity`** — the exception to the one-minute
default below. It is refetched only when something asks:

| Trigger | Today | Becomes |
|---|---|---|
| Panel becomes visible again, buffer clean | `reload()` (`files.tsx:306-313`) | `refetch()`, same `!dirty` gate |
| Reload button | `reload()` | `refetch()` |
| Save succeeds | `setFiles` from the write's own result | unchanged — see below |

**The restore loop still validates.** Reopening a session's tabs walks the
saved paths in order and drops the ones that have gone (`files.tsx:222-227`),
quietly, because the agent deletes files. That loop keeps its imperative shape
and calls `queryClient.fetchQuery`, which writes into the same cache the
active tab then reads — so the tab that lands in front is a cache hit, not a
second request.

**`writeFile` does not invalidate the content key.** The save already writes
the authoritative answer back from the mutation's own result, `mod_time`
included (`files.tsx:261-265`). It is a `setQueryData`, not an invalidation:
the server's answer is in hand, and refetching it could only race the buffer it
is meant to agree with.

### The transcript

`transcript` and `transcriptSince` are one resource read two ways, but not in
the direction a first reading suggests. `transcript(id, PAGE, offset)` pages
**backwards**: `chat.tsx:177` asks for it with `offset = messages.length` and
prepends the result above what is already shown. `transcriptSince` is the
forward edge.

So as `useInfiniteQuery`, `transcript` is the query and its offset is the
fetch-more param, in the older direction. `transcriptSince` is not a page
param at all — it is an append through `setQueryData`.

Three things the conversion has to keep, all of which live in `chat.tsx` today:

- **The delta is not driven by an SSE transcript event.** There is none. The
  effect at `chat.tsx:113-141` reruns because `session.last_event_at` moved on
  the session record, which arrives via `session_status`. Under React Query
  the trigger is the same, but it now hangs off the session query rather than
  a component's dependency array.
- **`epoch` resets the entry rather than keying it.** A `epoch_changed` page
  means the transcript forked or was replaced and the held `seq` numbers count
  against nothing. The epoch cannot be in the key: it is only known once the
  first page has arrived, so keying on it would fetch under one key and
  immediately refetch under another. `resetQueries` on the same key is the
  answer — the pages drop and the query fetches again from the top.
- **The first delta must not read a stale ref.** The comment at `chat.tsx:91`
  records the bug: an empty message list asks for everything after `seq -1`,
  which is the page just loaded, appended to itself. A single cache removes
  the two-effect ordering that caused it, which is most of the reason to do
  this at all.

This is the riskiest conversion in the sweep — it is the view the app is mostly
used through — so it lands as its own commit and is the last one in.

### Writes

Twenty-five called methods become mutations. Six deserve optimistic updates,
because they are direct manipulation and a snap-back reads as a failure:

| Mutation | Optimistic |
|---|---|
| `setSessionOrder` | yes — replaces the hand-rolled rollback in `store.ts:519-537` |
| `setGroupOrder` | yes — replaces `store.ts:653-676` |
| `updateSettings` (sort mode) | yes — replaces `store.ts:483-494` |
| `patchSession` (rename, pin) | yes |
| `setReviewed` | yes |
| `setPermissionMode` | yes |
| everything else | no — invalidate on success |

`onSettled` invalidates whatever the write touched. The rollback that
`reorderSessions` writes by hand is `onError` with the snapshot from
`onMutate` — the same logic, deleted from the store.

`setGroupOrder` also refetches the session list on success today
(`store.ts:664-665`), because the sessions carry the position they were
dragged to. That stays, as an invalidation of the sessions key rather than an
awaited refetch.

**One caller needs `refetchQueries`, not `invalidateQueries`.** Dropping a
group header beside another is two writes: re-parent, then order. The second
reads the sibling list back out of the store *after* the first has landed
(`sidebar.tsx:279-286`), because a group arriving from another parent is not in
the target's sibling list until the move commits. Today that works because
`store.moveGroup` awaits its own refetch. Fire-and-forget invalidation would
read the pre-move list and post an order missing the group being dragged. The
mutation awaits `refetchQueries` on the groups key, and the second write reads
`queryClient.getQueryData`.

**Three mutations share the settings key.** `MemoryBudget` writes `memory.*`
(`settings.tsx:497`), `SessionTitles` writes `autotitle.*` (`settings.tsx:631`)
and the sort switch writes `sessions.sort` (`store.ts:487`) — all through
`updateSettings`, all against `['host', id, 'settings']`. The daemon merges:
`handleUpdateSettings` decodes a `map[string]string` and upserts only those
keys (`internal/server/api.go:1676-1690`). So the optimistic update must merge
into the cached `settings` map too. Replacing it would make one pane's save
blank the other pane's fields until the next fetch.

The same three panes hold a **draft** while a control is in motion — the budget
slider updates a label on every step and only writes on pointer-up
(`settings.tsx:485-489`), and the custom prompt saves on blur. That draft stays
in component state. The query is the saved value; the draft is what the pointer
is doing. An invalidation landing mid-drag must not move the thumb, which is
why the settings key is `staleTime: Infinity` and is refetched only by its own
mutations.

### SSE stops patching and starts invalidating

The nine cases in `store.ts:718-780` become key invalidations:

| Event | Becomes |
|---|---|
| `session_status` | `setQueryData` patching that one row inside the sessions list, then invalidate the list — what `patchSession` plus `scheduleRefresh` do today (`store.ts:739-753`) |
| `session_updated` / `session_deleted` | invalidate `['host', id, 'sessions']` |
| `notification` / `notification_resolved` | invalidate notifications **and** sessions — a permission request writes `waiting_permission` to the session and announces only the notification (`internal/provider/claude/hooks.go:110,148`) |
| `terminal_opened` | invalidate `['host', id, 'terminals', sessionId]`; the store still owns the tabs it opens from the answer |
| `terminal_closed` | unchanged: closing a tab is a connection teardown, not a cache event |
| `session_evicted` | toast, then invalidate the host |
| `show` | unchanged: it is a UI instruction, not data |

There is no per-session key, because `getSession` has no caller. A single
session is a row of the list, and that is where the patch lands.

`scheduleRefresh` and its debounce go away. Invalidation is already coalesced.

### What stays in the store

The store keeps what no server knows about: `selection`, `panels`, `activeTabs`,
`tabs`, `detached`, `fileTarget`, `diffTarget`, `showNote`, `promptDraft`,
`query`, `toast`, `loading`, `pairingLink`, `terminalTheme`, `density`,
`grouping`, `groupOrder`, `dirOrder`.

It loses six fields, but not all in the same way:

| Field | Becomes |
|---|---|
| `sessions` | the `['host', id, 'sessions']` query |
| `notifications` | the notifications query |
| `groups` | the groups query |
| `stats` | a `select` off the session list — it rides the envelope |
| `sortMode` | a `select` off the settings query — it is `sessions.sort` |
| `groupsUnsupported` | not state at all: `statusOf(groupsQuery.error) === 404` |

`groupsUnsupported` is worth naming, because it is the one field that gets
smaller rather than moving. Today `refreshGroups` catches a 404, sets an empty
list and a flag, and swallows the error (`store.ts:583-603`). A 404 is the
query's error, and the retry rule below already guarantees it is not retried —
so the flag is a derived read of the error, and the catch block goes.

`hosts` and `hostStatus` are a third case: they are pushed from the Electron
main process over IPC, not fetched from a daemon. They stay in the store, and
`hosts:changed` keeps setting them.

### Three defaults that must not be left alone

React Query's defaults are tuned for a browser tab and are wrong here:

- **`refetchOnWindowFocus` defaults on.** In a desktop app every alt-tab would
  refetch every mounted query against every host. Off.
- **`retry` defaults to three with backoff.** A 404 from `readFile` is routine —
  the agent deletes files, and the files panel reopens tabs quietly for exactly
  that reason. Retry only on network failure and 5xx; never on 4xx.
- **`staleTime` defaults to zero**, so every remount refetches. Push tells us
  when things change, so reads can be generously stale: a minute for git,
  `Infinity` for `providers`, `models`, `settings` and file content. The last
  two are `Infinity` for correctness rather than thrift — a refetch under an
  edited buffer or a dragged slider would move something the user is holding.

## Migration order

One PR, in commits that each stand up on their own:

1. Add `@tanstack/react-query`, mount `QueryClientProvider`, set the defaults.
   It goes in `devDependencies`: esbuild bundles the renderer, so `ws` is the
   only true runtime dependency and React itself already sits there.
2. `queries.ts`: key factory and read hooks. Nothing consumes it yet.
3. SSE → invalidation, running alongside the store's existing patching.
4. Convert git: `git.tsx`, `commits.tsx`, `review.tsx`, `worktrees.tsx`.
5. Convert files: `files.tsx`, `file-tree.tsx`, `root-picker.tsx`,
   `quick-open.tsx`, `find-in-files.tsx`.
6. Convert the rest: `newsession.tsx`, `detail.tsx`, and the two panes of
   `settings.tsx` that talk to a daemon — `MemoryBudget` and `SessionTitles`.
   The rest of that file is `bridge.theme` and `bridge.prefs`, which are main
   process IPC and out of scope.
7. Move the lists out of the store; delete `refreshHost`, `refreshSessions`,
   `refreshNotifications`, `refreshGroups`, `refreshSortMode` and
   `scheduleRefresh`.
8. Mutations, with the six optimistic ones.
9. The transcript as an infinite query.

The paged commit log needs a key of its own, `gitLogPages`, because the cache
holds pages there and a single answer under `gitLog`, and one entry cannot be
both.

`sidebar.tsx` and `approvals.tsx` never appear in steps 4 to 6 because they
only write. They are converted in step 8 with the rest of the mutations.

Step 3 deliberately double-runs. The store keeps patching while the cache also
invalidates, so any step can be stopped at without leaving the app half-fed.
Step 7 is where the old path is removed.

## What we are not doing

- **Mobile.** Flutter has its own state, and nothing here crosses that line.
- **The TUI.** `internal/tui` reads the same daemon over HTTP and is unaffected.
- **The daemon.** No route, payload or SSE event changes shape. If this spec
  ever seems to need one, that is the signal it has drifted out of scope.
- **The terminal.** A tab is a live connection with its own protocol in the
  main process (`src/main/terminals.ts`), and a cache has nothing to offer a
  byte stream. The `terminals(sessionId)` **list** is a different thing — it
  is a read, it gets a key, and `syncShells` turns into a query. The bytes do
  not.
- **Persisting the cache.** Sessions and diffs are worth refetching on launch.
  The files panel's own per-session record (PR #112) stays in localStorage.
- **Changing any daemon route.** This is a renderer change end to end.

## Tests

The desktop suite runs as `node --test --experimental-strip-types test/*.test.ts`
and does not mount React, so the tests go where the logic is rather than at the
components:

- The key factory: keys are stable, hierarchical, and include `hostId`. Keys
  that carry an argument object — `sessions`, `gitLog`, `gitDiff`, `grepFiles`
  — differ when the argument differs.
- The retry predicate: retries a network error and a 502, not a 404 or a 413.
- The SSE→invalidation map: each of the nine event types names the keys it
  should take out, asserted against a fake `QueryClient` that records calls.
- The optimistic reorder: `onMutate` snapshots, `onError` restores — the
  behaviour `store.ts:519-537` has today, kept.
- The settings selects: `sessions.sort` of `'manual'` reads as manual and
  anything else, including a missing field, reads as activity — the fallback
  `store.ts:466` has today.
- The groups 404: a rejected groups query with status 404 reads as
  unsupported, and any other status reads as an error.
- The settings merge: an optimistic `memory.*` write leaves `autotitle.*` and
  `sessions.sort` untouched in the cached map.
- The pending distinction: a host with no cache entry reads as pending, and a
  host that answered with `[]` reads as empty — the skeleton-versus-"No
  sessions" split `sidebar.tsx:336` makes today.
- The dirty guard: content arriving for a path whose buffer is dirty leaves the
  buffer and its dirty flag alone; the same content arriving for a clean buffer
  replaces it.
- The transcript reducer: a delta appended to a page yields the same list the
  full fetch would have, and an `epoch_changed` page replaces the list rather
  than appending to it.

Manual, against the running app over CDP, as with PRs #109 and #112: open the
Files panel and the worktree picker on the same session and confirm that
`gitWorktrees` crosses the IPC boundary once rather than twice.
