# 36 — Helios-Owned HITL

Every human-in-the-loop moment — permission, question, elicitation, workspace
trust — is rendered by helios, on every surface, from one state machine in the
daemon. The CLI's own dialogs never render, and helios never types into a
terminal to answer one.

## Problem

Helios has tried both halves of this and shipped neither whole.

### Flow A — the original: the right shape, two missing pieces

This is the flow this spec restores. Blocking the hook was never the mistake.
It shipped without a terminal UI and without a working way to return the
answer, and got abandoned for the first of those.

```
Claude calls AskUserQuestion
    |
    v
PreToolUse /question  ──────────────────────────┐
    +-- create claude.question notification     │  tool NOT allowed to run
    +-- SSE → phone card                        │  CLI renders NOTHING
    +-- waitForDecision(5 min) ─────────────────┤  terminal shows a spinner
    |                                           │
    |   phone answers ──→ Decision{answered}    │
    |                                           │
    v                                           │
return permissionDecision: "allow"  ────────────┘
       + updatedInput{answers: {...}}
    |
    v
CLI runs AskUserQuestion and renders its OWN picker
    ("answers" is not in the tool's input schema — dropped on the floor)
```

Two gaps, neither of them the blocking hook itself:

- **No terminal surface.** The tool never ran, so the CLI rendered nothing and
  there was nothing to answer. The phone was the only possible answerer. This is
  what killed it — and it is a missing UI, not a broken mechanism.
- **No working answer channel.** `allow` means the tool runs anyway, and
  `answers` is not part of `AskUserQuestion`'s input, so it was dropped. The hook
  only ever added latency.

Recorded outcome (`docs/specs/34-askuserquestion-dual-answer.md:9-19`): every
question ever raised on that machine resolved `source=claude` with an empty
response — the cancellation path. Four for four. Never once answered.

Gap one is what this spec builds: helios renders the HITL, so blocking the tool
costs nothing. Gap two is still open and is tracked in Open Questions — it was
never solved by Flow B either, only routed around.

### Flow B — the detour: let the CLI own it, type the answer in

Spec 34 inverted the model. The hook returns `{}` immediately, the CLI renders
its own picker, and remote clients answer by driving that picker over the PTY.

It closes gap one by handing the terminal back the CLI's own UI, and sidesteps
gap two entirely — with no hook response, there is nothing to return. The price
is that the answer stops being a value and becomes a keystroke guess.

```
Claude calls AskUserQuestion
    |
    v
PreToolUse /question → {} immediately
    +-- create claude.question notification → phone
    |
    v
CLI runs AskUserQuestion, renders its picker    ← terminal answerable ✓
    |
    +── terminal answers ──→ PostToolUse ──→ resolveSessionQuestions
    |                                          retract the phone card
    |
    +── phone answers ──→ POST /api/notifications/{id}/action
                              |
                              v
                          handleQuestionAction
                              +-- Capture(session) → rendered screen text
                              +-- condenseAll(question, header) — needle match
                              +-- SendKey(Down) × option_index, 40ms apart
                              +-- SendKey(Enter)
                              +-- return nil          ← ALWAYS
                              |
                              v
                          Decision{answered} → notification resolved
```

Both surfaces work, which is why this shipped. But the answer path is a guess
dressed as a result:

1. **`answerQuestion` never verifies the answer landed** (`actions.go:183`). It
   sends keys and returns `nil` unconditionally. A dropped keystroke is
   indistinguishable from success — the notification resolves `answered`, the
   phone card disappears, and the CLI is left sitting at an unanswered question
   with no surface able to answer it.
2. **The screen guard matches text that is not the dialog.**
   `awaitQuestionOnScreen` (`actions.go:202`) matches condensed `question` *or*
   `header` anywhere on the visible screen. A header is ≤ 12 characters and
   naturally echoes the prose printed directly above the dialog. Observed:
   assistant text containing "Banner scope" and "foreground watchdog" matched
   two of three headers before the dialog had rendered.
3. **No settle delay after `Enter`** (`actions.go:193`) — the next question's
   guard runs against a pre-repaint screen.
