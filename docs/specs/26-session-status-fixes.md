# 26 — Session Status Fixes: Missing Hooks, Stale Detection, Compaction, Mobile Polling

## Problem

Session status on the mobile app is frequently wrong. Four root causes:

### 1. Missing hooks — sessions stuck in `idle`

The hook config (`internal/daemon/hooks.go`) only registers:

```
PermissionRequest, PreToolUse(AskUserQuestion only), Elicitation,
Stop, StopFailure, Notification, SessionStart, SessionEnd,
SubagentStart, SubagentStop
```

Missing:
- **`UserPromptSubmit`** — user types a prompt in the CLI → session should go `idle → active`
- **`PreToolUse` (general)** — Claude calls any tool → confirms session is `active`
- **`PostToolUse`** — tool completed → session still `active`
- **`PostToolUseFailure`** — tool failed → session still `active`
- **`PreCompact`** — context compaction starts → session should show `compacting`
- **`PostCompact`** — compaction finishes → session back to `active`

Because of this, after `Stop` fires (→ `idle`), the user types a new prompt locally and the session stays `idle` on mobile forever until the next `Stop`/`SessionEnd`.

### 2. No stale detection — zombie `active` sessions

If Claude crashes, the terminal is killed, or the machine reboots, the `SessionEnd` hook never fires. The session stays `active` or `waiting_permission` in the DB permanently.

There is no background process that checks whether active sessions are actually alive.

### 3. Mobile doesn't poll — stale UI

`SSEService._handleEvent` only refetches sessions on `session_status` events:

```dart
if (type == 'session_status') {
  fetchSessions();
}
```

No polling fallback exists. If the SSE connection drops or a hook doesn't broadcast `session_status`, the mobile shows stale data indefinitely.

### 4. Resume sets wrong status

`handleSessionResume` (`api.go:637`) sets status to `active`, but `claude --resume <id>` (without `-p`) just opens the session at the prompt — Claude is waiting for input, not working. This should be `idle`.

Contrast with `handleSessionSend` which uses `claude --resume <id> -p "..."` — Claude immediately starts working, so `active` is correct there.

## Corrected State Machine

### Status values

| Status | Meaning |
|--------|---------|
| `active` | Claude is working (processing prompt, running tools) |
| `compacting` | Claude is compacting context (can take 5-6 minutes) |
| `waiting_permission` | Claude is blocked on a permission/question/elicitation |
| `idle` | Claude finished its turn, waiting for user input |
| `error` | Turn ended due to API error |
| `suspended` | User explicitly killed the Claude process via remote control |
| `ended` | Session terminated normally (SessionEnd hook fired) |
| `stale` | Session appears dead — no hook activity and tmux pane gone |

### Transition diagram

```
                         SessionStart
                             │
                             ▼
          ┌────────────── active ◄──────────────────────┐
          │                  │  ▲                        │
          │                  │  │                        │
          │   PermissionReq  │  │ UserPromptSubmit       │
          │   AskUserQuestion│  │ PreToolUse             │
          │   Elicitation    │  │ PostToolUse            │
          │                  │  │ PermissionResolved     │
          │                  │  │ PostCompact            │
          │                  ▼  │                        │
          │       waiting_permission                     │
          │                                              │
          │   PreCompact                                 │
          │       │                                      │
          │       ▼                                      │
          │   compacting ───► active (PostCompact)       │
          │                                              │
          │   Stop              StopFailure              │
          │     │                    │                    │
          │     ▼                    ▼                    │
          │   idle                 error                  │
          │     │                    │                    │
          │     │ UserPromptSubmit   │ UserPromptSubmit   │
          │     │ RemotePrompt       │ RemotePrompt       │
          │     └────────────────────┘────────────────────┘
          │
          │   RemoteSuspend            SessionEnd
          │       │                        │
          │       ▼                        ▼
          │   suspended                  ended
          │       │                        │
          │       │ RemoteResume           │ RemoteResume
          │       └───► idle ◄─────────────┘
          │              │
          │              │ RemotePrompt / UserPromptSubmit
          │              └──► active
          │
          │   StaleReaper (no hook activity + tmux pane gone)
          │       │
          │       ▼
          └──► stale
                 │
                 │ RemoteResume → idle
                 │ RemotePrompt → active
                 └───────────────────────
```

### Key distinctions

