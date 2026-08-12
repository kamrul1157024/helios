# 35 — Git History and Worktree View

## Problem

Helios shows you what the agent has changed *right now* and nothing else. The
git panel is a working-tree view: staged, changed, untracked, and a unified
diff per file. The moment the agent commits, its work vanishes from the panel.

That is the wrong shape for how these sessions actually run. An agent works for
twenty minutes and leaves three commits behind; the question you want answered
on your phone is "what did it do on this branch", and the panel can only answer
"what is still uncommitted" — which, after a tidy agent, is nothing.

The second half of the same gap is worktrees. Running several agents in
parallel means one worktree per feature branch — the repo this spec was written
in had four checked out at once, one per in-flight spec. The daemon lists them
(`GET /api/git/worktrees`), mobile uses that list as a scope dropdown, and
desktop does not surface them at all. There is nowhere to see, in one glance,
which branch each agent is on and how far along it is.

## Current State

Three read-only endpoints in `internal/server/git.go`:

| Endpoint | Returns | Clients |
|---|---|---|
| `GET /api/git/status` | branch, ahead/behind, staged/unstaged/untracked | mobile `git_status_screen.dart`, desktop `git.tsx` |
| `GET /api/git/diff` | unified diff for one file, working tree only | both |
| `GET /api/git/worktrees` | path, branch, is_main | mobile only, as a scope picker |

Four defects found while reading, all fixed as part of this work:

1. An untracked file has an empty diff. `git diff -- file` ignores untracked
   files, so tapping one shows a blank pane on both clients.
2. `parseStatLine` computes insertions and deletions, discards the result, and
   recomputes them — dead code by any reading.
3. `gitCmd` has no timeout. A git command that hangs on a lock file hangs the
   request.
4. `gitCmd` drops stderr, so every failure reaches the client as
   "failed to get diff" with the reason thrown away.

## Goals

- See the commits on the current branch, on both clients.
- See what one commit changed, and what changed between any two commits.
- See every worktree of the repo with enough state to tell the agents apart.

## Non-Goals

- **Helios does not create, remove or switch worktrees.** You make them; Helios
  shows them. This keeps the whole feature read-only, which is the reason it
  needs no path confinement, no branch-name validation beyond argument safety,
  and no new failure modes on someone else's repository.
- No commit, stage, push, or any other mutation. The agent has a terminal for
  that, and so do you.
- No graph rendering. A list of commits, in order, is what the question needs.

## Which Commits Are "This Branch"

The useful default is `base..HEAD` — what this branch adds on top of where it
started. Resolving `base` has no single right answer, so it is a fallback chain:

1. the tracking branch, `@{upstream}`
2. `origin/HEAD`, the remote's default branch
3. a local `main`, then `master`

The resolved base is returned in the response so the UI can name it, and can be
overridden with `&base=`.

One case breaks the default: on `main`, fully pushed, `origin/main..HEAD` is
empty and the panel would show nothing at all — the worst possible answer to
"what happened here". So an empty branch range silently falls back to full
history and reports `scope: "all"`; the client shows a Branch/All toggle that
does the same thing deliberately.

## API

Two new endpoints, two extended. All read-only, all on `protectedMux` behind
bearer auth, all resolving their `path` through the same `resolveSafePath` as
the existing file and git endpoints.

### `GET /api/git/log`

```
?path=       required, anywhere inside the repo
&base=       override the resolved base
&all=true    full history instead of base..HEAD
&limit=      default 50, max 200
&skip=       for paging
```

```json
{
  "root": "/Users/x/repo", "branch": "feat/thing",
  "base": "origin/main", "scope": "branch",
  "commits": [{
    "sha": "cd98e15…", "short": "cd98e15", "author": "Kamrul",
    "date": "2026-08-11T21:03:11Z", "subject": "feat: …",
    "files": 16, "insertions": 1204, "deletions": 88
  }],
  "has_more": true
}
```

Parsed from a single `git log --format=…%x1e… --shortstat` call: one record
per commit, separated by `0x1e`, fields by `0x1f`. `has_more` comes from asking
for one commit more than the limit and dropping it.

### `GET /api/git/changes`

The file list for a commit or a range — the middle pane, before any patch is
fetched.

```
?path=   required
&to=     required; a sha or ref
&from=   optional; when absent, `to` is compared against its parent
```

```json
{
  "from": "41986ae", "to": "cd98e15", "single": true,
  "subject": "feat: …", "body": "…", "author": "Kamrul",
  "date": "2026-08-11T21:03:11Z",
  "files": [{"path": "internal/server/git.go", "status": "M",
             "insertions": 210, "deletions": 14}],
  "insertions": 1204, "deletions": 88, "truncated": false
}
```

Status letters come from `--name-status`, counts from `--numstat`, zipped by
path; a file present in one and not the other still appears, with whatever is
known. Binary files report `-` counts as zero and are flagged by the diff
endpoint, not here.

### `GET /api/git/diff` (extended)

Existing behaviour is unchanged when the new parameters are absent.

```
&from=&to=       patch for one file at that revision pair
&untracked=true  diff an untracked file against nothing
```

`untracked` runs `git diff --no-index -- /dev/null <file>`, whose exit status 1
means "there are differences" and is not an error — that is defect 1 above.

### `GET /api/git/worktrees` (extended)

Additive: the existing `path`, `branch` and `is_main` stay, so an older client
keeps working.

```json
{"worktrees": [{
  "path": "/Users/x/repo-feature", "branch": "feat/thing", "is_main": false,
  "head": "1d1d745", "subject": "feat: …", "detached": false,
  "ahead": 3, "behind": 12, "dirty": 7, "locked": false
}]}
```

`dirty` is a file count, not a boolean: "7 files touched" tells you an agent is
mid-flight where "dirty" does not. Each worktree costs a handful of git calls,
so the list is capped and the calls are the same short-timeout `gitCmd` as
everything else.

## Revisions as Arguments

The only real safety question in a read-only feature: `from`, `to` and `base`
are user-supplied strings that become git arguments. A value beginning with `-`
would be read as a flag, so revisions are validated against
`[A-Za-z0-9._/^~@{}-]+` with a leading `-` rejected and a length cap, and are
never concatenated into a range string — `from` and `to` are passed as separate
arguments so `git diff A B` cannot be talked into being something else.

## UI

Both clients get the same three views over the same data.

**Changes** — today's panel, unchanged, still the default.

**Commits** — the commit list, with the base and scope named in the header and
a Branch/All toggle. Selecting a commit shows its message and file list;
selecting a file shows the patch. Selecting a second commit with ⌘ or shift
compares the two, and the header says which range is being shown.

**Worktrees** — one row per worktree: branch, ahead/behind, dirty count, head
subject, and a marker on the one the current session is in. Selecting a row
scopes the whole panel to that worktree, which is what mobile's picker already
does; this only makes it visible and gives desktop the same thing.

Desktop puts the three behind a segmented control in the git panel header.
Mobile keeps `git_status_screen` as the Changes tab and adds the other two,
reusing the existing diff renderer and its Ask AI bar. Having no modifier keys,
mobile long-presses a commit to mark one end of the range and taps the other.

## Testing

Go tests build a real repository in `t.TempDir()` — a few commits, a rename, an
untracked file, a second worktree — and assert against it, the same way
`filesearch_test.go` does. Cases that matter: base resolution through each rung
of the fallback, the empty-range fallback to full history, paging boundaries,
a root commit (no parent), a merge commit (no shortstat), a rename, an
untracked diff, and a revision argument beginning with `-` being refused.
