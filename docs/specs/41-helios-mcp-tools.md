# Helios MCP Tools: Show the Human, Reach Other Sessions

Replaces the deck model in `39-agent-driven-explain-ui.md` and the learning
model in `40-learnings.md`. Both are withdrawn. The spike results in 39 are
still correct and this spec depends on them.

## Problem

An agent explains code in prose. Helios already shows code, diffs, terminals and
transcripts. The agent cannot reach any of it.

Two earlier designs tried to fix this by storing what the agent said. A deck
belonged to a session and died with it. A learning was a folder of files an
agent wrote. Both stored an artifact. Neither was worth keeping, because the
value is in the moment: the agent wants you to look at one thing, now.

So Helios does not store an explanation. It shows a view.

## The dividing line

Two mechanisms exist, and one rule separates them.

**A hook covers what the agent already emits.** Helios receives every tool call
and every question. It does not need a tool to learn about them.

**An MCP tool covers what no hook can infer.** The agent must state an intent
that its own tool calls do not carry.

This rule removed three tools during design:

| Rejected tool | Why |
|---|---|
| `helios_ask` | `handleQuestion` at `internal/provider/claude/hooks.go:283` already routes `AskUserQuestion` to the desktop and the phone. A blocking MCP call also cannot wait for a person, because Claude Code stops a tool call at about 60 seconds. |
| a tool to report the current file | `handleToolPre` at `hooks.go:986` receives `ToolName` and `ToolInput`. Helios can read `file_path` from a `Read` call. |
| a tool to answer another agent's prompt | It bypasses the human on the path Helios exists to protect. |

The rule does **not** remove `helios_show`. Reading a file and pointing at a
file are different intentions. The agent read `registration.go` twenty calls
ago. It wants to point at it now. A hook fires at the wrong moment, and it
cannot tell "I need this" from "look at this".

## Tool surface

Four tools.

```
helios_show(view, path?, line?, base?, note?)
helios_sessions(filter?)
helios_session_create(cwd, prompt, provider?, model?)
helios_session_send(session, text)
```

`helios_show` acts on the caller's own session. The other three act on other
sessions, which is why they need MCP at all.

### helios_show

```jsonc
{
  "view": "file" | "diff" | "terminal" | "agent",
  "path": "/Users/me/src/opal-app/internal/oauth/registration.go",
                                    // absolute. file: required, diff: optional.
  "line": 190,                      // file only
  "base": "main",                   // diff only
  "note": "This guard is what returned the 400."
}
```

Four views. `diff` with no `path` shows the whole change, so a separate `git`
view is not needed. `file` already opens the Files panel, so a `files` view is
not needed either.

**Paths are absolute.** `resolveSafePath` at `internal/server/files.go:134`
resolves a relative path with `filepath.Abs`, against the *daemon's* working
directory, which is `/`. A repo-relative path would therefore become one that
does not exist, and the failure would look like a missing file rather than a
bad argument. Absolute is also the form the agent already holds: its own `Read`
and `Edit` calls carry absolute paths, and the transcript's file chips pass
absolute paths to the same panel. A relative path is refused with a correction.
`~/` is accepted, because the daemon expands it.

`approvals` is deliberately absent. Helios already raises a notification on the
desktop and the phone when an agent waits for permission. An agent that can pull
your attention to its own approval is a small push toward being approved.

`note` is one line that says why. Without it you see a file and must guess what
you are looking at.

**Validation returns a correction, not a failure.** `view=file` with no `path`
answers `"view=file needs a path"`. The agent fixes the call itself. This is the
one real cost of a single tool with conditional fields, and this pattern pays
it. The deck push used the same approach and it worked.

**The result reports whether anyone saw it.**

```jsonc
{ "shown": true, "clients": 2 }
{ "shown": false, "reason": "no client attached" }
```

This matters. With no desktop and no phone attached, `helios_show` does nothing.
The agent must learn that and write prose instead.

