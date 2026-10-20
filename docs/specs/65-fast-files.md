# Fast Files: Send Less, Rebuild Less, Block Nothing, Show More

## The claim

The files panel must feel instant. Opening a file, expanding a folder, typing in
quick-open, and seeing a change from the agent must each take less time than a
person can notice.

The panel must also be good to use. Today it works, but it shows almost
nothing: no icons, no git state, no match highlights, and no sign of what the
agent changed. Part A of this spec makes the panel fast. Part B makes it worth
looking at.

Helios already has the correct base. The daemon keeps an in-memory index with
precomputed lowercase paths (`fileindex.go:21-78`). Content search uses ripgrep.
Desktop renders with CodeMirror, which draws only the visible lines. Mobile
renders code in a `ListView.builder`.

The time goes to waste around that base:

- One file event refetches almost everything.
- Each file body goes over the wire again, as escaped JSON, with no compression.
- The search index rebuilds completely every 5 seconds.
- Mobile highlights the full file again on each widget rebuild.

This spec removes that waste. It does not add a new engine.

**Reference.** px0 (github.com/px0-ai/px0) is a Go code-review IDE that is built
for speed. It reopens a file in 0.6 ms and ranks 95,710 paths in 6 ms. We
studied it. Its speed comes from five plain decisions:

1. It keeps the file list in memory, and it lowercases each path once.
2. It searches that list with brute force on all cores.
3. It caches each opened file by `path|mtime|size`.
4. It gzips every response.
5. It checks git state with `stat` calls, not with re-scans.

This spec takes 3, 4 and 5. Helios already does 1 and 2.

**Scope:** `internal/server`, `internal/transcript`, `desktop/`, `mobile/`. One
new Go dependency: `github.com/fsnotify/fsnotify`. Two new endpoints:
`/api/file/raw` and `/api/sessions/{id}/touched`. The existing endpoints keep
their shapes. Some of them get new fields, and old clients ignore those fields.

## Where we are

| Area | Today | Cost |
|---|---|---|
| File body | `os.ReadFile`, then a JSON string (`files.go:103`). Binary goes as base64 (`files.go:173`). | A 10 MB file is escaped in memory. A binary file grows by a third. |
| HTTP caching | No ETag, no `Last-Modified`, no 304. | Each reload sends the full body again. |
| Compression | None. `jsonResponse` writes plain JSON (`api.go:1991`). | Text goes over tunnels and Tailscale at full size. |
| Electron path | The main process calls `response.text()` and `JSON.parse` (`main/api.ts:113`). The result is copied over IPC. | A 10 MB parse blocks all other IPC calls. |
| Search index | 5 s TTL, then a full `git ls-files` or a full walk (`fileindex.go:14`). | A non-git root costs about 1.4 s of I/O every 5 s while you type. |
| Cold index | The first query waits for the full build (`fileindex.go:133-140`). | Up to 10 s of blank panel. Spec 58 asked for `indexing: true`. Nobody built it. |
| Ranking | Collects all matches, then runs a stable sort (`filesearch.go:430`). | A one-letter query sorts about 100k items to return 50. |
| Watcher lock | `Watch` and `collect` hold `w.mu` during file I/O (`filewatch.go:121`, `:183`). | A read request waits behind a slow sweep. |
| Directory sweep | `dirDigest` lists and hashes each watched directory each second (`filewatch.go:322`). | The sweep does work when nothing moved. |
| Git status | Six `git` processes in sequence for each request (`git.go:50-89`). No cache. | Each `file_changed` event runs all six again. |
| Desktop invalidation | `file_changed` invalidates all of `keys.files` and all of `keys.git` (`keys.ts:286-287`). | One agent write refetches the open folders, quick-open, grep (a full ripgrep run) and git status. |
| Desktop dirty check | `doc.toString()` and a full string compare on each key (`editor.tsx:134`, `files.tsx:777`). | O(file size) work for each keystroke. |
| Desktop tree | Each row is a `<button>`. No virtualization (`file-tree.tsx:92`). | A folder with thousands of entries draws all of them. |
| Desktop highlight.js | Runs on the main thread for diffs and markdown (`diff-view.tsx:73`, `markdown.ts:10`). | A large diff blocks input. |
| Mobile highlight | `_buildContent` calls `_highlightCodeLines` from `build()` (`file_browser_screen.dart:1074`). | Each `setState`, for example a tap on a line, parses the whole file again on the UI thread. |
| Mobile cache | `listFilesProvider` and `readFileProvider` are not `autoDispose` (`daemon_providers.dart:673`, `:687`). | Every opened file stays in memory, up to 10 MB each. |
| Mobile decode | `jsonDecode` on the UI isolate. `bytes` calls `base64Decode` on each read (`daemon_api_service.dart:1676`). | Frames drop. Images decode again on each build. |
| Mobile invalidation | `file_changed` invalidates every list, every file and every git provider (`cache_invalidator.dart:75-91`). | The same over-fetch as desktop. |

### A bug, not only a cost

`fileContentQuery` is `NEVER_STALE` (`queries.ts:310`). The comment says that
a background refetch must never move `saved` under a dirty buffer.
`invalidateQueries` refetches every *active* query, and it ignores `staleTime`
when it does this. The `file_changed` handler invalidates the whole
`keys.files` prefix. The open tab is an active query. So an agent write next to
a dirty tab refetches that tab, and it moves `saved` under the buffer. The
`syncFromDisk` path (`files.tsx:350-390`) was written to prevent exactly this,
and the broad invalidation goes around it.

Phase 2 fixes this. The narrow invalidation never touches `fileContent`.

## Fix first

These bugs ship before any other work. Each one is small, and each one can be a
PR of its own.

