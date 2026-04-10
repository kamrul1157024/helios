# 22 — Session Management & Remote Control

## Overview

Turn Helios from a "permission approval relay" into a **full remote control panel** for Claude Code sessions. Users should be able to see all their Claude sessions from the mobile app, view conversation history, send prompts remotely, and control which notifications they receive.

## Current State

Helios tracks Claude sessions minimally — it stores `(session_id, cwd, last_event)` in a `hook_sessions` table but never exposes this to clients. The mobile app is notification-centric: it shows permission requests but has no concept of "sessions." There is no way to:

- See which Claude sessions are running
- View what Claude has done in a session
- Send a prompt to Claude from the phone
- Control notification preferences

## Goals

1. **Session visibility** — See all Claude sessions, their status, project, and last activity
2. **Session history** — Tap into a session to read the full conversation
3. **Remote prompting** — Send a prompt to a Claude session from the phone
4. **Notification settings** — Toggle which event types generate push notifications

## Key Mechanisms

### Reading Session History

Claude stores transcripts at `~/.claude/projects/<project-slug>/<session-uuid>.jsonl`. Each line is a JSON object with types: `user`, `assistant`, `tool_use`, `tool_result`, `system`, `permission-mode`, `file-history-snapshot`, `attachment`.

The `transcript_path` field is provided in **every hook input** — we just need to capture and store it. Currently we don't.

### Sending Prompts to Claude Sessions

Two approaches:

1. **`tmux send-keys`** — Claude runs inside tmux panes. `tmux send-keys -t <pane> "prompt" Enter` types into Claude's readline. Requires knowing Claude is at its prompt (not mid-response). The `Stop` hook tells us Claude finished → waiting for input. `UserPromptSubmit` tells us it's busy.

2. **`claude -p --resume <session-id> "prompt"`** — Non-interactive mode. Sends a prompt to a persisted session, gets a response, exits. Doesn't require tmux but starts a new process.

**Decision: Start with `tmux send-keys` for sessions Helios creates via `helios new`. Later support `claude -p --resume` for headless flows.**

### Hook Data We're Not Capturing

The hooks API sends much more than we parse today:

| Field | Available in | Currently captured? |
|-------|-------------|-------------------|
| `session_id` | All hooks | Yes |
| `cwd` | All hooks | Yes |
| `transcript_path` | All hooks | **No** |
| `model` | SessionStart | **No** |
| `last_assistant_message` | Stop, SubagentStop | **No** |
| `stop_hook_active` | Stop | **No** |
| `permission_mode` | Most hooks | **No** |
| `tool_name` + `tool_input` | PreToolUse, PostToolUse, PermissionRequest | Partially (PermissionRequest only) |

## Session Lifecycle & State Machine

```
SessionStart hook        → status: active
UserPromptSubmit hook    → status: active
PermissionRequest hook   → status: waiting_permission
Permission resolved      → status: active
Stop hook                → status: idle  (Claude finished, waiting for input)
StopFailure hook         → status: error
SessionEnd hook          → status: ended
```

```
                    ┌──────────────────┐
                    │   SessionStart   │
                    └────────┬─────────┘
                             │
                             ▼
              ┌─────────────────────────────┐
              │          active              │◄──────────────────┐
              │  (Claude is working)         │                   │
              └──────┬──────────┬───────────┘                   │
                     │          │                                │
         PermissionReq│       Stop hook                   UserPromptSubmit
                     │          │                          (from phone or
                     ▼          ▼                           terminal)
        ┌────────────────┐  ┌──────────┐                        │
        │    waiting_     │  │   idle   │────────────────────────┘
        │   permission    │  │ (prompt  │
        └───────┬────────┘  │  ready)  │
                │           └──────────┘
          resolved                │
                │           SessionEnd
                │                │
                ▼                ▼
              active         ┌───────┐
                             │ ended │
                             └───────┘

        StopFailure at any point → status: error
```

## Database Schema Changes

### Replace `hook_sessions` with `sessions`