4. **Coupled to Claude's rendering.** Any layout change breaks injection.

Observed failure, 2026-08-12:

```
18:55:20  claude.question raised            notif-5213fd26a5d0b972
18:56:03  phone answers [{0,0},{1,0},{2,1}]
          daemon: "question-action: answered 3 question(s)"
          notification resolved  status=answered
          keys actually sent: Enter, Enter, Down, Enter
18:56:32  CLI transcript: "toolDenialKind": "user-rejected"
                          "[Request interrupted by user for tool use]"
18:56:48  claude.stop
```

Helios reported success. Claude recorded a rejection.

That `user-rejected` is a **real interrupt** — the CLI sat at a dialog nobody
could answer until something cancelled it. It is not what a hook deny looks
like; see Mechanism, where the same field reads `permission-rule`. The two are
easy to conflate and mean opposite things.

### The assumption that forced the detour

Spec 34 named it explicitly:

> **Why PTY injection rather than a hook response**
> Because it is the only way both surfaces can work. A blocking hook response
> is, by construction, an answer given *instead of* the CLI's UI — the terminal
> can never participate.

The premise is "*the CLI's* UI". It holds only while the CLI's dialog is the
only dialog there is. Give helios one and the sentence stops being true: a
blocking hook response is an answer given instead of a UI helios also owns, and
the terminal participates through that instead.

Removing the assumption does not produce a third design. It puts Flow A back.

### The same hole, still open, for permissions

`handlePermission` (`hooks.go:78`) blocks in `waitForDecision` for five minutes
and the CLI renders nothing. A terminal-only user cannot approve a tool at all —
they watch a spinner until it times out into a deny. The phone is mandatory.
This is not a hypothetical cost of the new design; it is the current behaviour.

## Design

Restore Flow A and close gap one: helios renders the HITL. On every surface.
From one place.

```
Claude needs a human
    |
    v
blocking hook (PreToolUse / PermissionRequest / Elicitation)
    |
    v
┌─────────────────────── daemon ────────────────────────┐
│  HITL state machine                                   │
│    spec: prompt, options, selection, multi-select     │
│    one pending decision, first answer wins            │
│                                                        │
│    ├─ render ANSI  ──→ terminal host ──→ all viewers  │
│    └─ render JSON  ──→ SSE ──────────→ phone/desktop  │
└────────────────────────────────────────────────────────┘
    |                              |
    v                              v
overlay in the terminal        card on the phone
    |                              |
    └──────────┬───────────────────┘
               v
        Decision resolved → hook returns → Claude continues
```

The CLI's dialog never renders, because the tool never runs. That is now a
feature: helios owns the presentation on both surfaces, so nothing depends on
how Claude Code draws anything.

### Flow C — Flow A, with helios rendering the UI

Same hook, same block, same `waitForDecision`. Two additions, marked `NEW`:

```
Claude calls AskUserQuestion
    |
    v
PreToolUse /question  (blocking — unchanged from Flow A)
    +-- create claude.question notification
    +-- Mgr.Register(notifID)
    +-- SSE → phone card
    +-- FrameOverlaySet → terminal host                          ← NEW
    |      composited for every viewer, present in the snapshot
    +-- waitForDecision
    |
    |   ┌─ terminal: ↓ ↓ Enter into the OVERLAY                  ← NEW
    |   │     host routes FrameInput → daemon as FrameOverlayInput
    |   │     daemon updates selection, re-renders, resolves on Enter
    |   │
    |   └─ phone: POST /api/notifications/{id}/action
    |         handleQuestionAction parses selections → Decision
    |
    v
Decision{answered, selections}
    +-- FrameOverlayClear → host → fresh snapshot, input to PTY  ← NEW
    +-- SSE retract → phone card disappears
    +-- hook returns deny + selections in the reason  (see Mechanism)
```

Against Flow A:

| | Flow A | Flow C |
|---|---|---|
| Hook | blocks in `waitForDecision` | unchanged |
| CLI dialog | never renders | unchanged |
| Terminal surface | none — gap one | helios overlay |
| Phone surface | notification card | unchanged |
| Answer to Claude | `allow` + `updatedInput{answers}`, dropped | `deny` + reason — verified |

The only thing Flow C adds is the terminal surface. That is the entire delta,
and it is what makes blocking the tool cost nothing.

No `Capture`. No `SendKey`. No timing. The answer either resolves the decision
channel or it does not, and nothing reports success unless it did.

### Where the overlay lives

The ptyhost is a separate process. The HITL brain is **not** in it.

```
   daemon (one HITL state machine)
      │
      │  mirror connection, already long-lived, per session
      │  internal/backend/host.go:115 → terminal/mirror.go:47
      │
      │   ──FrameOverlaySet(spec)──────→ │
      │   ──FrameOverlayClear──────────→ │  ptyhost
      │   ←──FrameOverlayInput(keys)──── │  (renderer + compositor)
      │                                  │
      │                                  ├─ Screen (vt emulator, untouched)
      │                                  ├─ draw the box at the live geometry
      │                                  ├─ composite it over snapshot/output
      │                                  └─ route viewer input to daemon
      │                                        │
      │                                        ├─→ helios attach
      │                                        ├─→ TUI preview
      │                                        └─→ desktop app terminal
```

The host's contract is three rules:

1. While an overlay is set, composite it over `Screen.RenderSnapshot()` for
   every viewer — including the snapshot sent to a viewer that attaches
   mid-question (`terminal/host.go:465`).
2. While an overlay is set, route `FrameInput` from **interactive** viewers to
   the control connection instead of the PTY. Observers see it, cannot answer.
3. If the control connection drops, clear the overlay and give input back to
   the PTY.

Rule 3 is the safety property. A daemon crash mid-question must degrade to a
plain working terminal, never a session that swallows keystrokes.

The daemon owns state because the terminal render, the phone JSON, the
highlighted option, and "who answered first" all have to agree. One brain, two
renderers. The PTY screen underneath is never written to, so dismissing is
`FrameOverlayClear` plus a repaint.

The wire carries *what the modal says*, not the bytes that draw it: title, body,
options, selected index, footer. Only the host knows the negotiated geometry, so
only the host can lay the box out — and a resize is then handled where the new
size already is, with no round trip back to the daemon. The daemon still decides
every word and which option is highlighted; it just does not count columns.

### One path for four events

| Event | Answer mechanism | Change |
|---|---|---|
| `PermissionRequest` | `allow` / `deny` + `updatedInput` + `updatedPermissions` | Already blocking. Overlay is pure addition — **fixes terminal-can't-approve** |
| `Elicitation` | `hookSpecificOutput{action, content}` — a first-class response, not a verdict | Already blocking and implemented (`hooks.go` `handleElicitation`). Overlay is pure addition |
| Workspace trust | Enter / Ctrl-C into the CLI's own dialog | **Unchanged** — the one HITL moment with no hook to hang an overlay on. See below |
| `AskUserQuestion` | `deny` + selections in `permissionDecisionReason` — see Mechanism | Returns to blocking |

## Changes

### `internal/terminal/protocol.go` — overlay frames

```go
FrameOverlaySet   FrameType = 0x0A  // control → host: an Overlay, as JSON
FrameOverlayClear FrameType = 0x0B  // control → host
FrameOverlayInput FrameType = 0x0C  // host → control: keystrokes
```

```go
RoleControl Role = "control"  // observer, plus the right to set overlays
```

### `internal/terminal/overlay.go` — the modal and how it draws

```go
type Overlay struct {
    Title    string
    Body     []string
    Options  []string
    Selected int
    Footer   string
}

func RenderOverlay(o Overlay, cols, rows int) []byte
```

`RenderOverlay` is bracketed by `\x1b7` / `\x1b8` and hides the cursor, so it can
be re-stamped after any PTY output without disturbing the application
underneath. It anchors to the bottom of the viewport — where a CLI puts its own
prompt — clips from the top when the box is taller than the screen, and returns
nil when the terminal is too small to draw anything legible.