### The other three

`helios_sessions` lists sessions so an agent can address one. It omits
terminated and archived sessions unless `all` is true. A long-lived install
holds hundreds of dead sessions and listing them buries the live ones.

`helios_session_create` and `helios_session_send` change real state. An agent
can spawn an agent, and can type into a session you are watching. Both sit
behind a setting in `internal/store/settings.go` and stay off until you turn
them on.

## Identity

`helios_show` acts on the caller's own panel. Only a session Helios spawned has
a panel. So Helios injects the session id when it starts the session.

```
sessionArgs() at internal/provider/claude/register.go:122 appends:
  --mcp-config '{"mcpServers":{"helios":{"type":"http",
      "url":"http://127.0.0.1:<internal-port>/mcp",
      "headers":{"X-Helios-Session":"<session-id>"}}}}'
```

The spike in spec 39 proved that Claude Code sends these headers.

Two properties of the flag were measured on 2026-08-24, because both could
break a user's setup.

| Test | Result |
|---|---|
| Inline JSON as the argument value | Works. A file path is not needed. |
| `--mcp-config` on its own | **Merges** with the user's config. `chrome-devtools` and other user-scope servers stay available. |
| `--mcp-config --strict-mcp-config` | **Replaces** the user's config. Only the injected server remains. |

**Helios must never pass `--strict-mcp-config`.** Adding it would look tidy and
would silently remove every other MCP server from every Helios session. The
merge behaviour is what makes this injection safe.

No tool takes a session id for the caller's own session. The agent never learns
its own id. `helios_session_send` takes one, because it addresses another
session on purpose.

An earlier design rejected header injection. The reason was that it only works
for sessions Helios spawns. That reason no longer applies, because `helios_show`
is meaningless for a session that has no panel.

The header is identity, not authorization. `/mcp` is unauthenticated and binds
loopback only, on the internal listener at `internal/server/server.go:152`. Any
local process can forge the header. The blast radius is a switched tab.

## Interaction

```
 ┌── the agent points at something ────────────────────────────────────────┐
 │                                                                          │
 │  AGENT                    DAEMON                    DESKTOP / PHONE      │
 │    │                         │                            │              │
 │    │ helios_show(            │                            │              │
 │    │   view:"file",          │                            │              │
 │    │   path:"…registration.go",                           │              │
 │    │   line:190,             │                            │              │
 │    │   note:"the 400")       │                            │              │
 │    ├────────────────────────▶│                            │              │
 │    │   X-Helios-Session: s1  │ resolve session from header│              │
 │    │                         │ count attached clients     │              │
 │    │                         │                            │              │
 │    │                         │ SSE show {session, view,   │              │
 │    │                         │           path, line, note}│              │
 │    │                         ├───────────────────────────▶│              │
 │    │◀────────────────────────┤ {shown:true, clients:2}    │              │
 │    │                         │                            │ switch panel │
 │    │ continues working       │                            │ open file    │
 │    │                         │                            │ show note    │
 └──────────────────────────────────────────────────────────────────────────┘

 ┌── nobody is watching ───────────────────────────────────────────────────┐
 │    │ helios_show(…)          │                                          │
 │    ├────────────────────────▶│ no client attached                       │
 │    │◀────────────────────────┤ {shown:false,                            │
 │    │                         │  reason:"no client attached"}            │
 │    │ writes prose instead    │                                          │
 └──────────────────────────────────────────────────────────────────────────┘

 ┌── reaching another session ─────────────────────────────────────────────┐
 │    │ helios_sessions()       │                                          │
 │    ├────────────────────────▶│ SELECT, live sessions only               │
 │    │◀────────────────────────┤ [{session, project, cwd, status, title}] │
 │    │                         │                                          │
 │    │ helios_session_send(s2, │                                          │
 │    │   "rebase onto main")   │ setting off → refused                    │
 │    ├────────────────────────▶│ setting on  → existing send path         │
 │    │                         ├───────────────────────▶ session s2       │
 └──────────────────────────────────────────────────────────────────────────┘
```

