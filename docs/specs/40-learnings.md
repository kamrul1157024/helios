# Learnings: A Folder an Agent Writes, a Library Helios Reads

> **Withdrawn. Never implemented. See `41-helios-mcp-tools.md`.**
>
> This design stored an explanation as a durable artifact. The premise was
> wrong: the value is in the moment, not in the record. An agent wants you to
> look at one thing now, and a folder of markdown is a heavy way to say that.
> `helios_show` says it in one tool call and stores nothing.
>
> Kept here for the reasoning it rules out — content as files rather than
> references, and the point that a walkthrough of a specific change should show
> that change frozen rather than re-read the file later.

Supersedes the deck model in `docs/specs/39-agent-driven-explain-ui.md`. That
spec's premises — decks bound to a session, content held as references the
daemon resolves, a Learn tab inside the session panel — are all withdrawn here.
Its measurements and spike results still stand and are not repeated.

## Problem

Explaining a large change produces something worth keeping. The deck model threw
it away: a deck belonged to a session, so it died with the session that happened
to produce it, and it lived behind a tab in that session's panel where nobody
would look for it a week later.

Three things were wrong, and they compound:

1. **The wrong owner.** A walkthrough of PR #6813 is not a property of the
   terminal that produced it.
2. **The wrong content model.** The daemon stored references and re-read files
   on display, so "cannot show stale code" was the goal. For a durable learning
   that is backwards — a learning about a specific change should show that
   change forever. Re-reading is the bug.
3. **The wrong writer.** Helios had a launcher that injected prompts, so the UI
   needed a session to inject into, which is what forced the binding in the
   first place.

## The model

A **learning** is a folder of files an agent writes, plus a row in SQLite so it
can be listed. Nothing else.

```
agent writes            Helios reads
─────────────           ────────────
markdown, diffs,   →    renders them
learning.json           lists them
```

Helios has no write path. It never creates a learning, never edits one, never
sends a prompt. That single constraint removes the session binding, the target
picker, the prompt templates, and the `sendPrompt` dependency together.

Because the agent authors the content, the daemon resolves nothing. Gone with
it: the working directory, the repository root, symbol lookup, base-SHA pinning,
and the read-time file access that made all three necessary.

## Interaction

Three phases, and the agent is present for only the first.

