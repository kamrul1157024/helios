# Mobile: Support a Second Provider

## Who this is for

This is a handoff. It is written for someone who can install Flutter, run the
mobile app, and verify the result — which the author of this document could
not. Every claim here comes from reading `mobile/lib`, never from running it.
**Treat each one as a hypothesis to confirm before you change anything.**

Flutter `^3.32.0`, Dart SDK `^3.8.0`, package `helios`, in `mobile/`.

Read [47-provider-interface.md](47-provider-interface.md) for the daemon-side
interface this depends on, and [46-codex-provider.md](46-codex-provider.md)
for the provider that motivates it. You do not need either to do the work
below, but the notification-catalogue endpoint in Part 3 comes from spec 47 and
may not exist yet.

---

## The defect, in one sentence

A notification whose type does not begin with `claude.` raises **no OS
notification at all**, so an agent blocked on a permission request waits
forever with no buzz on the phone.

That is the bug worth fixing. Everything else in this document is tidying
around it.

### Why it matters more here than on desktop

The Claude provider's own comment states the design intent
(`internal/provider/claude/register.go:40`):

> Helios sessions are driven from a phone as often as from a terminal, and a
> prompt that stops on the first permission question is a session the user
> cannot finish from the lock screen.

The phone is the surface Helios exists for. Desktop degrades a stranger's
notification to a banner; mobile drops it silently.

---

## Part 1 — verify the defect first

Do this before writing code. If it does not reproduce, stop and say so — the
rest of this document is then built on a wrong reading.

1. Install Flutter and run the app against a local daemon.
2. Raise a real notification with a type the app does not know:

```bash
# A registered type, for the control case — expect an OS notification.
curl -sS --max-time 90 -X POST http://127.0.0.1:7654/hooks/claude/permission \
  -H 'Content-Type: application/json' \
  -d '{"session_id":"probe","cwd":"/tmp","tool_name":"Bash",
       "tool_input":{"command":"echo probe"}}'
```

For the unknown-type case you need a notification whose `type` is not
`claude.*`. There is no endpoint that creates one directly, so either register
a throwaway hook in the daemon, or drive the app's SSE handler with a
synthetic event in a widget test. A widget test is cheaper and is wanted
anyway — see Part 4.

**Expected, if the reading is right:** the `claude.permission` case buzzes; the
unknown-type case appears in the dashboard as a plain status card and produces
no OS notification.

---

## Part 2 — the six sites

Line numbers are from the commit this document was written against. Confirm
each before editing.

### 2.1 The silent drop — `lib/screens/home_screen.dart:121-175`

An if/else chain over seven literal types **with no final else**:

```dart
if (type == 'claude.permission') {          // 121
  notifSvc.showPermissionNotification(...);
} else if (type == 'claude.question') {     // 130
  ...
} else if (type.startsWith('claude.elicitation')) {
} else if (type == 'claude.trust') {
} else if (type == 'claude.done') {
} else if (type == 'claude.error') {        // 166
  ...
}                                            // 175 — nothing else
```

**This is the bug.** An unmatched type falls out and nothing is raised.

**Change.** Replace the chain with a lookup driven by the notification
catalogue (Part 3), and give it a fallback that always raises something:

- If the type is actionable, use `showPermissionNotification` so the tray entry
  carries Approve and Deny buttons — `_handleNotificationAction` already routes
  those generically by `notificationId`, so no per-provider work is needed
  there.
- Otherwise `showNotification` with the server's label and the event's
  `detail`.
- Never fall through in silence.

Note the hardcoded strings while you are here: `'Claude has a question'`,
`'Claude finished a task.'`, `'Claude stopped due to an error.'`. These are
user-visible and wrong for another provider. They should come from the
catalogue.

### 2.2 The actionable allowlist — `lib/providers/card_registry.dart:70-75`

```dart
bool isActionableType(String type) =>
    type == 'claude.permission' ||
    type == 'claude.question' ||
    type == 'claude.trust' ||
    type == 'claude.error' ||
    type.startsWith('claude.elicitation.');
```

Feeds `shouldRaiseNotification`, which suppresses an already-answered approval
on reconnect. For an unknown type it returns false, so the replay guard does
not apply — a stale card can reappear after a reconnect.

**Change.** Drive from the catalogue's `blocking` flag. Until that endpoint
exists, keep the literal list but key it on the suffix after the provider
prefix, so `codex.permission` matches on `permission`.

Directly above it, at line 61, is a comment already describing the intended
behaviour and a body that does not do it:

```dart
/// Whether this notification needs user action (checks all registered providers).
bool needsAction(HeliosNotification n) {
  return n.needsClaudeAction;
}
```

