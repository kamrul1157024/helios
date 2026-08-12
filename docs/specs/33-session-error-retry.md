# 33 — Session Error Retry: Capture the Error, Unblock the Composer, Offer Retry

## Problem

When a turn dies on an API error — a rate limit, a stalled stream, a dropped
connection — the terminal shows the error and the user types `continue` to pick
up from where it stopped. From the phone there is no way to do that. The session
shows "Error", the composer is dead, and the notification has no buttons. The
session is stranded until the user gets back to a laptop.

**Observed live.** Session `4584bc95-fd71-498b-8e6a-9839cb051f32` has been
sitting at `status=error` since 2026-08-11 12:26 with its ptyhost still alive.
Its `claude.error` notification is still `pending`. It is a perfectly healthy
session that the phone cannot touch.

### The chain

API error → Claude fires `StopFailure` → `handleStopFailure`
(`internal/provider/claude/hooks.go:408`) → session status `error`, plus a
`claude.error` notification with status `pending`.

Three separate defects stack on top of that.

### 1. The mobile composer is hard-blocked, and only on mobile

`Session.canSendPrompt` (`mobile/lib/models/session.dart:87`):

```dart
bool get canSendPrompt {
  if (status == 'idle') return true;
  if (supportsPromptQueue && isActive) return true;
  return false;
}
```

`isActive` covers `active`, `waiting_permission`, `compacting`, `starting`
(`session.dart:79`). `error` is in neither branch, so `canSendPrompt` is false
and the text field, mic, command sheet and send button all go dead
(`session_detail_screen.dart:1203`, `:1311`, `:1345`, `:1428`, `:1454`).

**The daemon would have accepted the message.** `handleSessionSend`
(`internal/server/api.go:491-515`) rejects only `active`/`waiting_permission`
(as busy) and `terminated`. `error` falls straight through to the
wake-and-type path at `:520`. The desktop app has no status gate at all, which
is exactly why typing `continue` works there and not on the phone.

The retry transport already exists. The phone just refuses to use it.

### 2. The error text is thrown away

`hookInput` (`hooks.go:17-37`) has no field for a failure reason, and
`handleStopFailure` sets the notification detail from `sessionContext(...)`,
which is `"<project>: <last user message or title>"`. The two stranded rows in
the live database read:

```
helios: commit
helios: mobile will ssh and localforward, mobile need to deploy it keys to the vm
```

So the phone says "Session error" and shows the user their own last prompt back.
Nothing indicates it was a rate limit, and nothing says when it would be worth
retrying.

The real text **is** available. The transcript's last entry carries it:

```json
{"type":"assistant","isApiErrorMessage":true,
 "message":{"content":[{"type":"text",
   "text":"API Error: Response stalled mid-stream. The response above may be incomplete."}]}}
```

written 8 ms before the hook fired. Errors seen across the local transcripts:

| Text | Retryable |
|---|---|
| `API Error: Response stalled mid-stream. The response above may be incomplete.` | yes |
| `API Error: Stream idle timeout - no chunks received` | yes |
| `API Error: Connection to the API was lost (ECONNRESET). This is usually temporary — try again.` | yes |
| `rate limit exceeded` / `Claude AI usage limit reached\|<epoch>` | yes, after a wait |

`internal/transcript` is already wired into the daemon and nothing reads it on
this path.

### 3. The notification is a dead end that never clears

`claude.error` has no action handler (`register.go:161-165`), no card
(`card_registry.dart:23-53`), and is excluded from `needsClaudeAction`
(`notification_ext.dart:17`), so it lands in the dashboard's `activeStatuses`
bucket (`dashboard_screen.dart:37-48`) — visible, no buttons.

It is created `pending` but `handleStopFailure` never calls `ctx.Mgr.Register`,
so no decision slot exists and nothing can resolve it except a later `Stop` on
the same session (`hooks.go:363`). `TruncateNotifications` skips pending rows
(`store/notifications.go:159`), so these accumulate forever.

## Design

### What "retry" means

Send the string `continue` to the session as a normal prompt. That is what the
user does by hand in the terminal, and it is what the CLI's own conversation
state expects: the turn resumes from where the error interrupted it.

This is deliberately *not* a new resume mechanism. `POST /api/sessions/{id}/send`
already wakes a cold session, types into a live one, and waits for the
`UserPromptSubmit` hook to confirm the prompt actually landed
(`api.go:553-571`). Retry is one call to it.

### Where the retry lives

Two entry points, because the user reaches this from two directions:

- **The composer**, for the user already looking at the session. Unblocking it
  costs one line and is the fix that matters most: it restores the exact
  terminal workflow, and lets the user send something other than `continue`.
- **A Retry button on the `claude.error` card**, for the user who got the
  notification and does not want to open the session.

### Rate limits are a distinct case