```sql
CREATE TABLE sessions (
    claude_session_id TEXT PRIMARY KEY,
    cwd TEXT NOT NULL,
    transcript_path TEXT,
    model TEXT,
    status TEXT NOT NULL DEFAULT 'active',
    -- active | idle | waiting_permission | error | ended
    last_event TEXT,
    last_event_at TEXT,
    last_assistant_message TEXT,
    permission_mode TEXT,
    tmux_session TEXT,           -- tmux session name (for helios-managed sessions)
    tmux_pane TEXT,              -- tmux pane target
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    ended_at TEXT
);

CREATE INDEX idx_sessions_status ON sessions(status);
```

### Notification Preferences

```sql
CREATE TABLE device_notification_settings (
    device_kid TEXT PRIMARY KEY,
    permission_request BOOLEAN DEFAULT TRUE,
    session_started BOOLEAN DEFAULT FALSE,
    session_completed BOOLEAN DEFAULT TRUE,
    session_error BOOLEAN DEFAULT TRUE,
    session_idle BOOLEAN DEFAULT FALSE,
    FOREIGN KEY (device_kid) REFERENCES devices(kid)
);
```

## New Hooks to Install

Add to the Claude Code hook configuration alongside existing hooks:

| Hook Event | Endpoint | Purpose |
|------------|----------|---------|
| `UserPromptSubmit` | `POST /hooks/user-prompt` | Mark session active when user sends prompt |
| `PostToolUse` | `POST /hooks/post-tool-use` | Track tool activity |

Existing hooks to update (capture more fields):

| Hook Event | New fields to capture |
|------------|----------------------|
| `SessionStart` | `transcript_path`, `model` |
| `Stop` | `last_assistant_message`, `stop_hook_active` |
| All hooks | `transcript_path`, `permission_mode` |

## Expanded Hook Input Struct

```go
type hookInput struct {
    SessionID             string          `json:"session_id"`
    CWD                   string          `json:"cwd"`
    TranscriptPath        string          `json:"transcript_path,omitempty"`
    Model                 string          `json:"model,omitempty"`
    PermissionMode        string          `json:"permission_mode,omitempty"`
    ToolName              string          `json:"tool_name,omitempty"`
    ToolInput             json.RawMessage `json:"tool_input,omitempty"`
    LastAssistantMessage  string          `json:"last_assistant_message,omitempty"`
    StopHookActive        bool            `json:"stop_hook_active,omitempty"`
    Prompt                string          `json:"prompt,omitempty"`  // UserPromptSubmit
    HookEventName         string          `json:"hook_event_name,omitempty"`
}
```

## API Design

### Internal API (port 7654, localhost only, no auth)

```
GET  /internal/sessions                  List all sessions with status
GET  /internal/sessions/:id              Session detail
POST /internal/sessions/:id/message      Send prompt via tmux send-keys
```

### Public API (port 7655, JWT auth)

```
GET  /api/sessions                       List sessions
GET  /api/sessions/:id                   Session detail
GET  /api/sessions/:id/history           Transcript (parsed from .jsonl)
     ?limit=50&offset=0                  Paginated, newest first
POST /api/sessions/:id/message           Send prompt (requires session idle)

GET  /api/settings/notifications         Get device notification preferences
POST /api/settings/notifications         Update notification preferences
```

### Session List Response

```json
GET /api/sessions

{
  "sessions": [
    {
      "claude_session_id": "343b040c-4a6a-4ec6-b03d-f61ab9505b8c",
      "cwd": "/Users/user/workspace/helios",
      "project": "helios",
      "status": "idle",
      "model": "claude-sonnet-4-6",
      "last_event": "Stop",
      "last_event_at": "2026-04-10T10:35:00Z",
      "last_assistant_message": "I've completed the refactoring...",
      "permission_mode": "default",
      "pending_permissions": 0,
      "created_at": "2026-04-10T10:23:00Z"
    }
  ]
}
```

### Transcript History Response

```json
GET /api/sessions/:id/history?limit=50

{
  "messages": [
    {
      "role": "user",
      "text": "refactor the auth module",
      "timestamp": "2026-04-10T10:23:15Z"
    },
    {
      "role": "assistant",
      "text": "I'll refactor the auth module. Let me start by...",
      "timestamp": "2026-04-10T10:23:18Z"
    },
    {
      "role": "tool_use",
      "tool": "Read",
      "input_summary": "auth.go",
      "timestamp": "2026-04-10T10:23:19Z"
    },
    {
      "role": "tool_result",
      "success": true,
      "timestamp": "2026-04-10T10:23:20Z"
    }
  ],
  "total": 164,
  "returned": 50,
  "offset": 114
}
```