| # | Where | Bug | Fix |
|---|---|---|---|
| F1 | Desktop | A `file_changed` event moves `saved` under a dirty tab. See "A bug, not only a cost" above. | 2.1, narrow invalidation. |
| F2 | Desktop | The Files panel listens for ⌘P, ⌘B, ⌘⇧F and ⌘S on `window`, and it does not check `visible` (`files.tsx:492-515`). Panels stay mounted while hidden (`detail.tsx:309`). So ⌘S saves a file while the user is in Chat, and ⌘P opens a dialog that appears later. | The handler returns at once when the panel is not visible. |
| F3 | Desktop | ⌘B is bound twice: `app.tsx:53` toggles the sidebar, and `files.tsx:499` toggles the explorer. One press does both. | The explorer moves to ⌘⇧E. ⌘B stays with the sidebar. |
| F4 | Desktop | The Aa and `.*` toggles light up, but they do not run the search again. `submitted` keeps the old flags until the next Enter (`find-in-files.tsx:96-99`). | A toggle change runs the active search again. |
| F5 | Desktop | `FindInFiles` unmounts on a switch to Explorer (`files.tsx:599-618`). The query and the results are lost. | Keep it mounted, and hide it. |
| F6 | Mobile | "Ask AI" sends the prompt, then stays in the viewer. `popUntil` checks `name != '/file-browser'`, and the top route is `/file-viewer`, so the check passes at once (`file_browser_screen.dart:948-952`). The same check is in `git_status_screen.dart:960` and `:1599`. It shows no spinner and no error. | Pop to the session route by name. Show a spinner while it sends, and a snackbar on failure. |
| F7 | Mobile | The end of a long code line cannot be read. Rows clip, with no wrap and no sideways scroll (`file_browser_screen.dart:1097-1109`). | B7.1. |

## Budgets

These are the targets. Phase 0 measures them, and each later phase must not
make them worse. The fixture is a 100k-file tree, with a warm OS page cache.
All numbers are daemon times, from `Server-Timing`.

| Operation | Budget (p95) | Today (estimate) |
|---|---|---|
| Quick-open keystroke, warm index | 15 ms | Sort-bound, about 40 ms on 100k |
| Quick-open, cold index, first answer | 150 ms, with `indexing: true` | Up to 10 s |
| Folder expand | 5 ms | One `lstat` for each entry, no cache |
| File reopen, unchanged | 3 ms (a 304) | Full read and full encode |
| Open a 1 MB text file | 40 ms | Full read, JSON escape, no gzip |
| Agent write to visible update | 300 ms | Up to 1 s sweep, then a broad refetch |
| Git status | 20 ms | Six processes in sequence |

Two client budgets:

- Desktop: no main-thread task longer than 50 ms while a file opens or a diff
  shows.
- Mobile: no dropped frame when you tap a line in a 5000-line file.

# Part A: Speed

## Phase 0: Measure

Nothing else ships until these exist. A speed spec with no numbers cannot say
that it worked.

1. **`Server-Timing` on the files routes.** Add a small middleware for
   `/api/files*`, `/api/file*` and `/api/git/*`. It reports `total`, and each
   handler adds its own parts: `stat`, `read`, `index`, `rank`, `encode`, `git`.
   The desktop devtools show it with no new code.
2. **Go benchmarks** in `internal/server`:
   - `BenchmarkRank100k`: a synthetic index of 100k paths, queries of 1, 3 and
     8 characters.
   - `BenchmarkListDir`: a folder with 5000 entries.
   - `BenchmarkReadRaw`: 1 MB and 10 MB files, with and without gzip.
   - `BenchmarkIndexPatch`: 1000 create and remove events on a 100k index.
3. **A budget table in the PR.** Each later PR posts its before and after
   numbers against the table above.

## Phase 1: The daemon

### 1.1 A raw content endpoint

```
GET /api/file/raw?path=<abs-or-tilde>
```

- The body is the file bytes, with no JSON and no base64.
- `Content-Type` comes from the extension, then from `http.DetectContentType`.
  Text types get `; charset=utf-8`.
- `ETag: W/"<size>-<mtime-nanoseconds>"`. One `stat` makes it. The daemon does
  not hash the file for this.
- `X-Helios-Mod-Time` carries the RFC 3339 mod time. `PUT /api/file` already
  needs it as `base_mod_time`.
- `Cache-Control: no-cache`. The client may store the body, but it must check
  again before it uses the body.
- `If-None-Match` that matches gives `304` after one `stat` and no read.
- The size limit stays at 10 MB (`files.go:15`). Larger files give `413`, as
  today.
- The auth and the path rules are the same as `GET /api/file`.

**Why a weak ETag from `stat`.** A content hash costs about 20 ms on 10 MB, and
it would cost that on each request, which defeats the 304. Linux and APFS store
mtime in nanoseconds. Two writes in the same nanosecond with the same size are
not a real case.

**Why keep `GET /api/file`.** Mobile releases lag behind the daemon. Old clients
must keep working. The JSON endpoint gets gzip and an ETag too, but it keeps
its body shape.

**Write conflicts.** `PUT /api/file` also accepts `If-Match: <etag>`. When the
header is present, the daemon compares it with the current ETag and gives
`409` on a mismatch. This closes the gap where today's `base_mod_time` check
works at whole-second resolution (`filesearch.go:289`). The `base_mod_time`
check stays for old clients.

### 1.2 Compression

- gzip at `BestSpeed`, with writers from a `sync.Pool`.
- Only when the request sends `Accept-Encoding: gzip`.
- Only when the body is larger than 1 KB.
- Only for text and JSON. Never for `image/*` other than SVG. Never for SSE.
- It applies to `/api/files*`, `/api/file*` and `/api/git/*`.

`BestSpeed` compresses at more than 100 MB/s on one core. On localhost the cost
is small. Over a tunnel it saves a lot of time, because source code shrinks to
between a quarter and a fifth of its size.

### 1.3 The search index: fsnotify, with limits