A stalled stream is worth retrying immediately. A usage limit is not — retrying
inside the window just burns another error. When the error text carries a reset
time, the card surfaces it and disables Retry until it passes.

Claude emits the usage limit as `Claude AI usage limit reached|<unix epoch>`.
Parse the epoch when present; fall back to no countdown when absent rather than
guessing a duration.

### Auto-retry is out of scope

Tempting, and wrong for a first cut. A retry loop against a rate limit can
silently consume quota, and an automatic `continue` on an error the user has not
seen can take a destructive action they would have stopped. Manual retry first;
revisit once the error taxonomy has proven itself in practice.

## Changes

### `internal/provider/claude/hooks.go` — capture the error

Add a reason field to `hookInput`, in case a future CLI version supplies one
directly:

```go
// Reason is populated by StopFailure when the CLI reports why the turn
// ended. It is absent in Claude Code v2.1.x, hence the transcript fallback.
Reason string `json:"reason,omitempty"`
```

New helper, reading the transcript tail:

```go
// lastAPIError returns the error text Claude recorded for the turn that just
// failed, or "" when the transcript has none.
//
// The StopFailure payload does not carry the reason, so the transcript is the
// only place it exists. Entries are scanned from the end because the failure
// is always the last thing written — the hook fires within milliseconds of it.
func lastAPIError(transcriptPath string) string
```

Scan backwards over at most the final `apiErrorScanLimit` entries (start at 20)
for an entry with `isApiErrorMessage: true`, and return the concatenated text of
its content blocks. Bail out on a missing or unreadable file: an error
notification with no detail is still better than a failed hook.

Classification:

```go
// RateLimitInfo describes a usage-limit error and when it lifts.
type RateLimitInfo struct {
    IsRateLimit bool
    ResetAt     *time.Time // nil when the error carries no reset time
}

// classifyAPIError reports whether an error is a usage limit and when it
// clears. Claude formats it as "Claude AI usage limit reached|<unix epoch>".
func classifyAPIError(text string) RateLimitInfo
```

`handleStopFailure` uses both:

```go
errText := lastAPIError(input.TranscriptPath)
if input.Reason != "" {
    errText = input.Reason
}
rate := classifyAPIError(errText)

title := "Session error"
if rate.IsRateLimit {
    title = "Rate limit reached"
}

// Detail is the error itself. It used to be sessionContext(), which showed
// the user their own last prompt back and said nothing about what broke.
detail := errText
if detail == "" {
    detail = sessionContext(input.CWD, sess)
}

payload := map[string]interface{}{
    "session_id":    input.SessionID,
    "error":         errText,
    "is_rate_limit": rate.IsRateLimit,
    "retryable":     true,
}
if rate.ResetAt != nil {
    payload["reset_at"] = rate.ResetAt.UTC().Format(time.RFC3339)
}
```

`session_id` goes in the payload because the retry action handler needs it and
`store.Notification.SourceSession` is not passed to action handlers in a form
the mobile card can rely on — the trust action already takes this approach
(`actions.go:104-114`).

Register the decision slot so the notification can actually be resolved:

```go
if err := ctx.Mgr.CreateNotification(notif); err != nil {
    log.Printf("claude: create error notification for %s: %v", input.SessionID, err)
} else {
    ctx.Mgr.Register(notifID)
}
ctx.Notify("notification", notif)
```

`handleStopFailure` still does **not** block on a decision. Unlike a permission
hook there is no CLI request waiting on the answer — the turn is already over.
`Register` exists only so `Resolve` has somewhere to deliver, which is what lets
the notification clear.

### `internal/provider/claude/actions.go` — the retry action

```go
// handleErrorAction retries or dismisses a failed turn.
//
// Retry sends "continue", which is what a user types in the terminal after an
// API error: the CLI picks the turn up where it stopped rather than starting a
// new one.
func handleErrorAction(notif *store.Notification, body json.RawMessage) (notifications.Decision, error)
```

Accepts `{"action": "retry"}` or `{"action": "dismiss"}`. Retry sends `continue`
via `terminalBackend.SendText(sessionID, "continue")` and returns
`Decision{Status: "approved"}`; dismiss returns `Decision{Status: "dismissed"}`.

Reject retry when the terminal is gone, so the notification is not consumed by a
send that went nowhere:

```go
if terminalBackend == nil || !terminalBackend.Alive(sessionID) {
    return notifications.Decision{}, fmt.Errorf("session %s has no live terminal", sessionID)
}
```

A dead terminal needs the wake path in `handleSessionSend`, which the composer
already reaches. The card falls back to opening the session in that case.

Register it:

```go
provider.RegisterAction("claude.error", handleErrorAction)
```

### `mobile/lib/models/session.dart` — unblock the composer