```
 ┌── PHASE 1 ─ an agent builds it ─────────────────────────────────────────┐
 │                                                                          │
 │  YOU              CLAUDE CODE            DAEMON              DISK        │
 │   │                    │                    │                  │         │
 │   │ "walk me through   │                    │                  │         │
 │   │  PR #6813"         │                    │                  │         │
 │   ├───────────────────▶│                    │                  │         │
 │   │                    │ helios_learning_new│                  │         │
 │   │                    │  (title)           │                  │         │
 │   │                    ├───────────────────▶│  mkdir           │         │
 │   │                    │                    ├─────────────────▶│         │
 │   │                    │◀───────────────────┤ {path}           │         │
 │   │                    │                    │  status=building │         │
 │   │                    │                    │                  │         │
 │   │                    │ Write 01-idea.md   │                  │         │
 │   │                    ├──────────────────────────────────────▶│         │
 │   │                    │ Write 02-gates.diff│                  │         │
 │   │                    ├──────────────────────────────────────▶│         │
 │   │                    │ Write learning.json│                  │         │
 │   │                    ├──────────────────────────────────────▶│         │
 │   │                    │                    │                  │         │
 │   │                    │ helios_learning_done(path)            │         │
 │   │                    ├───────────────────▶│ validate + index │         │
 │   │                    │                    │  status=ready    │         │
 │   │                    │◀───────────────────┤ {steps, warnings}│         │
 │   │                    │                    │                  │         │
 │   │                 turn ends               │ SSE learnings_changed       │
 │   │                 session may be closed   │                  │         │
 └──────────────────────────────────────────────────────────────────────────┘

 ┌── PHASE 2 ─ you read it, whenever ──────────────────────────────────────┐
 │                                                                          │
 │  YOU                HELIOS                 DAEMON              DISK      │
 │   │                    │                      │                  │       │
 │   │ open Learnings     │ GET /api/learnings   │                  │       │
 │   ├───────────────────▶├─────────────────────▶│ SELECT (index)   │       │
 │   │◀───────────────────┤ title · time · status│                  │       │
 │   │                    │                      │                  │       │
 │   │ click one          │ GET /api/learnings/{path}               │       │
 │   ├───────────────────▶├─────────────────────▶│ read learning.json      │
 │   │                    │                      ├─────────────────▶│       │
 │   │                    │                      │ + each slot file │       │
 │   │◀───────────────────┤ steps, rendered      │◀─────────────────┤       │
 │   │                    │                      │                  │       │
 │   ├─ ▸ next ──────────▶│ client-side only. no daemon, no agent.  │       │
 │   ├─ ◂ prev ──────────▶│ the files are already here.             │       │
 │                                                                          │
 │   no agent exists. none is needed. nothing is running.                   │
 └──────────────────────────────────────────────────────────────────────────┘

 ┌── PHASE 3 ─ extending it, possibly months later ────────────────────────┐
 │                                                                          │
 │  YOU                HELIOS              ANY CLAUDE CODE        DISK      │
 │   │                    │                      │                  │       │
 │   │ click ↓ deeper     │                      │                  │       │
 │   ├───────────────────▶│                      │                  │       │
 │   │◀───────────────────┤ copies a prompt to the clipboard        │       │
 │   │                    │  "Continue Helios learning <path> —     │       │
 │   │                    │   more depth on step 3."                │       │
 │   │                    │                      │                  │       │
 │   │ paste it anywhere — this repo, another, your phone           │       │
 │   ├─────────────────────────────────────────▶│                  │       │
 │   │                    │  helios_learning_get │                  │       │
 │   │                    │◀─────────────────────┤ reads what exists│       │
 │   │                    │                      │ Write 06-…md     │       │
 │   │                    │                      ├─────────────────▶│       │
 │   │                    │                      │ rewrite learning.json    │
 │   │                    │                      ├─────────────────▶│       │
 │   │                    │  helios_learning_done│                  │       │
 │   │                    │◀─────────────────────┤                  │       │
 │   │◀── SSE ────────────┤                      │                  │       │
 └──────────────────────────────────────────────────────────────────────────┘
```

The clipboard hop in phase 3 is deliberate. Helios cannot send a prompt without
knowing which agent to send it to, and knowing that is exactly the coupling this
design removes. Handing you text you paste wherever you like keeps Helios
read-only and works when the original builder is long gone.

## On disk

```
~/.helios/learnings/2026-08-19-pr-6813-software-statements/
  learning.json          the only file Helios parses
  01-the-idea.md
  02-constraint.md
  03-registration.diff
  04-gates.md
  05-ssrf.diff
```

Folder name is dated and slugged from the title: readable, sortable, and
something you can `git add` as a unit. The export-to-a-committable-file idea
that spec 39 listed as a non-goal is now simply what a learning is.

### learning.json

```jsonc
{
  "title": "#6813 RFC 7591 software statements",
  "steps": [
    { "layout": "single", "caption": "the idea",
      "slots": [{ "type": "markdown", "file": "01-the-idea.md" }] },

    { "layout": "compare", "caption": "where it is mounted, and why",
      "slots": [
        { "type": "markdown", "file": "02-constraint.md", "label": "the constraint" },
        { "type": "diff", "file": "03-registration.diff",
          "label": "internal/oauth/registration.go" }] },

    { "layout": "stack", "caption": "four gates",
      "slots": [
        { "type": "markdown", "file": "04-gates.md" },
        { "type": "diff", "file": "05-ssrf.diff" }] }
  ]
}
```