The index stops using the 5 s TTL. An fsnotify watcher patches the index in
place. The TTL becomes a safety net of 5 minutes.

Specs 54 and 58 rejected a tree watcher, for three reasons. This design answers
each one, and it answers a fourth reason that those specs did not name.

| Objection | Answer |
|---|---|
| Raw events become SSE broadcasts and evict `session_status` (spec 54). | Raw events **never** reach SSE. They change the index, and they poke the spec 54 sweep. The sweep still emits only for watched paths, so the output stays proportional to what a person looks at. |
| `max_user_watches` is 8192 on some distributions. One `node_modules` uses all of it (spec 54). | The daemon watches **directories only**, and never ignored directories. It reads the kernel limit and uses at most half of it, across all roots. Over the budget, that root falls back. See below. |
| macOS and Linux behave differently (spec 54, spec 58). | The index needs only one fact: "this directory's entries changed". It does not trust event types. Each event triggers a `ReadDir` of the named directory and a compare with the index. Both platforms get the same correctness argument. |
| fsnotify on macOS uses kqueue. kqueue opens one descriptor for every *file* in a watched directory. | The daemon never uses kqueue. macOS polls. See "macOS" below. |

**What the watcher watches.**

- Git root: each directory that holds at least one path from
  `git ls-files --cached --others --exclude-standard`. Git already decided
  what is ignored, so `node_modules` and `target` are never watched. The daemon
  also watches `.git` itself, without recursion, for `index`, `HEAD` and
  `packed-refs`.
- Non-git root: each directory from the walk, without `skipDirs`
  (`filesearch.go:40`).
- At most `maxIndexedRoots` (8) roots, as today. Eviction from the LRU removes
  the root's watches.

**What an event does.**

1. Events go into a set of dirty directories for that root. The set is
   debounced for 100 ms.
2. After the debounce, the daemon runs `ReadDir` on each dirty directory and
   compares the result with the index. It inserts new entries and removes
   missing ones. Both keep the entries in sorted order.
3. A new directory gets a watch and a bounded walk.
4. On a git root, new paths go through `git check-ignore -z --stdin` in one
   batch before they enter the index.
5. A change to `.git/index` or `HEAD` starts a full rebuild in the background.
   The old index serves queries until the new one is ready.
6. More than 512 dirty directories in one window means a bulk change, for
   example a checkout. The daemon stops patching and runs one full rebuild.
7. Each event path that is in the spec 54 watch set calls `Files.Poke()`. An
   agent write then shows in about 100 ms, not after the next 1 s sweep.

**The fallback.** A root falls back to stat polling when its watch budget runs
out, when `Add` returns an error, or when the platform has no backend. In
fallback, it rebuilds when the mtime of `.git/index` or `HEAD` moves, and on a
`FilesTouched` hook (`hooks.go:91`), with the 5-minute TTL as the floor. This is
the design that spec 58 proposed, so it is a proven fallback, not a new one.
The search response reports `"watch": "live" | "polling"`, so a client and a
bug report can tell the two modes apart.

**macOS.** fsnotify v1 has no FSEvents backend, and its kqueue backend costs a
descriptor for every file. So every root on a platform other than Linux runs in
the fallback, by build tag. FSEvents, which needs cgo, is a later change. The
setting `files.watch` (`auto` | `polling`) forces the fallback on Linux too.

**Linux budget.** Read `/proc/sys/fs/inotify/max_user_watches` at start-up.
Give the index at most half. Other tools, for example editors and `tsc
--watch`, share the same limit. Kernels from 5.11 scale the default with RAM,
so most machines have a large limit. The fallback covers the rest.

### 1.4 Cold index and ranking

- **Cold index.** A query on a root with no index starts the build, waits up to
  150 ms, and then returns what it has, with `indexing: true`. The walk
  publishes partial entries as it goes. The client asks again when it sees
  `indexing: true`, with a 250 ms back-off. Spec 58 already defined this field.
- **Top-k ranking.** `rankEntries` keeps a min-heap of size `limit`. It does not
  collect and sort all matches. The order stays stable: ties break on the path.
  On 100k paths with a limit of 50, this changes O(n log n) to O(n log 50).
- **Parallel scoring.** Above 20k entries, split the scan into `GOMAXPROCS`
  parts. Each part keeps its own heap. Then merge the heaps. Under 20k entries,
  one goroutine is faster.

### 1.5 The watcher lock (spec 54)

- `Watch` takes the first digest *outside* `w.mu`. It inserts the entry under
  the lock afterwards. When a sweep changed the entry in the meantime, the newer
  value wins.
- `collect` copies the watch set under the lock and releases the lock. Then it
  does the stats and the hashes. Then it locks again only to write the results.
- `dirDigest` first compares the directory's own mtime with the last value. When
  the mtime is the same, it skips the `listDir` and the hash. A directory's
  mtime changes on each create, remove and rename of an entry, which are the
  only changes that the digest looks at.

### 1.6 Git status in one process

`handleGitStatus` runs one command:

```
git status --porcelain=v2 --branch -z --untracked-files=all
```

That gives the branch, the ahead and behind counts, staged changes, unstaged
changes and untracked files in one process. The response shape stays the same.

A small cache holds the answer for each repo root. The key is the root, and the
value is the answer plus a generation number. Three things increase the
generation: a fsnotify event under that root, a `.git/index` or `HEAD` change,
and a `PUT /api/file` under that root. A request with an unchanged generation
gets the cached answer.

## Phase 2: Desktop

### 2.1 Narrow invalidation (fixes the bug)

`file_changed` names paths. The handler uses them:

- For each path, invalidate `keys.dir(hostId, parent(path))`, and invalidate
  `keys.dir(hostId, path)` if it is a directory.
