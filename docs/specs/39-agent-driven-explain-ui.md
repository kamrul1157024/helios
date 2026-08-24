# Agent-Driven Explain UI: A Helios MCP Server and the Deck It Draws

> **Superseded by `41-helios-mcp-tools.md`.** The deck model here — decks owned
> by a session, content stored as references the daemon resolves at read time,
> and a Learn tab inside the session panel — has been withdrawn. So has its
> successor, `40-learnings.md`. Helios no longer stores an explanation at all;
> an agent points at a view with `helios_show`.
>
> Still current and depended on by 41: the *Spike results* section. A stdlib MCP
> server is sufficient, Claude Code stops a tool call at about 60 seconds and no
> env var raises it, and the desktop is inspectable over CDP.

## Problem

Asking an agent to explain a large change produces a wall of prose. For
`newscred/opal-app#6813` — 29 files, +3354/−36 — the useful answer is not text
at all: it is *this payload, entering here, hitting this guard, which is why the
400*. That answer needs a tree that jumps, code that scrolls to the right lines,
a diagram, and a caption pinned beside it.

Helios already renders every one of those surfaces. Nothing lets the agent point
at them.

Two consequences:

1. **The agent re-types what the UI could show.** It quotes source into the
   transcript, which costs output tokens, goes stale the moment the file
   changes, and is less readable than the editor sitting two panes away.
2. **The loop is open.** When the reader gets lost, wanders to another file, or
   selects a range, the agent never learns. It keeps explaining at the wrong
   altitude.

## What already exists

The transport, the panes, and the human-in-the-loop channel are all built. This
spec mostly wires them together.

| Need | Already there |
|------|---------------|
| Push daemon → desktop | `internal/server/sse.go:31` `Broadcast(SSEEvent{Type,Data})`, mounted at `internal/server/server.go:197` |
| Desktop receives | `desktop/src/main/sse.ts:49` fetches `/api/events`; dispatch at `desktop/src/renderer/store.ts:391` `onServerEvent` |
| Deliver a prompt into a session | `docs/specs/37-prompt-delivery-reliability.md` |
| Ask the human from a cold start | `internal/provider/claude/question.go`, `docs/specs/34-askuserquestion-dual-answer.md` |
| Session panel tab strip | `desktop/src/renderer/components/detail.tsx:20` `PANELS` |
| Ask the human, session-scoped | `internal/hitl/hitl.go:95` `Controller.Ask(sessionID, Prompt, onAnswer)` |
| Notify the phone | `internal/notifications/manager.go`, `internal/push` |
| Render markdown | `desktop/src/renderer/markdown.ts` |
| Render diff | `desktop/src/renderer/diff.ts`, `components/diff-view.tsx` |
| Render code with decorations | `components/editor.tsx` (CodeMirror) |
| File tree | `components/file-tree.tsx` |
| Serve a file diff | `internal/server/git.go:104` `handleGitDiff` → `git.go:142` `fileDiff` |
| Worktree listing | `internal/server/git.go:215` |

Genuinely new: an MCP endpoint, a deck store, one desktop pane, and the git
flags `fileDiff` does not pass today.

## Spike results (2026-08-19, this machine)

Three unknowns were measured before committing to the design. Two of them
changed it.

**1. Can the MCP server be dependency-free?** Yes. A 150-line stdlib-only Go
handler — `net/http` plus `encoding/json`, no SDK — completed the full handshake
and served a tool call to Claude Code on the first attempt.

```
POST /mcp  initialize                 id=0
POST /mcp  notifications/initialized  id=      → 202, empty body
GET  /mcp  Accept: text/event-stream           → 405, client continued fine
POST /mcp  tools/list                 id=1
POST /mcp  tools/call                 id=2     → "echo: handshake-ok"
```

Client negotiated `protocolVersion 2025-06-18` and echoed back the
`Mcp-Session-Id` we set on every subsequent request — so per-connection binding
is available if ever wanted. The server→client SSE stream is genuinely optional.

**2. How long can a tool call block?** Not long. This is the finding that
changed the design.

| Sleep | Result |
|-------|--------|
| 30 s | returned normally |
| 45 s | returned normally |
| 120 s | `<error>The operation timed out.</error>` at ~60 s |
| 120 s with `MCP_TOOL_TIMEOUT=300000 MCP_TIMEOUT=300000` | still timed out at ~60 s |

A blocking `helios_await(300)` is therefore impossible; the ceiling is ~60 s and
is not raisable by the documented env vars. Redesigned as a 45 s long-poll.