- **`RemoteResume`** = `claude --resume <id>` (no prompt) → Claude sits at input prompt → **`idle`**
- **`RemotePrompt`** = `claude --resume <id> -p "..."` (with prompt) → Claude starts working → **`active`**
- **`Stop` hook** → `idle` (Claude finished its turn, process still alive in tmux)
- **`SessionEnd` hook** → `ended` (Claude process exited normally)
- **`PreCompact`** → `compacting` (Claude is compacting context, can take 5-6 minutes)
- **`PostCompact`** → `active` (compaction done, Claude resumes working)
- **`StaleReaper`** → `stale` (no hooks fired recently AND tmux pane is dead/missing)

### When does a session become stale?

A session is stale when ALL of these are true:
1. Status is `active` or `waiting_permission` (should be doing something)
2. `last_event_at` is older than 2 minutes (no hook activity)
3. The tmux pane is gone OR the Claude process is not running in it

Sessions in `compacting` status are **never** reaped by the time-based check. Compaction can take 5-6 minutes, so a time threshold would cause false positives. Instead, the reaper only checks if the tmux pane is gone — if the pane dies during compaction, the next reaper cycle catches it.

This catches: Claude crashes, terminal killed, machine rebooted, network disconnect during remote session.

### Compaction flow

Compaction is triggered in two ways:
- **Auto**: Context window fills up during a turn. Flow: `active → compacting → active` (Claude continues working)
- **Manual**: User types `/compact`. Flow: `idle → active (UserPromptSubmit) → compacting → active → idle (Stop)`

The `PostCompact` handler always sets status back to `active`. If it was a manual compact, the `Stop` hook fires right after and sets `idle`. If auto, Claude continues working in `active`.

## Changes

### Backend: Add missing hook registrations

**`internal/daemon/hooks.go`** — add to `hookConfig()`:

```go
"UserPromptSubmit": []interface{}{
    map[string]interface{}{
        "matcher": "*",
        "hooks": []interface{}{
            map[string]interface{}{
                "type": "http",
                "url":  base + "/prompt/submit",
            },
        },
    },
},
"PreToolUse": []interface{}{
    // AskUserQuestion — blocking, creates notification, waits for decision
    map[string]interface{}{
        "matcher": "AskUserQuestion",
        "hooks": []interface{}{
            map[string]interface{}{
                "type":    "http",
                "url":     base + "/question",
                "timeout": 300,
            },
        },
    },
    // All tools — non-blocking, just confirms session is active
    map[string]interface{}{
        "matcher": "*",
        "hooks": []interface{}{
            map[string]interface{}{
                "type": "http",
                "url":  base + "/tool/pre",
            },
        },
    },
},
"PostToolUse": []interface{}{
    map[string]interface{}{
        "matcher": "*",
        "hooks": []interface{}{
            map[string]interface{}{
                "type": "http",
                "url":  base + "/tool/post",
            },
        },
    },
},
"PostToolUseFailure": []interface{}{
    map[string]interface{}{
        "matcher": "*",
        "hooks": []interface{}{
            map[string]interface{}{
                "type": "http",
                "url":  base + "/tool/post/failure",
            },
        },
    },
},
"PreCompact": []interface{}{
    map[string]interface{}{
        "matcher": "*",
        "hooks": []interface{}{
            map[string]interface{}{
                "type": "http",
                "url":  base + "/compact/pre",
            },
        },
    },
},
"PostCompact": []interface{}{
    map[string]interface{}{
        "matcher": "*",
        "hooks": []interface{}{
            map[string]interface{}{
                "type": "http",
                "url":  base + "/compact/post",
            },
        },
    },
},
```

Note on `PreToolUse`: Both matchers exist under the same event. The `*` handler just sets status to `active` — a no-op confirmation. If `AskUserQuestion` also matches `*` and both fire, the `/tool/pre` handler sets `active` first, then the `/question` handler overwrites it to `waiting_permission`. The final state is correct either way. No special-casing needed in any handler.

### Backend: Add hook handlers

**`internal/provider/claude/hooks.go`** — new handlers:

```go
func handlePromptSubmit(ctx, w, r, raw) {
    ctx.DB.UpdateSessionStatus(input.SessionID, "active", "UserPromptSubmit")
    ctx.Notify("session_status", ...)
}

func handleToolPre(ctx, w, r, raw) {
    ctx.DB.UpdateSessionStatus(input.SessionID, "active", "PreToolUse:"+input.ToolName)
    ctx.Notify("session_status", ...) // broadcasts tool name for mobile UI
}

func handleToolPost(ctx, w, r, raw) {
    ctx.DB.UpdateSessionStatus(input.SessionID, "active", "PostToolUse:"+input.ToolName)
    // no SSE broadcast — too noisy, just updates last_event_at for reaper
}

func handleToolPostFailure(ctx, w, r, raw) {
    ctx.DB.UpdateSessionStatus(input.SessionID, "active", "PostToolUseFailure:"+input.ToolName)
    // no SSE broadcast
}

func handlePreCompact(ctx, w, r, raw) {
    ctx.DB.UpdateSessionStatus(input.SessionID, "compacting", "PreCompact")
    ctx.Notify("session_status", ...)
}

func handlePostCompact(ctx, w, r, raw) {
    ctx.DB.UpdateSessionStatus(input.SessionID, "active", "PostCompact")
    ctx.Notify("session_status", ...)
}
```