- **Never** invalidate `keys.fileContent`. The open tabs already update through
  `syncFromDisk` (`files.tsx:397-410`), which respects a dirty buffer. Closed
  files are not observed, so they are marked stale with
  `refetchType: 'none'`.
- Invalidate `keys.fileSearch` with `refetchType: 'none'`. The next keystroke
  refetches it. A quick-open that is open does not flash.
- Do not refetch `keys.grep`. Show a "Results may be out of date · Search again"
  line instead. A full ripgrep run for each agent write is the most expensive
  side effect today. The user chooses when to run it again.
- Invalidate `keys.git(hostId)` once for each event, as today. The daemon
  cache from 1.6 makes it cheap.

`stream_reconnected` keeps the broad invalidation, because the gap can hide any
change.

### 2.2 Bytes through Electron, not JSON

- A new bridge call, `readFileRaw(path, etag?)`, calls `/api/file/raw` in the
  main process. It returns `{ status, etag, modTime, contentType, body:
  ArrayBuffer }`. Structured clone moves an `ArrayBuffer` with a fast copy, and
  the main process does no `JSON.parse`.
- The renderer decodes text with `TextDecoder`. Images become a `Blob` URL
  directly, with no base64.
- `fileContentQuery` keeps its key and its `NEVER_STALE` rule. Its `queryFn`
  sends the cached ETag. A `304` returns the cached data as it is.

### 2.3 Prefetch

- **Hover.** Point at a file row in the tree or in quick-open for 80 ms, and the
  app calls `prefetchQuery(fileContentQuery(...))`. When the user clicks, the
  body is already there.
- **Agent writes.** When `file_changed` names a file that is not open, prefetch
  it at low priority. At most 5 files are prefetched for each event. A person
  often opens the file that the agent just changed.
- **Arrow keys in quick-open.** Prefetch the selected row.

Prefetch uses the ETag, so a file that did not change costs one 304.

### 2.4 The dirty check

The editor stops calling `doc.toString()` on each change. It keeps the
`EditorState` of the last save. A change compares the documents only when the
lengths are equal. `Text.eq` stops at the first difference and walks the
CodeMirror rope with no string build. The save still calls `toString()` once.

### 2.5 The tree

The tree flattens the expanded nodes into one array of rows. It renders that
array with a windowed list: fixed 22 px rows, and 20 rows of overscan. Keyboard
navigation and "reveal" scroll to an index. `children` and `busy` are memoized
on the query data.

### 2.6 Highlighting off the main thread

- highlight.js for diffs and markdown fences moves to a Web Worker. The worker
  returns HTML for each line. A cache keyed by `(language, content hash)` keeps
  the last 50 blocks.
- Diff rows use the same windowed list as the tree. The 3000-line limit
  (`diff-view.tsx:33`) then goes away, because only the visible rows render.
- CodeMirror stays as it is. It already highlights only the visible part.

## Phase 3: Mobile

### 3.1 Highlight once, off the UI thread

- Highlighting moves out of `build()`. The screen highlights when the content
  changes, not when the widget rebuilds.
- `compute()` runs `highlight.parse` in an isolate. It returns a compact list of
  spans for each line: start, end and style index.
- A cache keyed by ETag keeps the last 10 highlighted files. A reopen of an
  unchanged file draws at once.
- Until the isolate answers, the screen shows plain text at once, then swaps in
  the colors. The file is readable at once, and the colors come a moment later.

### 3.2 Memory and decoding

- `listFilesProvider` and `readFileProvider` become `autoDispose`, with a
  30-second `keepAlive` after the last listener goes. Back-and-forth navigation
  stays instant, and closed files go away.
- `readFileProvider` uses `/api/file/raw` with the ETag. Text decodes with
  `utf8.decode`. Images go straight to `Image.memory` as the same `Uint8List`
  object on each build, so the image cache hits.
- Large JSON answers (folder listings and git) decode in an isolate when the
  body is larger than 64 KB.

### 3.3 Narrow invalidation

`cache_invalidator.dart` uses the same rules as 2.1:

- Invalidate the parent listing of each named path.
- Refresh an open file only when its viewer is visible, and send the ETag.
- Invalidate git once.

### 3.4 Markdown

Markdown renders in a lazy list of blocks, not in one `MarkdownBody` inside a
`SingleChildScrollView` (`file_browser_screen.dart:1014`). A long README then
builds only what is on screen.

# Part B: A Better Panel

## What a person sees today

These come from screenshots of the desktop app on Linux (2026-10-20), and from a
read of the mobile code.

| Area | Today |
|---|---|
| Tree | Names only. No file icons, no git state. `.git` and a built binary look the same as source files. Nothing is dimmed. |
| Tree loading | "…" flashes beside each open folder on each agent write. `busy` reads `isFetching` (`file-tree.tsx:66`). |
| Open a file | Nothing happens until the body arrives. `openFile` awaits the fetch before it adds the tab (`files.tsx:192`). |
| Loading and error | Bare "Loading…" text. The file header, with Reload and the path, goes away just when it is needed (`files.tsx:758`). |
| Editor header | Text glyphs as buttons (⟳ ⧉ {} ¶). One of them draws as an empty box on Linux, because the font has no such glyph. |
| Code font | Ligatures turn `!=` into `≠` and `==` into a long bar. In a review tool, the screen must show the real characters. |
| Editor | No word wrap, no Ln/Col, no change marks in the gutter. CodeMirror's search bar keeps its default look (`codemirror-theme.ts:71`). |
| Tabs | Two `index.ts` tabs look the same. No tooltip, no close-others, no middle-click. The active tab does not scroll into view, and the scrollbar is hidden (`styles.css:4509`). |
| Quick-open, empty | Lists files in index order. `.nvmrc` comes first, not the open or recent files. |
| Quick-open, results | Blue link-style text. No match highlights. The dialog stays at full height with two results. |
| Search | Runs only on Enter. The match is not highlighted, and the snippet starts at column 0, so a long line can hide the match. |
| Agent | Nothing in the panel says what the agent changed. A tool row's ↗ opens the file at the top, not at the edit (`store.ts:1632`). |
| Mobile viewer | Fixed 12 px font, no pinch, no wrap, no sideways scroll, and no text selection. "Copy" copies the whole file. |
| Mobile navigation | The breadcrumb starts at `/` and does not scroll to the end. A folder does not keep its scroll position. No pull to refresh. |
| Mobile errors | `listFiles` and `readFile` turn every failure into `null` (`daemon_api_service.dart:1266-1300`). A 404, a permission error and a network error all say "Failed to load file", with no Retry. |
| Mobile chips | Chips open the viewer with no `sessionId`, so "Ask AI" and line selection are gone (`message_card.dart:272`). |