`serveConn` coerces unknown roles to `RoleObserver` (`host.go:479`), so a daemon
talking to a stale ptyhost degrades to an observer connection and simply gets no
overlay. The daemon must detect that and fall back to phone-only — which is
exactly today's permission behaviour, so the version-skew path is already the
one users have.

### `internal/terminal/host.go` — compositing and input routing

`broadcast` and the `serveConn` catch-up path composite the overlay when one is
set. `FrameInput` from interactive viewers is forwarded to the control viewer
instead of `Write`. Overlay cleared on control disconnect.

The control connection is the one viewer that never receives the box. It feeds
the daemon's mirror, and `Capture` has to report what the agent drew, not what
helios drew over it. A resize re-stamps the overlay, because an application
blocked in a hook will not redraw itself.

### `internal/daemon/hooks.go` — restore the question timeout

Spec 34 dropped `timeout` from the `/question` PreToolUse entry because it no
longer blocked. It blocks again, so the timeout comes back — and must exceed
`waitForDecision`'s local timer, or the two race the way spec 34 documented at
`34:49-55`. Pick one number and derive both from it. ✅ done: `hookConfig` sets
`"timeout": claude.HookTimeoutSeconds` on all three blocking hooks
(`/permission`, `/elicitation`, `/question`), and that constant is
`decisionTimeout + 30s` — 330s against the daemon's 300s, so helios always gives
up first. This moves `HookConfigHash`, so installed hooks read as outdated once
and get rewritten.

The `*` PreToolUse matcher and the `AskUserQuestion` skip in `handleToolPre`
stay exactly as they are. That fix is independent of which HITL model wins.

### `internal/provider/claude/hooks.go`

`handleQuestion` blocks on `waitForDecision` again, alongside a
`Mgr.Register(notifID)` and an overlay set/clear around the wait. On a decision
it returns `permissionDecision: "deny"` with the selections formatted into
`permissionDecisionReason` — see Mechanism. Formatting lives in one function
with its own golden test, because that string is what Claude actually reads.

~~Delete the `AskUserQuestion` bypass at `hooks.go:91`.~~ **Kept.** The reason it
was written — the CLI's question UI fighting a blocking permission hook — is
gone, but ordering between the two PreToolUse hooks is the CLI's to decide, and
the question hook denies the tool, so the permission hook should never see the
event at all. If it ever does, an approval box asking whether Claude may ask a
question is nonsense on any surface. The bypass costs one comparison.

`resolveSessionQuestions` on `PostToolUse` is no longer load-bearing — nothing
outside the hook can answer. Keep it as the backstop for the `idle_prompt`
escape path (`hooks.go:534`).

### `internal/provider/claude/actions.go` — mass deletion

`handleQuestionAction` becomes the shape of `handlePermissionAction`
(`actions.go:25`): parse the body, return a `Decision`. Roughly five lines.

Deleted: `answerQuestion`, `awaitQuestionOnScreen`, `condense`, `condenseAll`,
`keyDelay`, `screenSettleTimeout`, `answerLocks`, `sessionAnswerLock`, and the
old `questionSpec`/`questionPayload` (moved to `question.go` next to the code
that reads them). `handleTrustAction` keeps its injection — see below.

### `internal/server/trust_watcher.go` — kept, and why

Screen matching was to go once trust moved onto the overlay. It does not move.

Trust is the one HITL moment with no hook: the dialog is drawn before the
session has a transcript, and Claude Code exposes no hook, flag, or subcommand
for it (`claude --help`, `claude project --help`). Rendering it as an overlay
would mean helios deciding trust on the CLI's behalf and writing
`hasTrustDialogAccepted` into `~/.claude.json` — helios answering a question the
CLI asked, by editing the CLI's private state. That is a worse trade than
screen scraping, so `trust_watcher.go` and the Enter/Ctrl-C in
`handleTrustAction` stay as they are.

The cost is honest: trust remains the one prompt where helios types into the
terminal, and the one that breaks if the dialog's wording changes.

