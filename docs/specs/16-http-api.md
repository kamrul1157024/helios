# HTTP API Protocol

## Overview

The daemon exposes two HTTP servers:

- **Client API** (port 7654) — for TUI, CLI, browser, mobile apps
- **Hook API** (port 7655) — for Claude Code hooks (internal)

Both are localhost-only by default. Remote access is handled by channel plugins (Telegram, Slack), not by exposing the API publicly.

## Client API (port 7654)

### Sessions

```
GET    /api/sessions                    List all sessions
POST   /api/sessions                    Create new session
GET    /api/sessions/:id                Get session details
DELETE /api/sessions/:id                Kill session
PATCH  /api/sessions/:id                Update session (rename, change mode)
POST   /api/sessions/:id/suspend        Suspend session
POST   /api/sessions/:id/resume         Resume session
POST   /api/sessions/:id/message        Send message to session
GET    /api/sessions/:id/log            Get auto-approve action log
```

### Notifications

```
GET    /api/notifications               List all notifications (filterable)
POST   /api/notifications/:id/approve   Approve a permission
POST   /api/notifications/:id/deny      Deny a permission
POST   /api/notifications/:id/dismiss   Dismiss a notification
POST   /api/notifications/approve-all   Approve all pending
POST   /api/notifications/batch         Batch action on multiple
```

### Events (real-time)

```
GET    /api/events                      SSE stream — all events
GET    /api/events?session=3            SSE stream — filtered by session
```

### System

```
GET    /api/health                      Health check
GET    /api/status                      Daemon status + session summary
GET    /api/channels                    List channel plugins and status
POST   /api/channels/:name/test         Send test notification via channel
```

## Request/Response Examples

### List Sessions

```
GET /api/sessions

Response 200:
{
  "sessions": [
    {
      "id": 1,
      "title": "refactor-auth",
      "status": "active",
      "working_dir": "/Users/user/workspace/opal-app",
      "auto_approve_mode": "ask",
      "turn_count": 47,
      "pending_notifications": 0,
      "created_at": "2026-04-09T10:23:00Z",
      "last_active_at": "2026-04-09T10:35:00Z"
    },
    {
      "id": 3,
      "title": "write-tests",
      "status": "suspended",
      "working_dir": "/Users/user/workspace/other",
      "auto_approve_mode": "auto-safe",
      "turn_count": 89,
      "pending_notifications": 0,
      "created_at": "2026-04-09T08:00:00Z",
      "last_active_at": "2026-04-09T09:15:00Z"
    }
  ]
}
```

### Create Session

```
POST /api/sessions
{
  "title": "fix login bug",
  "working_dir": "/Users/user/workspace/app",
  "auto_approve_mode": "auto-safe",
  "initial_message": "fix the login timeout issue in auth.py"
}

Response 201:
{
  "id": 4,
  "title": "fix login bug",
  "status": "active",
  "working_dir": "/Users/user/workspace/app",
  "auto_approve_mode": "auto-safe",
  "created_at": "2026-04-09T11:00:00Z"
}
```

`initial_message` is optional — if provided, the daemon sends it to Claude immediately after session creation (via tmux send-keys). This enables creating a session AND giving it a task in one call.

### Send Message to Session

```
POST /api/sessions/3/message
{
  "message": "now add unit tests for the auth module"
}

Response 200:
{
  "success": true,
  "message": "Message sent to session #3 (write-tests)"
}

Response 409:
{
  "success": false,
  "error": "session_busy",
  "message": "Session #3 is busy processing. Wait for it to finish."
}
```

### List Notifications

```
GET /api/notifications?status=pending&type=permission

Response 200:
{
  "notifications": [
    {
      "id": "notif-001",
      "session_id": 3,
      "session_title": "write-tests",
      "type": "permission",
      "tool": "Bash",
      "detail": "npm run test --coverage",
      "status": "pending",
      "created_at": "2026-04-09T10:33:00Z"
    }
  ]
}
```

### Approve Permission

```
POST /api/notifications/notif-001/approve

Response 200:
{
  "success": true,
  "message": "Permission approved for session #3"
}

Response 410:
{
  "success": false,
  "error": "already_resolved",
  "message": "This notification was already approved"
}
```

### Batch Actions

```
POST /api/notifications/batch
{
  "notification_ids": ["notif-001", "notif-002", "notif-003"],
  "action": "approve"
}

Response 200:
{
  "results": [
    {"id": "notif-001", "success": true},
    {"id": "notif-002", "success": true},
    {"id": "notif-003", "success": false, "error": "already_resolved"}
  ]
}
```

### SSE Events Stream

```
GET /api/events

event: notification
data: {"id":"notif-001","session_id":3,"type":"permission","tool":"Bash","detail":"npm run test"}

event: session_update
data: {"id":3,"status":"active","turn_count":90}

event: session_done
data: {"id":1,"title":"refactor-auth","turn_count":52}

event: notification_resolved
data: {"id":"notif-001","action":"approved","source":"telegram"}
```

Event types:
- `notification` — new notification created
- `notification_resolved` — notification was approved/denied/dismissed
- `session_update` — session state changed
- `session_created` — new session created
- `session_done` — Claude finished in a session
- `session_suspended` — session suspended
- `session_resumed` — session resumed
- `session_killed` — session killed
- `session_error` — error in a session
- `message_sent` — message sent to a session

## Hook API (port 7655)

Internal API for Claude Code hooks. Not meant for external consumption.

### Permission Request

```
POST /hooks/permission

Body (from Claude): 
{
  "session_id": "claude-session-abc",
  "tool_name": "Bash",
  "tool_input": {"command": "npm run test"},
  "cwd": "/Users/user/workspace/app"
}

Response (after user approves):
{
  "hookSpecificOutput": {
    "hookEventName": "PermissionRequest",
    "decision": {"behavior": "allow"}
  }
}

Response (after user denies):
{
  "hookSpecificOutput": {
    "hookEventName": "PermissionRequest",
    "decision": {"behavior": "deny", "message": "Denied via claud"}
  }
}
```

This endpoint blocks until the user makes a decision (or timeout).

The daemon identifies which claud session this is by matching the `cwd` and `session_id` from the hook input against its tracked sessions.

### Stop

```
POST /hooks/stop

Body (from Claude):
{
  "session_id": "claude-session-abc",
  "cwd": "/Users/user/workspace/app"
}

Response 200: {} (non-blocking, just updates state)
```

### Notification

```
POST /hooks/notification

Body (from Claude):
{
  "session_id": "claude-session-abc",
  "hook_event_name": "Notification",
  "cwd": "/Users/user/workspace/app"
}

Response 200: {}
```

### Stop Failure

```
POST /hooks/stop-failure

Body (from Claude):
{
  "session_id": "claude-session-abc",
  "cwd": "/Users/user/workspace/app"
}

Response 200: {}
```

### Session End

```
POST /hooks/session-end

Body (from Claude):
{
  "session_id": "claude-session-abc",
  "cwd": "/Users/user/workspace/app"
}

Response 200: {}
```

## Session-to-Hook Mapping

The daemon needs to map incoming hook calls to its own session IDs. When a hook fires, it includes Claude's `session_id` and `cwd`. The daemon matches these to find the corresponding claud session:

```
Daemon tracks:
  Session #3 {
    claude_session_id: "claude-session-abc",
    tmux_session: "claud-3",
    working_dir: "/Users/user/workspace/app"
  }

Hook arrives with:
  session_id: "claude-session-abc"
  cwd: "/Users/user/workspace/app"

Match found -> Session #3
```

The `claude_session_id` is captured during session creation via the `SessionStart` hook.