```dart
bool get canSendPrompt {
  if (status == 'idle') return true;
  // A turn that died on an API error leaves a live, idle agent. The daemon
  // accepts a prompt in this state (handleSessionSend treats only active,
  // waiting_permission and terminated as unsendable), and typing "continue"
  // is exactly the terminal recovery. Blocking it here stranded the session.
  if (status == 'error') return true;
  if (supportsPromptQueue && isActive) return true;
  return false;
}
```

### `mobile/lib/providers/claude/notification_ext.dart` — payload accessors

```dart
bool get isRateLimit => payload?['is_rate_limit'] == true;
bool get isRetryable => payload?['retryable'] == true;
String? get errorText => payload?['error'] as String?;
String? get errorSessionId => payload?['session_id'] as String?;
DateTime? get rateLimitResetAt {
  final raw = payload?['reset_at'] as String?;
  if (raw == null) return null;
  return DateTime.tryParse(raw)?.toUtc();
}
```

Add `claude.error` to the actionable set:

```dart
bool get needsClaudeAction =>
    isPending &&
    (isClaudePermission || isClaudeQuestion || isClaudeElicitation ||
     isClaudeTrust || isClaudeError);
```

This moves it from the dashboard's `activeStatuses` bucket into `pendingActions`,
which is correct — it now has an action. Spec 32's `isActionableType` must gain
`claude.error` in the same change if 32 has already landed; the two predicates
are asserted equal by test.

### `mobile/lib/providers/claude/cards.dart` — `ClaudeErrorCard`

Follows `ClaudeTrustCard`'s shape. Shows:

- the error text (`n.errorText ?? n.displayDetail`), monospace, wrapped
- a **Retry** button posting `{'action': 'retry'}` via `sse.sendAction`
- a **Dismiss** button posting `{'action': 'dismiss'}`
- an **Open session** link routing to `SessionDetailScreen`

When `isRateLimit` and `rateLimitResetAt` is in the future, Retry is disabled and
labelled with the remaining time ("Retry in 12m"), driven by a one-second
`Timer.periodic` that re-enables the button on expiry. When `rateLimitResetAt` is
null, Retry stays enabled — an unknown window is not a reason to lock the user
out.

Register it in `card_registry.dart`:

```dart
case 'claude.error':
  return ClaudeErrorCard(notification: notification, sse: sse);
```

### `mobile/lib/screens/session_detail_screen.dart` — error banner

Above the composer, when `session.status == 'error'`, an inline banner with the
error text and a Retry action that sends `continue` through the existing
`_sendPrompt` path. This is the surface the user hits when they open the session
from the notification.

## Testing

Go, in `internal/provider/claude/`:

1. `hooks_test.go` — `lastAPIError` on a transcript whose final entry has
   `isApiErrorMessage: true` returns the text; on one with no such entry returns
   `""`; on a missing file returns `""` without panicking; scanning stops at
   `apiErrorScanLimit`.
2. `classifyAPIError` — parses `Claude AI usage limit reached|1754899200` into a
   reset time; treats `API Error: Response stalled mid-stream.` as not a rate
   limit; a usage-limit string with no epoch yields `IsRateLimit` true and a nil
   `ResetAt`.
3. `handleStopFailure` writes the error text as the detail, sets
   `is_rate_limit`, and registers a decision slot (`Mgr.HasPending(id)`).
4. `actions_test.go` — `handleErrorAction` with `retry` calls `SendText` with
   `continue`; with a dead terminal it errors and does not resolve; `dismiss`
   returns a dismissed decision.

Dart, in `mobile/test/`:

5. `session_test.dart` — `canSendPrompt` is true for `error`, still false for
   `terminated`.
6. `claude_error_card_test.dart` — Retry disabled while `reset_at` is in the
   future, enabled once it passes, enabled when `reset_at` is absent.

Manual:

7. Force an API error (pull the network mid-turn), confirm the phone shows the
   real error text rather than the last prompt, hit Retry, confirm the session
   resumes.
8. Hit an actual usage limit and confirm the countdown renders and Retry unlocks
   when it expires.
9. Confirm the stranded session `4584bc95` becomes usable from the phone again.

## Implementation order

1. `lastAPIError` + `classifyAPIError` + tests — pure functions, no wiring.
2. `handleStopFailure` uses them; register the decision slot.
3. `handleErrorAction` + registration.
4. `canSendPrompt` accepts `error` — smallest change, biggest immediate relief.
5. Payload accessors, `ClaudeErrorCard`, registry entry.
6. Session detail error banner.

## Notes

- Steps 1-4 are independently useful. If the card slips, the composer fix alone
  restores the terminal workflow on the phone.
- Nothing here changes `handleSessionSend`. It already handles `error` correctly;
  the bug was entirely that the client would not call it.
- The two currently-stranded `claude.error` rows stay pending after this lands —
  they were created before the decision slot existed. They will age out only via
  a `Stop` on their sessions. Not worth a migration.
