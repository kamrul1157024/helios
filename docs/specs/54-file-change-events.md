# File Change Events: Watch What Someone Is Looking At

## The claim

The daemon must tell a viewer when a file changes. Today it never does.

Helios exists to watch an agent work. The agent's main output is a file edit.
That edit reaches no screen. A desktop tab keeps the old text. A phone keeps the
old listing.

Both clients are already built for the event. `CacheTarget.files` and
`CacheTarget.git` each have a switch arm in `cache_invalidator.dart:60-72`.
Neither arm can run, because `effectsFor` never returns those targets. The
desktop holds file and git query keys (`keys.ts:47,63-72`) and maps no event to
them. The consumers exist. The producer does not.

The producer watches **metadata for the paths a viewer holds**. Not the tree, and
not the tool call. A read registers a path; a sweep re-stats what is registered;
anything that moved goes out on the stream. So the work the daemon does is
proportional to what people are looking at, rather than to what changed on disk.
That one property is the whole design. See *Why not a tree watcher* and *Why not
the tool payload*.

**Scope:** `internal/server` (a registry, a digest, one broadcast),
`internal/daemon` (a sweep), one line in each provider's `PostToolUse`,
`mobile/`, `desktop/`. One new SSE event type. No route changes shape. No
existing field changes meaning. Both clients return an empty effect list for an
unknown event type (`cache_effects.dart:190-191`, `keys.ts:221-222`), so an older
client talking to a newer daemon behaves exactly as it does today. `internal/tui`
is untouched, and `go.mod` gains no dependency.

## Where we are

| | Today |
|---|---|
| File change event | none. The stream carries 10 types, and none names a path |
| Filesystem watcher | none. `trust_watcher.go` watches sessions, not files |
| Mobile files cache | `CacheTarget.files` invalidates `listFilesProvider` (`cache_invalidator.dart:68-72`) — unreachable |
| Mobile git cache | `CacheTarget.git` invalidates five providers (`cache_invalidator.dart:60-67`) — unreachable |
| Mobile refresh today | one button (`file_browser_screen.dart:271`) |
| Mobile writes | none. `api_client.dart` has no `PUT`. The mobile file view is read-only |
| Desktop files cache | `keys.fileDir`, `keys.fileContent`, `keys.fileAsset`, two search keys (`keys.ts:63-72`); `effectsFor` (`keys.ts:178`) returns no file effect |
| Desktop refresh today | one trigger. A hide/show reloads the **active** tab, and only when it is clean (`files.tsx:332-339`) |
| Desktop write conflict | `base_mod_time` compare, 409, a toast that says "reload first" (`filesearch.go:197`, `files.tsx:301`) |
| Desktop reconnect | the tray reconciles on `status === 'online'` (`ipc.ts:138`); no event reaches `effectsFor` |
| Mobile reconnect | `stream_reconnected` reaches `effectsFor` (`daemon_api_service.dart:284`) and takes out sessions and notifications only |
| Read handlers | `handleListFiles` (`files.go:26`) does a readdir plus one `Info()` per entry; `handleReadFile` (`files.go:87`) stats one path |
| Periodic work | `internal/daemon` already runs sweeps — `reaper.go`, `evict.go` |

## What this costs today, stated plainly

### The product's main case is the one that is invisible

The agent writes a file. Every open viewer keeps the old bytes. Nothing on any
screen moves.

On the desktop the user must hide the files panel and show it again, and that
reloads one tab. On the phone the user must press a button. Neither client has
any way to learn that the file moved.

### Stale content is worse than a stale list

A directory listing that misses a new file is a small error. The user sees fewer
rows than exist.

A file view that shows text the agent replaced is a large error. The user reads
it, reasons about it, and asks the agent about code that is gone. The screen is
not incomplete. It is wrong, and it looks correct.

### The dead branches make the bug look fixed

A reader of `cache_invalidator.dart:68` sees `CacheTarget.files` handled, with a
careful comment about why `readFile` is left alone. The reader concludes the path
works. It does not. `effectsFor` (`cache_effects.dart:128-193`) has no case that
returns that target, so the arm cannot run.

`CacheTarget.git` is dead in the same way, and its comment is equally careful.
Two enum members, one switch arm each, zero producers.