Slots carry `file`, never inline content. Two content types:

| type | file | rendered by |
|------|------|-------------|
| `markdown` | `.md` | `desktop/src/renderer/markdown.ts` |
| `diff` | `.diff` | `desktop/src/renderer/components/diff-view.tsx` |

There is no `code` type. A fenced block inside a `.md` covers it, and dropping
it removes a renderer, a schema branch, and the syntax-highlight path.

Three layouts: `single`, `compare`, `stack`. **An unrecognised layout is a
validation warning, not a silent fallback.** Spec 39's renderer degraded
`stack` to `single` without saying so, which is indistinguishable from the
layout being ignored.

## SQLite is an index, not a store

```sql
CREATE TABLE IF NOT EXISTS learnings (
    path       TEXT PRIMARY KEY,
    title      TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'building',
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
```

Four columns, and the reasoning is not brevity: **anything copied out of
`learning.json` can drift out of sync with it.** Step count, repo name and
layout all live in the file and are read from the file. The index holds only
what the database uniquely provides — ordering, status, and a list without
touching the disk.

The path is the identity; there is no separate id. Renaming a folder therefore
re-keys the learning, which is honest — it is a different artifact.

Dropping the table loses the list, not the learnings. A rescan of
`~/.helios/learnings/` rebuilds it.

## Tools

```
helios_learning_new(title)        → { path }        creates the folder, status=building
helios_learning_done(path)        → { steps, warnings[] }   validates, indexes, notifies
helios_learnings(filter?)         → the library
helios_learning_get(path)         → title, steps, and the file list
```

Four tools, none of which carry content. The agent writes every file with its
own `Write` tool, which it does better than it would fill a JSON payload, and no
markdown or diff ever passes through MCP.

`helios_learning_get` exists so an agent that did not build a learning can
extend one — phase 3 above.

### Validation happens at `done`

`done` is load-bearing. Until it is called, `learning.json` may reference files
that are not written yet, so `building` keeps a half-finished learning out of
the library and gives the UI something honest to show if an agent dies mid-write.

`done` checks and reports, rather than refusing:

- every `file` exists
- every `file` resolves **inside the learning folder** — the agent writes
  `learning.json`, so a slot naming `../../../.ssh/id_rsa` must be refused.
  Containment did not disappear with `cwd`; its anchor moved from the repository
  to the learning folder.
- `layout` and `type` are recognised
- at least one step

Warnings come back in the tool result so the agent can fix them without the
reader being involved. Only a missing or unparseable `learning.json` is a hard
failure.

## UI

Learnings are a sibling of sessions in the sidebar, not a panel inside one. The
Learn tab from spec 39 is removed and `PANELS` returns to five.

```
┌ SESSIONS ─────────────┐  ┌ #6813 · RFC 7591 software statements ─────────┐
│ ● opal-app        2m  │  │                           ◂  3 / 9  ▸         │
│ ● helios         14m  │  │  WHERE IT IS MOUNTED, AND WHY                 │
│                       │  │  ┌───────────────┐┌───────────────────────────┐│
├ LEARNINGS ────────────┤  │  │ MCP is        ││ @@ -186,6 +186,12 @@      ││
│ #6813 software st… 2m │  │  │ unauthenticat…││ +  stmt, err := unwrap(…) ││
│ the deck store     1h │  │  └───────────────┘└───────────────────────────┘│
│ how auth works     3d │  │                                               │
│ ⋯ mobile push  building│ │                          [↓ deeper]  [? huh]  │
└───────────────────────┘  └───────────────────────────────────────────────┘
```

Selecting a learning replaces the session detail view entirely — no tab strip,
no terminal, no approvals. A learning has none of those.

The empty state teaches rather than launches, since Helios cannot create one:

```
┌ LEARNINGS ─────────────────────────────────────────────┐
│              No learnings yet                          │
│                                                         │
│   Ask any Claude Code session to build one:             │
│     "walk me through this PR as a Helios learning"      │
│                                                         │
│   Requires the Helios MCP server.    ✓ registered       │
└─────────────────────────────────────────────────────────┘
```