### 2.3 The card switch — `lib/providers/card_registry.dart:23-58`

Six cases, `default: return null`. Callers handle null differently:

| Caller | Behaviour on null |
|---|---|
| `dashboard_screen.dart:104` | `card ?? _buildStatusCard(n, hm)` — visible, not answerable |
| `session_detail_screen.dart:641` | `if (card != null)` — nothing rendered |

**Change.** The Claude cards in `lib/providers/claude/cards.dart` are mostly
generic already: `ClaudePermissionCard` renders a tool name, a tool input and
Approve/Deny. Decide between two routes and say which you took:

- **Rename and reuse.** Move the cards to `lib/providers/cards/` as
  `PermissionCard`, `QuestionCard`, `TrustCard`, `ErrorCard`, dispatching on
  the type suffix. Cheapest, and correct if the payloads match.
- **Keep per-provider cards** and add a `codex/` directory. Only worth it if
  Codex's payloads diverge.

Check the payload shapes before choosing. Spec 46 records that Codex's
`PermissionRequest` carries `tool_name` and `tool_input` like Claude's, **but
has no `tool_use_id`** and cannot offer "don't ask again" — so
`ClaudePermissionCard`'s permission-suggestion UI must be conditional on the
payload rather than always shown.

### 2.4 The alert catalogue — three places

| File | What |
|---|---|
| `lib/services/notification_service.dart:26-34` | `_defaultAlertTypes`, seven `claude.*` keys |
| `lib/screens/notification_settings_screen.dart:16+` | `_blockingTypes` / informational lists with labels and descriptions |
| `lib/providers/card_registry.dart:70` | `isActionableType` |

Three copies of one catalogue. The daemon should serve it (Part 3).

`isAlertEnabled` at line 44 already returns `?? true`, so an unknown type is
noisy rather than silent. **Keep that.** It is the one place the current design
degrades in the safe direction, and it must survive the refactor.

### 2.5 The source gates — two

```dart
// lib/models/session.dart:118
bool get canSwitchPermissionMode => source == 'claude' && isIdle;

// lib/screens/session_detail_screen.dart:816
if (session.source == 'claude') { ...permission mode button... }
```

**Change.** A provider has permission modes when the daemon says so. The
providers endpoint already returns `permission_modes` per provider, so gate on
that list being non-empty rather than on the source string. Desktop has the
same gate at `detail.tsx:538` and it is redundant there for the same reason.

### 2.6 Claude-branded chrome — `lib/providers/claude/verbs.dart`

`randomClaudeVerb()` drives the "Accomplishing…" animation in
`session_detail_screen.dart:62,93,1356`. Cosmetic, and it will say
"Clauding…" over a Codex session.

**Change.** Lowest priority. Either make the list generic or let a provider
supply its own. Do not block the fix in 2.1 on this.

---

## Part 3 — the dependency

Spec 47 proposes the daemon serve the notification catalogue:

```
GET /api/notification-types
→ [{"type":"claude.permission","provider":"claude","label":"Permission requests",
    "detail":"...","blocking":true,"group":"action_required","default_alert":true}]
```

**This may not exist when you start.** Check first.

If it does not, do not wait for it and do not build it from the mobile side.
Do 2.1's fallback with the literal lists, keyed on the type suffix so a second
provider works. That fixes the silent drop, which is the part that matters, and
leaves the catalogue as a follow-up. Say clearly in your report which of the
two you did.

---

## Part 4 — how to verify

`mobile/` currently has no widget tests for notification dispatch. Add them
with the fix; they are the only way this stays fixed.

1. **The regression test.** Feed the SSE handler a synthetic event with type
   `codex.permission` and assert an OS notification is requested. Mock the
   `MethodChannel('com.helios.helios/notifications')`. This test must fail
   before your change.
2. **The control.** Same with `claude.permission`; it passes before and after.
3. **The replay guard.** `shouldRaiseNotification` with a non-pending
   `codex.permission` must return false, as it already does for Claude.
4. **The alert default.** `isAlertEnabled('codex.permission')` is true.
5. `flutter analyze` clean, `flutter test` green.
6. **On a device or emulator:** run against a live daemon, raise a real
   blocking notification, confirm the tray entry appears with working Approve
   and Deny buttons.

Step 6 is the one that counts. The rest is scaffolding around it.

---

## What to report back

- Whether Part 1 reproduced. If it did not, the reading was wrong and the rest
  of this document is suspect.
- Which route you took in 2.3, and why.
- Whether `/api/notification-types` existed, and what you did about it.
- Anything here that was wrong. This document was written without running the
  app, and the line numbers, the payload assumptions and the claim about
  `_handleNotificationAction` being provider-agnostic are all worth doubting.