Helios never sends a prompt on its own. It only shows views and relays what an
agent asked for.

## Desktop

Most of this exists.

| Need | Already there |
|---|---|
| Open a file in a panel | `store.ts:515` `openFile(hostId, path)`; it switches to the Files panel |
| Switch a panel | `store.ts:510` `setPanel(panel)` |
| Receive daemon events | `store.ts:391` `onServerEvent` |
| Render a diff | `components/diff-view.tsx`, `components/git.tsx` |

Three additions:

- `FileTarget` at `store.ts:67` gains `line`, so the panel can scroll to it.
- A `diffTarget`, matching `fileTarget`, so the Git panel can open one file's
  diff.
- A note strip. It shows `note` above the view and says which agent asked. It
  clears on the next manual navigation.

The note strip is what separates a deliberate show from a normal panel switch.
Without it the view moves and nothing says why.

## What this deletes

The deck code from spec 39 is built and must come out.

| Delete | Reason |
|---|---|
| `internal/store/decks.go` and its test | no stored artifact |
| `internal/server/decks.go` and its test | keep only `TestMCPIsInternalOnly`, moved |
| `internal/mcp/resolve.go` | the daemon resolves nothing now |
| `desktop/.../learn.tsx`, `slots.tsx` | no reader |
| `decks` table; `'learn'` in `PANELS`; `Deck*` types; Learn CSS | — |

`internal/mcp/server.go` stays. The protocol layer never depended on decks.

## Risks

- **`helios_show` moves the view under you.** That is the purpose and the
  failure mode. No throttle at first. The tool description says to use it
  rarely. If it becomes noise, add a per-session mute, not a rate limit.
- **Helios must not raise its window.** Switching a tab inside the app is
  acceptable. Taking focus from another application is not.
- **`helios_session_send` lets an agent drive an agent.** Setting-gated, off by
  default.
- **A forged `X-Helios-Session` header shows a view in another session.** Local
  processes only. Accepted.
- **A future change adds `--strict-mcp-config`.** It would strip every other MCP
  server from every Helios session. A test must assert that `sessionArgs` does
  not contain the flag.

## Testing

- `internal/mcp`: each view validates; `view=file` with no path returns the
  correction; an unknown view is refused; the header resolves to a session; a
  missing header is refused.
- `internal/mcp`: `shown:false` when no client is attached.
- `internal/mcp`: `session_send` and `session_create` refuse while the setting
  is off.
- `internal/server`: one `show` broadcast per call; `/mcp` stays off the public
  mux.
- Desktop: a `show` event switches the panel, opens the file at the line, and
  renders the note; the note clears on manual navigation.
- End to end: call `helios_show` by curl against a live session and confirm the
  desktop moves.

## Implementation order

1. Delete the deck code. Keep `internal/mcp/server.go` and the internal-only
   test.
2. Add `helios_show` to `internal/mcp`, with validation and the client count.
3. Broadcast `show` over SSE. Handle it in `store.ts`.
4. Add `line` to `FileTarget`. Add `diffTarget`. Add the note strip.
5. Inject `--mcp-config` in `sessionArgs`.
6. Add `helios_sessions`. Add the two gated session tools.

Steps 1 to 4 give a working demo. Step 5 removes the manual registration step.

## Open

**Does a show reach every attached client, or the desktop only?** I suggest
every client. Panel state belongs to the session, not to a client, so this needs
no new concept.

**Is follow mode still worth building?** A hook can open whatever file the agent
reads. `helios_show` now covers the deliberate case, and the session status line
already half answers "what is it doing". Follow may not earn its cost.

**Does `helios_show` need a `view` for the transcript position?** `view=agent`
switches to the transcript, but it cannot point at a message. Scrolling to one
would need a message id that the agent does not have.
