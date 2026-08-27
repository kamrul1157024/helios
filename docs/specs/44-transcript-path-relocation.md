# Transcript Path Relocation: Follow a Session Into Its Worktree

## Problem

A session that enters a git worktree loses its transcript. The messages view
goes empty and stays empty for the rest of the session's life, while the
terminal keeps working normally — the agent is fine, only Helios' view of it is
broken.

Observed on session `c7e3af80-96a6-4157-9583-21b739bab1b3` ("SA warning
removal"), 2026-08-27. Its cwd, read out of the transcript itself:

| time | cwd |
|------|-----|
| 05:32:53 | `/workspace/opal-app` ← `SessionStart` registered the session here |
| 05:34:23 | `/workspace/opal-app/frontend` |
| **05:35:25** | `/workspace/opal-app/.claude/worktrees/remove-workspace-tools-warning` |

Claude Code names a transcript's directory after the session's cwd, so at 05:35
the file moved:

- recorded in `sessions.transcript_path`:
  `~/.claude/projects/-home-md-kamrul-hassan-workspace-opal-app/c7e3af80….jsonl`
  — **gone**
- actually on disk:
  `~/.claude/projects/-home-md-kamrul-hassan-workspace-opal-app--claude-worktrees-remove-workspace-tools-warning/c7e3af80….jsonl`
  — 1.3 MB, 146 messages, still being appended to

Reading each path directly:

```
stale  → ERR: stat transcript: … no such file or directory
actual → total=146 returned=5
```

`GET /api/sessions/<id>/transcript` (`internal/server/api.go:421`) hands the
stale path to `transcript.Page`, gets that error, and answers **500
`failed to read transcript`** on every poll. Mobile and desktop both render
that as an empty conversation.

**Blast radius:** 54 of 237 sessions in `helios.db` point at a transcript file
that is not there. Most are terminated sessions whose files aged out, but every
worktree-entering session joins them, and several `-worktree-` paths in that
list are this same failure.

## Root cause

Two writers own `transcript_path`, and neither can correct it after the fact.

`internal/store/sessions.go:163` is write-once:

```sql
UPDATE sessions SET transcript_path = ?
WHERE session_id = ? AND transcript_path IS NULL
```

Every hook calls `updateSessionTranscript`
(`internal/provider/claude/hooks.go:1214`) carrying the *current* path, so the
CLI was telling us the new location roughly 200 times after the move. The
`IS NULL` guard made all of it a no-op. The guard dates to the initial commit
`f6cc4a3`, with no rationale beyond its comment, "sets the transcript path if
not already set".

`UpsertSession` (`internal/store/sessions.go:84`) *can* overwrite the path, but
its only hook caller is `SessionStart` (`hooks.go:783`), which does not fire
again for a cwd change. That is also why `sessions.cwd` still reads
`/workspace/opal-app` for this session.

## Design

Three changes, one per layer:

```text
 hook event    GET /transcript
      |               |
      v               v
 +---------+     +---------+
 | A store |<----| C serve |
 | last    | save| resolve |
 | one     |     | + repair|
 | wins    |     +----+----+
 +---------+          | miss
                      v
                 +---------+
                 | B find  |
                 | by id   |
                 +---------+
```

What one request does:

```text
GET /transcript
      |
      v
stat(recorded)  --ok--> serve
      |
   missing
      |
      v
scan projects/
  */<id>.jsonl  --hit--> save,
      |                  serve
   nothing
      |
      v
200 + empty page
  (never a 500)
```

A live session never reaches the scan: A re-records the path on the first hook
after the move, so C's `stat` hits on the next poll.

### A. The recorded path follows the file (`internal/store`)

Drop the `IS NULL` guard, and skip the write when nothing changed so the common
case stays a no-op:

```sql
UPDATE sessions SET transcript_path = ?
WHERE session_id = ? AND (transcript_path IS NULL OR transcript_path != ?)
```

The last hook to speak wins. This is the whole fix for sessions running under
current hooks: any hook event after the move re-records the path.

### B. Find a transcript by session ID (`internal/discovery`)

Sessions that moved *before* this change have a stale row and may never fire
another hook — a terminated session never will. `FindClaudeTranscript(sessionID)`
walks `~/.claude/projects/*/` for `<sessionID>.jsonl` and returns the match:

```go
func FindClaudeTranscript(sessionID string) string
```

Returns `""` when nothing matches. When several directories hold a file for the
same session, the newest by mtime wins — that being the one still being written
to. The lookup is exact-name, so subagent transcripts (`agent-*.jsonl`) cannot
be mistaken for a session's own.

This belongs in `discovery` because that package already owns knowledge of the
`~/.claude/projects` layout.

### C. The API resolves and repairs (`internal/server`)

```go
func (s *PublicServer) resolveTranscriptPath(session *store.Session) string
```

- recorded path exists → return it (one `stat`, the normal case)
- recorded path missing, source is `claude` → `FindClaudeTranscript`, write the
  result back with `UpdateSessionTranscriptPath`, return it
- nothing found → `""`

Used by `handleSessionTranscript` and by `handleGenerateSessionTitle`, which
reads the same field. Writing the result back means the directory walk happens
once per relocation, not once per poll.

A `""` result takes the existing "session has no transcript" branch: **200 with
an empty page, not a 500**. A transcript that is genuinely gone is an empty
transcript — the client polls this endpoint and has nothing to retry, so an
error status only produces noise and an error banner over an empty list.

### D. Not in scope

- **`sessions.cwd` staleness.** The same move leaves cwd pointing at the
  pre-worktree directory, which misdirects the diff and file-browser views.
  Hooks carry cwd on every event, but the agent also `cd`s into subdirectories
  (`/opal-app/frontend` above), so blindly following it would flip the session's
  displayed project name back and forth. Fixing it needs a rule for which cwd is
  the session's own — separate change, separate spec.
- **`internal/daemon/reaper.go:39`** reads `TranscriptPath` to backfill the last
  user message. It runs on live sessions, which self-heal via A.
- **Backfilling the 54 stale rows in a batch.** C repairs each row the first
  time anything asks for it, which is enough; a migration would walk every
  project directory at startup for rows nobody will ever open.

## Edge cases

| case | behaviour |
|------|-----------|
| transcript deleted outright | 200, empty page; the row keeps its stale path |
| two copies of the same session ID | newest mtime wins |
| non-`claude` session with a bad path | no lookup, empty page |
| `~/.claude/projects` unreadable or absent | `""`, empty page |
| session with no path at all | unchanged — the existing empty branch |
| relocation mid-poll | the delta's epoch changes, so the client resets to a full page (spec 38) rather than splicing onto the old file |

## Testing

Three tests, each verified to fail against the current code with the same
`500 … no such file or directory` seen in production:

- `internal/store` — `UpdateSessionTranscriptPath` overwrites an existing path.
- `internal/discovery` — `FindClaudeTranscript` picks the newest of two copies;
  returns `""` for an unknown ID. Uses `t.Setenv("HOME", t.TempDir())`.
- `internal/server` — a session whose recorded transcript has moved to another
  project directory serves its messages *and* has the new path recorded
  afterwards; a session whose transcript was deleted returns an empty page
  rather than a 500.

## Success criteria

1. Entering a worktree does not interrupt the messages view.
2. Sessions already holding a stale path recover the first time they are opened,
   including terminated ones.
3. `GET /api/sessions/<id>/transcript` never returns 500 for a missing file.
4. Steady state costs one extra `stat` per request and no extra writes.