**3. Is the desktop UI inspectable, or is frontend work blind?** Inspectable.
Launching the renderer with `--remote-debugging-port` and an isolated
`--user-data-dir` exposes CDP: `Page.captureScreenshot` returns the real window,
and `Runtime.evaluate` reads the DOM. Visual iteration on `learn.tsx` does not
require a human in the loop for every change.

## Interaction flow

Two directions, two different mechanisms, and they never mix. The agent writes
to the deck over MCP. The reader writes to the agent by injecting a prompt.
Nothing blocks, and no tool waits.

```
 READER            DESKTOP                DAEMON                    AGENT
   │            (learn panel)      (deck store · /mcp)        (claude session)
   │                  │                     │                        │
   │  click           │                     │                        │
   │  ⌁ explain PR    │                     │                        │
   ├─────────────────▶│                     │                        │
   │                  │  POST inject prompt │                        │
   │                  ├────────────────────▶│  "…explain PR #6813    │
   │                  │                     │   session=s_91cc"      │
   │                  │                     ├───────────────────────▶│
   │                  │                     │                        │
   │                  │                     │      helios_deck       │  turn
   │                  │                     │◀───────────────────────┤  starts
   │                  │                     │      helios_deck_push  │
   │                  │                     │◀───────────────────────┤
   │                  │  SSE deck_updated   │  (act 1 — first paint) │
   │                  │◀────────────────────┤                        │
   │  ◀── act 1 ──────┤                     │                        │
   │  starts reading  │                     │      helios_deck_push  │
   │                  │                     │◀───────────────────────┤
   │                  │  SSE deck_updated   │      (acts 2-4)        │
   │                  │◀────────────────────┤                        │
   │                  │                     │      helios_deck_end   │
   │                  │                     │◀───────────────────────┤
   │                  │                     │                        │ turn
   │                  │                     │                     idle  ends
   │                  │                     │                        │
   ├─ ▸ next ────────▶│  client-side only. no daemon, no agent.      │
   ├─ ◂ prev ────────▶│  the deck is already stored.                 │
   ├─ click ssrf.go ─▶│  excursion. esc returns to the spine.        │
   ├─ d (diff mode) ─▶│  GET /api/git/diff                           │
   │                  ├────────────────────▶│                        │
   │                  │◀────────────────────┤                        │
   │                  │                     │                        │
   │  click           │                     │                        │
   │  ↓ deeper (3)    │                     │                        │
   ├─────────────────▶│  POST inject prompt │                        │
   │                  ├────────────────────▶│  "…more depth on       │
   │                  │                     │   step 3 … after_step=3"
   │                  │                     ├───────────────────────▶│ turn
   │                  │                     │  helios_deck_push      │ resumes
   │                  │                     │  (after_step:3)        │ warm
   │                  │                     │◀───────────────────────┤
   │                  │  SSE deck_updated   │                        │
   │                  │◀────────────────────┤                        │
   │  ◀── sub-deck ───┤  attached under 3. steps 1-9 untouched.      │
   │                  │                     │                     idle
```

Three properties fall out:

- **Reading costs nothing.** Between the two turns the session is idle. A deck
  can sit on screen for an hour.
- **The deck outlives the agent.** Navigation reads the store, so it works with
  the session cold, after a desktop restart, and on a phone that connected late.
- **One inbound mechanism.** Every reader action that needs the agent is a
  prompt injection — the same path the Learn button uses. There is no second
  protocol to keep alive.

## Design

### Principle: the agent supplies intent, the daemon supplies content

The agent must never emit source or diff text. It emits a *reference* and the
daemon reads the file and runs git.

```jsonc
// rejected
{"type":"code","text":"178 func (h *Handler) Register(w, r) {\n179 …"}

// accepted
{"type":"code","ref":"internal/oauth/registration.go","symbol":"Register",
 "mode":"annotated","base":"main"}
```

This is the load-bearing decision. It buys, in order of importance:

- **Content cannot drift.** The pane shows the file as it is, not as the agent
  remembered it.
- **Roughly 6× fewer output tokens** for a nine-step deck (~1.5k vs ~9k),
  which is also the difference between first paint at 3 s and at 25 s.
- **Rendering stays the renderer's job.** Syntax highlighting, folding and
  decorations already work; inlined text would bypass all of it.

### Transport

The MCP server is hosted by the daemon over streamable HTTP at `POST /mcp` on
the **internal** listener, alongside hooks and the admin API. That listener is
already exactly what MCP needs — `127.0.0.1` unconditionally
(`internal/server/server.go:152`) and no auth — whereas the public listener
binds `0.0.0.0` when the tunnel provider is `local`.

