# 34 — AskUserQuestion: Answerable From the Terminal *and* the Phone

## Problem

When Claude asks a question, it appears on the phone but the terminal is stuck
showing a spinner with nothing to answer. In practice neither side answers it:
**all four questions ever recorded on this machine expired unanswered.**

```
id                      created              resolved             source  response
notif-e643ad5b02d3e559  2026-08-11 09:58:44  2026-08-11 10:00:32  claude  (empty)
notif-a30c2a1b370b096c  2026-08-11 11:29:46  2026-08-11 11:34:46  claude  (empty)
notif-6adfcaf3b84f1399  2026-08-11 12:05:21  2026-08-11 12:09:11  claude  (empty)
notif-3cd48c61179265ff  2026-08-11 15:34:42  2026-08-11 15:46:10  claude  (empty)
```

Every one resolved with `resolved_source=claude` and an empty response — that is
the cancellation path, not an answer. The feature has never once worked
end to end.

### 1. The hook blocks, so the CLI never renders its own UI

`hookConfig` (`internal/daemon/hooks.go:39-49`) registers `/question` as a
`PreToolUse` hook matching `AskUserQuestion`, with a 300-second timeout.
`handleQuestion` (`hooks.go:162`) then blocks in `waitForDecision` for up to five
minutes waiting for a **mobile** answer.

While that hook is outstanding the tool has not been allowed to run, so the CLI
has not rendered its question UI. There is no code path by which the terminal
user can answer. The phone is the only possible answerer, and if it does not
answer, the question is denied.

### 2. Both PreToolUse matchers fire on the same event

`hookConfig` registers `PreToolUse` twice — `AskUserQuestion` → `/question`, and
`*` → `/tool/pre`. Both match `AskUserQuestion`. Confirmed in `daemon.log`:

```
15:58:44 hook: dispatching claude.question (2486 bytes)
15:58:44 hook: dispatching claude.tool.pre (2486 bytes)
```

Identical payload size — the same event, delivered twice. `handleToolPre`
(`hooks.go:667`) then writes status `active`, overwriting the
`waiting_permission` that `handleQuestion` set moments earlier. So the session
list never shows the session as needing an answer; only the notification hints
at it.

### 3. Two independent five-minute timeouts race

`waitForDecision` runs a local 5-minute timer (`hooks.go:590`) while the hook
config declares `"timeout": 300`. Whichever fires first decides how the event is
recorded: the request context cancelling gives `resolved`/`claude`
(`hooks.go:611`), the local timer gives `timeout`/`system` (`hooks.go:607`). The
`notif-a30c2a1b370b096c` row above expired at exactly 5m00s — that is this race.

## Design

Invert the model. Today helios intercepts the question and tries to answer it on
Claude's behalf. Instead, **let the CLI own the question** and have helios drive
the CLI's own UI when the answer arrives from the phone.

```
AskUserQuestion
  ├─ /question hook → returns {} immediately (no decision, no block)
  │     └─ creates claude.question notification → phone
  └─ CLI renders its OWN question UI          ← terminal answerable

Phone answers    → backend.SendKey(session, ↓×k, Enter)
                   → CLI UI resolves → PostToolUse → cancel notification
Terminal answers → PostToolUse fires → cancel notification on phone
```

First answer wins; the other surface's notification is retracted.

### Why PTY injection rather than a hook response

Because it is the only way both surfaces can work. A blocking hook response is,
by construction, an answer given *instead of* the CLI's UI — the terminal can
never participate.

This is not a novel mechanism here. `handleTrustAction` (`actions.go:96-134`)
already answers Claude's workspace-trust dialog by sending `KeyEnter` into the
PTY, and `StartTrustWatcher` (`server/trust_watcher.go:122`) already reads
rendered screen text via `Backend.Capture` to detect that dialog. This spec
applies the same two primitives to a dialog with more than one option.

### Why the CLI's UI is trustworthy to drive

`Backend.Capture` returns **emulator output**, not the raw PTY stream. Claude
positions text with cursor-column jumps, so phrases never appear contiguously in
the raw bytes — the trust watcher documents this precisely
(`trust_watcher.go:94-98`). Capture gives the rendered screen, which is what the
user sees and what can be matched against the question text.

### Selection model

`AskUserQuestion` carries a `questions` array; each entry has `question`,
`header` and `options`. The CLI presents them one at a time with the first
option highlighted. To pick option index `k`: send `KeyDown` `k` times, then
`KeyEnter`.

Before each injection, `Capture` must show the expected question. If it does
not, abort rather than press keys blind — a stray Enter into a session that has
moved on is a real action the user did not ask for. This is the single most
important safety property in this spec.

Free-text answers use `SendText`, which submits the text followed by Enter.

### Concurrency

One in-flight injection per session, guarded by a mutex keyed on session ID. Two
devices answering simultaneously must not interleave keystrokes.

## Changes

### `internal/backend/backend.go` — arrow keys

```go
const (
	KeyEnter  Key = "enter"
	KeyEscape Key = "escape"
	KeyCtrlC  Key = "ctrl-c"
	KeyUp     Key = "up"
	KeyDown   Key = "down"
)
```