**`internal/provider/claude/register.go`** — register new handlers:

```go
provider.RegisterHook("claude.prompt.submit", handlePromptSubmit)
provider.RegisterHook("claude.tool.pre", handleToolPre)
provider.RegisterHook("claude.tool.post", handleToolPost)
provider.RegisterHook("claude.tool.post.failure", handleToolPostFailure)
provider.RegisterHook("claude.compact.pre", handlePreCompact)
provider.RegisterHook("claude.compact.post", handlePostCompact)
```

### Backend: Fix resume status

**`internal/server/api.go`** — `handleSessionResume`:

```go
// Before (wrong):
s.shared.DB.UpdateSessionStatus(id, "active", "RemoteResume")

// After (correct):
s.shared.DB.UpdateSessionStatus(id, "idle", "RemoteResume")
```

Also update the SSE broadcast and response to say `"idle"` instead of `"active"`.

The `SessionStart` hook will fire when Claude actually starts, but with no `-p` flag it will sit at the prompt, so the next status change will come from `UserPromptSubmit` (if the user types locally) or `RemotePrompt` (if sent from mobile).

### Backend: Stale session reaper

**`internal/daemon/daemon.go`** — add a goroutine alongside the existing pairing token cleanup:

```go
// Periodic stale session reaper
go func() {
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()
    tmuxClient := tmux.NewClient()
    for {
        select {
        case <-ticker.C:
            reapStaleSessions(db, tmuxClient, shared.SSE)
        case <-ctx.Done():
            return
        }
    }
}()
```

**`internal/daemon/reaper.go`** — new file:

```go
package daemon

func reapStaleSessions(db *store.Store, tmux *tmux.Client, sse *server.SSEBroadcaster) {
    sessions, err := db.ListSessions()
    if err != nil {
        return
    }

    for _, sess := range sessions {
        // Only check sessions that should be alive
        // Skip compacting — compaction can take 5-6 minutes, no time-based check
        if sess.Status != "active" && sess.Status != "waiting_permission" {
            continue
        }

        // Check if last activity is older than 2 minutes
        if sess.LastEventAt == nil {
            continue
        }
        lastEvent, err := time.Parse(time.RFC3339, *sess.LastEventAt)
        if err != nil {
            continue
        }
        if time.Since(lastEvent) < 2*time.Minute {
            continue
        }

        // Check if tmux pane is still alive
        if sess.TmuxPane != nil && *sess.TmuxPane != "" {
            if tmux.HasPane(*sess.TmuxPane) {
                continue // pane exists, session might just be slow
            }
        }

        // Session is stale — mark it
        db.UpdateSessionStatus(sess.SessionID, "stale", "StaleReaper")
        sse.Broadcast(server.SSEEvent{
            Type: "session_status",
            Data: map[string]interface{}{
                "session_id": sess.SessionID,
                "status":     "stale",
            },
        })

        log.Printf("reaper: marked session %s as stale (last activity: %s)",
            sess.SessionID, lastEvent.Format(time.RFC3339))
    }
}
```

The reaper also handles `compacting` sessions whose pane has died — it doesn't skip those based on time, but it does check if the pane is gone:

```go
// Compacting sessions — only check pane liveness, not time
if sess.Status == "compacting" {
    if sess.TmuxPane != nil && *sess.TmuxPane != "" {
        if !tmux.HasPane(*sess.TmuxPane) {
            // Pane is gone during compaction — mark stale
            db.UpdateSessionStatus(sess.SessionID, "stale", "StaleReaper")
            sse.Broadcast(...)
        }
    }
    continue
}
```

### Mobile: Add 3-second polling + broader event handling

**`mobile/lib/services/sse_service.dart`**:

Add a polling timer that runs regardless of SSE connection state:

```dart
Timer? _pollTimer;

Future<void> start() async {
  if (_running) return;
  _running = true;
  _startPolling();
  await _connect();
}

void _startPolling() {
  _pollTimer?.cancel();
  _pollTimer = Timer.periodic(const Duration(seconds: 3), (_) {
    fetchSessions();
  });
}

void stop() {
  _running = false;
  _connected = false;
  _client?.close();
  _client = null;
  _reconnectTimer?.cancel();
  _reconnectTimer = null;
  _pollTimer?.cancel();
  _pollTimer = null;
  notifyListeners();
}
```

Also refetch sessions on more event types:

```dart
void _handleEvent(String type, dynamic data) {
  _eventController.add(SSEEvent(type, data));
  fetchNotifications();
  // Refetch sessions on any session-relevant event
  if (type == 'session_status' ||
      type == 'notification' ||
      type == 'notification_resolved' ||
      type == 'subagent_status') {
    fetchSessions();
  }
}
```

### Mobile: Add compacting and stale status to UI

**`mobile/lib/models/session.dart`**:

```dart
bool get isCompacting => status == 'compacting';
bool get isStale => status == 'stale';
bool get isActive => status == 'active' || status == 'waiting_permission' || status == 'compacting';
bool get canSendPrompt => status == 'idle' || status == 'ended' || status == 'suspended' || status == 'stale';
bool get canStop => status == 'active' || status == 'waiting_permission' || status == 'compacting';
bool get canSuspend => isActive || isIdle;
bool get canResume => isEnded || isSuspended || isStale;
```

**`mobile/lib/screens/sessions_screen.dart`** and **`session_detail_screen.dart`**:

```dart
// _statusColor
case 'compacting': return Colors.indigo;
case 'stale':      return Colors.grey;

// _statusIcon
case 'compacting': return Icons.compress;
case 'stale':      return Icons.help_outline;

// _statusLabel
case 'compacting': return 'Compacting';
case 'stale':      return 'Stale';
```

### Mobile UI

#### Session List

```
┌──────────────────────────────────────┐
│           Sessions                   │
│──────────────────────────────────────│
│                                      │
│  ● helios                   active   │
│    ~/workspace/helios         now    │
│    PreToolUse:Edit                   │
│                                      │
│  ◉ opal-app              compacting  │
│    ~/workspace/opal-app     30s ago  │
│    PreCompact                        │
│                                      │
│  ◬ openclaw          waiting perm    │
│    ~/workspace/openclaw    1m ago    │
│    PermissionRequest                 │
│                                      │
│  ● api-server                 idle   │
│    ~/workspace/api-server   3m ago   │
│    Stop                              │
│                                      │
│  ✗ ml-pipeline              error    │
│    ~/workspace/ml-pipeline  5m ago   │
│    StopFailure                       │
│                                      │
│  ? data-sync                 stale   │
│    ~/workspace/data-sync   12m ago   │
│    StaleReaper                       │
│                                      │
│  ⏸ llm-learning          suspended   │
│    ~/workspace/llm-learning 1h ago   │
│    RemoteSuspend                     │
│                                      │
│  ○ old-project               ended   │
│    ~/workspace/old-project   3h ago  │
│    SessionEnd                        │
│                                      │
│──────────────────────────────────────│
│  Sessions   Notifications  Settings  │
└──────────────────────────────────────┘

Legend:
  ● green    = active (working)
  ◉ indigo   = compacting (context compaction)
  ◬ orange   = waiting permission
  ● blue     = idle (prompt ready)
  ✗ red      = error
  ? grey     = stale (session appears dead)
  ⏸ purple   = suspended
  ○ grey     = ended
```

#### Session Detail — Compacting

```
┌──────────────────────────────────────┐
│  ← opal-app       ◉ Compacting      │
│  sonnet-4-6                    ■  ⏸  │
│──────────────────────────────────────│
│                                      │
│  ... (transcript messages)           │
│                                      │
│  ┌────────────────────────────────┐  │
│  │  ◉ Compacting context...      │  │
│  │  This may take a few minutes  │  │
│  └────────────────────────────────┘  │
│                                      │
│──────────────────────────────────────│
│  Session is compacting...        ■   │
└──────────────────────────────────────┘

- Stop (■) and Suspend (⏸) available
- Prompt bar disabled with "Session is compacting..." hint
```

#### Controls per status

```
┌──────────────────┬────────────┬──────────┬─────────┬─────────┐
│ Status           │ Prompt bar │ Stop (■) │ Suspend │ Resume  │
├──────────────────┼────────────┼──────────┼─────────┼─────────┤
│ active           │ disabled   │ ✓        │ ✓       │         │
│ compacting       │ disabled   │ ✓        │ ✓       │         │
│ waiting_perm     │ disabled   │ ✓        │ ✓       │         │
│ idle             │ enabled    │          │ ✓       │         │
│ error            │ enabled    │          │         │ ✓       │
│ stale            │ enabled    │          │         │ ✓       │
│ suspended        │ enabled    │          │         │ ✓       │
│ ended            │ enabled    │          │         │ ✓       │
└──────────────────┴────────────┴──────────┴─────────┴─────────┘
```