Mounting it there rather than on the public server is what keeps an
unauthenticated tool surface off the network, so it is asserted in a test rather
than left to inspection. Within the machine it is a generic server: any local
MCP client can use it, including agents Helios did not spawn.

Verified by spike (see *Spike results*): a stdlib-only Go handler is sufficient.
No SDK, no JSON-RPC dependency. The observed handshake is:

```
POST /mcp   initialize                  → protocolVersion 2025-06-18
POST /mcp   notifications/initialized   → 202, no body (no id on a notification)
GET  /mcp   Accept: text/event-stream   → 405 is fine; the client continues
POST /mcp   tools/list
POST /mcp   tools/call
```

Any local process can reach `/mcp` and drive any pane. Accepted: the internal
listener already trusts loopback for hooks and admin, and the blast radius is a
rendered panel.

### Identity: the session is a parameter, stamped by the button

Every tool takes a `session` argument. The agent does not infer it, and the
daemon does not guess it. It arrives one of two ways.

**Primary — the Learn button stamps it.** Helios owns prompt delivery
(`docs/specs/37-prompt-delivery-reliability.md`), and the Learn panel lives
inside a session's own tab strip, so the target is implied by where the user
clicked:

```
click ⌁ explain this PR   in session s_91cc's Learn panel
        │
        ▼
Helios sends into s_91cc:
   "Walk me through PR #6813 using the Helios explain pane.
    session=s_91cc  base=main  cwd=~/workspace/opal-app"
```

**Fallback — cold start.** The user typed a prompt instead of clicking. The
agent calls `helios_sessions()` and asks with its *own* native AskUserQuestion,
which Helios already renders to desktop and phone
(`docs/specs/34-askuserquestion-dual-answer.md`,
`internal/provider/claude/question.go`). This sidesteps a chicken-and-egg: a
`helios_ask` about *which session to target* would itself need a session to
render on.

Deliberately rejected, in order of temptation:

| Rejected | Why |
|---|---|
| `X-Helios-Session` header injected via `--mcp-config` | only works for sessions Helios spawned; `sessionArgs()` stays untouched instead |
| resolve by cwd | two checkouts of one repo is a case Helios already handles (`newsession.tsx:126`) |
| "exactly one live session" guess | silently wrong on the second session |
| `ambiguous_session` error-as-picker | clever; unnecessary once the button stamps |
| `helios_attach` connection binding | viable — Claude Code does honour `Mcp-Session-Id`, confirmed by spike — but a second mechanism earning nothing |

Because `session` is explicit, one agent driving another session's pane is free
by construction rather than a separate feature.

### The deck model

```
deck    per session. one live at a time. replaces on re-begin.
 ├─ step[]      ordered. append-only within a deck.
 │   ├─ layout      single | compare | stack
 │   ├─ slots[]     1 for single, 2 for compare/stack
 │   │   └─ content markdown | code | diff | payload | dataflow | diagram | map
 │   ├─ caption
 │   └─ note        the narration shown in the explain pane
 └─ cursor   what the agent last pushed
```

Storing a deck rather than streaming imperative commands is deliberate. Events
like "scroll to line 40" cannot be stepped back through, are lost on reopen, and
show a late-joining phone an empty screen. A deck with a cursor gives replay,
back/forward and mobile parity for the same amount of code.

### Where it lives: a Learn panel, not a new pane

`PANELS` at `desktop/src/renderer/components/detail.tsx:20` is
`['chat','terminal','approvals','git','files']`. Learn is a sixth entry. No
app-wide layout change; the panel splits internally into tree | content |
narration.

```
┌ s_91cc · opal-app ─────────────────────────────────────────────┐
│ chat │ terminal │ approvals │ git │ files │ ⌁ learn │ zsh │ + │
└────────────────────────────────────────────────────────────────┘
```

**The empty state is the launcher.** No deck yet, so the panel is the buttons:

```
┌ ⌁ learn ───────────────────────────────────────────────────────┐
│                                                                 │
│              nothing to walk through yet                        │
│                                                                 │
│        ⌁ explain this PR          ⌁ explain the working diff    │
│        ⌁ explain this file        ⌁ explain a selection         │
│                                                                 │
│        sends a prompt to this session                           │
└─────────────────────────────────────────────────────────────────┘
```

Each button templates a prompt and stamps `session=` with the session that owns
the tab strip. The same affordance belongs in the tree context menu, the diff
view header, and on an editor selection.

```
explain this PR         "Walk me through PR {n} using the Helios explain pane.
                         session={id} base={base} cwd={cwd}"
explain this file       "…explain {path} … session={id}"
explain working diff    "…explain the uncommitted diff … session={id} base=HEAD"
explain this selection  "…explain {path}:{a}-{b} … session={id}"
```