`internal/backend/host.go` `keySequence`:

```go
case KeyUp:
	return []byte("\x1b[A"), nil
case KeyDown:
	return []byte("\x1b[B"), nil
```

### `internal/daemon/hooks.go` — stop the double dispatch

Keep `/question` as a `PreToolUse` matcher but drop its `timeout`: it no longer
blocks, so a 300-second budget is misleading.

The `*` matcher stays — every other tool needs it. `handleToolPre` learns to
skip the one tool that has a dedicated handler (below). Folding `/question` into
`handleToolPre` entirely was considered and rejected: it would collapse two
clean handlers into one branching one and lose the per-type hook registration
the provider interface is built around.

### `internal/provider/claude/hooks.go` — non-blocking question hook

`handleQuestion` keeps everything up to and including `ctx.Notify`, then returns
immediately:

```go
// Deliberately no decision and no block. Returning an empty object lets the
// CLI run AskUserQuestion and render its own UI, which is the only way the
// terminal user can answer. The phone answers by driving that same UI —
// see handleQuestionAction.
w.Header().Set("Content-Type", "application/json")
fmt.Fprint(w, `{}`)
```

Drop the `ctx.Mgr.Register(notifID)` call: nothing waits on a decision channel
any more. Resolution now happens through `Mgr.Resolve` from the action handler
or `CancelPendingFromClaude` from the completion paths, neither of which needs a
registered slot.

The payload gains the session id, which the action handler needs to reach the
terminal.

`input.ToolInput` is **already** `{"questions": [...]}` — verified against a
stored payload. So `session_id` must be spliced into that object, not wrapped
around it. Wrapping would produce `{"questions": {"questions": [...]}}` and
`notification_ext.dart`'s `questions` accessor (`payload?['questions'] as List?`)
would cast a Map to a List and return null, silently emptying the card:

```go
// input.ToolInput is already {"questions": [...]}; splice session_id into it
// rather than wrapping, which would nest questions under itself and break the
// mobile card's payload['questions'] accessor.
var payload map[string]json.RawMessage
if err := json.Unmarshal(input.ToolInput, &payload); err != nil {
    payload = map[string]json.RawMessage{}
}
sessionJSON, _ := json.Marshal(input.SessionID)
payload["session_id"] = sessionJSON
payloadJSON, _ := json.Marshal(payload)
payloadStr := string(payloadJSON)
```

This replaces the bare `payloadStr := string(input.ToolInput)` at `hooks.go:176`
and leaves `questions` exactly where the card already looks for it. A test
asserts the stored payload has `questions` as a JSON array at the top level.

`handleToolPre` skips the tool that has its own handler:

```go
// AskUserQuestion has a dedicated hook that sets waiting_permission. The
// "*" PreToolUse matcher fires for it too, and writing "active" here is what
// used to erase that status a millisecond after it was set.
if input.ToolName == "AskUserQuestion" {
    w.Header().Set("Content-Type", "application/json")
    fmt.Fprint(w, `{}`)
    return
}
```

`handleToolPost` resolves the notification when the tool completes — whoever
answered:

```go
// The tool finished, so the question is answered. Retract the notification on
// every other surface. Which side answered does not matter.
if input.ToolName == "AskUserQuestion" {
    resolveSessionQuestions(ctx, input.SessionID)
}
```

```go
// resolveSessionQuestions clears pending claude.question notifications for a
// session and tells clients to drop them.
func resolveSessionQuestions(ctx *provider.HookContext, sessionID string)
```

Implemented over a new store query rather than `ResolveSessionNotifications`,
which would also clear unrelated pending permissions for the session.

Add the same call to the `idle_prompt` branch of `handleNotification`
(`hooks.go:468`): pressing Escape at a question returns Claude to its prompt
without a `PostToolUse`, and the phone should not keep offering to answer a
question that is gone.

### `internal/store/notifications.go`

```go
// ResolveSessionNotificationsByType is ResolveSessionNotifications narrowed to
// one type, so clearing a question does not also clear a pending permission.
func (s *Store) ResolveSessionNotificationsByType(sourceSession, nType, status, source string) ([]string, error)
```

Same body as `ResolveSessionNotifications` with `AND type = ?` in both
statements.

### `internal/provider/claude/actions.go` — answer by driving the CLI

`handleQuestionAction` is rewritten. It no longer returns an answer for a
blocked hook; it types into the terminal.

```go
// handleQuestionAction answers Claude's question by driving the CLI's own
// question UI.
//
// The alternative — returning the answer from a blocking PreToolUse hook —
// is what this replaces: it prevented the CLI from ever rendering the UI, so
// the terminal user could not answer at all.
func handleQuestionAction(notif *store.Notification, body json.RawMessage) (notifications.Decision, error)
```

Request body, extending the existing shape so the mobile card's `answers` map
still applies:

```json
{ "action": "answer", "selections": [{"question_index": 0, "option_index": 2}] }
{ "action": "answer", "text": "something else" }
{ "action": "skip" }
```