### The desktop reload rule is right, and has no event behind it

`files.tsx:332-339` reloads the active tab when the panel becomes visible, and
skips a dirty one. That is the correct rule. It runs on the wrong trigger.

A background tab therefore stays stale for as long as it is open. A file the user
watches without leaving the panel stays stale forever. The effect covers the case
where the user was not looking, and misses the case where the user is.

### A save can only find out by failing

The 409 at `files.tsx:301` is the only path by which the desktop learns a file
moved under it. The user types for a minute, presses save, and is then told to
reload and redo the work by hand. The information existed a minute earlier.

## The change

### The event

```json
{
  "type": "file_changed",
  "data": {
    "paths": [
      { "path": "/home/u/repo/internal/server/api.go", "kind": "file", "mod_time": "2026-09-01T04:03:11.204Z" },
      { "path": "/home/u/repo/internal/server", "kind": "dir" },
      { "path": "/home/u/repo/docs/old.md", "kind": "file", "gone": true },
      { "path": "/home/u/repo", "kind": "repo" }
    ]
  }
}
```

Every entry names something a client asked for. There is no coarse "something
changed" flag, because the daemon never has to guess: it knows exactly which of
the paths it was asked about have moved.

Every entry named has **changed content**, not merely changed metadata. That is
the daemon's promise, and *Metadata gates the check* is how it keeps it.

`mod_time` is informational — for logs and for a listing row. No client decides
anything from it. `gone` marks a path that no longer exists.

### The watch set comes from the reads

A read is the subscription. There is no subscribe call and no unsubscribe call.

| Handler | Registers | Kind |
|---|---|---|
| `GET /api/file` (`files.go:87`) | the file | `file` |
| `GET /api/files` (`files.go:26`) | the directory | `dir` |
| `GET /api/git/status` (`server.go:206`) | the repo root | `repo` |

Each read refreshes the entry's TTL. An entry not read for ten minutes leaves the
set and stops costing anything. The set is capped, and the least recently read
entry is dropped first, so a script hammering the read routes cannot grow it
without limit.

This is what makes the design safe. The cost of watching is bounded by the number
of things a human has open, which is tens. It is not bounded by the size of the
working tree, which is hundreds of thousands.

Registering also digests immediately, and a first digest never broadcasts.
Otherwise every read would announce itself.

### Metadata gates the check. Content decides it

**Changed metadata does not mean changed content.** A formatter rewrites a file
with identical output. `go generate` and codegen rewrite unconditionally. `git
checkout` restores bytes that were already there. An editor saves with no edit.
An agent retries an `Edit` and produces the same text. Every one of those moves
`mtime` and changes nothing.

Acting on `mtime` alone would be wrong on the one path that touches the user's
work: a dirty tab would raise *Changed on disk* over a file that did not change,
and invite the user to discard their edits for nothing. A false alarm there is
worse than a slow update.

So the sweep is two stages. Metadata is the cheap filter. Content is the answer.

```
     sweep
       |
       v
  stat the entry
       |
       +-- mtime and size unchanged --> done. No read, no hash, no event.
       |
       +-- mtime or size moved
                |
                v
          read and hash
                |
                +-- hash equals the stored hash --> store the new mtime. No event.
                |
                +-- hash differs --> store both. Name it in the event.
```

In the steady state the sweep does one stat per entry and nothing else. A hash
only runs on an entry whose metadata already moved, which is rare, and the file
is at most 10 MB (`files.go:15`). The bytes are warm: the client read them
moments earlier.

| Kind | Gate | Digest | Steady-state cost |
|---|---|---|---|
| `file` | `mtime`, `size` | SHA-256 of the contents | one stat |
| `dir` | none | a hash of the `[]fileEntry` slice `handleListFiles` builds (`files.go:56-70`) | one readdir, one `Info()` per entry |
| `repo` | none | the contents of `.git/HEAD` and of the ref it names | two small reads |

A directory needs no second stage, because for a listing the metadata **is** the
content. The client's screen shows each entry's name, size and `ModTime`
(`files.go:56-70`), so an entry whose mtime moved is a row that changed. The
digest is computed by the same code that produces the answer the client holds, so
it cannot disagree with the screen.