Open: whether the buttons are disabled when no session is targeted, or spawn one
via `handleCreateSession` and stamp the new id. Spawning reads better but means
a button can silently create sessions.

### Storage

Decks live in the existing SQLite store (`internal/store/store.go:14`), beside
`sessions` and `notifications`. Not files: a file would lose the transactional
write that `deck_updated` is broadcast from, and would be a second persistence
layer next to one that already works.

Size is not a concern. The agent emits no code, so twelve steps of prose plus
refs is roughly 15 KB of JSON.

```sql
CREATE TABLE IF NOT EXISTS decks (
    deck_id    TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    title      TEXT NOT NULL,
    kind       TEXT NOT NULL DEFAULT 'explain',
    base       TEXT NOT NULL DEFAULT '',    -- commit SHA, never a branch name
    steps      TEXT NOT NULL DEFAULT '[]',  -- JSON
    cursor     INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    ended_at   TEXT
);
```

**`base` is resolved to a commit SHA at deck creation.** A deck stored as
`base:"main"` is meaningful for ten minutes and misleading a month later, when
`registration.go:190` is some other line. Cheap now, impossible to retrofit.

**Retention:** decks die with their session — cascade on `session_id`, keeping
only the most recent few per session. A deck is tied to a moment.

**Non-goal, deliberately:** exporting a deck to a committable file
(`docs/walkthroughs/6813.deck.json`) so a walkthrough becomes onboarding
material. Genuinely worth having, entirely separate from persistence, and the
schema above does not preclude it.

### Layout × content

Layout and content are independent axes. This is what makes "markdown on
compare *and* on single" fall out instead of being two features.

```
single                    compare                   stack
┌──────────────┐          ┌──────┬🔒┬──────┐        ┌──────────────┐
│              │          │  A   │  │  B   │        │      A  ~40% │
│      A       │          │      │  │      │        ├──────────────┤
│              │          │      │  │      │        │      B       │
└──────────────┘          └──────┴──┴──────┘        └──────────────┘
```

Only the panel's content column is affected. The tree and narration columns are
unchanged by layout.

`compare` defaults to **percentage-synced** scroll. Line-correspondence sync is
applied only when both slots are diff-derived views of the same file, where an
alignment pass is meaningful. The lock is user-togglable per step.

Rejected: `grid` and any 3+ slot layout. Nothing reads at a third of a pane.

### Content reference schema

```jsonc
{"type":"markdown", "md":"…"}                            // agent authors

{"type":"code",     "ref":"path",                        // daemon reads
                    "symbol":"Register",                 // or "lines":[178,193]
                    "mode":"annotated|plain",
                    "base":"main"}

{"type":"diff",     "ref":"path", "base":"main",
                    "mode":"annotated|unified|split",
                    "whitespace":false,                  // -w
                    "functionContext":true}              // -W

{"type":"payload",  "ref":"testdata/dcr_google.json", "label":"rejected"}
{"type":"payload",  "inline":{…}, "synthetic":true}      // must self-label

{"type":"dataflow", "hops":[{"label","payload"|"ref","code":"path:line","note"}]}
{"type":"diagram",  "mermaid":"…"}
{"type":"map",      "base":"main", "core":["internal/verifier","…"]}
```

`ref` resolves against the session's cwd and is rejected if it escapes the repo
root. `map` carries no data: the daemon derives clusters from `git diff --stat`
and the agent only marks which are load-bearing.

**Sample data must be real.** `payload` prefers `ref` into a fixture. Inline
payloads require `"synthetic":true`, and the renderer labels them visibly.
Invented data that looks like a production capture is worse than no data.

### Diff rendering, from a learner's perspective

Raw `git diff` is built for reviewers who already know the file. Three modes,
chosen per step:

**`annotated` — the default.** The function as it exists *now*, changed lines
tinted, no `-` lines. The reader reads working code, not a delta.

```
┌ registration.go · Register ───────────────────────────┐
│ 183 ▎                                                  │
│ 184 ▎   stmt, err := h.verifier.Unwrap(ctx, req)      │
│ 185 ▎   if err != nil {                               │
│ 186 ▎       badRequest(w, "invalid_software_statement")│
│ 187 ▎       return                                     │
│ 188 ▎   }                                              │
│ 189 ▎   req.RedirectURIs = stmt.RedirectURIs(req)     │
│ 190                                                    │
│ 191     if len(req.RedirectURIs) == 0 {               │
│ 192         badRequest(w, "At least one redirect_uri…")│
│ 193     }                                              │
└────────────────────────────────────────────────────────┘
```