## B1. Feel instant

Part A makes the bytes fast. This section makes the screen honest about it.

- **The tab opens on click.** The tab and its header appear at once. The body
  shows a skeleton of about 20 grey lines until the bytes arrive. With the
  prefetch from 2.3, the skeleton is usually never seen.
- **The header stays.** The path, Reload and the other actions stay visible in
  the loading state and in the error state. The error state says what failed,
  in words: "Not found", "Permission denied", "Too large (14 MB, the limit is
  10 MB)", or "Host unreachable".
- **No flicker on refetch.** The tree shows "…" only for a folder with no data
  yet (`isLoading`), never for a background refetch.
- **An error stays on its row.** A folder that fails to list shows the error on
  its own row, with Retry. It does not show the error above the whole tree.
- **One spinner.** All loading states use the same spinner as
  `html-preview.tsx:170`.

## B2. The tree

- **Icons.** One SVG icon for each file type, from `filetype.ts`. Folders get an
  open icon and a closed icon. The icons live in `icons.tsx`, next to the
  existing ones. No icon font, and no new dependency.
- **Git state.** Each row joins the git status query that already exists. A
  changed file shows a letter badge on the right: M, A, D, U or R, with a
  tooltip such as "Modified". A folder that holds a change shows a small dot.
  Colors come from the theme variables that the git views already use.
- **Ignored files.** `GET /api/files` adds `ignored: bool` to each entry. The
  daemon fills it from the warm index from 1.3: an entry under a git root that
  is not in the index is ignored. When the index is cold, the field is absent,
  and nothing is dimmed. Ignored rows show at 50% opacity, with the tooltip
  "Ignored by .gitignore. Search skips it."
- **Hidden entries.** `.git`, `.hg` and `.svn` are not shown.
- **Keyboard.** The tree is one focus stop, with a moving active row:
  - ↑ and ↓ move the active row.
  - → opens a folder, and ← closes it or goes to the parent.
  - Enter opens the file.
  - Typing letters jumps to the next row that starts with them.
- **Reveal.** Reveal scrolls the row to the middle of the view, as its prop
  comment already promises (`file-tree.tsx:13`). It uses the windowed list from
  2.5.
- **Indent guides.** A 1 px line for each level, in the border color. Deep trees
  are hard to read without them.
- **Context menu.** Copy path, Copy relative path, Reveal in the system file
  manager, and Open in terminal. No create, rename or delete in this spec. See
  "What we are not doing".

## B3. Quick-open

- **Empty query.** Show the open tabs first, then the last 20 opened files for
  this session. The desktop already keeps the open tabs for each session in
  localStorage (`files.tsx:226-291`). The recent list goes next to them. The
  daemon ranking does not change, so spec 58's rule of "no frecency in the
  ranking" still holds.
- **Match highlights.** Each match in `GET /api/files/search` gets
  `positions: number[]`, the UTF-16 offsets of the matched characters in `rel`.
  Both clients index strings in UTF-16, so they use the offsets as they are.
  `fuzzyScore` already finds them, and now it returns them. The client shows the
  matched characters in bold, in the file name and in the folder.
- **`file:line`.** `foo.go:42` and `foo.go:42:7` strip the suffix before the
  search, then open the file at that line and column.
- **Rows.** An icon, the file name in the normal text color (not link blue), and
  the folder in a muted color. The selected row has a filled background.
- **Height.** The dialog fits its rows, up to 60% of the window height.
- **Prefetch.** The selected row prefetches its file (2.3).

## B4. Search

- **As you type.** The search runs 200 ms after the last keystroke, with at
  least 2 characters. Enter runs it at once. Each new search cancels the old
  one: the main process aborts the old `fetch`, and the daemon kills `rg` when
  the request context ends.
- **Toggles.** Aa (match case), ab (whole word) and `.*` (regex). Each change
  runs the search again (F4). Whole word maps to `rg -w`.
- **Include and exclude.** An optional field takes globs, for example
  `*.go, !*_test.go`. They map to `rg -g`.
- **Match ranges.** Each grep match adds `ranges: [[start, end], …]`, as
  character offsets in the line. ripgrep's JSON already gives these as
  `submatches`. The client highlights the ranges. It cuts the snippet so that
  the first match is in view, with an ellipsis on the left when needed.
- **Results.** A header line says "42 results in 7 files". File groups can
  collapse, and each group shows its count and an icon. ↑ and ↓ move through the
  results, and Enter opens one. The open file flashes the matched line for
  1.2 s.
- **State.** The query, the toggles and the results stay when the user switches
  to Explorer and back (F5).

## B5. Editor and tabs

### The editor

- **Status line.** A thin line under the editor shows: `Ln 42, Col 7`, the
  selection size when there is one, the language, `UTF-8` or `binary`, and
  `Read-only` for files over 1 MB. The editor already tracks the cursor
  (`editor.tsx:103-109`).
- **Word wrap.** Alt+Z toggles `EditorView.lineWrapping`. The choice is saved
  for each host. Markdown and plain text wrap by default.