A repo needs no second stage either. `.git/HEAD` is a few bytes and a ref file is
41, so reading them outright is cheaper than deciding whether to. Comparing their
contents rather than their mtimes catches a commit, a checkout, a branch switch
and a reset, with no false positive at all.

**`.git/index` is deliberately not watched.** `git status` refreshes the index as
a side effect, so watching it would make the daemon's own answer change the thing
it watches: a status read moves the index, the sweep sees it, the clients refetch
status, and the loop never settles. The cost of leaving it out is that a bare
`git add` does not announce itself. That is a staged/unstaged split lagging by one
real change, and it is worth the loop it avoids.

The residual gap is a file rewritten twice inside one filesystem timestamp tick
with the same size. The registry keeps full-precision `mtime` internally rather
than the truncated form the API reports, which makes this reachable only on a
filesystem with one-second granularity. Viewer arrival covers it, as it covers
every other miss.

### The sweep, and the poke

One sweep, every second, in `internal/daemon` beside the existing ones. It
re-digests the whole set and broadcasts one `file_changed` naming everything that
moved. One event per sweep, however many paths changed.

`PostToolUse` runs a sweep at once rather than waiting for the tick. For both
providers this is one line beside the status write (`claude/hooks.go:1045`,
`codex/hooks.go:325`):

```go
ctx.Files.Poke()
```

It passes no path and reads no `tool_input`. The hook is a hint that now is a good
time to look, not a description of what happened. `PUT /api/file`
(`filesearch.go:162`) pokes for the same reason: the writer already has its
answer, but the other clients do not.

Pokes are debounced to one sweep per 250 ms, so a burst of tool calls does not
sweep repeatedly.

Cost when nothing is open: one timer, zero syscalls. The set is empty.

### Why not a tree watcher

`fsnotify` on each session CWD is the obvious answer, and it fails on the
property this design is built around.

`sse.go:36-42` drops an event when a client's 64 slots are full. Spec 52 exists
because of that drop. A recursive watch reports every write under the tree.
`npm install` writes tens of thousands of files. `go build` fills a cache. `git
checkout` rewrites the index and the working tree. Each write would become a
broadcast, and would evict `session_status` and `notification` from every
connected client's buffer at exactly the moment a build runs. A file event that
silences approval notifications is a bad trade at any latency.

The metadata sweep has the inverse behaviour. A build touching ten thousand files
produces one event naming the two paths somebody has open. The output is
proportional to the watch set, and the watch set is what a human is looking at.

The cost arrives before the damage as well. A recursive inotify watch takes one
descriptor per directory, `fs.inotify.max_user_watches` is 8192 by default on
several distributions, and one `node_modules` tree passes that alone. macOS
FSEvents coalesces differently and reports renames differently, so the two
platforms would need separate correctness arguments for one feature. A stat is a
stat on both.

### Why not the tool payload

The other obvious answer is to read the paths out of the hook: Claude's `Write`
and `Edit` carry `file_path`, which `internal/transcript/reader.go:317-330`
already extracts. It is precise, it is instant, and it was the first draft of
this section.

It is the wrong shape, for three reasons.

It is provider-coupled. Every tool that writes needs a rule, in every provider,
for as long as both exist. Codex puts `apply_patch` in a `command` string
(`codex/hooks.go:646-649`), so reading it back means parsing a patch envelope out
of a shell command.

It misses most writers. A `Bash` call that runs `sed -i`, a build, a `git
checkout`, and the user's own editor are all invisible to it. Each of those
needed a coarse "something under this root changed" fallback, which meant a
second event shape, a coalescer to stop a build flooding the stream, and a client
path that refetches everything because it cannot be told what moved.

It is a second source of truth. The tool input says what the agent *asked* for.
The digest says what is *there*. When those disagree, the digest is right.

The metadata sweep needs none of it. The hook survives as one line that carries
nothing, which is exactly as much provider coupling as this feature deserves.

### The client's answer differs by target

**Directory listings are always safe.** A listing holds no user state. Invalidate
the whole family rather than the named path: both Riverpod and React Query
refetch only entries that have an observer, so a family with one open directory
costs one request, and a family nobody is watching costs nothing.

**File content is not always safe.** This is the rule that matters.

