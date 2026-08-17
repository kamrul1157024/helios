# Transcript Serving: Parse Once, Keep It in RAM, Append the Tail

## Problem

`GET /api/sessions/<id>/transcript` re-reads and re-parses the entire Claude
`.jsonl` transcript on every call, and every client re-calls it on every hook
event. Cost scales with transcript size, not with what changed.

Call chain today:

- `internal/server/api.go:411` → `transcript.ParseClaudeTranscript(path, limit, offset)`
- `internal/transcript/reader.go:86` `parseClaude` reads the whole stream into
  `allMessages`, slices a page off the end, and **throws the rest away**.
- `internal/provider/claude/autotitle.go:399` `readSession` does the same full
  parse, triggered from the `Stop` hook (`internal/provider/claude/hooks.go:617`).
- `internal/daemon/reaper.go:54` already reads *backward* in 64 KB chunks rather
  than parsing everything — the only place that does.

Event amplification:

- `session_status` is broadcast from 14 sites, including **every** `PreToolUse`
  (`hooks.go:1008`), prompt submit (`hooks.go:904`), and stop (`hooks.go:602`).
- Mobile (`mobile/lib/screens/session_detail_screen.dart:98`) debounces 500 ms
  then calls `_loadTranscript()` — a full `limit=200, offset=0` page.
- Desktop (`desktop/src/renderer/components/chat.tsx:82`) keys an effect on
  `session.last_event_at` + `status`, so it refetches `PAGE=200, offset=0` on
  every hook too.

One tool call by the agent = one full parse per attached viewer, plus another
from autotitle at the end of the turn.

## Measurements (2026-08-17, this machine)

`ParseClaudeTranscript(path, 200, 0)`:

| file | lines | messages | parse | alloc/parse | JSON response |
|------|-------|----------|-------|-------------|---------------|
| 17.6 MB | 6856 | 2660 | **205 ms** | **75.6 MB** | 0.06 MB |
| 9.5 MB | 5440 | 2131 | 108 ms | 32.7 MB | 0.08 MB |
| 5.7 MB | 1977 | 772 | 63 ms | 20.7 MB | 0.09 MB |
| 0.3 MB | 200 | 66 | 3.6 ms | 0.9 MB | — |

The response is 0.06 MB out of 17.6 MB read: **99.7 % of the work is discarded**,
then redone on the next event.

**Parsed messages are an order of magnitude smaller than the file.** Parsing and
*holding* the results (`HeapAlloc` after `runtime.GC()`):

| scope | raw jsonl | messages | retained heap | cold parse |
|-------|-----------|----------|---------------|------------|
| biggest single file | 17.6 MB | 2 660 | **1.6 MB** (9.2 %) | 198 ms |
| 10 biggest files | 62.1 MB | 10 939 | **7.0 MB** (11.3 %) | 686 ms |
| every transcript on disk (146) | 87.4 MB | 17 806 | **11.5 MB** (13.2 %) | 1.03 s |

**~680 B per message.** The parsed form is small because the parser already
discards what is large: `tool_result` bodies keep only a tool name and a success
flag, and `attachment` / `file-history-snapshot` entries are dropped entirely.
Those are the bulk of the bytes:

```
user                    67.1%   (mostly tool_result bodies — discarded)
assistant               18.8%
attachment              10.6%   (skipped)
file-history-snapshot    2.2%   (skipped)
system / pr-link / last-prompt / permission-mode / worktree-state / queue-operation  <1% each
```

Holding *every* session this machine has ever run costs 12 MB. There is no
memory argument for re-reading anything.

## Verifying the append-only assumption

Everything rests on: **the prefix of a transcript file never changes**, so a
cached parse stays valid and only the tail needs reading. Checked, not assumed:

1. **Live file, sampled every 12 s for 1 min** while an agent worked: size grew
   210 444 → 262 611 B monotonically; `md5` of the first 100 KB identical at
   every sample.
2. **145 files scanned**: zero duplicate `uuid` values within any file → entries
   are never rewritten in place.
3. **`sessionId` inside every entry equals the filename** in all 145 files → a
   resumed session appends to its existing file rather than rewriting it.
4. **Compaction appends.** A file carrying 2 compact entries shows the same
   monotonic, non-duplicating structure.
5. **Fork copies into a new file and tags what it copied.** Exactly one pair of
   files shares a first `uuid` (`52fc5845…` / `82c4956f…`). Every copied entry
   in the new file carries `forkedFrom: {sessionId, messageUuid}` and keeps its
   original uuid; the source file is untouched. A fork lands on a new path and
   inode, so it cannot alias a cached entry.
