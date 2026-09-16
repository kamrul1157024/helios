# Worktree-Aware Session Creation and Live Worktree Switching

## Status

Proposed.

## Problem

Three things are missing:

1. **No worktree creation flow.** A user who wants a session in a new worktree
   must create it with `git worktree add` in a terminal, then find the path in
   the directory picker. Conductor.build showed what this can look like: one
   click creates the workspace and the branch together.

2. **Worktrees of the same repo scatter.** `groupForCWD` matches the CWD string
   exactly. `/repo/helios` and `/repo/helios-worktrees/fix-auth` are two
   strings, so the second session lands in no group even though both belong to
   the same project.

3. **No way to move a running session.** A user who realises they started in the
   wrong worktree must stop the session and start a new one. The agent should be
   told "your working directory is now X" and carry on.

## Non-Goals

- Worktree deletion, pruning, or locking from the UI.
- Custom worktree path configuration — the convention below is the only shape.
- Cross-repo jumps: a session can move between worktrees of the same repo, not
  between repos.
- Branch creation options beyond the default (`-b` off HEAD). No `--track`, no
  start-point picker, no detached HEAD.

---

## Worktree Path Convention

```
/Users/x/workspace/
├── helios/                          ← main worktree
│   ├── .git/                           (directory — full repo)
│   ├── internal/
│   └── ...
└── helios-worktrees/                ← sibling, auto-created
    ├── fix-auth-timeout/            ← worktree for fix-auth-timeout
    │   ├── .git                        (file — linked worktree)
    │   └── ...
    └── feat-streaming/              ← worktree for feat/streaming
        ├── .git
        └── ...
```

A sibling directory, not a child. A child would appear in `git status`,
`.gitignore` would need an entry, and `git clean -fdx` would destroy it.

Branch name sanitisation: `/` becomes `-`, sequences of `-` collapse to one.
`feat/auth/v2` becomes `feat-auth-v2`. This keeps worktree directories flat.

---

## 1. API: `POST /api/git/worktrees`

### Request

```
POST /api/git/worktrees
Content-Type: application/json

{
  "repo":   "/Users/x/workspace/helios",
  "branch": "fix-auth-timeout"
}
```

`repo` can be any path inside the repository — any worktree, any subdirectory.
The handler resolves to the main worktree via `gitRepoRoot` +
`parseWorktreeList`.

### Behaviour

```
                   ┌─────────────────────┐
                   │  Decode repo, branch │
                   └──────────┬──────────┘
                              │
                   ┌──────────▼──────────┐
                   │  gitRepoRoot(repo)  │──── not a repo ──→ 400
                   └──────────┬──────────┘
                              │
                   ┌──────────▼──────────┐
                   │  check-ref-format   │──── invalid ──→ 400
                   └──────────┬──────────┘
                              │
                   ┌──────────▼──────────┐
                   │  parseWorktreeList  │
                   │  findMainWorktree   │
                   └──────────┬──────────┘
                              │
                   ┌──────────▼──────────┐
                   │  Branch already     │
                   │  checked out?       │──── yes ──→ 409
                   └──────────┬──────────┘
                              │ no
                   ┌──────────▼──────────┐
                   │  Branch exists but  │
                   │  not checked out?   │
                   └────┬──────────┬─────┘
                   yes  │          │ no
                        ▼          ▼
              git worktree add   git worktree add
              <path> <branch>    -b <branch> <path>
                        │          │
                        └────┬─────┘
                             │
                   ┌─────────▼──────────┐
                   │  describeWorktree   │
                   │  Return 201        │
                   └────────────────────┘
```

### Response

```json
{
  "worktree": {
    "path":     "/Users/x/workspace/helios-worktrees/fix-auth-timeout",
    "branch":   "fix-auth-timeout",
    "is_main":  false,
    "head":     "cd98e15",
    "subject":  "feat: scheduled runs",
    "date":     "2026-09-15T10:03:11Z",
    "detached": false,
    "locked":   false,
    "ahead":    0,
    "behind":   0,
    "dirty":    0,
    "base":     "origin/main"
  }
}
```

### Errors

| Condition | Status | Body |
|---|---|---|
| Not a git repo | 400 | `"not a git repository"` |
| Empty branch | 400 | `"branch name is required"` |
| Invalid ref name | 400 | `"invalid branch name: <reason>"` |
| Branch checked out elsewhere | 409 | `"branch already checked out in <path>"` |
| `git worktree add` fails | 500 | stderr from git |

### Implementation

`handleCreateWorktree` in `internal/server/git.go`. Register beside the
existing listing endpoint:

```go
protectedMux.HandleFunc("POST /api/git/worktrees", s.handleCreateWorktree)
```

Go 1.22 mux dispatches by method, so `GET` and `POST` on the same path do not
conflict.

Helper functions: `sanitizeBranch(name string) string` and
`findMainWorktree(entries []worktreeEntry, fallback string) string`.

---

## 2. Repo-Aware Auto-Grouping

### The problem