*Mobile: invalidate it.* The mobile file view is read-only. `api_client.dart` has
no `PUT`, so no buffer on that client can be dirty. The comment at
`cache_invalidator.dart:69-71` guards a case that does not exist there yet.
Replace it with the invalidation, and keep a note that a future mobile editor
must take the desktop rule below.

*Desktop: reload the clean, mark the dirty.*

- **Clean tab** → refetch, then compare. Text equal to `saved` → stop. Text
  different → `reload(path)` (`files.tsx:308`), which already exists. This is the
  whole feature: the text on screen becomes the text on disk while the user
  watches.
- **Dirty tab** → refetch and compare, and never write to the buffer. Text equal
  to `saved` → stop, and show nothing. Text different → set `staleOnDisk`
  and show a bar over the editor: *Changed on disk*, with a **Reload** action.
  `reload` clears the flag, and so does a successful save. Dismissing hides the
  bar and leaves the tab's unsaved pill in place. No merge, no prompt, no
  character lost.
- **`gone: true`** → mark the tab, and do not close it. An unsaved buffer over a
  deleted file is still the user's work, and saving it recreates the file.

**The reading position already survives, and the keyboard does not.** `reload`
bumps `version`, which remounts the editor. The position is fine: `CodeEditor`
reads `restoreRef.current` on mount, sets the selection, and applies `scrollTop`
after a frame (`editor.tsx:151-160`), and the panel hands it `views[path]` from
a ref at render time. Nothing to build.

Focus is the problem. `editor.tsx:149` calls `created.focus()` on every mount.
Under the hide/show trigger that is right — the panel just appeared. Under a
live event it is not: the user can be typing in the transcript composer when the
agent saves a file, and the caret jumps into the editor mid-sentence. So the
editor takes an `autoFocus` prop, and a remount the user did not ask for passes
false.

This is the one requirement in this spec that only exists because the trigger
changed. It is invisible until the reload stops being something a person asked
for.

**Compare against what you hold, not against a timestamp.** An event naming a
path means *refetch*, not *replace*. Compare the fetched text with the tab's
`saved` string. If they are equal, do nothing at all: no remount, no bar, no
cursor moved.

That one rule covers two separate problems.

It covers the echo. A desktop save writes its own answer into the cache
(`files.tsx:294-296`) and then receives its own `file_changed` back, because the
broadcaster has no addressing and cannot skip the writer. Without the compare,
every save would remount the editor of whoever pressed ⌘S — which is exactly the
loss the `version` comment at `files.tsx:44-46` was written to prevent.

It also covers anything the daemon's hash missed. The daemon compares hashes so
it does not broadcast noise; the client compares text so it does not act on noise
that got through. The check that protects unsaved work is the one closest to the
work.

Comparing `mod_time` would have been the obvious alternative and it does not
survive contact with the API: `GET /api/file` formats `mod_time` with
`time.RFC3339` (`files.go:129`) and `PUT /api/file` formats it with
`time.RFC3339Nano` (`filesearch.go:226`). A tab's cached value therefore changes
precision depending on how it was last filled. `filesearch.go:240` already
truncates both sides to seconds to paper over this. Comparing bytes needs no such
rule.

**Git.** A `repo` entry invalidates the git family, and so does any file or
directory change, because a working-tree write moves `git status`. Mobile:
`CacheTarget.git` already does this (`cache_invalidator.dart:60-67`) and now gets
its first producer. Desktop: `keys.git(hostId)` takes every git read out in one
call (`keys.ts:47`). Neither narrows by cwd; the observer rule above makes the
wide invalidation cheap.

### Viewer arrival closes the rest

Spec 52 argued that staleness matters only when someone looks at it. That holds
here, and it covers the two gaps the sweep leaves.

A `file_changed` broadcast during a disconnect is gone — `sse.go:65-70` keeps no
replay buffer and there is no `Last-Event-ID`. And a path whose TTL expired while
a tab sat open is no longer watched at all.

Both heal the same way: arriving at a viewer re-reads, and a re-read
re-registers. The watch set repairs itself as a side effect of the thing that
needed it.