For #6813 this is decisive: the change is *where the block sits* — above line
191. A three-line hunk hides that; the whole function shows it.

**`unified`** when the delta itself is the point (a flipped condition, a
reordered guard). **`split`** when something was replaced wholesale. Split
manages its own two columns and therefore stays `layout:"single"` — diff is
content, never a layout. Collapsing the two would make split-diff and
compare-of-two-files the same shape in the schema and different code paths in
the renderer.

`fileDiff` (`internal/server/git.go:142`) must gain these flags:

```
git diff -M -w --histogram --function-context <base>...<head> -- <path>
         │  │      │              │
         │  │      │              └─ whole enclosing function, not 3 lines
         │  │      └─ better hunk alignment than default myers
         │  └─ ignore whitespace; kills reindent noise
         └─ rename detection; else a moved file is N deletions + N additions
```

`--function-context` is the cheap win — the missing context, with no symbol
index. `-w` is defeatable per step, because occasionally whitespace *is* the
change.

### Navigation: spine and excursions

Four navigation axes would need four mental models. Two suffice.

**Spine.** The deck is linear. `◂ ▸` walk it. Nothing else moves along it.

**Excursion.** Anything clicked — a tree file, a dataflow hop, a code link in
markdown, a cluster in the map — detaches, pushes one breadcrumb, and `esc`
returns to the exact spine position *and scroll offset*. Sub-decks are
excursions, so drilling into a cluster and clicking a stray file are the same
gesture with the same exit.

Breadcrumbs never exceed depth 2. An excursion from an excursion replaces rather
than stacks; three-deep breadcrumbs are how readers get lost.

```
┌ TREE ──────────┐┌ CODE ─────────────────────┐┌ EXPLAIN ───────────────┐
│ ● THE CHANGE   ││ registration.go            ││ #6813 · step 3/9       │
│   statement.go ││ 184 ▎ stmt, err := Unwrap( ││ ⏸ detached · 4 unseen  │
│   ssrf.go   ◂──││ 185 ▎ if err != nil {      ││ ────────────────────── │
│ ● WIRED IN     ││ 186 ▎   badRequest(…)      ││ spine  step 3          │
│   registration ││                            ││  └ ssrf.go     ⎋ esc   │
│ ○ tests        ││                            ││ ────────────────────── │
└────────────────┘└────────────────────────────┘│ Four gates. Only the   │
                                                 │ last is not a string   │
                                                 │ check.                 │
                                                 │ [✓][↓ deeper][? huh]   │
                                                 │ [ ⌁ follow agent ]     │
                                                 └────────────────────────┘
```

Keys:

```
▸ space →   next step         j / k   next / prev changed hunk in this file
◂ ←         prev step         ] / [   expand / collapse context lines
esc         pop excursion     d       cycle annotated → unified → split
⏎           enter excursion   o       open for real, leave the deck
f           follow agent      /       find within step
```

`j/k` is not a luxury: inside a 400-line annotated function the reader needs
hunk-to-hunk hops, and overloading `◂ ▸` for that would blur the spine.

Mobile: swipe L/R is the spine, tap is an excursion, system back is `esc`,
pull-down is follow. Same model, no keys.

**Follow and detach.** The agent drives; the UI follows live. Any manual
navigation detaches. While detached the agent keeps appending and the pane shows
an unseen count — it never yanks the viewport. `f` snaps to the *latest* step,
not to where the agent was when you detached: the reader asked for current, not
for a replay.

### Closing the loop

**The deck is generated once per turn, and the agent then stops.** There is no
waiting tool. The agent builds the deck, ends its turn, and the session goes
idle costing nothing. Navigation is entirely client-side against the stored
deck — this is what the store-a-deck-not-commands decision buys.

When the reader wants more, the UI **injects a prompt**, exactly as the Learn
button does. One mechanism for every interaction, no protocol:

```
reader clicks  ↓ deeper  on step 3
        │
        ▼
Helios sends into s_91cc:
   "Reader wants more depth on step 3 (registration.go:190).
    Append a sub-deck under it. session=s_91cc deck=d_4f2 after_step=3"
        │
        ▼
agent: helios_deck_push({session, steps:[…], after_step:3})
```

The session is warm and the transcript intact, so this is a continuation — the
agent still remembers the deck it just built.

This was originally specified as a blocking `helios_await`. That is impossible:
Claude Code aborts a tool call at roughly 60 s and no documented env var raises
it (see *Spike results*). Prompt injection is not a workaround for that limit —
it is simply the better design, and it removes the tool, the poll window, the
queued-interaction store, and the risk of the agent misreading an idle poll as
"nobody is there".