### Send Prompt

```json
POST /api/sessions/:id/message
{ "message": "now add unit tests for the auth module" }

// Success (session was idle)
200 { "success": true }

// Session is busy
409 { "success": false, "error": "session_busy", "message": "Session is currently active" }

// Session ended
410 { "success": false, "error": "session_ended" }

// No tmux mapping (session not managed by helios)
400 { "success": false, "error": "no_tmux", "message": "Session not managed by helios. Use 'helios new' to create managed sessions." }
```

### Notification Settings

```json
GET /api/settings/notifications
{
  "permission_request": true,
  "session_started": false,
  "session_completed": true,
  "session_error": true,
  "session_idle": false
}

POST /api/settings/notifications
{ "session_started": true, "session_idle": true }
// Merges with existing settings

200 { "success": true }
```

## SSE Events (new)

```
event: session_status_changed
data: {"session_id": "abc", "status": "idle", "previous_status": "active", "cwd": "/path"}

event: session_activity
data: {"session_id": "abc", "tool": "Edit", "summary": "edited auth.go"}
```

## Transcript Reader

New package `internal/transcript/reader.go`:

- Reads `.jsonl` file line by line
- Filters to meaningful entries: `user`, `assistant`, `tool_use`, `tool_result`
- Skips internal types: `permission-mode`, `file-history-snapshot`, `attachment`, `system`
- Summarizes tool_use/tool_result for compact display
- Supports pagination (offset + limit from end of file)

```go
type TranscriptMessage struct {
    Role         string `json:"role"`          // user | assistant | tool_use | tool_result
    Text         string `json:"text,omitempty"`
    Tool         string `json:"tool,omitempty"`
    InputSummary string `json:"input_summary,omitempty"`
    Success      *bool  `json:"success,omitempty"`
    Timestamp    string `json:"timestamp"`
}

func ReadTranscript(path string, limit, offset int) (*TranscriptResult, error)
```

## tmux Integration

New package `internal/tmux/client.go`:

```go
type Client struct{}

func (c *Client) HasSession(name string) bool
func (c *Client) CreateSession(name, cwd, command string) error
func (c *Client) SendKeys(target, text string) error
func (c *Client) CapturePane(target string) (string, error)
func (c *Client) KillSession(name string) error
func (c *Client) ListSessions() ([]string, error)
```

### `helios new` command

```bash
helios new "refactor auth" --cwd ~/workspace/app
# Creates tmux session "helios-refactor-auth"
# Runs: claude --name "refactor auth" inside it
# Tracks the tmux_session mapping in the sessions table
```

### Safety: Sending Prompts

Before `tmux send-keys`:
1. Check session status is `idle` (Stop hook fired, no subsequent activity)
2. Double-check with `tmux capture-pane` — last line should match Claude's prompt pattern
3. If status is `active` or `waiting_permission`, reject with 409

## Mobile App Changes

### Navigation Restructure

Current: single `DashboardScreen` showing notifications.

New: **bottom tab bar** with 3 tabs:

```
┌──────────────────────────────────────┐
│           helios                   • │
│──────────────────────────────────────│
│                                      │
│   (tab-specific content here)        │
│                                      │
│                                      │
│                                      │
│                                      │
│──────────────────────────────────────│
│  Sessions   Notifications  Settings  │
│     ●            ○            ○      │
└──────────────────────────────────────┘
```

### Sessions Screen

```
┌──────────────────────────────────────┐
│           Sessions                   │
│──────────────────────────────────────│
│                                      │
│  ● helios                     idle   │
│    ~/workspace/helios       2m ago   │
│    "I've completed the refac..."     │
│                                      │
│  ◉ opal-app               active    │
│    ~/workspace/opal-app     now      │
│    Running: Edit auth.go             │
│                                      │
│  ◬ openclaw          waiting perm    │
│    ~/workspace/openclaw    30s ago   │
│    Bash: npm run test                │
│                                      │
│  ○ llm-learning            ended    │
│    ~/workspace/llm-learning  3h ago  │
│                                      │
│──────────────────────────────────────│
│  Sessions   Notifications  Settings  │
└──────────────────────────────────────┘

Status indicators:
  ● green  = idle (ready for input)
  ◉ blue   = active (Claude working)
  ◬ orange = waiting permission
  ✗ red    = error
  ○ grey   = ended
```