- **Change marks.** The gutter shows added, changed and removed lines against
  `HEAD`. The data comes from the existing `GET /api/git/diff` for the one file.
  It refreshes when the file changes.
- **Ligatures.** Off in the editor and in diffs, with
  `font-variant-ligatures: none`.
- **Search bar.** CodeMirror's search panel uses the app's input, button and
  toggle styles.
- **Font size.** The editor follows the same size setting as the terminal.
- **Large files.** The view for files over 1 MB shows line numbers and supports
  ⌘F. It uses CodeMirror in read-only mode, not a `<pre>`.
- **Icons.** Each header action uses an SVG icon from `icons.tsx`, with a
  tooltip and an `aria-label`. The text glyphs go away. `{}` and `¶` become
  "Source" and "Preview", as a two-part toggle with labels.

### Tabs

Tabs stay permanent: each open adds a tab. The changes make many tabs easy to
handle:

- **Same names.** When two tabs have the same file name, each one shows the
  shortest folder suffix that tells them apart, in a muted color, for example
  `index.ts · desktop/src`.
- **Tooltip.** Each tab shows its full path on hover.
- **Close.** Middle-click closes a tab. A dirty tab shows ● until hover, and
  then shows ✕. Closing the active tab moves to the tab on its right, or on its
  left when it is the last tab. Closing a dirty tab asks in the app's own
  dialog, not with `confirm()` (`files.tsx:441`).
- **Context menu.** Close, Close others, Close to the right, Close saved, Copy
  path, and Reveal in tree.
- **Keys.** These work only while the Files panel is visible and has focus
  (F2):
  - ⌃Tab and ⌃⇧Tab go to the next and the previous tab.
  - ⌘W closes the active tab. Today `app.tsx:59` binds ⌘W to "close session
    terminal". The app handler steps aside when the event comes from inside the
    Files panel.
  - ⌥⇧T reopens the last closed tab, with its scroll position and caret. The
    last 20 closed tabs are kept.
- **Overflow.** A thin scrollbar shows. The active tab scrolls into view.

## B6. What the agent changed

Helios knows which session made a change. px0 and other review tools do not
know this. The panel shows it.

### The data

```
GET /api/sessions/{id}/touched
```

```jsonc
{
  "files": [
    {
      "path": "/abs/internal/server/files.go",
      "last_at": "2026-10-20T04:41:07Z",
      "tool": "Edit",        // the provider's tool name
      "count": 3,            // tool calls on this path in the session
      "line": 126,           // first changed line of the last edit, when known
      "deleted": false
    }
  ]
}
```

- **The source is the transcript.** The transcript store parses each transcript
  once and keeps it in memory (spec 38). A new provider method,
  `TouchedPaths(tool string, input map[string]any) []Touch`, gives the paths
  that one tool call wrote. Claude reads `file_path` from `Write`, `Edit`,
  `MultiEdit` and `NotebookEdit`. Codex reads the `*** Add File:`,
  `*** Update File:` and `*** Delete File:` lines of an `apply_patch`.
- **Why the transcript and not the hook.** Spec 54 rejected tool payloads as a
  *freshness signal*, because they miss writers such as `sed -i` in `Bash`.
  Here the transcript is used for *attribution* only. The writes that the
  transcript misses still show up in the git list below. The freshness signal
  stays the same as in spec 54.
- **The line.** For `Edit`, the daemon finds `new_string` in the file on disk
  and reports its first line. The desktop already does the same in
  `edit-offsets.ts`, and that logic moves to the daemon so that mobile gets it
  too. If the text is not found, `line` is absent.
- **Freshness.** The answer changes when the transcript grows. Clients learn
  that from `session_status`, which moves the session's `last_event_at`
  (`keys.ts:211`). The same effect invalidates `keys.touched(hostId,
  sessionId)`. The query has an observer only while the Changes view or the
  tree is visible, so other sessions cost nothing.

### The Changes view

The explorer gets a third mode beside Explorer and Search: **Changes**. It has
two sections:

1. **Edited by this session.** These are the files from `touched`, newest first.
   Each row shows an icon, the file name, the folder, a relative time ("2m"), and
   the git badge. A click opens the file at `line`.
2. **Other uncommitted changes.** These are the files from git status that are
   not in the first section. This catches `Bash` writes, other sessions in the
   same worktree, and the user's own edits.

A file that the agent deleted shows struck through. A click opens its diff in
the Files tab (B8).

### Marks everywhere else

- **Tree.** A file that this session edited shows a small filled dot in the
  accent color, to the left of the git badge. A folder that holds one shows a
  hollow dot. The tooltip says "Edited by this session, 2m ago".
- **Tool rows.** The ↗ on a tool row opens the file at the edited line.
  `store.openFile` gets an optional `line` (`store.ts:1632`).
- **Flash.** When the agent writes to a file that is open in a clean tab, the
  tab updates (through `syncFromDisk`), and the changed lines get a background
  tint that fades out over 1.5 s. The diff comes from `diff.ts`, which already
  exists. On a dirty tab, the "Changed on disk" bar gets a new action, "Show
  diff", that opens a diff of the disk copy against the buffer.

## B7. Mobile reading

### B7.1 The code viewer

- **Long lines.** A wrap toggle sits in the AppBar menu. With wrap off, the code
  scrolls sideways as one block, and the line-number gutter stays fixed. With
  wrap on, rows have a variable height. The choice is saved.
- **Font size.** Pinch changes the font size from 9 to 20 px. The size is saved.
  The view keeps the line that was at the top of the screen in place while the
  size changes.
- **Copy lines.** A long press starts a line selection. The current tap-to-select
  for "Ask AI" uses the same selection. The action bar offers Copy, Copy with
  path and line (`path:12-20`), and Ask AI.
- **Go to line.** An AppBar action takes a line number.
- **Share.** The AppBar menu has Share and Copy path.