`skip` sends `KeyEscape` and returns `Decision{Status: "denied"}`.

Injection, per selection:

1. `terminalBackend.Alive(sessionID)` — error out if the terminal is gone.
2. `Capture(sessionID)`; require the current question's text or header to be
   present, matched case-insensitively the way `containsTrustPrompt` does. Abort
   with an error if absent.
3. `SendKey(KeyDown)` × `option_index`, with `keyDelay` between presses.
4. `SendKey(KeyEnter)`.
5. Wait for the screen to change before the next question, bounded by
   `screenSettleTimeout`.

Constants, in one place with the reasoning attached:

```go
// keyDelay spaces out injected keystrokes. The CLI redraws between them and
// a burst can be coalesced into a single highlight move.
const keyDelay = 40 * time.Millisecond

// screenSettleTimeout bounds the wait for the next question to render.
const screenSettleTimeout = 3 * time.Second
```

Per-session mutex:

```go
// answerLocks serialises injection per session. Two devices answering at once
// must not interleave keystrokes into the same dialog.
var answerLocks sync.Map // sessionID -> *sync.Mutex
```

On success return `Decision{Status: "answered"}` with the selections echoed in
`Response`, so the notification records what was sent.

### `mobile/lib/providers/claude/cards.dart`

`ClaudeQuestionCard` posts `selections` (question index + option index) instead
of the `answers` string map. The card already renders the options list, so this
is a change to what the tap handler sends, not to the layout.

Surface injection failure: `sendAction` returning non-200 currently just returns
false (`daemon_api_service.dart:315`). The card should show the error — "Couldn't
answer: session has no live terminal" — rather than silently doing nothing,
which is indistinguishable from the bug being fixed.

## Risks

**The CLI's question UI changes shape.** Injection is coupled to how Claude
renders options. If the layout changes, the `Capture` guard fails closed and the
action returns an error — the phone reports it could not answer, and the
terminal still works. Degradation, not breakage. This is the reason the guard is
mandatory rather than best-effort.

**Two questions in flight for one session.** Not currently possible — the tool
blocks the turn — but the per-session mutex and the `Capture` check both hold if
it becomes possible.

**`PostToolUse` may not fire on every path.** `idle_prompt` and `Stop` are the
backstops, and both already run.

## Testing

Go:

1. `backend/host_test.go` — `KeyUp`/`KeyDown` map to `\x1b[A` / `\x1b[B`,
   alongside the existing `keySequence` table test at `host_test.go:277`.
2. `hooks_test.go` — `handleQuestion` returns `{}` without blocking and creates
   a pending notification; it completes well inside a test timeout, which is the
   direct regression guard for the five-minute block.
3. `hooks_test.go` — `handleToolPre` with `tool_name: "AskUserQuestion"` leaves
   status `waiting_permission` untouched.
4. `hooks_test.go` — `handleToolPost` with `tool_name: "AskUserQuestion"`
   resolves a pending question notification and leaves a pending permission for
   the same session alone.
5. `actions_test.go`, against a fake backend recording keystrokes — option index
   2 sends exactly `↓ ↓ Enter`; a `Capture` that does not contain the question
   sends **nothing** and returns an error; `skip` sends `Escape`; concurrent
   answers do not interleave.
6. `store/notifications_test.go` — `ResolveSessionNotificationsByType` clears
   only the named type.
7. `hooks_test.go` — the stored question payload has `questions` as a top-level
   JSON **array** and a `session_id` string, guarding the double-nesting
   mistake described above.

Manual — the flows that have never worked:

8. Ask a question, answer it **in the terminal**, confirm the phone notification
   clears and no keystrokes are injected.
9. Ask a question, answer it **on the phone**, confirm the terminal UI advances
   to the chosen option and the turn continues.
10. Multi-question payload answered entirely from the phone.
11. Answer on the phone while the terminal sits at the question, then confirm
    the CLI does not also wait for a second answer.
12. Press Escape in the terminal; confirm the phone notification clears.

## Implementation order

1. Arrow keys in `backend` + test. Self-contained.
2. `ResolveSessionNotificationsByType` + test.
3. `handleQuestion` non-blocking; drop `Register`; hook config timeout removed.
4. `handleToolPre` skip; `handleToolPost` + `idle_prompt` resolution.
5. `handleQuestionAction` injection, with the `Capture` guard written before the
   keystroke code, not after.
6. Mobile card payload change and error surfacing.

Steps 1-4 alone already fix the terminal (the question becomes answerable there)
and the status clobbering. Step 5 restores the phone. Landing 1-4 without 5
would regress phone answering, so they ship together.

## Notes

- `waitForDecision` stays for permission and elicitation, which genuinely do
  block a waiting CLI request. Only the question path stops using it — so the
  duplicate-timeout race disappears for questions and remains, harmlessly and
  by design, for the two hooks that really are request-scoped.
- The `denied` path for questions changes meaning: it used to mean "helios told
  Claude no", it now means "helios pressed Escape". Same outcome for the agent.