## Mechanism: how the answer reaches Claude — gap two, closed

PreToolUse's output vocabulary is `allow` / `deny` / `ask` plus `updatedInput`.
There is no "here is the tool's result, skip execution", so the answer rides back
as `deny` with the selections in `permissionDecisionReason`.

Measured against Claude Code 2.1.232 (Opus 5, Vertex), interactive session, a
command hook on `PreToolUse` matching `AskUserQuestion`:

```json
{"hookSpecificOutput":{
  "hookEventName":"PreToolUse",
  "permissionDecision":"deny",
  "permissionDecisionReason":"Answered by the user in helios.\n1. Banner scope -> \"Only the active host\"\n..."}}
```

Transcript recorded:

```
assistant   tool_use    AskUserQuestion  (3 questions)
user        tool_result is_error=true, content = the reason, verbatim
            toolDenialKind = "permission-rule"
assistant   text        "1. Banner scope — Only the active host
                         2. Wake strategy — Heartbeat watchdog
                         3. Rollout — Behind a flag"
```

Findings:

- **Claude reads it as the answer.** It continued in the same turn, correctly, on
  both a single-question and a three-question payload. No retry, no apology, no
  second `AskUserQuestion`.
- **`toolDenialKind` is `permission-rule`, not `user-rejected`.** The
  `user-rejected` seen in the 18:56 failure came from a real user interrupt, not
  from a hook deny. A hook deny reads as "a rule blocked this", which is accurate.
- **No `[Request interrupted by user for tool use]`.** That artifact is also
  specific to a real interrupt.
- **The reason survives verbatim**, newlines included, so a multi-question payload
  answers in one round trip.

Costs, both accepted:

- `is_error: true` on the `tool_result`. The answer is carried on the error
  channel. Behaviour was clean here; the residual unknown is whether it reads
  oddly much later in a long conversation or after compaction. Cheap to revisit
  if it ever does.
- The CLI renders the line as `⎿ Error: Answered by the user in helios. …`
  under the overlay. Honest, mildly ugly.

The reason string is therefore load-bearing and belongs in one place with its
own test. Include an explicit "these are the user's answers, do not ask again"
clause — it was present in the verified payload.

Tool substitution over MCP is **not** needed and is dropped from the plan. It
stays recorded here only as the fallback if `is_error` ever proves to matter.

### Settled: the five-minute ceiling

Under injection the hook returned instantly, so the timeout was theoretical.
Blocking makes it real, and now a human is looking at an overlay when it expires.

The budget stays at five minutes. Raising it trades a bounded wait for a session
that can hang for an hour on a prompt nobody is looking at, and expiry is already
survivable on both sides: `waitForDecision` returning nil takes the overlay down
with the hook's own `defer`, and the question path answers Claude with
`unansweredReason` — "nobody answered in time, continue with your best judgement
rather than asking again" — so the turn keeps going instead of stalling or
re-asking. What the ceiling must not do is expire *after* the CLI has already
walked away, which is what deriving `HookTimeoutSeconds` from `decisionTimeout`
prevents.

### Note for testing

`AskUserQuestion` is **not registered in headless `-p` mode** — the tool does not
exist without an interactive session, and the hook never fires. Any end-to-end
test of this path has to drive a real PTY.

## Risks

**The overlay hides output the user wants to see.** It composites over the
screen. Mitigation: a key to collapse it to one line, and clear-plus-repaint on
resolve so nothing is lost — the `Screen` underneath was never written to.

**A viewer attaches mid-question.** Handled by rule 1: the overlay is part of the
snapshot, not a one-shot broadcast.

**Two interactive viewers answer at once.** The decision channel already
serialises this — `Mgr.Register` reserves the slot before publishing
(`hooks.go:130-132`), first answer wins, the rest get a retraction. Same
mechanism the phone and terminal use against each other.

**Daemon dies mid-question.** Rule 3. Overlay clears, input returns to the PTY,
the hook's own timeout ends the wait.

**Stale ptyhost without overlay support.** Falls back to phone-only.

## Testing

Go:

1. `terminal/overlay_test.go` — frame round-trip, and the render: title, body,
   options, the highlight, bottom anchoring, clipping from the top, uniform row
   width regardless of styling, and nothing drawn into a terminal too small.
   ✅ done.
2. `terminal/overlay_host_test.go` — with an overlay set, a snapshot to a new
   viewer contains it and the control connection is never painted with it;
   `FrameInput` from an interactive viewer reaches the control connection and
   **not** the PTY; an observer's input reaches neither; a non-control viewer
   cannot set one. ✅ done.
3. `terminal/overlay_host_test.go` — control disconnect clears the overlay and
   the next `FrameInput` reaches the PTY. ✅ done.
4. `terminal/attach_e2e_test.go` — end-to-end overlay render and answer over a
   real `helios ptyhost` and a real attach. ✅ done.
5. `hitl/hitl_test.go` — the state machine against a fake overlay surface: the
   paint, arrow keys and digits, the ends of the list, Enter and Escape,
   answer-at-most-once, release idempotence, no terminal, a failed paint leaving
   nothing pending, input for an unprompted session. `hitl/keys_test.go` covers
   the decoder, including coalesced reads. ✅ done.
6. `provider/claude/hooks_test.go` — permission painted, approved and denied
   **from the terminal**, Escape denies, allow-always carries the suggestion,
   the phone's answer takes the overlay down, and no terminal (or no controller)
   still leaves the hook waiting for the phone. ✅ done.
7. `provider/claude/hooks_test.go` — elicitation: URL mode answerable from the
   terminal, form mode offering only Decline, the phone still filling the form.
   ✅ done.
8. `provider/claude/hooks_test.go` — `handleQuestion` blocks, registers a
   decision slot, and returns the answer once one arrives, from either surface;
   an unanswered question says so; a free-text question is left to the phone.
   ✅ done.
9. `provider/claude/question_test.go` — golden test on the
   `permissionDecisionReason` string for a multi-question payload, including the
   "do not ask again" clause, plus free text, partial answers, skip, timeout and
   out-of-range indices. ✅ done.
10. `provider/claude/actions_test.go` — `handleQuestionAction` returns a
    `Decision` and touches no backend. ✅ done.

Manual (needs a real PTY — see the headless caveat above):

11. Permission raised, approved **from the terminal** — the flow that has never
    worked.
12. Question answered from the terminal; phone card clears.
13. Question answered from the phone; terminal overlay clears mid-render.
14. Attach after a question is already pending; overlay is there.
15. Kill the daemon with an overlay up; terminal returns to normal and stays
    usable.

## Implementation order

1. Overlay frames, the box renderer, host compositing + input routing.
   Self-contained. ✅ done.
2. Daemon HITL state machine: one pending prompt per session, driving the
   overlay and the phone's JSON from the same state. ✅ done — `internal/hitl`.
3. **Permission over it.** Zero mechanism risk, and it fixes the
   terminal-can't-approve hole. The right place to prove the stack. ✅ done.
4. Elicitation and trust onto the same path; delete `trust_watcher.go` scraping.
   ✅ elicitation done. Trust does **not** move — it has no hook; see
   `trust_watcher.go` above.
5. Question: settle the timeout ceiling, restore blocking, delete injection.
   The answer mechanism is already settled. ✅ done.

Steps 1–3 stand on their own merit even if 5 slips.

## Notes

- Flow B is gone with step 5. The phone's wire contract survived it unchanged —
  `{action, selections, text}` — so the card kept working across the switch; only
  its comments, which described driving the CLI's highlight, needed correcting.
- An overlay is one list of choices, so a multi-question `AskUserQuestion` is
  walked one question at a time (`questionAsker`), and `multiSelect` is answered
  with a single choice on every surface.
- The double-dispatch fix (`handleToolPre` skipping `AskUserQuestion`) and the
  duplicate-timeout race are spec 34's, and survive this spec unchanged. They
  were never about which surface renders.
- `waitForDecision` becomes the single wait for all four events rather than
  three, which is what it was always shaped for.