| Trigger | Today | Becomes |
|---|---|---|
| Desktop panel becomes visible | reloads the active clean tab (`files.tsx:332-339`) | reloads every clean tab, and the listing |
| Mobile file screen mounts | nothing | invalidate the listing |
| Mobile app returns to the foreground | nothing for files | invalidate the listing |
| SSE reconnects, mobile | `stream_reconnected` takes out sessions and notifications (`cache_effects.dart:154-160`) | add the files and git targets |
| SSE reconnects, desktop | the tray reconciles (`ipc.ts:138`); nothing reaches `effectsFor` | raise `stream_reconnected` into the renderer's event path, and map it |

The desktop reconnect row is the only one that needs new plumbing, and it is one
line beside the reconcile that is already there.

### The state, per open file

```
                  file opened
                       |
                       v
                +-------------+
        +------>|    Clean    |<---------------------+
        |       +-------------+                      |
        |          |       ^                         |
        |  user    |       | save succeeds           | reload lands
        |  types   |       |                         |
        |          v       |                         |
        |       +-------------+                      |
        |       |    Dirty    |                      |
        |       +-------------+                      |
        |             |                              |
        |             | file_changed names this path |
        |             v                              |
        |       +---------------+   Reload pressed   |
        |       | Dirty + stale |--------------------+
        |       +---------------+
        |             |
        |             | save pressed -> 409, buffer unchanged
        |             v
        |          stays Dirty + stale
        |
        +-- file_changed names this path, tab is Clean:
            reload at once. No bar, no prompt, no keystroke lost.
```

The only edge that shows a bar is the one into **Dirty + stale**. Every other
heal is silent, which is the point: a user reading a file should see it change,
not be asked about it.

## What we are not doing

- **A recursive tree watcher.** Argued above. Its output is proportional to what
  changed rather than to what is watched, and that floods a lossy 64-slot
  broadcaster during any build.
- **Reading paths out of the tool input.** Argued above. It is provider-coupled,
  it misses every writer that is not a named file tool, and the digest is a
  better source of truth than the tool's own request.
- **Watching `.git/index`.** `git status` moves it, so watching it would make the
  daemon's answer change what the daemon watches.
- **Fixing the `mod_time` precision split.** `GET /api/file` and `GET /api/files`
  format with `time.RFC3339` (`files.go:67,129`); `PUT /api/file` formats with
  `time.RFC3339Nano` (`filesearch.go:201,226`); the write-conflict check
  truncates both to seconds to cope (`filesearch.go:240`). It is a real wart and
  it is not this spec's to fix. This design routes around it by comparing bytes
  rather than timestamps, which is why nothing here depends on the answer.
- **Sending file contents in the event.** The event says what moved. A client
  fetches the bytes only when it holds that file. A content payload would push
  bytes to every client, including the ones holding nothing, through the same 64
  slots.
- **A replay buffer, or `Last-Event-ID`.** Same argument as spec 52. Re-reading
  on viewer arrival is a smaller promise, and it repairs the watch set at the
  same time.
- **Merging a change into a dirty buffer.** No three-way merge, no diff view. The
  user reloads or keeps their text.
- **Making `readFileProvider` refresh on its own.** No `staleTime`, no poll. The
  comment at `daemon_providers.dart:505-509` is right about why, and this spec
  gives it the explicit trigger it was waiting for.
- **Per-client watch sets.** `sse.go` broadcasts to every client and has no
  addressing. Adding it would buy nothing: a client already ignores a path it
  does not hold, and the set is the union of a handful of open files.
- **Tuning the sweep for a large watched directory.** A directory digest costs a
  readdir plus one stat per entry, so a watched `node_modules` would be
  expensive. The set cap bounds the damage. If a real directory shows up as a
  cost, digest the large ones less often — but build that when there is evidence,
  not now.
- **Changing `internal/server/sse.go`.** The buffer size stays, the `default:`
  branch stays, and this spec is careful not to need them changed.
- **The TUI.** It reads the session store, not the file tree.

## Tests

Go, the registry in `internal/server`:

- Registering a path digests it and broadcasts nothing.
- A file whose content changes is named in the next sweep, with its new
  `mod_time`.
- A file read again with no change broadcasts nothing.
- **A file rewritten with identical bytes broadcasts nothing**, and the sweep
  stores the new `mtime` so it does not hash the same file twice.
- A file whose `mtime` and `size` are both unchanged is never read or hashed —
  asserted by counting reads, not by the absence of an event.
