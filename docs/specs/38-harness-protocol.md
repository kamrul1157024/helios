# 38 — Harness Protocol

## Problem

Helios manages Claude Code. It says it manages "providers" (`docs/specs/18-provider-interface.md`), but a provider is a set of Go
functions compiled into the daemon, and the daemon knows Claude's shapes
everywhere: it writes Claude's hook config, parses Claude's JSONL transcripts,
validates Claude's permission-mode vocabulary, scrapes Claude's trust dialog
off the screen, and spawns `claude --resume` by name from the terminal host.

Supporting a second agent — Codex is the immediate one — is not a matter of
registering another provider. Every one of those couplings has to be answered
again, in Go, inside the daemon, by someone who can rebuild it.

Codex also cannot be integrated the way Claude was. Claude Code supports
`type: "http"` hooks; Codex supports **only** `type: "command"`
(https://learn.chatgpt.com/docs/hooks). The event vocabulary is nearly
identical — `SessionStart`, `PreToolUse`, `PermissionRequest`, `PostToolUse`,
`UserPromptSubmit`, `PreCompact`, `PostCompact`, `Stop`, `SubagentStart`,
`SubagentStop` — but the transport is not, and neither will the next agent's
be.

This spec defines a **harness**: a separate process that owns one agent
session and speaks one protocol to helios over a unix socket. Anyone can write
one, in any language, against a published SDK, without touching the daemon and
without knowing anything about helios's internals.

## Non-goals

- **No networking.** Unix socket, same machine, spawned by helios. No TCP, no
  tokens, no TLS, no discovery over the network. Helios runs on the machine
  the harness runs on. The socket's `0600` permission is the whole security
  model, exactly as it is for `helios ptyhost` today.
- **No load balancing, no scheduling, no placement.** One harness process
  serves exactly one session.
- **No plugin ABI.** Harnesses are processes, not shared libraries. The
  coupling is the wire protocol and nothing else.
- **No remote harnesses.** `docs/specs/multi-host-spec.md` may want this
  later; the protocol does not forbid it, but nothing here implements it.

## Current state

Everything a harness will own, and where it lives today.

| Concern | Today | Claude-shaped? |
|---|---|---|
| Hook config install | `internal/daemon/hooks.go:14` `hookConfig` writes `~/.claude/settings.json` | Entirely |
| Hook ingress | `internal/server/hooks.go:15` `handleHook`, routed at `internal/server/server.go:100` | No — generic dispatch by path |
| Hook semantics | `internal/provider/claude/hooks.go`, 16 handlers, 1297 lines | Entirely |
| Decision actions | `internal/provider/claude/actions.go`, 5 handlers | Entirely |
| Transcript parsing | `internal/transcript/reader.go:75` `ParseClaudeTranscript` | Entirely |
| Session discovery | `internal/discovery/claude.go:31` `DiscoverClaudeSessions` | Entirely |
| Title generation | `internal/provider/claude/autotitle.go:195` | Mostly (uses `claude -p --model haiku`) |
| Error classification | `internal/provider/claude/apierror.go:40,100` | Entirely |
| Trust-dialog watcher | `internal/server/trust_watcher.go:122` — regex over the screen | Entirely |
| Launch/resume argv | `internal/provider/claude/register.go:88,122`; `cmd/helios/ptyhost.go:78` | Entirely |
| Permission modes | `register.go:58` `PermissionModes`; validated at `internal/server/api.go:613` | Entirely |
| PTY host | `internal/terminal/*`, `cmd/helios/ptyhost.go:28` | No |
| Warm pool | `internal/terminal/registry.go` | No |
| Notifications, store, SSE, mobile API | `internal/notifications`, `internal/store`, `internal/server` | No |
| HITL overlay | `internal/hitl/hitl.go:95`, `internal/terminal/overlay.go` | No |

The last four rows are the ones worth keeping. Everything above them is a
translation layer that happens to be compiled in.

## Design

### The split

```
  ┌──────────────────────────── helios daemon ────────────────────────────┐
  │                                                                       │
  │  fleet          Registry, warm pool, eviction, spawn, sidecars        │
  │  render         views → terminal overlay, notifications, agents tab   │
  │  state          sessions + notifications in SQLite                    │
  │  callbacks      one activation per view, whichever surface acted      │
  │  ingress        POST /hooks/{...} — a router, not a translator        │
  │                                                                       │
  └───────────────────────────────┬───────────────────────────────────────┘
                                  │  unix socket, one per session
                                  │  frames (§Transport)
  ┌───────────────────────────────┴───────────────────────────────────────┐
  │                            harness process                            │
  │                                                                       │
  │  agent          spawn, resume, interrupt, stop                        │
  │  meaning        what a tool is, what needs asking, what the answer did│
  │  context        transcript, compaction, memory — entirely its own     │
  │  terminal       PTY, if it has one at all                             │
  │                                                                       │
  └───────────────────────────────────────────────────────────────────────┘
```

The line is: **helios owns the fleet and every human-facing surface; the
harness owns one agent and everything that agent means.** Helios renders what
it is given and reports what the human did. It never learns what a tool is,
which tools are dangerous, or how a transcript is stored — those are the
couplings that made Claude special, and each one is a reason someone would
have to patch the daemon to ship a harness.

### Three harness shapes

```
  PTY harness (claude, codex)          headless harness (SDK/API agents)
  ─────────────────────────────        ─────────────────────────────────
   helios ──socket──> harness           helios ──socket──> harness
                        │                                    │
                        │ frames:                            │ frames:
                        │   control                          │   control only
                        │   output/input/resize              │
                        │   overlay                          │
                        ▼                                    ▼
                   PTY ─> claude CLI                   HTTPS ─> model API
                                                       (no terminal ever)

  wrapped session (`helios wrap -- claude`)
  ─────────────────────────────────────────
   user's own terminal ─> claude CLI ─> global hooks ─> daemon
   no harness process; daemon falls back to the built-in translator
```

A headless harness declares `terminal: false` in its descriptor. Helios then
hides the terminal tab, refuses `helios attach` with a clear message, and
falls back to notification-only HITL. Nothing else changes: the same events,
the same questions, the same notifications on the same phone.

## Transport

### Socket

One unix socket per session, path chosen by helios and passed to the harness
at spawn. Reuses the existing layout (`internal/terminal/paths.go`):

```
~/.helios/run/<digest>.sock      the socket
~/.helios/run/<digest>.json      sidecar: pid, child pid, cwd, protocol
```

`<digest>` is a short hash of the session ID, not the ID itself: macOS caps
`sun_path` at 104 bytes and a UUID under a long home directory exceeds it
(`internal/terminal/paths.go:16`).

Helios dials. The harness listens. Exactly one control connection is honoured
at a time — the daemon's — alongside any number of viewer connections.

### Framing

The existing terminal frame protocol (`internal/terminal/protocol.go`),
unchanged, plus one frame type:

```
  ┌────────────┬──────┬──────────────────────────┐
  │ uint32 len │ type │ payload (len-1 bytes)    │
  └────────────┴──────┴──────────────────────────┘
    big-endian   uint8
```

| Type | Name | Direction | Required |
|---|---|---|---|
| `0x01` | hello | viewer → harness | terminal only |
| `0x02` | snapshot | harness → viewer | terminal only |
| `0x03` | output | harness → viewer | terminal only |
| `0x04` | input | viewer → harness | terminal only |
| `0x05` | resize | viewer → harness | terminal only |
| `0x06` | status | harness → viewer | terminal only |
| `0x07` | exit | harness → viewer | terminal only |
| `0x08` / `0x09` | ping / pong | both | yes |
| `0x0a`–`0x0c` | overlay set / clear / input | both | overlay only |
| `0x0d` | paste | control → harness | terminal only |
| **`0x0e`** | **control** | **both** | **yes** |

A headless harness implements `0x08`, `0x09`, and `0x0e`. That is three frame
types, and the SDK implements all three for it.

Keeping one framing for both shapes is deliberate. The alternative — NDJSON
for headless, frames for PTY — doubles the SDK surface and the daemon's
connection handling to save twenty lines of length-prefix parsing that no
harness author ever sees.

### Control envelope

`0x0e` payloads are JSON, JSON-RPC 2.0 in shape, symmetric — either side may
call:

```jsonc
// request
{"id": 17, "method": "session.prompt", "params": {"text": "fix the test"}}

// response
{"id": 17, "result": {"accepted": true}}
{"id": 17, "error": {"code": "unsupported", "message": "no prompt queue"}}

// notification (no id, no reply)
{"method": "state", "params": {"status": "active"}}
```

`id` is unique per originator. No batching. Requests are answered out of
order; both sides must handle interleaving, because a permission question can
sit unanswered for minutes while ten events flow past it.

Error codes are a closed set: `unsupported`, `invalid`, `busy`, `timeout`,
`internal`.

## Handshake

The harness sends `hello` as its first control frame. Nothing else may precede
it.

```jsonc
{"method": "hello", "params": {
  "protocol": 1,
  "session_id": "80fc19d3-...",
  "descriptor": { /* §Descriptor */ },
  "resume_from": 412        // last seq helios acked, 0 for a fresh process
}}
```

Helios replies with a `hello` of its own carrying its protocol version and the
last sequence number it durably recorded, so the harness knows where to
resume its event stream (§Reconnection).

### Descriptor

The same object `<harness> describe` prints (§Discovery). Everything helios
needs to render a provider without a line of Go:

```jsonc
{
  "id": "claude",
  "name": "Claude Code",
  "icon": "terminal",
  "protocol": 1,

  "capabilities": {
    "terminal": true,        // PTY frames; false = headless, no terminal tab
    "overlay": true,         // in-terminal HITL; false = notification-only
    "prompt_queue": true,    // accepts a prompt mid-turn
    "interrupt": true,
    "resume": true,          // survives eviction and comes back with context
    "transcript": true,      // serves transcript.read / pushes transcript.append
    "configurable": ["model", "permission_mode"],
    "title": false           // harness names sessions itself; false = helios does
  },

  "models": [
    {"id": "opus", "name": "Opus", "description": "Most capable"},
    {"id": "sonnet", "name": "Sonnet", "description": "Fast and capable"}
  ],

  "permission_modes": ["plan", "manual", "acceptEdits", "auto",
                       "dontAsk", "bypassPermissions"],
  "default_permission_mode": "auto",

  "commands": [
    {"name": "/compact", "description": "Compact context", "icon": "compress"}
  ],

  "event_types": [
    {"type": "tool_pre", "label": "Tool Started", "category": "tools",
     "description": "A tool is about to run"}
  ]
}
```

`models`, `permission_modes`, `commands`, and `event_types` replace
`provider.RegisterProvider`, `RegisterCommands`, and `RegisterEventTypes`
(`internal/provider/registry.go:157,87,222`). They become data, served at
runtime, versioned with the harness rather than with helios.

An absent capability is `false`. Helios must degrade, never fail, on a missing
one.

## Methods

### Helios → harness

| Method | Params | Result | Capability |
|---|---|---|---|
| `describe` | — | Descriptor | — |
| `session.start` | `{cwd, prompt?, model?, permission_mode?, skip_permissions?}` | `{}` | — |
| `session.prompt` | `{text, queue?}` | `{accepted, queued}` | — |
| `session.interrupt` | — | `{}` | `interrupt` |
| `session.stop` | `{reason}` | `{}` | — |
| `session.set` | `{model?, permission_mode?}` | `{applied, requires_restart}` | `configurable` |
| `transcript.read` | `{cursor?, limit}` | `{entries[], cursor}` | `transcript` |
| `command.run` | `{command}` | `{}` | `commands` non-empty |
| `view.action` | `{view_id, action_id, values, surface}` | `{dismiss?, view?}` | a human activated an action |
| `agent.hook` | `{event, payload}` (opaque) | `{response}` (opaque) | hooks (§Ingress) |

`session.start` is called once, immediately after the handshake. A harness
resumed from eviction gets it with no `prompt` — restoring context from
whatever it stored is its own business.

`session.prompt` is the whole reason this protocol beats typing into a PTY.
Today `Mirror.SendText` writes bytes and hopes; `docs/specs/37` is an entire
spec about the ways that fails. A harness answers `{accepted: true}` when the
agent has the text, and helios stops guessing.

### Harness → helios

| Method | Params | Reply | Notes |
|---|---|---|---|
| `hello` | `{protocol, session_id, descriptor, resume_from}` | `hello` | first frame |
| `state` | `{status, detail?}` | — | notification |
| `event` | `{seq, type, label, view?}` | — | notification |
| `view.present` | `{seq, view_id, view, modal?, surfaces?}` | — | put a view in front of the human |
| `view.update` | `{view_id, view}` | — | replace a live view in place |
| `view.dismiss` | `{view_id, reason?}` | — | take it down everywhere |
| `transcript.append` | `{seq, entry}` | — | notification |
| `title` | `{title}` | — | notification, `title` capability |
| `log` | `{level, message}` | — | notification, goes to the daemon log |

There is no request/response question here. The harness **presents** a view;
helios renders it and calls back on activation; the harness decides what that
means and when the view comes down. Whether anything inside the harness is
blocked waiting on it is invisible to helios (§View lifecycle).

### Status vocabulary

Closed. A harness maps its agent's states onto these; helios renders exactly
these and nothing else.

| Status | Meaning | Today's equivalent |
|---|---|---|
| `starting` | process up, agent not ready for input | the gap `docs/specs/37` Defect 1 describes |
| `active` | agent is working | `UserPromptSubmit` → `"active"` |
| `waiting_input` | agent is at its prompt, idle | `Stop` / `idle_prompt` → `"idle"` |
| `waiting_human` | agent cannot proceed until someone acts | `"waiting_permission"` |
| `error` | turn failed | `StopFailure` → `"error"` |
| `ended` | agent exited | `SessionEnd` → `"terminated"` |

`starting` is new and load-bearing: it is exactly the readiness signal
`internal/server/api.go` currently reconstructs from `SignalAgentReady`, and
it lets the daemon stop conflating "socket exists" with "agent can read".

`waiting_permission` is renamed `waiting_human` because "permission" is a
Claude noun. Helios needs the badge — a session nobody is looking at that
cannot proceed is the single most important thing to surface — but it does not
need to know that what it is waiting for is an approval rather than a
password, a code review, or a coin flip. The harness sets it and clears it.

## Tools

Helios has no model of a tool. It is worth stating flatly, because the entire
Claude integration is built on the opposite assumption.

A harness has tools. Tools may have pre- and post-hooks. A tool may run
silently, may narrate itself with `event`, may write to the transcript, or may
stop and put a view in front of a human before it proceeds. Which tools do
which, how hooks are attached, whether hooks are HTTP or command or in-process
function calls, what a hook is allowed to veto — **none of that reaches
helios.** The harness receives its own hook payloads (§Ingress is only a pipe
for the ones that arrive over HTTP), decides something needs a human, and
presents a view.

When the human acts, helios calls `view.action` back into the harness. What
the harness does with it is equally invisible: release the blocked pre-hook,
rewrite the tool's arguments, run the tool and then present a second view from
the post-hook, write a permission rule to disk, or nothing at all. Helios sees
a callback returning `{dismiss: true}` and takes the card down.

This is the whole point. A harness whose tools have a lifecycle helios has
never heard of works today, on a phone that shipped last year.

## View

## View

The one thing helios must not own is what a harness's agent *means*. There is
no `approval` type here, no `permission`, no tool vocabulary, no notion that
`Bash` is dangerous and `Read` is not. A harness that invents a concept helios
has never heard of must be able to put it in front of a human without asking
for a helios release.

So the protocol carries **views**: a minimal, harness-authored description of
what to render. Helios is a renderer and an input collector. It knows widgets;
it does not know meanings.

```
                       harness supplies one view
                                  │
            ┌─────────────────────┼─────────────────────┐
            ▼                     ▼                     ▼
     terminal overlay        notification            agents tab
     (hitl.go)               (push / tray)           (mobile, desktop)
     ────────────────        ─────────────           ──────────────────
     text, code, list        title + summary         everything
     ↑↓ select, enter        + up to 3 actions       full fields, scroll
     esc cancel

                  same view, three fidelities, one callback
```

### Shape

```jsonc
{
  "title": "Bash",                       // required, one line
  "summary": "rm -rf ./build",           // optional, one line, for notifications
  "blocks":  [ /* display */ ],
  "fields":  [ /* input, optional */ ],
  "actions": [ /* buttons, optional */ ]
}
```

### Lifecycle

A view is not a question with an answer. It is a thing that is on screen until
the harness says otherwise, and actions on it are **callbacks into the
harness**.

```
  harness                    helios                        human
     │                          │                            │
     │ view.present             │                            │
     │  {view_id: "v7", modal}  │                            │
     ├─────────────────────────>│ render on every surface    │
     │                          ├───────────────────────────>│
     │                          │                            │
     │                          │      taps "Allow once"     │
     │                          │<───────────────────────────┤
     │ view.action              │                            │
     │  {view_id: "v7",         │                            │
     │   action_id: "allow",    │                            │
     │   values: {...},         │                            │
     │   surface: "notification"}                            │
     │<─────────────────────────┤                            │
     │                          │                            │
     │ ...harness does whatever it wants: release the         │
     │    pre-hook, rewrite args, run the tool, present       │
     │    a second view from the post-hook, write a rule...   │
     │                          │                            │
     │ {dismiss: true}          │                            │
     ├─────────────────────────>│ take it down everywhere    │
     │                          ├───────────────────────────>│
```

- **`view.present`** puts a view up. `view_id` is harness-chosen and unique
  within the session. `modal: true` asks for the terminal overlay and the
  keyboard (`docs/specs/36`); `modal: false` is a card. `surfaces` optionally
  narrows where it appears.
- **`view.action`** is helios calling the harness. The return value controls
  what happens next:
  - `{dismiss: true}` — default; take it down.
  - `{view: {...}}` — replace it in place. This is how a harness shows
    "Applying…", then a result, without a second notification.
  - `{}` — leave it up. For an action that toggles something.
- **`view.update`** and **`view.dismiss`** let the harness drive the same
  transitions unprompted — which is what happens when the human answers at
  the CLI instead, and the harness needs the phone's card to disappear.
  Today that is `ResolveSession` and `resolveSessionQuestions`
  (`hooks.go:580,937`), reimplemented per hook; here it is one method.

**Nothing in this exchange is blocking, from helios's side.** A tool waiting
on a pre-hook is the harness's business. If a view is never actioned, helios
does nothing about it — no timeout, no default answer, no denial. The harness
sets its own deadline and calls `view.dismiss` when it expires, because only
the harness knows what timing out means.

That deletes a whole mechanism: `notifications.Manager`'s `Register`,
`WaitForDecision`, `Resolve`, `CancelPending`, `CancelPendingFromClaude`, and
the `decisionTimeout` / `HookTimeoutSeconds` pair (`hooks.go:851,857`) exist
only to hold an HTTP request open while a human decides. With callbacks, the
request was never held.

**Races.** Two surfaces can activate at once. Helios locks a view on first
activation — every surface shows it as submitting — and sends one
`view.action`. If the harness returns `{}`, the lock releases. A harness must
still be idempotent about a repeat `view_id`+`action_id`, because a client
that lost its connection mid-activation will retry.

**Persistence.** Helios stores presented views as notification rows, so a
phone that was asleep renders them on wake and a daemon restart does not lose
them. On reconnect, the harness re-presents whatever is still live; helios
dismisses any stored view the harness did not re-present.

### Blocks — display

Six. Each degrades to plain text on a surface that cannot do better.

| Block | Payload | Overlay | Notification | Agents tab |
|---|---|---|---|---|
| `text` | `{content, style?}` | as lines | folded into summary | as text |
| `code` | `{content, language?}` | dim, wrapped, truncated | first line | highlighted |
| `diff` | `{content}` — unified diff | ±-coloured, truncated | line count | full diff view |
| `keyvalue` | `{items: [{label, value}]}` | `label: value` lines | first pair | table |
| `link` | `{url, label?}` | URL as text | — | tappable |
| `markdown` | `{content}` | raw text | first line | rendered |

`style` is `normal` \| `dim` \| `strong` \| `warning` \| `danger`. It is
emphasis, not semantics: helios does not treat `danger` as "requires
confirmation", it just renders it red.

`diff` is in the list because unified diff is a *format*, not a tool
behaviour — any harness that edits files produces one, and rendering it as
`code` throws away the only affordance that matters.

### Fields — input

Four. Each has an `id`; its value comes back keyed by it in `view.action`.

| Field | Payload | Overlay | Notification | Agents tab |
|---|---|---|---|---|
| `select` | `{id, label, options: [{value, label, description?}], multiple?, default?}` | ↑↓ list | quick actions if single-select and ≤3 options | list / chips |
| `text` | `{id, label, placeholder?, multiline?, default?}` | inline editor | — | text input |
| `boolean` | `{id, label, default?}` | two-item list | — | switch |
| `secret` | `{id, label}` | — | — | masked input |

A surface that cannot render a required field does not render a broken
version of it. It shows the title, the summary, and the line **"Answer this
in the Helios app"** — which is exactly what `formHint` (`hooks.go:504`) does
today for elicitations.

### Actions — the callback

```jsonc
"actions": [
  {"id": "allow_once",   "label": "Allow once",                "style": "primary"},
  {"id": "allow_always", "label": "Allow, and don't ask again"},
  {"id": "deny",         "label": "Deny",                      "style": "danger"}
]
```

Every action is a call into the harness. `id` is the harness's own name for a
function or endpoint on its side; helios treats it as an opaque string it will
hand back.

`style` is `primary` \| `normal` \| `danger`, and again is emphasis only.
Optional `submits: false` marks an action that should not validate required
fields first — a "cancel" or a "remind me later".

`quick_actions: ["allow_once", "deny"]` narrows what a push notification
shows, since one fits two or three. Absent, helios takes the first three.

### The callback

```jsonc
{"method": "view.action", "id": 31, "params": {
  "view_id": "v7",
  "action_id": "allow_once",
  "values": {"command": "rm -rf ./build"},
  "surface": "notification"
}}
```

`values` is every field's value keyed by field id. `surface` is `overlay` |
`notification` | `panel`, included because a harness may reasonably want to
log where a decision came from — `resolved_source` in the notifications table
does this today.

There is no "cancelled" and no `action: null`, because helios never
manufactures an answer. If the human dismisses a notification, that is a
dismissal of the *card*, not an answer, and the harness is not told: the view
stays live on the other surfaces and in the app. A harness that wants an
explicit out gives the view a `cancel` action of its own.

### Worked examples

The four things helios has hardcoded today, expressed as views by the harness.
Note that nothing below is a helios concept.

**A tool wants to run** — was `claude.permission`:

```jsonc
{"title": "Bash", "summary": "rm -rf ./build",
 "blocks": [{"type": "code", "language": "bash", "content": "rm -rf ./build"}],
 "fields": [{"type": "text", "id": "command", "label": "Command",
             "default": "rm -rf ./build", "multiline": true}],
 "actions": [{"id": "allow_once", "label": "Allow once", "style": "primary"},
             {"id": "allow_always", "label": "Allow, and don't ask again"},
             {"id": "deny", "label": "Deny", "style": "danger"}],
 "quick_actions": ["allow_once", "deny"]}
```

The editable command field *is* `updated_input` (`actions.go:21`) — but helios
no longer knows that. It returns `values.command` and the harness puts it
where Claude wants it.

**The agent is asking a question** — was `claude.question`:

```jsonc
{"title": "Transport",
 "fields": [{"type": "select", "id": "q1", "label": "Which transport?",
             "options": [{"value": "unix", "label": "Unix socket",
                          "description": "Local only, no port"},
                         {"value": "tcp", "label": "TCP"}]}],
 "actions": [{"id": "submit", "label": "Submit", "style": "primary"},
             {"id": "skip", "label": "Skip", "submits": false}]}
```

**Credentials** — was `claude.elicitation.form`. The harness compiles the MCP
JSON Schema into fields itself; helios never sees a schema:

```jsonc
{"title": "Jira credentials",
 "fields": [{"type": "text", "id": "host", "label": "Host"},
            {"type": "secret", "id": "token", "label": "API token"}],
 "actions": [{"id": "submit", "label": "Connect", "style": "primary"},
             {"id": "cancel", "label": "Cancel"}]}
```

**An OAuth link** — was `claude.elicitation.url`:

```jsonc
{"title": "Authorize GitHub",
 "blocks": [{"type": "link", "url": "https://github.com/login/...",
             "label": "Open in browser"}],
 "actions": [{"id": "done", "label": "I've authorized"},
             {"id": "cancel", "label": "Cancel"}]}
```

**A rate limit** — no actions at all, so it is a card and nothing can be
activated on it. The harness takes it down with `view.dismiss` when the limit
resets:

```jsonc
{"title": "Rate limit reached",
 "summary": "Resets at 14:00",
 "blocks": [{"type": "keyvalue", "items": [{"label": "Resets", "value": "14:00"}]}]}
```

**A post-hook reporting what happened** — the same primitive, after the fact.
Nothing was ever blocked; the harness just wanted the human to see a diff:

```jsonc
{"title": "Edited 3 files",
 "summary": "+42 −17 in internal/server",
 "blocks": [{"type": "diff", "content": "--- a/internal/server/api.go\n+++ ..."}],
 "actions": [{"id": "revert", "label": "Revert", "style": "danger"}]}
```

`revert` is a function in the harness. Helios will call it if someone taps it,
minutes or hours later, and has no idea what it does.

### What helios still owns

Rendering, delivery, and persistence — not meaning.

- Fan-out to every surface, and locking a view on first activation so two
  surfaces do not both call the harness.
- Storing the view so it survives a daemon restart and re-renders on a phone
  that was asleep.
- Notification policy: which views raise a push, quiet hours, per-session
  mute. That is helios's relationship with the user's attention, not the
  harness's.

### Events

Advisory. They drive narration (`internal/reporter`), the activity feed, and
`last_event`. Losing one degrades the feed and nothing else.

```jsonc
{"method": "event", "params": {
  "seq": 413,
  "type": "tool_pre",
  "label": "Edit internal/server/api.go",
  "at": "2026-08-15T09:14:02Z",
  "view": { /* optional, for the expanded row in the activity feed */ }
}}
```

`type` is an opaque id the descriptor declared in `event_types` — helios uses
it only for filtering and grouping by the `category` the descriptor gave it.
`label` is the one line helios renders. There is no `tool` field and no
`summarizeToolInput` (`hooks.go:1267`): what a tool did, and how to say it in
one line, is the harness's judgement.

### Transcript

Same principle. Helios renders what the harness stores; it does not know the
format, does not parse a file, and does not normalise anything.

- `transcript.append` (harness → helios): one entry as it happens. This is
  what makes a terminal-less session feel alive.
- `transcript.read` (helios → harness): `{cursor?, limit}` → `{entries[],
  cursor}`. The cursor is opaque — a byte offset, a line number, a message id,
  whatever the harness's storage wants. Helios passes back what it was given.

An entry is a header and a view's worth of blocks:

```jsonc
{"id": "m88", "at": "2026-08-15T09:14:02Z",
 "author": {"label": "Claude", "kind": "agent"},
 "blocks": [
   {"type": "markdown", "content": "Removing the stale build directory."},
   {"type": "code", "language": "bash", "content": "rm -rf ./build"}
 ],
 "collapsed": true}
```

`author.kind` is `user` \| `agent` \| `system` \| `tool` and exists only so
the renderer can pick an alignment and a colour. `collapsed` asks the surface
to fold the entry by default — the harness's call, because only it knows
whether a 400-line tool result is worth the scroll.

`internal/transcript/reader.go` is deleted, not moved: its Claude-shaped
parsing goes into the Claude harness, and its `summarizeToolInput`
(`reader.go:240`) goes with it.

For a harness with `transcript: false`, helios shows the terminal and nothing
else — acceptable for a PTY harness, useless for a headless one, which is why
headless harnesses should treat `transcript` as effectively required.

## Terminal capability

What helios does when each capability is absent.

| Capability | Present | Absent |
|---|---|---|
| `terminal` | terminal tab, attach, mirror, warm-pool RSS accounting | tab hidden; `helios attach` refuses with "this session has no terminal"; transcript is the only view |
| `overlay` | HITL painted over the session (`docs/specs/36`) | notification-only HITL — phone, desktop, TUI list |
| `prompt_queue` | composer accepts a prompt mid-turn | composer disabled until `waiting_input` |
| `interrupt` | stop button | stop button hidden |
| `resume` | evictable; comes back with context | pinned in the warm pool; eviction ends the session |
| `transcript` | transcript tab, search, narration input | transcript tab hidden |
| `configurable` | model / mode pickers, per listed field | pickers hidden |
| `title` | harness names the session | helios auto-titles (`autotitle.go`) |

Two consequences inside helios worth naming, because they are currently
implicit in "there is always a PTY":

- `internal/server/trust_watcher.go:122` scrapes the screen for Claude's trust
  dialog. It must run only for sessions whose harness has `terminal: true`,
  and ideally only for the Claude harness — see §Claude harness.
- `backend.Host.markActive` (`internal/backend/host.go:62`) derives activity
  from screen output. A headless session must instead be marked active by
  `state`, `event`, and `transcript.append` arriving.

## Hook ingress

Hook configuration is global — `~/.claude/settings.json`, `~/.codex/` — so it
cannot address a per-session harness socket. Helios therefore keeps the HTTP
endpoint it already has and becomes a **router**: it matches `session_id`,
finds the owning harness, forwards the payload untouched, and writes the
harness's reply back to the CLI.

```
  claude CLI                helios daemon                 harness
      │                          │                            │
      │  POST /hooks/claude/permission                        │
      │  {session_id, tool_name, tool_input, ...}             │
      ├─────────────────────────>│                            │
      │                          │ look up harness by session │
      │                          │  agent.hook {event, payload}
      │                          ├───────────────────────────>│
      │                          │                            │ build a view
      │                          │     view.present {view}    │
      │                          │<───────────────────────────┤
      │                          │ render: notification,      │
      │                          │ overlay, agents tab        │
      │                          │                            │
      │   agent.hook has NOT returned. The daemon holds the    │
      │   CLI's request open; the harness is holding the RPC.  │
      │                          │                            │
      │                          │ view.action {allow, values}│
      │                          ├───────────────────────────>│
      │                          │  {dismiss: true}           │
      │                          │<───────────────────────────┤
      │                          │                            │ now release
      │                          │  agent.hook → {response}   │ the held RPC
      │                          │<───────────────────────────┤
      │  {"hookSpecificOutput": {"decision": {"behavior": "allow"}}}
      │<─────────────────────────┤                            │
```

The ordering is the point. `agent.hook` and `view.action` are independent
calls on the same connection, interleaved — which is why §Control envelope
requires out-of-order replies. The harness parks `agent.hook`, presents a
view, fields a callback, and only then answers the parked call. Helios is not
waiting on a decision it understands; it is proxying a request whose reply
happens to be late.

Helios sets no deadline on that. The CLI's own hook timeout is the only clock,
and it is configured by the harness (`Descriptor.hook_config`) because only
the harness knows what the CLI does when it expires. The daemon's held
goroutine is bounded by the socket: if the harness dies, the connection drops
and the request fails.

The daemon never looks inside `payload` or `response`. The harness owns both
shapes, which is what lets Codex's different hook JSON work without a daemon
change.

Codex, whose hooks are command-only, gets there through a shim the SDK ships:

```
  codex CLI ──stdin──> helios-hook <event>  ──HTTP──> POST /hooks/codex/<event>
                       (curl-sized binary)            (same router)
```

Registered in `~/.codex/` as `{"type": "command", "command": "helios-hook
PreToolUse"}`. Identical destination, identical routing.

**Wrapped sessions.** `helios wrap -- claude` starts an agent the user owns;
there is no harness process. Hooks for a session with no harness fall through
to the built-in in-process translator, which is what the Claude handlers
become in stage 0 of the migration. This path must keep working — it is how
people adopt helios.

## Lifecycle

### Discovery

A manifest per harness, in `~/.helios/harnesses/`:

```jsonc
// ~/.helios/harnesses/codex.json
{"id": "codex", "exec": ["/usr/local/bin/codex-helios-harness"], "protocol": 1}
```

At daemon start, for each manifest, helios runs `<exec> describe`, reads the
Descriptor from stdout, and caches it keyed by the binary's mtime. One-shot,
no socket, no session. This is what avoids the chicken-and-egg of needing a
running harness to know a harness exists.

The Claude harness ships inside the helios binary and needs no manifest.

### Spawn

```
<exec> serve --session <id> --socket <path> --cwd <dir>
             [--resume]
             [--prompt <text>] [--model <id>] [--permission-mode <mode>]
```

Same shape as `handlePtyHost` (`cmd/helios/ptyhost.go:28`) today. The harness
binds the socket, writes the sidecar, sends `hello`, and waits for
`session.start`.

### Whole-session flow

```
 create ──> Registry.Start ──> spawn harness ──> hello ──> session.start
                                                              │
                                                              ▼
                                                    state: starting
                                                              │
                                     agent ready ──> state: waiting_input
                                                              │
                     ┌────────────────────────────────────────┤
                     │                                        │
              session.prompt                            view.present
                     │                                        │
              state: active                         state: waiting_human
                     │                                        │
                event × n                       view.action from phone /
                     │                           terminal / TUI, then
                     │                           view.dismiss
                     └────────────────────────────────────────┤
                                                              │
                                              state: waiting_input
                                                              │
              evicted for room (Registry.evictForRoom)        │
                     │                                        │
              SIGTERM ──> harness exits ──> session cold      │
                     │                                        │
              user returns ──> spawn --resume ──> hello(resume_from) ──┘
```

Eviction, the warm pool, and the RSS ceiling stay exactly where they are
(`internal/terminal/registry.go:340`). A harness is a process with an RSS like
any other; the only new requirement is that it exit cleanly on SIGTERM and
come back with its context on `--resume`.

### Reconnection and replay

Two failures to survive, both of which lose data today:

1. **The daemon restarts.** Hooks in flight fail; events fire into a closed
   socket.
2. **The harness restarts** (eviction, crash). Helios must not double-count
   events it already recorded.

Both are handled by the `seq` already present on `event` and
`transcript.append`:

- The harness numbers every notification monotonically from 1 and keeps the
  last N in memory.
- Helios records the highest `seq` it has durably stored, per session.
- On `hello`, helios replies with that number. The harness replays anything
  newer, then goes live.
- `resume_from` in the harness's `hello` tells helios what the harness itself
  believes it has sent, so a *harness* restart is detectable (`resume_from`
  goes backwards) and helios can backfill with `transcript.read {cursor}`.

Views are not replayed by `seq`. They are re-presented: on reconnect the
harness sends `view.present` again for everything still live, and helios
dismisses any stored view that did not come back. A view whose harness has
died is dead by definition — the callback it would have made has nowhere to
go — so re-presenting is the only correct recovery, and it is the harness that
knows which ones survived.

## The SDK

`helios-harness-sdk`, Go first. The published surface is the protocol; the Go
package is the reference implementation of it.

### What an author implements

```go
// Required. This is the whole contract.
type Harness interface {
    Describe() sdk.Descriptor
    Start(ctx context.Context, spec sdk.StartSpec) error
    Prompt(ctx context.Context, text string) (sdk.Accepted, error)
    Interrupt(ctx context.Context) error
    Stop(ctx context.Context, reason string) error

    // Action is called when a human activates something on a view this
    // harness presented. Whatever it does — release a held tool, run a
    // function, call an endpoint — is invisible to helios.
    Action(ctx context.Context, a sdk.Activation) (sdk.Next, error)
}

// Optional, each gated by the matching capability.
type Terminal     interface { PTY() *sdk.PTY }
type Transcript   interface { Read(ctx context.Context, cursor string, limit int) (sdk.Page, error) }
type Configurable interface { Set(ctx context.Context, c sdk.Config) (restart bool, err error) }
type HookSource   interface { Hook(ctx context.Context, event string, payload json.RawMessage) (json.RawMessage, error) }
type Titler       interface { Title(ctx context.Context) (string, error) }
```

### What the SDK gives back

```go
func Serve(ctx context.Context, h Harness) error   // argv, socket, sidecar,
                                                   // frame loop, dispatch,
                                                   // SIGTERM, cleanup

type Client interface {
    SetState(sdk.Status, ...sdk.Detail)
    Emit(sdk.Event)                                  // auto-numbers seq
    Append(sdk.Entry)                                // transcript.append
    Present(sdk.View) sdk.ViewID
    Update(sdk.ViewID, sdk.View)
    Dismiss(sdk.ViewID)
    Log(sdk.Level, string, ...any)
}

func NewPTY(cmd string, args []string, opt sdk.PTYOptions) *sdk.PTY
```

Note what is missing: there is no `Ask` that blocks and returns an answer. The
SDK will not pretend a callback is a function call, because the two differ in
every way that matters — a view can outlive the tool that raised it, be
actioned twice, be actioned never, or be answered at the CLI instead.

For the common case where a harness *does* want to park a goroutine until
someone acts, that is four lines of its own code and the SDK stays honest:

```go
// harness-side, not SDK-side
pending := make(chan sdk.Activation, 1)
id := c.Present(sdk.View{
    Title:   "Bash",
    Summary: cmd,
    Blocks:  []sdk.Block{sdk.Code(cmd, "bash")},
    Actions: []sdk.Action{
        {ID: "allow", Label: "Allow once", Style: sdk.Primary},
        {ID: "deny",  Label: "Deny",       Style: sdk.Danger},
    },
})
h.waiting[id] = pending          // Action() delivers into this

select {
case a := <-pending:
    return a.Action == "allow"
case <-ctx.Done():               // the harness's own deadline, not helios's
    c.Dismiss(id)
    return false
}
```

The types are literal throughout — no `sdk.Approval()` constructor, because
the moment the SDK ships one, "approval" is a concept again.

`NewPTY` is `internal/terminal` — host, ring buffer, screen, mirror fan-out,
overlay compositing, bracketed-paste handling — lifted out of the daemon and
published. A PTY harness gets tiers of behaviour it never writes: multi-viewer
streaming, snapshot-and-replay, resize negotiation, the paste fix from
`docs/specs/37`.

### Headless harness, in full

```go
func main() {
    sdk.Serve(context.Background(), &myHarness{})
}

type myHarness struct{ c sdk.Client; agent *myAgent }

func (h *myHarness) Describe() sdk.Descriptor {
    return sdk.Descriptor{
        ID: "myagent", Name: "My Agent",
        Capabilities: sdk.Capabilities{Transcript: true, Interrupt: true},
        Models: []sdk.Model{{ID: "default", Name: "Default"}},
    }
}

func (h *myHarness) Start(ctx context.Context, spec sdk.StartSpec) error {
    h.agent = newAgent(spec.CWD)
    h.c.SetState(sdk.StateWaitingInput)
    if spec.Prompt != "" {
        _, err := h.Prompt(ctx, spec.Prompt)
        return err
    }
    return nil
}

func (h *myHarness) Prompt(ctx context.Context, text string) (sdk.Accepted, error) {
    h.c.SetState(sdk.StateActive)
    h.c.Append(sdk.Entry{Author: sdk.Author{Label: "You", Kind: sdk.KindUser},
        Blocks: []sdk.Block{sdk.Text(text)}})

    go func() {
        for ev := range h.agent.Run(text) {
            switch ev.Kind {
            case kindTool:
                // My tool, my hook, my labels. Helios has never heard of any
                // of this. The tool call parks here until Action() releases it.
                h.c.SetState(sdk.StateWaitingHuman)
                id := h.c.Present(sdk.View{
                    Title:   ev.Tool,
                    Summary: ev.Summary,
                    Blocks:  []sdk.Block{sdk.Code(ev.Rendered, ev.Language)},
                    Actions: []sdk.Action{
                        {ID: "run",  Label: "Run it", Style: sdk.Primary},
                        {ID: "skip", Label: "Skip"},
                    },
                })
                h.held[id] = ev
            case kindText:
                h.c.Append(sdk.Entry{Author: sdk.Author{Label: "My Agent", Kind: sdk.KindAgent},
                    Blocks: []sdk.Block{sdk.Markdown(ev.Text)}})
            }
        }
        h.c.SetState(sdk.StateWaitingInput)
    }()

    return sdk.Accepted{OK: true}, nil
}

func (h *myHarness) Action(ctx context.Context, a sdk.Activation) (sdk.Next, error) {
    ev, ok := h.held[a.ViewID]
    if !ok {
        return sdk.Next{Dismiss: true}, nil    // already gone; harmless
    }
    delete(h.held, a.ViewID)

    if a.Action == "run" {
        ev.Allow()
        // A post-hook can present its own view later. Nothing is waiting.
        return sdk.Next{View: &sdk.View{Title: ev.Tool, Summary: "Running…"}}, nil
    }
    ev.Deny()
    return sdk.Next{Dismiss: true}, nil
}
```

No terminal, no frames, no sockets, no JSON, and no helios concepts anywhere.
That is the bar for "bring your own harness".

## The Claude harness

Every Claude-specific behaviour in helios today, and where it goes. Nothing on
this list is dropped.

### Descriptor

```jsonc
{
  "id": "claude", "name": "Claude Code", "icon": "terminal", "protocol": 1,
  "capabilities": {
    "terminal": true, "overlay": true, "prompt_queue": true,
    "interrupt": true, "resume": true, "transcript": true,
    "configurable": ["model", "permission_mode"], "title": false
  },
  "models": [ /* register.go:161 verbatim */ ],
  "permission_modes": [ /* register.go:58 verbatim */ ],
  "default_permission_mode": "auto",
  "commands": [ /* register.go:249 verbatim */ ],
  "event_types": [ /* register.go:230 verbatim */ ]
}
```

`title: false` — helios keeps auto-titling, because it does it with the
provider's own small model and the logic is not Claude-specific in any
interesting way. See §Auto-title.

### Start and resume

`sessionArgs` (`register.go:122`) and `ResumeArgs` (`register.go:88`) move
into the harness unchanged, and `cmd/helios/ptyhost.go:78`'s hardcoded
`claude --resume` disappears with them:

```go
func (h *claudeHarness) Start(ctx context.Context, spec sdk.StartSpec) error {
    argv := sessionArgs(spec)          // register.go:122
    if spec.Resume {
        argv = resumeArgs(spec.SessionID, spec.PermissionMode)  // register.go:88
    }
    h.pty = sdk.NewPTY(argv[0], argv[1:], sdk.PTYOptions{
        Dir: spec.CWD, Env: agentEnv(),        // ptyhost.go:191
    })
    h.c.SetState(sdk.StateStarting)
    return h.pty.Run(ctx)
}
```

The permission-mode-on-resume rule — repeat the flag or the session silently
comes back in the CLI's default (`register.go:79`) — stays in the harness,
where the CLI's semantics belong.

### Hooks

All sixteen. `event` column is what arrives on `agent.hook`; the right column
is what the harness does with it.

| Claude hook | Handler today | Becomes |
|---|---|---|
| `PermissionRequest` | `hooks.go:88` `handlePermission` | park the call; `view.present` with a `code` block and allow/always/deny actions; on `Action`, `action_id` → `hookSpecificOutput.decision.behavior` |
| `PreToolUse` (`AskUserQuestion`) | `hooks.go:283` `handleQuestion` | park; `view.present` with one `select` field per question; on `Action`, `values` → tool-denial carrying the answer (`question.go:213`) |
| `PreToolUse` (`*`) | `hooks.go:989` `handleToolPre` | `event{type: tool_pre}` + `state: active` |
| `PostToolUse` | `hooks.go:1028` `handleToolPost` | `event{type: tool_post}` |
| `PostToolUseFailure` | `hooks.go:1059` | `event{type: tool_post_failure}` |
| `Elicitation` (form) | `hooks.go:402` `handleElicitation` | park; schema compiled to `fields` by the harness; `view.present` |
| `Elicitation` (url) | `hooks.go:402` | park; `view.present` with a `link` block |
| `UserPromptSubmit` | `hooks.go:888` `handlePromptSubmit` | `state: active` + `event{prompt_submit}` + `transcript.append` |
| `Stop` | `hooks.go:566` `handleStop` | `state: waiting_input` + `event{stop}` |
| `StopFailure` | `hooks.go:623` `handleStopFailure` | `state: error` with `detail` from `classifyAPIError` |
| `Notification` (`idle_prompt`) | `hooks.go:711` | `state: waiting_input` + `view.dismiss` for anything still up (`resolveSessionQuestions`, `hooks.go:937`) |
| `Notification` (`permission_prompt`) | `hooks.go:711` | `event{notification}` |
| `SessionStart` | `hooks.go:749` | `state: waiting_input` + descriptor refresh (model, transcript path, permission mode) |
| `SessionEnd` | `hooks.go:815` | `state: ended` |
| `PreCompact` | `hooks.go:1087` | `event{compact_pre}` |
| `PostCompact` | `hooks.go:1115` | `event{compact_post}` |
| `SubagentStart` | `hooks.go:1145` | `event{subagent_start}` |
| `SubagentStop` | `hooks.go:1187` | `event{subagent_stop}` |

"Park" means the harness holds the `agent.hook` RPC open and answers it after
a callback arrives. There is no wire-level notion of a blocking question; the
harness simply has not replied yet.

What the handlers do *besides* translating — `UpdateSessionStatus`,
`CreateNotification`, `Notify`, `Report`, `ResolveSession`,
`renameSessionWindow` — is not translation. It is helios's own bookkeeping,
and it stays in the daemon, driven by `state`, `event`, and the view
lifecycle. This is the single biggest simplification in the migration:
`hooks.go` loses roughly two thirds of its body to code that was never
Claude-specific.

`hookConfig` (`internal/daemon/hooks.go:14`) stays where it is, still writing
`~/.claude/settings.json`, still pointing at the daemon — but the harness owns
the *content*, exposing it as `Descriptor.hook_config` so a harness upgrade
can add a hook without a helios release. `InstallHooks`, `HooksOutdated`, and
the TUI's health check (`internal/tui/view.go:107`) become
harness-parameterised.

### Permission round trip, end to end

```
  claude          daemon                         harness           surfaces
    │  PermissionRequest hook                       │                  │
    ├──────────────>│                               │                  │
    │               │ agent.hook{PermissionRequest} │                  │
    │               ├──────────────────────────────>│                  │
    │               │                               │ AskUserQuestion? │
    │               │                               │  → reply "ask",  │
    │               │                               │    done          │
    │               │                               │ build the view:  │
    │               │                               │  code block,     │
    │               │                               │  3 actions,      │
    │               │                               │  editable field  │
    │               │                               │ park the RPC     │
    │               │  view.present{v7, modal}      │                  │
    │               │<──────────────────────────────┤                  │
    │               │ store it as a notification    │                  │
    │               │ SSE ─────────────────────────────────────────────>│ phone
    │               │ HITL overlay ────────────────────────────────────>│ terminal
    │               │                               │                  │
    │               │   whichever surface acts first; helios locks the  │
    │               │   view and sends exactly one callback             │
    │               │<──────────────────────────────────────────────────┤
    │               │  view.action{v7, "allow_always",                  │
    │               │    values: {command: "rm -rf ./build"}}           │
    │               ├──────────────────────────────>│                  │
    │               │                               │ its own ids, its │
    │               │                               │ own meaning; write
    │               │                               │ the permission   │
    │               │                               │ rule; build      │
    │               │                               │ hookSpecificOutput
    │               │  {dismiss: true}              │                  │
    │               │<──────────────────────────────┤                  │
    │               │ take the card down everywhere │                  │
    │               │                               │                  │
    │               │  agent.hook → {response}      │ release the park │
    │               │<──────────────────────────────┤                  │
    │  behavior: allow, updatedPermissions: [...]   │                  │
    │<──────────────┤                               │                  │
```

The daemon's column contains no Claude nouns and no decision. It rendered a
view, sent one callback, and took the card down when told. That
`allow_always` means "write a permission rule and allow" is knowledge that
exists only inside the harness — as is the fact that a CLI request was waiting
on it at all.

Three details preserved from the current implementation, all of them now the
harness's business:

- `AskUserQuestion` short-circuits to `"ask"` without presenting anything
  (`hooks.go:105`) — the question picker *is* the permission prompt for that
  tool, and allowing it returns an empty answer set.
- The "Allow, and don't ask again" action only appears when the CLI supplied
  `permission_suggestions` (`hooks.go:228`); the harness omits it from
  `actions` when it has no rule to apply.
- The editable command field maps to
  `hookSpecificOutput.decision.updatedInput` (`hooks.go:186`) — the harness
  reads `values.command` and puts it there.

### Actions

`actions.go` stops existing. Its five handlers were five ways of turning a
client's POST into a Claude-shaped decision; there is now one path, and it is
not Claude-shaped: take `{action_id, values}` off the wire, look up which
harness presented the view, call `view.action`, apply the returned
`{dismiss}` or `{view}`.

| Handler | Today | Becomes |
|---|---|---|
| `handlePermissionAction:21` | builds `{updated_input, apply_permission}` | deleted |
| `handleQuestionAction:53` | builds `{selections}` | deleted |
| `handleElicitationAction:75` | builds form values | deleted |
| `handleErrorAction:105` | retry / dismiss a failed turn | deleted — the harness presents a view with a "Retry" action and calls `session.prompt` itself |
| `handleTrustAction:148` | answer the trust dialog | stays helios, gated on `terminal` (§Trust dialog) |

`provider.RegisterAction` (`registry.go:63`) disappears with them, and so does
most of `notifications.Manager`: `Register`, `WaitForDecision`, `Resolve`,
`CancelPending`, `CancelPendingFromClaude`, `ResolveSession`. What remains is
storage, fan-out, and the per-view activation lock.

### Transcript

`ParseClaudeTranscript` (`internal/transcript/reader.go:75`) moves into the
harness and becomes `Transcript.Read`, emitting entries and blocks rather than
helios's `Message` struct. `summarizeToolInput` (`reader.go:240`) goes with
it — how to render a `Bash` invocation in one line is a Claude judgement, and
a harness that formats it as a `diff` or a `keyvalue` table instead is not
wrong.

The daemon's `GET /api/sessions/{id}/transcript` (`internal/server/api.go:411`)
stops reading a file and calls `transcript.read`, passing the client's opaque
cursor straight through.

The harness also pushes `transcript.append` on `UserPromptSubmit` and `Stop`,
so the mobile transcript stops polling. `transcript_path` remains on the
session record — useful for support and for `helios wrap` — but nothing in the
daemon parses it.

`internal/discovery/claude.go:31`, which scans `~/.claude/projects/*.jsonl` to
find sessions started outside helios, moves to the harness as a `discover`
subcommand the daemon runs at startup, alongside `describe`.

### Errors and rate limits

`lastAPIError` (`apierror.go:40`) scans the transcript tail because Claude
Code v2.1.x does not populate `reason` on `StopFailure`. That is a
CLI-version workaround and belongs in the harness. `classifyAPIError`
(`apierror.go:100`) — is it a rate limit, when does it reset — is also
Claude-shaped, and its output rides in `state`:

```jsonc
{"method": "state", "params": {
  "status": "error",
  "detail": {"message": "Claude AI usage limit reached", "at": "..."}
}}
```

`detail.message` is the one line helios puts on the session row. Everything
richer — that it is a rate limit, when it resets, that retrying is worth it —
is a view the harness sends alongside:

```jsonc
{"method": "view.present", "params": {"seq": 414, "view_id": "err-91", "view": {
  "title": "Rate limit reached",
  "summary": "Resets at 14:00",
  "blocks": [{"type": "keyvalue",
              "items": [{"label": "Resets", "value": "14:00"}]}],
  "actions": [{"id": "retry", "label": "Retry now", "style": "primary"},
              {"id": "dismiss", "label": "Dismiss"}]
}}}
```

Nothing in the agent is waiting on this one — the turn is already over. The
harness gets `view.action{retry}` whenever the human gets to it, or never, and
retries by calling `session.prompt` on itself. That is `handleErrorAction`
(`actions.go:105`) and `docs/specs/33-session-error-retry.md`, expressed
without helios knowing what a rate limit is.

Today this distinction is a hack: `handleStopFailure` calls `Mgr.Register`
purely so `Resolve` "has somewhere to deliver" (`hooks.go:688`), for a
notification nothing is waiting on. With views there is no distinction to
hack around — presenting is presenting, and whether something is parked
behind it is the harness's private business.

### Trust dialog

`internal/server/trust_watcher.go` regex-matches Claude's "Do you trust the
files in this folder?" against the screen because it appears *before* any hook
can fire. It is genuinely pre-protocol: the agent is not yet running.

It stays in helios but becomes harness-driven — the descriptor gains an
optional `screen_watchers` list of `{pattern, view}` pairs, and the watcher
only runs for harnesses with `terminal: true`. Claude's patterns
(`trust_watcher.go:87`) move into the Claude descriptor, along with the view
to present when one matches. Helios presents it on the harness's behalf and
routes the callback back the usual way; the harness answers by writing to its
own PTY. A headless harness never triggers it.

### Auto-title and the small model

`TriggerAutoTitle` / `RegenerateTitle` (`autotitle.go:195,224`) stay in
helios. They are not Claude logic; they are helios's naming policy, which
happens to need a cheap model. The dependency is
`provider.RegisterSmallModelCaller` (`register.go:200`, `claude -p --model
haiku`), which becomes a descriptor field:

```jsonc
"small_model": {"exec": ["claude", "--bare", "-p", "--model", "haiku",
                         "--output-format", "json", "--system-prompt", "{system}"]}
```

Helios runs it with the prompt on stdin, exactly as it does now. A harness
that declares no `small_model` falls back to any other installed harness that
does — auto-titling should not stop working because someone installed a
headless agent.

A harness that would rather name sessions itself sets `title: true` and pushes
the `title` notification.

### What stays Claude-shaped inside helios afterwards

Nothing. The intended end state is that `internal/provider/claude` contains
only the harness implementation, and `grep -ri claude internal/server
internal/store internal/backend` returns no hits.

## The Codex harness

Sketched to prove the protocol, not to specify Codex.

| Concern | Claude | Codex |
|---|---|---|
| Hook transport | `type: "http"` direct to daemon | `type: "command"` → `helios-hook` shim → daemon |
| Config location | `~/.claude/settings.json` | `~/.codex/` (JSON or inline TOML) |
| Event names | `PreToolUse`, `Stop`, ... | same names, different payload fields |
| Extra fields | — | `turn_id`, `tool_use_id` |
| Async hooks | — | `async: true`, ≤8 concurrent |
| Blocking responses | `hookSpecificOutput.decision` | `continue`, `stopReason`, `systemMessage` |
| Trust model | — | non-managed hooks need explicit review |

Every difference is inside `agent.hook`, which the daemon does not read. The
Codex harness is `Describe` + argv + a payload translator + `sdk.NewPTY`.

## Migration

Staged so that every stage ships on its own and nothing regresses.

**Stage 0 — the interface, in-process.** Define `internal/harness` with the
protocol types and the `Harness` interface. Wrap the existing Claude code
behind it, still compiled in, still called directly. No socket, no new
process, no behaviour change. The value is that the daemon now calls a
narrower surface, and the wrapped-session fallback path (§Ingress) is written
once and kept forever.

**Stage 1 — out of process.** Extract `sdk.NewPTY` from `internal/terminal`.
Turn `helios ptyhost` into `helios harness claude serve`. Add the control
frame, the router in `handleHook`, the manifest loader, and `describe`
caching. Claude now runs through the wire protocol. Everything else is
unchanged and the whole thing is verifiable against the existing test suite:
`internal/provider/claude/hooks_test.go` is 2061 lines and mostly still
applies.

**Stage 2 — publish.** `helios-harness-sdk` as a Go module with the reference
harness as its example. Document the wire protocol as the normative artifact;
the SDK is one implementation of it.

**Stage 3 — Codex.** The first harness written against the published SDK by
someone reading only the spec. Whatever they have to ask about is a defect in
this document.

**Stage 4 — view renderers.** Mobile, desktop, and the terminal overlay stop
switching on `notification.type` (`claude.permission`, `claude.question`,
`claude.elicitation.form|url`) and grow one renderer per surface that walks
blocks, fields, and actions. This is the largest client-side change in the
plan and the one that ends the coupling for good: after it, a new harness with
a concept nobody has seen before renders correctly on a phone that shipped
months earlier.

Until it lands, a shim in the daemon recognises the Claude harness's views and
re-emits them in the old notification shapes, so clients need not upgrade in
lockstep.

## Open questions

1. **Auto-approve belongs to the harness now.** Today's rules match Claude
   tool names (`docs/specs/12-auto-approve.md`). Views have no tool names and
   helios cannot know that activating `allow_once` on its behalf is safe — it
   does not know what `allow_once` does. The consequence is that auto-approve
   moves wholesale into the harness, and helios's contribution is a settings
   surface: the descriptor declares a `settings_view`, helios renders it in
   preferences, and the callbacks go to the harness like any other. This is
   the right answer but it is a visible feature regression until the Claude
   harness reimplements the rules, and that should be planned rather than
   discovered.
2. **Slash commands.** `command.run` assumes the harness executes them. For a
   PTY harness the honest implementation is typing the command into the
   terminal, which is what helios does now. Worth revisiting once a harness
   exists that can execute one properly.
3. **Multi-session harnesses.** Every message carries a session id, so the
   protocol permits one harness process serving several sessions. Nothing
   here implements it, and the warm pool assumes one process per session.
4. **`screen_watchers`.** Descriptor-driven screen scraping (§Trust dialog) is
   the one place the daemon still pattern-matches a terminal on a harness's
   behalf. It is the least principled part of this design and should be
   deleted if Claude ever fires a hook before the trust dialog.
5. **Where the block vocabulary stops.** Six blocks and four fields cover
   everything helios renders today, but the list is a standing invitation to
   grow — a table here, a progress bar there, and eventually helios is a
   layout engine. The discipline this spec proposes: a new block type is only
   admitted when at least two harnesses need it and no combination of the
   existing six will do. `markdown` is the pressure valve; a harness that
   wants something exotic renders it to markdown and accepts what it gets.
6. **Narration input.** `internal/reporter` currently reads tool names and
   inputs to narrate a session out loud. With views it sees `title`,
   `summary`, and `label` instead. That is probably richer, since the harness
   wrote those for humans — but the persona prompts (`reporter/personas.go`)
   assume the old shape and will need rewriting.
7. **A dead harness leaves live views.** If a harness is evicted or crashes
   while views are up, their callbacks have nowhere to go. Helios should mark
   them stale rather than delete them — a permission request that vanished
   silently is worse than one that says "this session is no longer running" —
   but it is unclear whether waking the session should re-present them
   (harness's choice, §Reconnection) or whether the user should be asked.
8. **Push notification actions are a lie in one respect.** iOS and Android
   quick actions fire without the app in the foreground, so an activation may
   reach a daemon whose harness is cold. Helios has to wake the session,
   reconnect, and only then deliver `view.action` — which can take seconds and
   can fail. The current code sidesteps this because the daemon itself could
   resolve a decision without the agent being up. Needs a defined behaviour:
   queue and deliver on wake, or refuse with a clear message.