### Session Detail Screen

```
┌──────────────────────────────────────┐
│  ← helios                   ● idle  │
│  ~/workspace/helios · sonnet · 47t   │
│──────────────────────────────────────│
│                                      │
│  You                       10:23 AM  │
│  refactor the auth module            │
│                                      │
│  Claude                    10:23 AM  │
│  I'll refactor the auth module.      │
│  Let me start by reading the         │
│  current implementation...           │
│                                      │
│  ┌ Read auth.go ──────────────────┐  │
│  └ ✓ ────────────────────────────┘  │
│                                      │
│  ┌ Edit auth.go ──────────────────┐  │
│  │ replaced validateToken()       │  │
│  └ ✓ ────────────────────────────┘  │
│                                      │
│  Claude                    10:24 AM  │
│  I've completed the refactoring.     │
│  The auth module now uses...         │
│                                      │
│  ┌──────────────────────── Send ──┐  │
│  │ Type a prompt...               │  │
│  └────────────────────────────────┘  │
└──────────────────────────────────────┘

- Tool calls shown collapsed with tool name + summary
- Tap to expand and see full input/output
- Input bar disabled when session is active/busy
- Shows "Claude is working..." when status=active
```

### Settings Screen

```
┌──────────────────────────────────────┐
│           Settings                   │
│──────────────────────────────────────│
│                                      │
│  NOTIFICATIONS                       │
│                                      │
│  Permission requests          [ON]   │
│  Session started              [OFF]  │
│  Session completed            [ON]   │
│  Session error                [ON]   │
│  Session idle                 [OFF]  │
│                                      │
│  DEVICE                              │
│                                      │
│  Name: Android — Helios App          │
│  Status: active                      │
│  Push: enabled                       │
│                                      │
│  [Disconnect]                        │
│                                      │
│──────────────────────────────────────│
│  Sessions   Notifications  Settings  │
└──────────────────────────────────────┘
```

### Notification Preferences Storage

- Stored on device in `SharedPreferences` for local filtering
- Synced to server via `POST /api/settings/notifications` so the server skips push for disabled types
- Server checks per-device preferences before calling `push.SendToAll`

## Push Notification Filtering

The push sender currently broadcasts to all subscriptions. With preferences:

```go
// Before: send to everyone
func (s *Sender) SendToAll(payload PushPayload) { ... }

// After: check per-device preferences
func (s *Sender) SendToAll(payload PushPayload) {
    subs, _ := s.db.ListPushSubscriptions()
    for _, sub := range subs {
        if sub.DeviceKID != "" {
            prefs, _ := s.db.GetNotificationSettings(sub.DeviceKID)
            if !prefs.ShouldNotify(payload.Type) {
                continue // skip this device
            }
        }
        go s.sendToSubscription(sub, data)
    }
}
```

## Implementation Order

| Phase | What | Depends on |
|-------|------|-----------|
| 1 | Expand hook input parsing + new session schema + state machine | — |
| 2 | Session API endpoints (list, detail) | Phase 1 |
| 3 | Transcript reader (parse .jsonl) | Phase 1 |
| 4 | Mobile: Sessions tab + session detail with history | Phase 2, 3 |
| 5 | Mobile: Bottom tab navigation restructure | Phase 4 |
| 6 | Notification settings (backend + mobile) | — |
| 7 | Push filtering by preferences | Phase 6 |
| 8 | tmux integration + `helios new` | Phase 1 |
| 9 | Mobile: Send prompt input bar | Phase 4, 8 |

## Out of Scope (Future)

- Creating new Claude sessions from the phone
- Subagent tracking / agent tree visualization
- Per-session notification overrides
- `claude -p --resume` based prompt sending (alternative to tmux)
- Session search / filtering
- Session grouping by project