6. **Rewind appends a branch; it does not truncate.** The transcript is a DAG:
   entries carry `parentUuid`, and the live head is tracked by `last-prompt`
   entries carrying `leafUuid`, appended repeatedly (261 of them in one file).
   A rewind or an edited message starts a new branch at the rewind point; the
   abandoned branch stays in the file. 38 of 145 files contain a `parentUuid`
   with more than one child, i.e. an in-file branch point.

Nothing observed ever shrinks or rewrites a transcript. The truncation guard
below is therefore defensive — it covers something *outside* Claude Code editing
the file — and is expected never to fire in normal use. It stays because it is
one integer comparison.

Also observed: ~4–5 % of entries carry timestamps out of order relative to the
previous line (interleaved `last-prompt` / `permission-mode` writes). **File
order is the ordering** — nothing may sort by timestamp.

## Design

Parse a transcript once, keep the parsed messages in RAM, and on each request
read only the bytes appended since. Serving a page becomes a slice of a slice.

### A. In-memory transcript store (`internal/transcript`)

```go
type cached struct {
    mu          sync.Mutex
    messages    []Message
    parsedBytes int64  // offset of the last complete '\n' consumed
    tailHash    uint64 // hash of the 4 KB preceding parsedBytes
    dev, ino    uint64 // file identity from os.Stat
    epoch       uint64 // bumped on every rebuild
    lastUsed    time.Time
}
```

Keyed by transcript path. On every request, `Stat` and branch:

- `size == parsedBytes`, same inode → serve from RAM, no I/O beyond the stat.
- `size > parsedBytes` → open, `Seek(parsedBytes)`, parse **only the new bytes**,
  append to `messages`. ~10 KB per event (measured ~50 KB/min of growth on an
  active session) → sub-millisecond.
- `size < parsedBytes`, different inode, or `tailHash` mismatch → the prefix
  moved (rewind, fork, external rewrite). Drop and rebuild, bump `epoch`.

Note what is *not* held: the file's bytes. Only the parsed messages survive a
refresh (~10 % of raw, 680 B/message), plus one offset and one hash.

Rules:

- **Never advance `parsedBytes` past the last `'\n'`.** The writer may be
  mid-line; a partial trailing line is re-read next time rather than parsed into
  a broken message.
- Keep the existing over-long-line guard (`reader.go:138` `readLine`, 16 MB cap).
  Largest line observed: 391 KB.
- Per-entry mutex, so two viewers refreshing the same session serialise on one
  append instead of doing the work twice.

Memory: at 680 B/message, a 128 MB cap holds ~190 000 messages — an order of
magnitude past every transcript this machine has. Bound it anyway with an LRU
plus an idle TTL (evict after ~1 h untouched); eviction costs at most one cold
reparse, and cold parse of the *largest* file is 198 ms.

#### Keeping it in sync

The read path is the sync point: every request stats the file and catches up
before serving. Measured on the 17.6 MB transcript:

```
os.Stat            1.66 µs
pread 10 KB         1.35 µs
```

A refresh therefore costs microseconds, and there is nothing to keep in sync
between requests — a cache that only advances when read cannot drift, and a
daemon restart loses nothing but a lazy rebuild.

Rejected alternatives, and why they are optimisations rather than mechanisms:

- **fsnotify watcher.** Adds a dependency (go.mod has none today) and an FD per
  watched file, and a dropped event would leave a viewer stale indefinitely — so
  the stat check has to exist regardless. Only worth it as a push trigger.
- **Hook-driven warm.** Free, since hooks already carry `transcript_path`
  (`hooks.go:23`, consumed at `hooks.go:1217`): the daemon can refresh the entry
  in the hook handler so the HTTP request is a pure slice. But hooks do not fire
  for every message (an assistant turn with no tool call only produces `Stop`),
  so this cannot replace the stat — it front-runs it.
- **Polling tail goroutine per session.** All the cost of a watcher, none of the
  precision. Only justified if the SSE push in §E lands.

### B. Delta responses so events do not re-ship the page

With the store in place the server cost is gone, but each event still ships
50–200 messages over the wire (over Tailscale, for mobile) and makes the client
rebuild its list and lose scroll position.

- Each message gets a **`seq`** — its index in `cached.messages`. Stable, since
  the prefix is append-only.
- Responses carry **`epoch`**.
- New param `?after_seq=<n>&epoch=<e>` returns only messages past `n` —
  typically 1–3 messages, a few KB.
- Epoch mismatch → respond with a fresh newest page and `epoch_changed: true`;
  the client resets. This is the rewind / fork / restart path.
- `limit`/`offset` keep their current end-anchored meaning (`reader.go:110`) for
  older pages, and `total` stays exact and free — the whole list is in RAM.