```sql
-- groups.go:385 — today's query
SELECT group_key FROM sessions
WHERE cwd = ? AND group_key IS NOT NULL
ORDER BY COALESCE(last_event_at, created_at) DESC LIMIT 1
```

Exact string match. Two sessions in the same repo but different worktrees have
different CWD strings. Neither inherits the other's group.

### The fix

```
              ┌──────────────────────────────┐
              │       groupForCWD(cwd)       │
              └──────────────┬───────────────┘
                             │
               ┌─────────────▼─────────────┐
               │  Exact CWD match?         │
               │  (existing query)         │
               └─────┬───────────────┬─────┘
                found│               │not found
                     ▼               ▼
                return          ┌────────────────┐
                group           │ RepoResolver   │
                                │ (cwd) → paths  │
                                └───┬──────────┬───┘
                               nil  │          │ paths
                               (not │          │
                                git)│          ▼
                                    ▼    ┌──────────────────┐
                               return   │ WHERE cwd IN     │
                               nil      │ (wt1, wt2, ...)  │
                                        │ AND group_key    │
                                        │ IS NOT NULL      │
                                        │ ORDER BY ... 1   │
                                        └───┬──────────┬───┘
                                       found│          │not found
                                            ▼          ▼
                                       return     return nil
                                       group
```

### Performance

Running `git rev-parse` and `git worktree list` on every `UpsertSession` would
be unacceptable on the hook path.

**Guard 1:** The git fallback fires only when the exact-match query returns
nothing. The fast path — which covers every session after the first one in a
directory — is unchanged.

**Guard 2:** `UpsertSession` calls `groupForCWD` only on INSERT. The
`ON CONFLICT DO UPDATE` path does not re-run it (`sessions.go:122`). The git
fallback runs once per session lifetime.

Even then, `git rev-parse --show-toplevel` is <5ms and
`git worktree list --porcelain` is the same.

### Package boundary

`store` must not import `server`. Inject a resolver:

```go
// Store field
RepoResolver func(cwd string) []string
```

Set by `server` at init time. The function takes a directory path and returns
the worktree paths that share its repository, or nil when the directory is not
inside a git repo.

```go
// server/git.go
func worktreeResolver(cwd string) []string {
    root, err := gitRepoRoot(cwd)
    if err != nil { return nil }
    out, err := gitCmd(root, "worktree", "list", "--porcelain")
    if err != nil { return nil }
    worktrees := parseWorktreeList(out, root)
    paths := make([]string, 0, len(worktrees))
    for _, wt := range worktrees {
        paths = append(paths, wt.Path)
    }
    return paths
}
```

Injected in `NewShared`:

```go
func NewShared(db *store.Store, ...) *Shared {
    db.RepoResolver = worktreeResolver
    // ...
}
```

### Files

| File | Change |
|---|---|
| `internal/store/store.go` | Add `RepoResolver` field to `Store` |
| `internal/store/groups.go` | Expand `groupForCWD` with git fallback |
| `internal/server/git.go` | Add `worktreeResolver` |
| `internal/server/server.go` | Inject resolver in `NewShared` |

---

## 3. Live Worktree Switch

### The problem

A session's CWD is frozen at `SessionStart`. A user who started in the wrong
worktree must stop, start again, and lose the conversation. The daemon has no
mechanism to change the CWD after launch.

### The fix: `POST /api/sessions/{id}/worktree`

#### Request

```json
{ "path": "/Users/x/workspace/helios-worktrees/fix-auth" }
```

#### Behaviour

```
              ┌──────────────────────────────┐
              │  Decode path, load session   │
              └──────────────┬───────────────┘
                             │
               ┌─────────────▼─────────────┐
               │ Session exists and has a   │
               │ live terminal?             │──── no ──→ 400
               └─────────────┬─────────────┘
                             │ yes
               ┌─────────────▼─────────────┐
               │ gitRepoRoot(session.CWD)  │
               │ gitRepoRoot(path)         │
               │ Same repo root?           │──── no ──→ 400
               └─────────────┬─────────────┘
                             │ yes
               ┌─────────────▼─────────────┐
               │ path is a worktree?       │──── no ──→ 400
               │ (in parseWorktreeList)    │
               └─────────────┬─────────────┘
                             │ yes
               ┌─────────────▼─────────────┐
               │ UPDATE sessions           │
               │ SET cwd = path            │
               │ WHERE session_id = id     │
               └─────────────┬─────────────┘
                             │
               ┌─────────────▼─────────────┐
               │ SendPrompt(id,            │
               │   "cd " + path)           │
               └─────────────┬─────────────┘
                             │
               ┌─────────────▼─────────────┐
               │ Broadcast session_updated │
               │ Return 200               │
               └───────────────────────────┘
```

#### Why `cd`

The daemon needs to tell a running agent "you are now in a different directory."
Three mechanisms exist:

| Mechanism | Fit |
|---|---|
| MCP tool call | Agent → daemon only. No push channel. |
| HITL overlay | Blocks the session. For decisions, not context. |
| `SendPrompt` | Types text into the terminal. Agent treats it as user input. |