### B7.2 Navigation

- **Breadcrumb.** It starts at the session root, not at `/`, and it scrolls to
  the last crumb. Each crumb is at least 48 dp tall. A long press on the root
  crumb shows the full path.
- **Title.** The AppBar shows the root folder name and the worktree.
- **Scroll position.** Each folder keeps its own scroll position, with a
  `PageStorageKey` for each path.
- **History.** "Show in folder" adds a history entry (`file_browser_screen.dart:228`),
  so Back returns to the file.
- **Pull to refresh.** Both the folder list and the viewer have it, as the Git
  screen does (`git_status_screen.dart:178`).

### B7.3 Rows and states

- **Rows.** Each row has an icon from `utils/file_types.dart`. The duplicate map
  in `file_browser_screen.dart:416-456` goes away. Each row also has the git
  badge and the agent mark from B6, and the relative date from `modTime`.
- **Ignored.** Ignored entries are dimmed, as on desktop (B2).
- **Errors.** `listFiles` and `readFile` return a typed error, not `null`. The
  screen says what failed, in the same words as B1, and offers Retry in both the
  list and the viewer.
- **Changes filter.** A chip above the list switches to the Changes view from
  B6, with the same two sections.

### B7.4 Previews

- **Markdown links.** A relative link opens that file in the viewer. An
  external link opens in the browser. A heading anchor scrolls to the heading.
- **Markdown images.** A relative image loads through the daemon, as on desktop
  (spec 51).
- **Markdown style.** Code blocks, links and quotes use the theme colors that
  the chat uses (`message_card.dart:328`), not fixed hex values
  (`file_browser_screen.dart:1030`).
- **SVG.** It renders with `flutter_svg`, which the app already depends on
  (`pubspec.yaml:65`). A toggle shows the source. Today SVG shows as "Binary
  file", because `Image.memory` cannot decode it.
- **Images.** Double-tap zooms to 2×, and a second double-tap resets the zoom.
  A checkerboard shows behind transparent images.

### B7.5 Chips and Ask AI

- **Chips pass the session.** Chips in the chat, and the folder redirect, pass
  `sessionId` (`message_card.dart:272`, `cards.dart:496`,
  `file_browser_screen.dart:684`). "Ask AI" and line selection then work from a
  chip.
- **Ask AI.** Fixed in F6. The markdown view does not show the "Tap lines to
  select" hint, because markdown has no line taps.
- **False paths.** A chip that names a missing file shows "Not found" with the
  path. It does not show a bare "Failed to load file".

### B7.6 Touch and theme

- **Tap targets.** Every control is at least 48 dp. This covers the breadcrumb
  crumbs, the clear-selection icon (18 px today), the chat chips (22 dp today),
  and the JS switch (scaled to 0.7 today).
- **Theme.** The hard-coded colors move to the theme: the amber folder, the
  "Ask AI" accent and the line-selection color (`file_browser_screen.dart:775`,
  `:842`, `:1081`). The HTML preview shows a dark backdrop around the page in
  dark mode.

## B8. One place for files and changes: the Git tab goes away

Today a person reads a file in one tab and its diff in another. The Git tab
(`components/git.tsx`, `commits.tsx`, `worktrees.tsx`; mobile
`git_status_screen.dart`) holds a scope picker, a list of changed files, the
diffs, review comments and a worktree view. The Changes view from B6 already
lists the changed files. Two lists of the same files, in two tabs, is one too
many.

### Desktop

- **Changes is the git view.** The scope picker from the Git panel sits at the
  top of the Changes mode. Its default is the same as today: "Review vs
  `<base>`" when the branch has commits ahead of its base, else Uncommitted.
  The choice is still kept for each session (`helios.git.scope.*`).
- **The sections follow the scope.**
  - Uncommitted: the two B6 sections, "Edited by this session" and "Other
    uncommitted changes".
  - Review, one commit, or a range: the files that the scope changed, with the
    B6 agent mark on files that this session edited.
- **History stays in the scope picker.** The scope menu lists recent commits,
  as today. Picking a commit or a range changes the list. There is no extra
  mode.
- **A diff is a view of a file tab, not a tab of its own.** A file with changes
  in the current scope gets a three-part toggle in its header: Source, Diff,
  and Preview (Preview only for markdown and HTML). A click in Changes opens
  the file with Diff on. One file has one tab.
- **The diff view.** It is the existing `diff-view.tsx`, windowed as in 2.6,
  with the split/unified choice saved, next and previous change keys (F7 and
  ⇧F7), and the review comments that the Git panel offers today
  (`ReviewView`). A deleted file opens as a diff with no Source view.
- **Worktrees.** The worktree picker in the Files root row gains the worktree
  list and actions from `worktrees.tsx`. The separate Worktrees view goes away.
- **The tab goes away.** `git` leaves `PANELS` (`detail.tsx:44`). A saved panel
  of `git` opens Files in Changes mode. Every link that opened the Git tab
  opens the Changes mode or a diff in Files instead.

### Mobile

- The Git entry in the session screen goes away
  (`session_detail_screen.dart:1163`). The Files screen's Changes chip opens
  the same view, with the scope bar from `git_status_screen.dart` at its top.
- A changed file opens in the file viewer with a Source / Diff toggle. The diff
  is the existing `GitDiffScreen` body, with Ask AI, inside the viewer.
- Commit detail stays reachable from the scope bar.

### What stays the same

- The git endpoints and their shapes.
- The scope logic, the review comments, and the Ask AI on diffs. They move;
  they do not change.

## Rollout

Each phase is one or more PRs. Each PR posts numbers against the budget table.
Each UI PR posts before and after screenshots.

1. **Fix first.** F1 to F7, each one as a small PR. F1 is section 2.1, and it
   ships here, ahead of the rest of Phase 2.