**Explicit clicks only.** Implicit telemetry — dwell time, scroll depth,
wandering to another file — is gone, and is not missed, because the signals
worth having were already buttons. Selecting lines does not emit anything; it
enables the existing *explain this selection* action. Opening another file does
not notify anyone; that file has its own *explain this file* button.

| Button | Injected prompt carries |
|--------|-------------------------|
| `↓ deeper` | `after_step`, the step's path and lines |
| `? huh` | `at_step`, plus "back up a level, the altitude is wrong" |
| free-text ask | `at_step` and the question |
| `explain this selection` | `path`, line range |
| `explain this file` | `path` |

**Consequence for the store:** `helios_deck` must not wipe an existing deck, and
`helios_deck_push` appends. A sub-deck attaches under `after_step`. Otherwise
clicking `↓ deeper` destroys the deck being read.

Signals:

| Source | Emitted |
|--------|---------|
| `✓ got it` / `↓ deeper` / `? huh` buttons | `{kind, step}` |
| free-text box in the explain pane | `{kind:"ask", step, text}` |
| editor selection | `{kind:"selected", path, lines}` |
| excursion to an unrelated file | `{kind:"wandered", path}` |
| ≥3 steps advanced in <2 s | `{kind:"skimmed", from, to}` |

`? huh` twice consecutively is the strongest signal in the set — it means the
altitude is wrong, not that more detail is needed.

Explicitly excluded: dwell-time and scroll telemetry. Too noisy, and it makes
the agent chase ghosts.

## Tool surface

Seven tools enabled by default, two gated. Deliberately excluded: `read_file`,
`grep`, `bash`, `git_*`, `pr_fetch` — the harness does all of these better, and
a second filesystem API only splits the agent's attention. The Helios MCP server
exposes what **only Helios owns**: the display, the human, and other sessions.

### Tier 1 — deck

```
helios_sessions(filter?)                        → id, project, cwd, title, status
helios_deck(session, title, kind)               open or reuse; never wipes
helios_deck_push(session, steps[], after_step?) → {cursor, warnings[], resolved{}}
helios_deck_end(session)
```

No waiting tool: the agent builds and stops. `after_step` attaches a sub-deck
rather than appending at the tail.

`helios_sessions` is Tier 0, not a Tier 3 extra: it is how a cold-started agent
finds a target.

Batching matters. Six separate step tools would mean six round trips for Acts 1
and 2; one batched push lands them together and halves time to first paint.

`helios_deck_push` returns a useful result rather than an ack:

```jsonc
{ "cursor": 4,
  "warnings": ["step 2 slot 1: testdata/dcr_google_unwrapped.json not found"],
  "resolved": {"internal/oauth/registration.go#Register": [178, 193]} }
```

`resolved` lets the agent cite true line numbers in later prose without reading
the file again. `warnings` closes the correction loop without involving the
reader.

### Tier 2 — the human

| Tool | Backed by | Surfaces on |
|------|-----------|-------------|
| `helios_ask(question, options[], timeout_s)` | `internal/hitl/hitl.go:95`, already session-scoped | terminal overlay, desktop, phone |
| `helios_notify(title, body, level)` | `internal/notifications`, `internal/push` | phone push |

`helios_ask` is near-free wiring and is arguably the highest-value tool here —
it is the reason Helios exists.

### Tier 3 — driving other sessions (gated)

```
helios_session_send(session, text)     types into another session   default OFF
helios_session_start(cwd, prompt, …)   spawns                       default OFF
```

Both sit behind a setting in `internal/store/settings.go`, off until turned on.
Drawing a deck on another session's pane is *not* gated — worst case is a
confusing panel — but typing into another agent is a different act.

### Worked example — #6813, Acts 1 and 2

```jsonc
helios_deck({session:"s_91cc", title:"#6813 RFC 7591 software statements",
             kind:"explain"})

helios_deck_push({session:"s_91cc", steps:[
  {layout:"single", caption:"the idea",
   slots:[{type:"markdown", md:
     "Google Gemini Enterprise tried to register on **2026-08-14** and got a
      400 in 1.4 ms.\n\nThe redirect URI it needed was inside a signed JWT we
      did not know how to open."}]},

  {layout:"compare", caption:"same request, before and after",
   slots:[{type:"payload", ref:"testdata/dcr_google.json",   label:"rejected"},
          {type:"payload", ref:"testdata/dcr_unwrapped.json", label:"after"}]},

  {layout:"single", caption:"where the 400 came from",
   slots:[{type:"dataflow", hops:[
     {label:"POST /oauth/register", ref:"testdata/dcr_google.json"},
     {label:"decode drops the unknown field", code:"internal/oauth/registration.go:179"},
     {label:"RedirectURIs = nil"},
     {label:"400", code:"internal/oauth/registration.go:191",
      note:"before the allowlist is even loaded"}]}]},

  {layout:"stack", caption:"what the PR inserts",
   slots:[{type:"diagram", mermaid:"flowchart LR\n A[decode]-->B[unwrap]-->C[SSRF gates]-->D[allowlist]"},
          {type:"code", ref:"internal/oauth/registration.go", symbol:"Register",
           mode:"annotated", base:"main"}]}
]})
```