`SendPrompt` is the only one that reaches the agent. Typing `cd <path>` is what
a human would type. The agent's shell changes its working directory, and the
next tool call runs in the new tree.

The prompt is short and unambiguous. If the agent is mid-turn, the prompt is
queued (for agents that support it) or typed when the session goes idle.

#### CWD update

This is a new capability: `sessions.cwd` was read-only after `SessionStart`.
The switch endpoint writes it directly:

```sql
UPDATE sessions SET cwd = ?, project = ? WHERE session_id = ?
```

`project` updates too — it is `filepath.Base(cwd)`, so switching from
`/repo/helios` to `/repo/helios-worktrees/fix-auth` makes the project label
`fix-auth` rather than `helios`. Both values reach clients through the SSE
broadcast.

#### Files

| File | Change |
|---|---|
| `internal/server/api.go` | Add `handleSwitchWorktree` handler |
| `internal/server/server.go` | Register `POST /api/sessions/{id}/worktree` |
| `internal/store/sessions.go` | Add `UpdateSessionCWD(id, cwd string)` |

---

## 4. Desktop: Worktree Picker in New-Session Dialog

### Where it appears

Inside the `DirectoryList` component in `newsession.tsx`, between the "Recent"
section and the filesystem completions:

```
┌─────────────────────────────────────────┐
│ Type a path, or search…                 │
├─────────────────────────────────────────┤
│ Home                               ~    │
│                                         │
│ Recent                                  │
│ ● helios                  2 active      │
│   /Users/x/workspace/helios             │
│ ● opal-app                             │
│   /Users/x/workspace/opal-app          │
│                                         │
│ Worktrees                               │
│ ⌘ main                                 │
│   /Users/x/workspace/helios             │
│ ⌘ fix-auth          3 dirty            │
│   /Users/x/workspace/helios-wt/fix-auth │
│ + New worktree                          │
│                                         │
│ In /Users/x/workspace/                  │
│ 📁 another-project                     │
│   /Users/x/workspace/another-project    │
└─────────────────────────────────────────┘
```

### Behaviour

The section appears when the current `cwd` is inside a git repo with worktrees.
It queries `worktreesQuery(hostId, cwd)` — the existing endpoint.

Clicking a worktree row sets `cwd` to that worktree's path.

The "+ New worktree" row opens an inline input for a branch name. On Enter, it
calls `createWorktree(cwd, branch)` and sets `cwd` to the returned path.

### Files

| File | Change |
|---|---|
| `desktop/src/renderer/components/newsession.tsx` | Add `WorktreeSection` component |
| `desktop/src/renderer/bridge.ts` | Add `createWorktree` method on `HostApi` |
| `desktop/src/main/api.ts` | Add `createWorktree` method on `ApiClient` |

---

## 5. Desktop: Switch Worktree on a Running Session

### Where it appears

In the session header or context menu, beside the existing CWD label. A
"Switch worktree" option opens a picker that lists the worktrees of the
session's repo.

```
┌─────────────────────────────────────────┐
│ Switch worktree                         │
├─────────────────────────────────────────┤
│ ⌘ main                    (current)    │
│   /Users/x/workspace/helios             │
│ ⌘ fix-auth          3 dirty            │
│   /Users/x/workspace/helios-wt/fix-auth │
│ ⌘ feat-streaming                       │
│   /Users/x/workspace/helios-wt/feat-str │
│ + New worktree                          │
└─────────────────────────────────────────┘
```

### Behaviour

1. User picks a worktree (or creates one inline).
2. Client calls `POST /api/sessions/{id}/worktree` with the path.
3. Daemon updates the CWD in the database and types `cd <path>` into the
   agent's terminal.
4. Client receives the `session_updated` SSE event and refreshes the CWD chip.

### Files

| File | Change |
|---|---|
| `desktop/src/renderer/components/sidebar.tsx` or session header | Add worktree switch action |
| `desktop/src/renderer/bridge.ts` | Add `switchWorktree(id, path)` method |
| `desktop/src/main/api.ts` | Add `switchWorktree(id, path)` method |

---

## Implementation Phases

| Phase | What | Files |
|---|---|---|
| 1 | Backend: `POST /api/git/worktrees` | `server/git.go`, `server/server.go` |
| 2 | Repo-aware grouping | `store/store.go`, `store/groups.go`, `server/server.go`, `server/git.go` |
| 3 | Live switch: `POST /api/sessions/{id}/worktree` | `server/api.go`, `server/server.go`, `store/sessions.go` |
| 4 | Desktop: worktree picker in new-session dialog | `newsession.tsx`, `bridge.ts`, `api.ts` |
| 5 | Desktop: switch worktree on running session | sidebar or header, `bridge.ts`, `api.ts` |
| 6 | Mobile: worktree picker and switch | `new_session_sheet.dart`, `daemon_api_service.dart` |

Phases 1 and 2 ship together. Phase 3 can ship alone — it needs no UI. Phases
4 and 5 ship together. Phase 6 follows.