Showing MCP registration state here matters: without it nothing will ever
appear, and an empty list would otherwise look like a bug.

## Changes

| File | Change |
|------|--------|
| `internal/store/decks.go` | **delete**, replaced by `learnings.go` |
| `internal/store/learnings.go` | new. four-column index: upsert, list, get, delete |
| `internal/store/store.go` | drop the `decks` table, add `learnings` |
| `internal/store/sessions.go` | drop the deck cleanup from `DeleteSession` — learnings outlive sessions |
| `internal/mcp/resolve.go` | **delete** most of it. `safeJoin` survives, re-anchored to the learning folder; `findSymbol`, `resolveBase`, `readLines`, `runGit` all go |
| `internal/mcp/tools.go` | four learning tools; `helios_sessions` no longer needed for this |
| `internal/learning/` | new. folder creation, slug, `learning.json` parse and validate |
| `internal/server/decks.go` | **delete**, replaced by `learnings.go` |
| `internal/server/learnings.go` | new. `GET /api/learnings`, `GET /api/learnings/{path}`; `learnings_changed` broadcast |
| `desktop/.../learn.tsx` | rework: reader, no launcher, no prompt injection |
| `desktop/.../slots.tsx` | drop `code`, add `diff` via `diff-view.tsx`, add `stack` |
| `desktop/.../detail.tsx` | remove `'learn'` from `PANELS` |
| `desktop/.../sidebar.tsx` | Learnings section |
| `desktop/src/shared/models.ts` | `Deck`/`DeckStep`/`DeckSlot` → `Learning`/`LearningStep`/`LearningSlot` |

## Testing

- `internal/learning`: `learning.json` parse; missing file reported; a slot
  escaping the folder refused; unknown layout and unknown type warned rather
  than dropped; slug collision.
- `internal/store/learnings.go`: upsert by path, list ordering, `building`
  excluded from the default list, rescan rebuilds the index.
- `internal/mcp`: `new` creates a folder and returns its path; `done` on a
  folder with a dangling `file` reports a warning and still indexes; `get`
  returns enough for another agent to extend.
- `internal/server`: list and detail endpoints; `learnings_changed` broadcast
  once per `done`; MCP still internal-only.
- Desktop: `compare` and `stack` render two slots; a diff slot renders through
  `diff-view.tsx`; spine navigation works with no agent and no daemon writes.
- End to end: create a folder by hand, call `done`, assert it appears in the
  list and renders.

## Implementation order

1. `internal/learning` — folder, slug, parse, validate. Pure, testable alone.
2. `internal/store/learnings.go` + schema; delete `decks.go`.
3. `internal/mcp` — four tools; delete `resolve.go` down to `safeJoin`.
4. `internal/server/learnings.go` + `learnings_changed`.
5. Desktop: sidebar section and reader; remove the Learn tab.
6. `slots.tsx`: `diff` and `stack`; drop `code`.
7. The skill teaching an agent when and how to build one.

Steps 1–4 are verifiable with curl and a hand-written folder before any UI
exists.

## Open

**Does Helios watch the folder, or wait for `done`?** Watching gives
progressive render as the agent writes, at the cost of flicker on partial
writes and a lost completion signal. Waiting is simpler and was worth less once
the agent stopped holding a session open. Leaning: wait for `done`, revisit if
building a large learning feels dead.

**Is a learning deletable from Helios?** It is a write, and the only destructive
one. Without it the library only grows. Leaning: yes, deleting the folder, with
a confirmation — but it is the single exception to "Helios never writes" and
should be called out as such rather than slipped in.

**Should `helios_learnings` be readable by agents at all,** or only by the UI?
An agent that can list learnings can notice one already exists and extend it
instead of duplicating. That seems useful and costs nothing.