Roughly 700 output tokens for four steps that render several hundred lines of
real, current code.

Push progressively — Act 1 paints while the agent is still reading files.

## The skill: a large PR is not a big small PR

Ships at `.claude/skills/helios-explain/SKILL.md`. It is a decision table run
*before* the first `helios_deck_push`, not prose.

```
measure: files changed, lines added, packages touched, test ratio

  ≤5 files, ≤300 lines   LINEAR      every hunk. no map step. code-first.
  ≤15 files              THEMED      2-4 clusters. map optional.
                                     core file per cluster only.
  >15 files or >1000 ln  MAP-FIRST   mandatory step 0 map.
                                     one sentence naming the single idea.
                                     ≤4 load-bearing files; ignore the rest.
                                     diagram before code.
                                     say what was skipped.
                                     ≤12 steps, then stop.
```

Deck skeleton for MAP-FIRST and THEMED:

```
ACT 1  the idea      1-2 steps   why it exists; before/after. no code.
ACT 2  dataflow      2-4 steps   real payload through the hops. code links only.
ACT 3  the code      ≤4 steps    load-bearing files, each anchored to a hop.
ACT 4  evidence      1-2 steps   tests as proof; gotchas; what was skipped.
       ── end the turn. the reader drives from here ──
```

Rules worth encoding explicitly:

- **No code until the reader can predict what the code must do.** Acts 1–2 build
  the prediction; Act 3 confirms it. Reversed, every hunk is memorisation.
- Lead with why the PR exists, not with what changed.
- Tests are evidence, not a cluster to walk.
- Name what you skipped. Otherwise "not covered" is indistinguishable from
  "nothing there".
- Sample data comes from fixtures, the PR body, or a live capture. Synthesised
  payloads carry `synthetic:true`.

For MAP-FIRST decks the tree pane also switches from directory order to cluster
order, showing changed files only.

## Changes

| File | Change |
|------|--------|
| `internal/mcp/server.go` | new. streamable-HTTP MCP, stdlib only: JSON-RPC framing, tool registry, dispatch |
| `internal/mcp/tools_deck.go` | new. Tier 1 |
| `internal/mcp/tools_human.go` | new. Tier 2 → `hitl`, `notifications` |
| `internal/mcp/tools_sessions.go` | new. `helios_sessions`; Tier 3 setting-gated |
| `internal/mcp/resolve.go` | new. `ref`/`symbol` → path+lines; repo-root containment; grep-based Go symbol lookup |
| `internal/store/decks.go` | new. deck, steps, cursor, per session; queued interactions |
| `internal/server/decks.go` | new. `GET /api/sessions/{id}/deck`, `POST …/deck/interact`; broadcasts `deck_updated` |
| `internal/server/server.go` | mount `/mcp` on the **internal** mux (loopback, no auth); register the deck route among the session paths |
| `internal/server/git.go:142` | `fileDiff` gains `-M -w --histogram --function-context`, each togglable |
| `internal/store/settings.go` | `mcp.session_write_tools` (default false) |
| `desktop/src/renderer/components/detail.tsx:20` | add `'learn'` to `PANELS` |
| `desktop/src/renderer/components/learn.tsx` | new. panel: empty-state launcher, spine/excursion, follow-detach, feedback controls |
| `desktop/src/renderer/components/slots.tsx` | new. layout container + content dispatch |
| `desktop/src/renderer/components/file-tree.tsx` | accept external reveal; cluster grouping mode |
| `desktop/src/renderer/components/editor.tsx` | line-band decorations, scroll-to, selection → interaction |
| `desktop/src/renderer/components/diff-view.tsx` | annotated mode |
| `desktop/src/renderer/store.ts:391` | handle `deck_updated` |
| `desktop/package.json` | mermaid |
| `.claude/skills/helios-explain/SKILL.md` | new |

Mobile is deliberately out of scope for the first cut; the deck model makes it
additive later.