- A file whose content changes to the same length is named. Size alone is not the
  gate.
- A file that grows and shrinks back between two sweeps, ending with different
  bytes and the same size, is named.
- A deleted file is named once with `gone:true`, and then leaves the set.
- A directory is named when an entry is added, when one is removed, when one is
  renamed, and when a child's own content changes.
- An entry not read for the TTL leaves the set. A read inside the TTL extends it.
- The set is capped, and the least recently read entry is evicted first.
- A commit on the watched branch names the repo. A `git status` read on its own
  names nothing — the loop guard.
- One sweep produces one event, however many paths moved.
- Two pokes inside the debounce window run one sweep.

Go, the providers:

- `PostToolUse` pokes, for Claude and for Codex.
- The poke reads no `tool_input` — asserted by poking from a hook payload with
  none, and getting a sweep.
- `PUT /api/file` pokes, and the resulting event carries the `mod_time` the route
  returned.

Dart, `mobile/test/cache_effects_test.dart`, which asserts the mapping without a
container:

- `file_changed` naming a file returns the files target and the git target.
- `file_changed` naming only a repo returns the git target.
- `stream_reconnected` returns four targets, not two.
- A payload with no `paths` returns no effects.

Desktop, `desktop/test/cache-keys.test.ts`:

- `effectsFor` maps `file_changed` to `keys.files(hostId)` and `keys.git(hostId)`.
- `effectsFor` maps `stream_reconnected` to the same two.

Desktop, the per-tab decision. **There is no component test framework** —
`desktop/package.json` has no testing-library and no jsdom, and every test under
`desktop/test/` is a node-level logic test. So the rule that protects unsaved
work must not live inside the component. Extract it:

```ts
fileEffectFor(tab, saved, fetched): 'reload' | 'mark' | 'ignore'
```

It takes the two texts rather than the event, because the decision is a
comparison and nothing else. It lives in `keys.ts` beside `effectsFor`, for the
same reason: that file is the one place the renderer keeps pure decisions.
`fetched` is null when the file has gone. Test it in the same style as
`cache-keys.test.ts`:

- A clean tab whose fetched text differs returns `reload`.
- A dirty tab whose fetched text differs returns `mark`, never `reload`.
- A clean tab whose fetched text equals `saved` returns `ignore` — the save echo.
- A **dirty** tab whose fetched text equals `saved` returns `ignore`. This is the
  false-alarm case: no bar over a file that did not change.
- `gone` returns `mark` for a dirty tab and for a clean one, never a close.

What is left in the component is the bar and the remount, and those are checked
by hand below.

Manual:

- Open a file on the desktop. Ask the agent to edit it. The text updates while
  you watch, with no click and no scroll jump.
- Type into that file first, then ask the agent to edit it. Your text survives,
  the bar appears, and Reload replaces the buffer.
- Open a directory on the phone. Ask the agent to add a file. The row appears.
- Edit a file in your own editor, outside Helios, with no agent running. The
  desktop tab updates within a second. This is the case no hook could ever cover.
- Run `git checkout` in a plain terminal. The open tabs and the git panel follow.
- Ask the agent to run `npm install`. The stream stays usable, approvals keep
  arriving, and the panel refreshes only the paths that are open.
- Start typing into a file, then run the project's formatter over the whole tree
  so it rewrites that file with identical bytes. No bar appears, and your text is
  untouched. Then run a formatter that really does reformat it. The bar appears.

## Success criteria

- An agent edit reaches every open viewer of that file in under a second, with no
  user action.
- An edit made outside Helios does the same. No writer is privileged.
- No unsaved character is ever lost to an event.
- **A rewrite with identical bytes produces no event and no bar.** A touch, a
  no-op formatter run, and a checkout that restores the same content are all
  silent.
- In the steady state the sweep does one stat per watched file and reads nothing.
- A build touching ten thousand files produces events naming only the paths a
  client has open.
- With nothing open, the sweep does no filesystem work.
- The daemon holds no provider-specific knowledge of which tools write.
- `internal/server/sse.go` is unchanged, no tree watcher is added, and `go.mod`
  gains no dependency.
- No member of `CacheTarget` is unreachable, and no file or git query key in
  `keys.ts` is without a producer.