## Hook-to-Status Summary

| Claude Code Event | Hook URL | Status Transition | Broadcasts SSE |
|---|---|---|---|
| `SessionStart` | `/hooks/claude/session/start` | → `active` | `session_status` |
| `UserPromptSubmit` | `/hooks/claude/prompt/submit` | → `active` | `session_status` |
| `PreToolUse` (AskUserQuestion) | `/hooks/claude/question` | → `waiting_permission` | `notification` |
| `PreToolUse` (other) | `/hooks/claude/tool/pre` | → `active` (confirm) | `session_status` |
| `PostToolUse` | `/hooks/claude/tool/post` | → `active` (confirm) | — |
| `PostToolUseFailure` | `/hooks/claude/tool/post/failure` | → `active` (confirm) | — |
| `PreCompact` | `/hooks/claude/compact/pre` | → `compacting` | `session_status` |
| `PostCompact` | `/hooks/claude/compact/post` | → `active` | `session_status` |
| `PermissionRequest` | `/hooks/claude/permission` | → `waiting_permission` | `notification` |
| `Elicitation` | `/hooks/claude/elicitation` | → `waiting_permission` | `notification` |
| Permission/Question resolved | (inline in handler) | → `active` | `notification_resolved` |
| `Stop` | `/hooks/claude/stop` | → `idle` | `session_status` |
| `StopFailure` | `/hooks/claude/stop/failure` | → `error` | `session_status` |
| `SessionEnd` | `/hooks/claude/session/end` | → `ended` | `session_status` |
| `Notification` | `/hooks/claude/notification` | (no change) | — |
| `SubagentStart` | `/hooks/claude/subagent/start` | (no change) | `subagent_status` |
| `SubagentStop` | `/hooks/claude/subagent/stop` | (no change) | `subagent_status` |
| Remote resume | `POST /api/sessions/:id/resume` | → `idle` | `session_status` |
| Remote prompt | `POST /api/sessions/:id/send` | → `active` | `session_status` |
| Remote suspend | `POST /api/sessions/:id/suspend` | → `suspended` | `session_status` |
| Remote stop | `POST /api/sessions/:id/stop` | (sends Escape, `Stop` hook fires) | — |
| Stale reaper | (background goroutine) | → `stale` | `session_status` |

## Implementation Order

1. Fix `handleSessionResume` to set `idle` instead of `active`
2. Add `UserPromptSubmit`, `PreToolUse/*`, `PostToolUse`, `PostToolUseFailure` hook handlers
3. Add `PreCompact`, `PostCompact` hook handlers
4. Register new handlers and update `hookConfig()` in daemon
5. Add stale session reaper goroutine (skip `compacting` from time-based check)
6. Add `compacting` and `stale` status to mobile Session model
7. Add 3-second polling timer to `SSEService`
8. Broaden SSE event types that trigger `fetchSessions()`
9. Add compacting/stale status color/icon/label to mobile UI
10. Run `helios hooks install` to update Claude Code settings

## Notes

- After implementing, users must run `helios hooks install` to update their Claude Code hook config (or restart the daemon which calls `InstallHooksIfMissing`).
- `PostToolUse` and `PostToolUseFailure` handlers intentionally do NOT broadcast SSE events — they'd be too noisy. They just update `last_event_at` in the DB so the stale reaper has fresh timestamps. The 3-second poll will pick up any status shown in the DB.
- The `PreToolUse` catch-all does broadcast because it carries the tool name, which is useful for the mobile UI to show "Running: Edit server.go" etc.
- `PreToolUse` has two matchers: `AskUserQuestion` (blocking, 300s timeout) and `*` (non-blocking activity tracker). Even if both fire for `AskUserQuestion`, the final state is correct — `/tool/pre` sets `active`, then `/question` overwrites to `waiting_permission`. No special-casing needed.
- The stale reaper skips `compacting` sessions from time-based checks entirely. Compaction can take 5-6 minutes — a 2-minute threshold would cause false positives. The reaper only marks a `compacting` session as stale if the tmux pane is gone.
- `PostCompact` always sets status to `active`. For auto-compaction, Claude continues working. For manual `/compact`, the `Stop` hook fires immediately after and sets `idle`.