## Risks

- **Authless `/mcp` on loopback.** Any local process can drive any session's
  pane and, if the gate is opened, send text into other sessions. Mitigation:
  loopback bind asserted in a test; Tier 3 writes default off. Revisit the
  moment the daemon binds wider.
- **`helios_deck` wiping a live deck.** Clicking `↓ deeper` mid-read must not
  destroy what is on screen. Mitigation: open-or-reuse semantics, and a test
  that a second `helios_deck` on the same session preserves existing steps.
- **`ref` path traversal.** `resolve.go` must reject anything outside the repo
  root — the agent controls the string and the daemon reads the file.
- **Mermaid bundle size.** Adds meaningfully to the renderer. Lazy-load on the
  first diagram step.
- **Symbol lookup by grep** is roughly 90 % right for Go and wrong for
  overloaded or generic names. Fall back to explicit lines, and report the
  resolution in `resolved` so the agent can see what it got.
- **Deck replaces on re-begin.** An agent that calls `helios_deck` twice destroys
  what the reader was mid-way through. Warn in the tool result; consider
  refusing while an excursion is open.
- **Injected prompts landing in a busy session.** A reader clicking `↓ deeper`
  while the agent is mid-turn queues a prompt behind whatever it is doing.
  Mitigation is existing behaviour (`docs/specs/37-prompt-delivery-reliability.md`);
  the button should show that the request is queued rather than appear inert.

## Testing

- `internal/mcp`: handshake (`initialize`, `notifications/initialized` → 202,
  `tools/list`, `tools/call`), unknown method → -32601, unknown `session`
  rejected, gated tools refuse when the setting is off.
- `internal/mcp/resolve.go`: `../` escapes rejected; symbol hit, miss, and
  ambiguous; line ranges past EOF clamped with a warning.
- `internal/store/decks.go`: append, cursor, per-session isolation, `after_step`
  attachment, and a second `helios_deck` preserving an existing deck.
- `internal/server/git.go`: golden diffs for each flag combination, including a
  rename and a pure-reindent change, proving `-M` and `-w` do what the spec
  claims.
- `internal/server/decks.go`: push broadcasts exactly one `deck_updated`; a
  feedback click injects exactly one prompt carrying `after_step`.
- Desktop: spine advance, excursion push/pop restoring scroll offset, navigation
  working with no agent attached at all.
- End-to-end against a fixture repo: build a four-step deck, assert the rendered
  panel, click `↓ deeper`, assert a sub-deck attaches under step 3 without
  disturbing steps 1–4.

## Implementation order

1. `internal/store/decks.go` + `internal/server/decks.go` + `deck_updated`.
   Verifiable with curl before any MCP exists.
2. `internal/mcp` with Tier 1 only, mounted and reachable. The spike server is
   the starting point.
3. `learn.tsx` empty-state launcher + `'learn'` in `PANELS` + prompt templates —
   the agent can now be pointed at a session for real.
4. `slots.tsx` with `markdown` and `code` content only, `single` and `compare`.
   This is the first end-to-end demo.
5. `fileDiff` flags + `diff` content + annotated mode.
6. `dataflow`, `payload`, `map`, `diagram`, `stack`.
7. Excursions, feedback buttons and their prompt templates.
8. Tier 2 tools.
9. The skill.
10. Tier 3 behind the setting.

Steps 1–4 are the demo. Everything after is depth.

## Success criteria

- Explaining #6813 costs under 2k output tokens for the deck itself and paints
  its first step within 5 s of the request.
- No source or diff text appears in the agent's tool arguments.
- A reader who clicks `↓ deeper` on any step gets a drilled sub-deck without
  re-prompting the session.
- `git diff --function-context` output makes the *placement* of the unwrap block
  visible without the reader opening the file.
- Turning the MCP server off leaves every existing surface working unchanged.

## Open questions

1. **Layout by role or by name?** `{"role":"before_after"}` mapped to a layout by
   the skill yields more consistent decks; raw `layout` is more expressive and
   one more thing for the agent to get wrong. Leaning: role for the common
   cases, `layout` as an escape hatch.
2. **When does `wandered` fire** — immediately on excursion, so the agent can
   react while the reader reads, or deferred until `esc`, so the deck is not
   rewritten mid-read? Leaning immediate, since the pane never auto-jumps while
   detached.
3. **Revision.** The agent learns at step 7 that step 3 was wrong. Add
   `helios_deck_revise(step_id, patch)`, or accept the stale step? Revise is
   correct and pushes a step-id concept through the whole schema. Leaning: ship
   without it, add if it hurts.