### C. Clients: page 50 and infinite scroll

Both clients pull 200 messages and rebuild the list on every event; mobile has
no way to load older ones at all.

- **Initial page 50**, newest first (down from `PAGE = 200`).
- **Mobile** (`session_detail_screen.dart:936`): the list is already
  `reverse: true`. The `_hasMore` branch renders a dead `"N earlier messages"`
  label; replace it with a loader that calls `_loadOlder()` when it scrolls into
  view, guarded by a `_loadingOlder` flag, prepending the results.
- **Desktop** (`chat.tsx:122`): `loadOlder` exists but is manual — drive it from
  a sentinel at the top of the scroller and drop `PAGE` to 50.
- **On SSE `session_status`**: call `?after_seq=` with the newest held `seq` and
  append. No list rebuild, no scroll jump; the 500 ms debounce stops being
  load-bearing.
- Optionally trim the in-memory list (e.g. 1000 messages) once the user is back
  at the bottom.

### D. Same store for autotitle

`autotitle.readSession` (`autotitle.go:399`) full-parses only to scan the tail in
reverse, once per `Stop` hook. Point it at the store: after this change it reads
a slice.

### E. Decided: the file stays the source of truth, read linearly

A rewind appends a branch rather than truncating, so an abandoned branch stays
in the file and the linear reader (`reader.go:86`) shows it. Measured over 145
transcripts, 45 contain entries that are not on the current leaf path — 80fc19d3
shows 3064 entries of which 673 are on the live path, mostly because compaction
re-parents later turns onto the summary.

**Helios keeps reading the file linearly and shows what is in it.** No walking
`parentUuid` back from `last-prompt.leafUuid`, no reconstructing the live branch,
no hiding pre-compaction history. The transcript file is the source of truth; if
something is in the file, it is shown.

This keeps the whole design trivial: the visible list only ever grows, `seq` is a
plain index into it, and `epoch` is needed solely for the defensive rebuild in
§A. Reading the entire file is fine when it is needed — the point of the store is
that it is needed once per session, not once per event.

### F. Optional, later: push deltas over SSE

The daemon runs the hook that triggers the event and now already holds the
messages, so it can broadcast `transcript_append` with the delta and remove the
round-trip entirely. Clients keep `?after_seq=` as the reconnect path.

## Ordering

1. **A** — in-memory store with tail append. Invisible to clients, removes
   nearly all the cost.
2. **B** — `seq` + `epoch` + `after_seq`.
3. **C + D** — page 50, infinite scroll, delta on event, autotitle rewired.
4. **F** — SSE push, only if events still feel late.

## Edge cases

| case | handling |
|------|----------|
| rewind | appends a branch; nothing to invalidate, and the branch is shown (§E) |
| external truncation / rewrite | `size < parsedBytes` or tail-hash mismatch → rebuild, new epoch, client resets |
| fork (`--fork-session`) | new inode → treated as a different file |
| `/clear` | new session file, same path as above |
| compaction | plain appends; nothing special |
| partial last line | never consumed; stop at the last `'\n'` |
| line larger than a chunk | existing 16 MB `readLine` cap; observed max 391 KB |
| out-of-order timestamps | ignore timestamps for ordering; file order wins |
| concurrent viewers | per-entry mutex; one append, not one per viewer |
| daemon restart | store rebuilds lazily on first request (≤ 198 ms worst case here) |
| memory growth | LRU + idle TTL; eviction costs one reparse |

## Testing

- Store output equals a full parse, over every `.jsonl` in a fixture corpus.
- Append to a fixture → the store's messages equal a fresh full parse, and only
  the appended bytes were read.
- `?after_seq=` delta equals the difference between two full parses.
- Truncate a fixture mid-file → epoch bumps, result matches a fresh parse.
- Fixture containing a rewind branch parses to the same linear list before and
  after the branch is appended (no reset, no reorder).
- Write a partial final line → not served; completing it serves it exactly once.
- Fixture with a 400 KB line spanning a refresh boundary.
- Concurrency: N goroutines refreshing one growing file, race detector on.
- Benchmark on the 17 MB fixture: warm page **< 1 ms**, refresh after a 10 KB
  append **< 1 ms**, cold build ~200 ms.
- `cmd/apitest` (`main.go:384`) gains a delta-fetch assertion.

## Success criteria

- p95 `GET /transcript` on an active 17 MB session: **205 ms → < 1 ms** warm.
- Allocation per event: **75.6 MB → < 100 KB**.
- Steady-state store size for a heavy user: **~12 MB** (measured: all 146
  transcripts on this machine).
- Mobile can scroll back through a full transcript; neither client jumps scroll
  position when a message arrives.