2. **Phase 0.** `Server-Timing`, benchmarks, and the baseline numbers.
3. **Phase 1a.** The raw endpoint, gzip, ETag and 304, and the `If-Match` on
   `PUT`. These are safe to ship first, and they help every client.
4. **Phase 1b.** The watcher lock, `dirDigest` mtime skip, single-process git
   status, top-k ranking, and the cold index. The daemon fields for Part B go
   here too: `positions` on search matches, `ranges` on grep matches, `ignored`
   on listings, and the whole-word and glob grep options.
5. **Phase 1c.** fsnotify on Linux, with the fallback everywhere else. Behind the setting
   `files.watch` (`auto` | `polling`) for one release, so a user can turn it off
   when a platform misbehaves.
6. **Phase 2 with B1 to B5.** Desktop. Speed first, then the panel:
   2.2, 2.3, B1, 2.5 with B2, B3, B4, 2.4 with B5, 2.6.
7. **B6.** The `touched` endpoint and the provider method, then the Changes
   view, the marks and the flash on desktop.
8. **B8.** Desktop, then mobile. After B6, because the Changes view is its
   base.
9. **Phase 3 with B7.** Mobile: 3.1 and B7.1, 3.2, 3.3, B7.2, B7.3 (with the B6
   marks), B7.4, B7.5, B7.6, 3.4.

## Testing

**Daemon**

- **Index patch.** Unit tests for create, remove, rename, a new directory, a
  gitignored file, and a bulk change of more than 512 directories. Each one
  checks that the patched index is equal to a fresh build.
- **Fallback.** Set a watch budget of 10 on a tree of 50 directories. The root
  must report `"watch": "polling"` and still find a new file after a
  `FilesTouched` poke.
- **ETag.** A `GET` with a matching `If-None-Match` returns 304 and does not
  read the file. A `PUT` with a stale `If-Match` returns 409.
- **Positions and ranges.** For a set of queries, each `positions` entry points
  at a character of the query, in order. Each grep `ranges` entry covers text
  that matches the pattern.
- **Touched.** Fixture transcripts for Claude (`Write`, `Edit`, `MultiEdit`)
  and Codex (`apply_patch` with add, update and delete) give the expected paths,
  counts and lines.

**Desktop**

- **F1.** Open a file and edit it, so that it is dirty. Send a `file_changed`
  for a sibling path. The test checks that `saved` and `mod_time` did not
  change.
- **F2 and F3.** With the Files panel hidden, ⌘S writes nothing, and ⌘P opens
  nothing. ⌘B toggles only the sidebar.
- **F4.** Run a search, then click Aa. The test checks that a second request
  went out with the new flag.
- **Keyboard.** e2e tests on the stub daemon (`desktop/e2e/daemon.ts`) for tree
  navigation, quick-open `file:line`, search result navigation, and ⌃Tab.
- **Screenshots.** The e2e suite saves screenshots of the tree, quick-open,
  search and the Changes view, so that a PR review can see what changed.

**Mobile**

- **Frames.** A widget test opens a 5000-line file and taps a line. The test
  checks that `highlight.parse` ran once.
- **F6.** A widget test pushes session → browser → viewer, then sends Ask AI.
  The test checks that the session route is on top.
- **Errors.** A 404, a 403 and a socket error each show their own message and a
  Retry button.

## What we are not doing

- **Server-side highlighting.** px0 highlights with Chroma on the server,
  because its client is plain DOM. CodeMirror already highlights only what is
  visible, and it does this on the client for free.
- **A content index or trigram index.** ripgrep searches this repo in about
  27 ms. An index would cost memory on every root for a gain that nobody can
  feel. Look at this again only when a measured grep goes over 300 ms.
- **Our own grep engine.** px0 has one. ripgrep is faster, and it is already
  here.
- **Chunked loading of large files.** CodeMirror needs the whole document to
  edit it. The 10 MB limit and gzip keep the full read inside the budget.
- **A persistent on-disk index.** It stays in memory for the daemon's life, as
  spec 58 decided.
- **Mobile quick-open.** Spec 58 owns it. This spec makes it cheap, because the
  index and the raw endpoint serve it.
- **Preview tabs.** We considered VS Code's model, where a single click opens a
  tab that the next click replaces. The owner chose permanent tabs. B5 makes
  many tabs easy to handle instead.
- **Create, rename and delete in the tree.** The agent writes the files. The
  person mostly reads and reviews them. These actions need their own design for
  conflicts with a running agent, so they get their own spec.
- **A minimap.** The change marks in the gutter and the search flash cover the
  "where am I" need at a much lower cost.
- **A tablet two-pane layout on mobile.** Worth doing, but it is a layout
  change for the whole app, not for the files screen alone.
- **Folder sets from spec 58.** This spec keeps one root. It does not block
  spec 58, and the tree work in B2 carries over to top-level folders.

## Decisions on the open questions

1. **macOS.** It uses the fallback in this change. FSEvents with cgo comes
   later, after the release builds the darwin daemon on a macOS runner.
2. **Watch budget.** Half of `max_user_watches`. It is not a setting.
3. **Grep staleness.** The "Results may be out of date · Search again" line
   stays. Search does not rerun on its own.
4. **Explorer shortcut.** ⌘⇧E. ⌘B belongs to the sidebar alone.
5. **Other sessions' edits.** Each session marks only its own edits. The others
   show under "Other uncommitted changes".
6. **The Git tab (B8).** It goes away on desktop and mobile. A diff is a toggle
   in the file tab. History stays in the scope picker.

## Supersedes

- Spec 54, "Why not a tree watcher". That section still holds for SSE: raw
  events never become broadcasts. The section no longer forbids a watcher that
  feeds the index.
- Spec 58, "Why not fsnotify". It is replaced by 1.3. The design that spec 58
  proposed (git stat plus the hook) becomes the fallback.
