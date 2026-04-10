# 24 — Session Management with tmux

## Overview

Add session lifecycle tracking, transcript viewing, and remote prompting to Helios. Sessions are tmux-native: Helios launches Claude inside tmux panes, tracks them via hooks, and uses `tmux send-keys` for remote prompting. Desktop users can `tmux attach` to interact directly. tmux-resurrect + continuum provide crash/reboot survival.

## Current State

- `hook_sessions` table stores `(session_id, cwd, last_event)` — minimal, no status, no tmux mapping
- Mobile app is notification-only — no session list, no conversation view
- No way to launch Claude sessions from Helios
- No way to send prompts remotely
- Notifications have no link back to their session in the UI

## Goals

1. **Session lifecycle** — Track status (active → idle → ended) via hooks
2. **Session list** — See all sessions with status, project, last activity
3. **Session detail** — View conversation transcript from `.jsonl` files
4. **Remote prompting** — Send prompts to idle sessions via `tmux send-keys`
5. **Stop command** — Send Escape/Ctrl+C to active sessions via `tmux send-keys`
6. **Subagent tracking** — Track spawned subagents as children of their parent session
7. **Session creation** — `helios new "prompt"` launches Claude in a managed tmux pane
8. **Notification → session nav** — Tap a notification to jump to its session
9. **Crash recovery** — tmux-resurrect restores panes, Helios resumes Claude sessions
10. **Generic model** — Message model and renderer are provider-agnostic

## Architecture

### Generic Message Model

The message/transcript system is provider-agnostic. Provider-specific code translates raw transcripts into the generic model.

```
┌─────────────────────────────────────────────────────┐
│                  Generic Layer                      │
│                                                     │
│  Session       — id, status, cwd, tmux_pane, ...    │
│  Message       — role, content, timestamp, metadata │
│  MessageWidget — renders a Message                  │
│  SessionScreen — lists messages, prompt bar         │
└──────────────────────────┬──────────────────────────┘
                           │ uses
┌──────────────────────────▼──────────────────────────┐
│               Provider Layer (Claude)               │
│                                                     │
│  TranscriptParser  — .jsonl → List<Message>         │
│  ClaudeMessageCard — provider-specific rendering    │
│  ClaudeToolCard    — renders tool_use/tool_result   │
└─────────────────────────────────────────────────────┘
```

### Backend

```
internal/
  store/
    sessions.go          — sessions table CRUD
  tmux/
    client.go            — tmux operations (send-keys, list, create, capture)
  transcript/
    reader.go            — parse .jsonl → []Message (generic)
  provider/
    claude/
      transcript.go      — Claude-specific .jsonl parsing
      hooks.go           — session lifecycle hooks (existing, extended)
  server/
    api.go               — session API endpoints
    server.go            — route registration
```

### Mobile

```
mobile/lib/
  models/
    session.dart         — generic Session model
    message.dart         — generic Message model (role, content, metadata)
  providers/
    claude/
      transcript.dart    — Claude .jsonl → List<Message>
      message_card.dart  — Claude-specific message rendering
  screens/
    sessions_screen.dart — session list (generic)
    session_detail.dart  — message list + prompt bar (generic)
  widgets/
    message_card.dart    — generic message card (dispatches to provider)
```

## Session Lifecycle

### State Machine

```
SessionStart hook        → status: active
PermissionRequest hook   → status: waiting_permission
Permission resolved      → status: active
Stop hook                → status: idle  (Claude finished, waiting for input)
StopFailure hook         → status: error
SessionEnd hook          → status: ended
Prompt sent (remote)     → status: active
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
         PermissionReq│       Stop hook                  Remote prompt
                     │          │                          or terminal
                     ▼          ▼                           input
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

### tmux Mapping

When Helios creates a session (`helios new`), it records the tmux pane ID. For externally-created sessions (user runs `claude` directly in tmux), Helios can discover the mapping by walking the process tree:

1. `tmux list-panes -a -F '#{pane_id} #{pane_pid}'`
2. `pgrep -P <pane_pid>` → find child `claude` process
3. Match by CWD via `lsof -p <pid> | grep cwd`

This discovery runs on-demand (session list refresh), not continuously.

## Database Schema

### Replace `hook_sessions` with `sessions`

```sql
CREATE TABLE sessions (
    session_id TEXT PRIMARY KEY,       -- claude's session UUID
    source TEXT NOT NULL DEFAULT 'claude',
    cwd TEXT NOT NULL,
    project TEXT,                       -- derived from cwd basename
    transcript_path TEXT,
    model TEXT,
    status TEXT NOT NULL DEFAULT 'active',
    -- active | idle | waiting_permission | error | ended
    last_event TEXT,
    last_event_at TEXT,
    tmux_pane TEXT,                     -- e.g. "%7" (helios-managed sessions)
    tmux_pid INTEGER,                  -- claude process PID
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    ended_at TEXT
);

CREATE INDEX idx_sessions_status ON sessions(status);
CREATE INDEX idx_sessions_source ON sessions(source);
```

Migration: rename `hook_sessions` → `sessions` and add new columns.

## Hook Changes

### Expanded hookInput

```go
type hookInput struct {
    SessionID             string          `json:"session_id"`
    CWD                   string          `json:"cwd"`
    TranscriptPath        string          `json:"transcript_path,omitempty"`
    Model                 string          `json:"model,omitempty"`
    ToolName              string          `json:"tool_name,omitempty"`
    ToolInput             json.RawMessage `json:"tool_input,omitempty"`
    PermissionSuggestions json.RawMessage `json:"permission_suggestions,omitempty"`
    HookEventName         string          `json:"hook_event_name,omitempty"`
    MCPServerName         string          `json:"mcp_server_name,omitempty"`
    Message               string          `json:"message,omitempty"`
    Mode                  string          `json:"mode,omitempty"`
    RequestedSchema       json.RawMessage `json:"requested_schema,omitempty"`
    URL                   string          `json:"url,omitempty"`
    ElicitationID         string          `json:"elicitation_id,omitempty"`
}
```

### Updated Session Hooks

**SessionStart** — create/update session with `active` status, capture `transcript_path` and `model`:
```go
func handleSessionStart(...) {
    session := &store.Session{
        SessionID:      input.SessionID,
        Source:         "claude",
        CWD:           input.CWD,
        Project:       filepath.Base(input.CWD),
        TranscriptPath: input.TranscriptPath,
        Model:         input.Model,
        Status:        "active",
    }
    ctx.DB.UpsertSession(session)
    ctx.Notify("session_status", session)
}
```

**Stop** — set status to `idle`, resolve pending notifications (existing logic):
```go
func handleStop(...) {
    ctx.DB.UpdateSessionStatus(input.SessionID, "idle", "Stop")
    // ... existing resolve logic ...
    ctx.Notify("session_status", session)
}
```

**SessionEnd** — set status to `ended`:
```go
func handleSessionEnd(...) {
    ctx.DB.UpdateSessionStatus(input.SessionID, "ended", "SessionEnd")
    ctx.Notify("session_status", session)
}
```

**PermissionRequest** — set status to `waiting_permission` (set back to `active` on resolve).

### SSE Events

```
event: session_status
data: {"session_id": "abc", "status": "idle", "cwd": "/path", "project": "helios"}
```

## Generic Message Model

### Go (backend transcript reader)

```go
// internal/transcript/reader.go
package transcript

type MessageRole string
const (
    RoleUser      MessageRole = "user"
    RoleAssistant MessageRole = "assistant"
    RoleToolUse   MessageRole = "tool_use"
    RoleToolResult MessageRole = "tool_result"
    RoleSystem    MessageRole = "system"
)

type Message struct {
    Role      MessageRole            `json:"role"`
    Content   string                 `json:"content,omitempty"`
    Tool      string                 `json:"tool,omitempty"`
    Summary   string                 `json:"summary,omitempty"`
    Success   *bool                  `json:"success,omitempty"`
    Metadata  map[string]interface{} `json:"metadata,omitempty"`
    Timestamp string                 `json:"timestamp"`
}

type TranscriptResult struct {
    Messages []Message `json:"messages"`
    Total    int       `json:"total"`
    Returned int       `json:"returned"`
    Offset   int       `json:"offset"`
}

// Parser is implemented by each provider to convert raw transcript to generic messages.
type Parser interface {
    Parse(path string, limit, offset int) (*TranscriptResult, error)
}
```

### Claude Transcript Parser

```go
// internal/provider/claude/transcript.go
package claude

// Claude .jsonl format — each line:
// {"type": "user", "message": {"content": [...]}, "timestamp": "..."}
// {"type": "assistant", "message": {"content": [...]}, "timestamp": "..."}
// {"type": "tool_use", ...}
// {"type": "tool_result", ...}
// Skip: "system", "permission-mode", "file-history-snapshot", "attachment"

func (p *TranscriptParser) Parse(path string, limit, offset int) (*transcript.TranscriptResult, error) {
    // Read .jsonl line by line
    // Filter to user, assistant, tool_use, tool_result
    // Convert to generic transcript.Message
    // Summarize tool_use: "Read auth.go", "Edit server.go line 42"
    // Support pagination (offset + limit from end)
}
```

### Dart (mobile)

```dart
// models/message.dart
enum MessageRole { user, assistant, toolUse, toolResult, system }

class Message {
  final MessageRole role;
  final String? content;
  final String? tool;
  final String? summary;
  final bool? success;
  final Map<String, dynamic>? metadata;
  final String timestamp;

  factory Message.fromJson(Map<String, dynamic> json);
}
```

```dart
// models/session.dart
class Session {
  final String sessionId;
  final String source;
  final String cwd;
  final String? project;
  final String status;  // active | idle | waiting_permission | error | ended
  final String? model;
  final String? lastEvent;
  final String? lastEventAt;
  final String createdAt;
  final String? endedAt;
  final int pendingPermissions;  // computed from notifications

  factory Session.fromJson(Map<String, dynamic> json);
}
```

## Message Rendering

### Generic Message Card (router)

```dart
// widgets/message_card.dart
class MessageCard extends StatelessWidget {
  final Message message;
  final String source;  // "claude", "hermes", etc.

  @override
  Widget build(BuildContext context) {
    // Dispatch to provider-specific renderer
    switch (source) {
      case 'claude':
        return ClaudeMessageCard(message: message);
      default:
        return DefaultMessageCard(message: message);
    }
  }
}
```

### Claude Message Card

```dart
// providers/claude/message_card.dart
class ClaudeMessageCard extends StatelessWidget {
  final Message message;

  @override
  Widget build(BuildContext context) {
    switch (message.role) {
      case MessageRole.user:
        return _UserBubble(message.content);
      case MessageRole.assistant:
        return _AssistantBubble(message.content);
      case MessageRole.toolUse:
        return ClaudeToolCard(
          tool: message.tool,
          summary: message.summary,
          metadata: message.metadata,
        );
      case MessageRole.toolResult:
        return _ToolResultChip(success: message.success);
      default:
        return const SizedBox.shrink();
    }
  }
}
```

### Default Message Card (fallback)

```dart
// widgets/default_message_card.dart
class DefaultMessageCard extends StatelessWidget {
  // Simple text rendering for any provider without a custom renderer
}
```

## Stop Command

Pressing the stop button in the mobile UI sends Escape to the tmux pane — identical to pressing Escape in the terminal:

```go
// tmux send-keys -t %<pane> Escape
func (c *Client) SendEscape(paneID string) error
```

**API:**
```
POST /api/sessions/:id/stop

// Success
200 {"success": true}

// Session not active
409 {"success": false, "error": "session_not_active"}

// No tmux pane
400 {"success": false, "error": "no_tmux_pane"}
```

**Mobile UI:** Red stop button visible when session status is `active` or `waiting_permission`. Replaces the send button in the prompt bar.

```
┌──────────────────────────────────────┐
│  Claude is working...           ■    │  ← stop button (red square icon)
└──────────────────────────────────────┘
```

When idle, the stop button hides and the prompt input appears:

```
┌──────────────────────── Send ──┐
│ Type a prompt...               │
└────────────────────────────────┘
```

## Subagent Tracking

Claude spawns subagents (Agent tool) that run as child sessions. We can track them via hooks and transcript files.

### How Subagents Work

- `SubagentStart` hook fires with `session_id` (parent) and agent metadata
- `SubagentStop` hook fires when the subagent finishes
- Subagent transcripts: `~/.claude/projects/<slug>/<parent-session-id>/subagents/agent-<agent-id>.jsonl`
- Meta files: `agent-<agent-id>.meta.json` → `{"agentType": "general-purpose", "description": "..."}`
- Each transcript entry has both `sessionId` (parent) and `agentId`

### Database

```sql
CREATE TABLE subagents (
    agent_id TEXT PRIMARY KEY,          -- e.g. "a263a417722ddc204"
    parent_session_id TEXT NOT NULL,    -- parent session UUID
    agent_type TEXT,                    -- "general-purpose", "Explore", "Plan", etc.
    description TEXT,                   -- from meta.json or SubagentStart hook
    status TEXT NOT NULL DEFAULT 'active',  -- active | completed
    transcript_path TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    ended_at TEXT,
    FOREIGN KEY (parent_session_id) REFERENCES sessions(session_id)
);

CREATE INDEX idx_subagents_parent ON subagents(parent_session_id);
```

### Hooks to Install

```json
{
  "SubagentStart": [{"hooks": [{"type": "http", "url": "http://localhost:7654/hooks/claude/subagent/start"}], "matcher": "*"}],
  "SubagentStop":  [{"hooks": [{"type": "http", "url": "http://localhost:7654/hooks/claude/subagent/stop"}],  "matcher": "*"}]
}
```

### Hook Handlers

```go
func handleSubagentStart(ctx *provider.HookContext, ...) {
    // Create subagent record linked to parent session
    ctx.DB.CreateSubagent(&store.Subagent{
        AgentID:         input.AgentID,
        ParentSessionID: input.SessionID,
        AgentType:       input.AgentType,
        Description:     input.Description,
        Status:          "active",
    })
    ctx.Notify("subagent_started", ...)
}

func handleSubagentStop(ctx *provider.HookContext, ...) {
    ctx.DB.UpdateSubagentStatus(input.AgentID, "completed")
    ctx.Notify("subagent_completed", ...)
}
```

### API

```
GET /api/sessions/:id/subagents            List subagents for a session
GET /api/subagents/:agent_id/transcript     Subagent transcript (parsed)
```

### Mobile UI — Session Detail

Subagents appear inline in the parent session transcript as expandable cards:

```
┌──────────────────────────────────────┐
│  ┌ 🤖 Explore: "Find API endpoints" ┐
│  │ Status: completed · 12 messages   │
│  │ [Tap to view transcript]          │
│  └───────────────────────────────────┘
```

Tapping opens the subagent's own transcript in a nested view.

The session detail header shows active subagent count:

```
│  ← helios          ● idle · 2 agents │
```

## Suspend and Resume

Two levels of control:

### Stop (interrupt)

Send Escape → Claude stops its current turn → status becomes `idle`. The Claude process stays alive in tmux. Resume by sending a new prompt.

- **Use case:** Claude is doing something wrong, you want to redirect it
- **Mobile UI:** Stop button (■) in the bottom bar when active
- **State:** `active` → `idle` (Claude process alive, waiting for input)

### Suspend

Kill the Claude process entirely. The tmux pane goes back to a shell prompt. Session is preserved server-side — resume later with `claude --resume`.

- **Use case:** Free up resources, you're done for now but want to come back later
- **Mobile UI:** Suspend button (⏸) next to stop, or in session context menu
- **State:** `idle` or `active` → `suspended` (process dead, session resumable)

```go
func (c *Client) Suspend(paneID string) error {
    // Send Ctrl+C to kill Claude gracefully
    // tmux send-keys -t <pane> C-c
    return c.exec("send-keys", "-t", paneID, "C-c")
}
```

### Resume (from suspended or ended)

Spawn a new Claude process in tmux with `--resume`:

```go
func resumeSession(session *store.Session, tmux *tmux.Client) error {
    cmd := fmt.Sprintf("cd %s && claude --resume %s", session.CWD, session.SessionID)
    paneID, err := tmux.CreateWindow("helios", session.CWD, cmd)
    if err != nil {
        return err
    }
    session.TmuxPane = paneID
    session.Status = "active"
    return db.UpdateSession(session)
}
```

- Claude loads full conversation history from its server-side state
- `SessionStart` hook fires → Helios updates session status to `active`
- All hooks fire normally — mobile app sees the session come back alive

**API:**
```
POST /api/sessions/:id/suspend   Suspend (kill process, keep session)
POST /api/sessions/:id/resume    Resume (new tmux pane + claude --resume)

// Suspend
200 {"success": true, "status": "suspended"}
409 {"error": "session_ended"}  // can't suspend ended session

// Resume
200 {"success": true, "status": "active", "tmux_pane": "%12"}
409 {"error": "session_active"}  // already running
```

### State Machine (updated)

```
                    ┌──────────────────┐
                    │   SessionStart   │
                    └────────┬─────────┘
                             │
                             ▼
              ┌─────────────────────────────┐
              │          active              │◄──────────────┐
              │  (Claude is working)         │               │
              └──────┬──────────┬───────────┘               │
                     │          │                            │
         PermissionReq│       Stop hook              Resume (prompt
                     │          │                     or /resume)
                     ▼          ▼                            │
        ┌────────────────┐  ┌──────────┐                    │
        │    waiting_     │  │   idle   │────────────────────┘
        │   permission    │  │          │
        └───────┬────────┘  └─────┬────┘
                │                 │
          resolved           Suspend (kill)
                │                 │
                ▼                 ▼
              active       ┌────────────┐
                           │ suspended  │──── Resume ──→ active
                           └────────────┘
                                 │
                           SessionEnd (or timeout)
                                 │
                                 ▼
                           ┌───────┐
                           │ ended │──── Resume ──→ active
                           └───────┘

        StopFailure at any point → status: error
```

### Mobile UI

Session detail screen — context menu (⋮) with:
- **Suspend session** (when active/idle) → kills process, shows "Session suspended"
- **Resume session** (when suspended/ended) → spawns new tmux pane

Session list — status badges:
```
  ● green   = idle (prompt ready)
  ◉ blue    = active (working)
  ◬ orange  = waiting permission
  ⏸ yellow  = suspended
  ✗ red     = error
  ○ grey    = ended
```

## tmux Client

```go
// internal/tmux/client.go
package tmux

type Client struct{}

// HasPane checks if a tmux pane exists.
func (c *Client) HasPane(paneID string) bool

// CreateWindow creates a new tmux window, returns the pane ID.
func (c *Client) CreateWindow(sessionName, cwd, command string) (string, error)

// SendKeys types text into a pane. Only safe when session status is "idle".
func (c *Client) SendKeys(paneID, text string) error

// SendEscape sends Escape key to stop Claude (like pressing Esc in terminal).
func (c *Client) SendEscape(paneID string) error

// CapturePane captures the visible content of a pane (for verification).
func (c *Client) CapturePane(paneID string) (string, error)

// ListClaudePanes walks the process tree to find panes running Claude.
// Returns map[paneID]claudePID.
func (c *Client) ListClaudePanes() (map[string]int, error)

// EnsureSession creates the "helios" tmux session if it doesn't exist.
func (c *Client) EnsureSession() error
```

### Process Tree Walk

```go
func (c *Client) ListClaudePanes() (map[string]int, error) {
    // 1. tmux list-panes -a -F '#{pane_id} #{pane_pid}'
    // 2. For each pane_pid: pgrep -P <pid> to find children
    // 3. Check if child process is "claude" (ps -o comm= -p <pid>)
    // 4. Return map[pane_id]claude_pid
}
```

## API Endpoints

### Internal API (port 7654)

```
GET  /internal/sessions                    List all sessions
POST /internal/sessions                    Create session (helios new)
POST /internal/sessions/:id/send           Send prompt via tmux (internal only)
```

### Public API (port 7655, JWT auth)

```
GET  /api/sessions                         List sessions with status
GET  /api/sessions/:id                     Session detail
GET  /api/sessions/:id/transcript          Parsed transcript (paginated)
     ?limit=50&offset=0
POST /api/sessions/:id/send                Send prompt (requires idle status)
```

### Responses

```json
GET /api/sessions

{
  "sessions": [
    {
      "session_id": "343b040c-...",
      "source": "claude",
      "cwd": "/Users/user/workspace/helios",
      "project": "helios",
      "status": "idle",
      "model": "claude-sonnet-4-6",
      "last_event": "Stop",
      "last_event_at": "2026-04-10T10:35:00Z",
      "created_at": "2026-04-10T10:23:00Z",
      "pending_permissions": 0
    }
  ]
}
```

```json
GET /api/sessions/:id/transcript?limit=200&offset=0

{
  "messages": [
    {"role": "user", "content": "refactor the auth module", "timestamp": "..."},
    {"role": "assistant", "content": "I'll refactor the auth module...", "timestamp": "..."},
    {"role": "tool_use", "tool": "Read", "summary": "auth.go", "timestamp": "..."},
    {"role": "tool_result", "success": true, "timestamp": "..."}
  ],
  "total": 164,
  "returned": 164,
  "offset": 0,
  "has_more": false
}

// Pagination: offset counts from the END (newest).
// offset=0, limit=200 → last 200 messages
// offset=200, limit=200 → messages 201-400 from the end
// This supports future infinite scroll (load older on scroll up).
```

**Mobile v1:** Always requests `?limit=200` (last 200 messages). No infinite scroll — the API supports it but the UI won't implement it yet.
```

```json
POST /api/sessions/:id/send
{"message": "now add unit tests"}

// Success (idle session)
200 {"success": true}

// Session busy
409 {"success": false, "error": "session_busy"}

// Session ended — will resume via tmux
200 {"success": true, "resumed": true}

// No tmux pane (external session, not managed)
400 {"success": false, "error": "no_tmux_pane"}
```

## `helios new` Command

```bash
helios new "refactor the auth module" --cwd ~/workspace/app
```

1. Ensure tmux `helios` session exists → `tmux new-session -d -s helios` if needed
2. Create a new window → `tmux new-window -t helios: -c <cwd> "claude 'prompt'"`
3. Capture pane ID from output
4. Wait for `session.start` hook to fire → link pane ID to session ID in DB
5. Print: `Session started in tmux pane %N. Attach with: tmux attach -t helios`

### Sending Prompts

```go
func (s *InternalServer) handleSessionSend(w http.ResponseWriter, r *http.Request) {
    session := db.GetSession(id)

    if session.Status == "active" || session.Status == "waiting_permission" {
        // Busy — reject
        return 409
    }

    if session.Status == "ended" {
        // Resume: create new tmux window with claude --resume
        paneID := tmux.CreateWindow("helios", session.CWD,
            fmt.Sprintf("claude --resume %s", session.SessionID))
        db.UpdateSessionTmuxPane(id, paneID)
        // Prompt will be sent after Claude starts
    }

    if session.TmuxPane == "" {
        return 400 // not managed
    }

    // Verify pane exists and Claude is at prompt
    content := tmux.CapturePane(session.TmuxPane)
    if !looksLikePrompt(content) {
        return 409 // not ready
    }

    tmux.SendKeys(session.TmuxPane, message)
    db.UpdateSessionStatus(id, "active", "RemotePrompt")
}
```

## Mobile UI

### Navigation — Bottom Tabs

```
┌──────────────────────────────────────┐
│           helios                   • │
│──────────────────────────────────────│
│                                      │
│   (tab-specific content here)        │
│                                      │
│──────────────────────────────────────│
│  Sessions   Notifications  Settings  │
│     ●            ○            ○      │
└──────────────────────────────────────┘
```

### Sessions List Screen

```
┌──────────────────────────────────────┐
│           Sessions                   │
│──────────────────────────────────────│
│                                      │
│  ● helios                     idle   │
│    ~/workspace/helios       2m ago   │
│                                      │
│  ◉ opal-app               active    │
│    ~/workspace/opal-app     now      │
│                                      │
│  ◬ openclaw          waiting perm    │
│    ~/workspace/openclaw    30s ago   │
│                                      │
│  ○ llm-learning            ended    │
│    ~/workspace/llm-learning  3h ago  │
│                                      │
│──────────────────────────────────────│
│  Sessions   Notifications  Settings  │
└──────────────────────────────────────┘

Status:
  ● green  = idle (prompt ready)
  ◉ blue   = active (working)
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
│                                      │
│  ┌──────────────────────── Send ──┐  │
│  │ Type a prompt...               │  │
│  └────────────────────────────────┘  │
└──────────────────────────────────────┘

- Tool calls collapsed by default, tap to expand
- Prompt bar enabled when status=idle, disabled otherwise
- Shows "Claude is working..." indicator when active
- Pull to refresh transcript
```

### Notification → Session Navigation

Each notification already has `source_session`. Tapping a notification in the list navigates to the session detail screen:

```dart
// In notification card or history card
onTap: () {
  Navigator.push(context, MaterialPageRoute(
    builder: (_) => SessionDetailScreen(sessionId: n.sourceSession),
  ));
}
```

## tmux-resurrect Integration

### Setup

`helios setup-resurrect` configures tmux plugins:

1. Install TPM if not present
2. Add tmux-resurrect + tmux-continuum to `~/.tmux.conf`
3. Configure: don't auto-restore claude processes (Helios handles resume)
4. Set continuum save interval to 5 minutes

### Recovery Flow

```
Terminal crash / reboot
    │
    ▼
tmux-continuum auto-restores tmux sessions + pane layouts
    │
    ▼
Panes exist but Claude processes are dead
    │
    ▼
helios daemon starts → checks sessions table
    │
    ▼
For each session with status != "ended":
    ├── tmux pane exists + Claude alive → OK
    ├── tmux pane exists + Claude dead → resume: `claude --resume <id>`
    └── tmux pane gone → create pane + resume
    │
    ▼
All sessions restored (Claude's state is server-side)
```

## Implementation Order

| Phase | What | Files |
|-------|------|-------|
| 1 | `sessions` + `subagents` tables, migrate from `hook_sessions` | `store/sessions.go`, `store/store.go` |
| 2 | Update hook handlers for session lifecycle | `provider/claude/hooks.go` |
| 3 | Subagent hooks (start/stop) + install | `provider/claude/hooks.go`, `daemon/hooks.go` |
| 4 | Session API endpoints (list, detail, subagents) | `server/api.go`, `server/server.go` |
| 5 | Generic transcript model + Claude parser | `transcript/reader.go`, `provider/claude/transcript.go` |
| 6 | Transcript API endpoint (sessions + subagents) | `server/api.go` |
| 7 | tmux client (send-keys, send-escape, create, capture) | `tmux/client.go` |
| 8 | `helios new` + send prompt + stop endpoints | `cmd/helios/main.go`, `server/api.go` |
| 9 | Mobile: Session + Message models | `models/session.dart`, `models/message.dart` |
| 10 | Mobile: Sessions list screen + bottom tabs | `screens/sessions_screen.dart` |
| 11 | Mobile: Session detail + generic message card | `screens/session_detail.dart`, `widgets/message_card.dart` |
| 12 | Mobile: Claude message renderer | `providers/claude/message_card.dart` |
| 13 | Mobile: Prompt bar + send + stop button | `screens/session_detail.dart` |
| 14 | Mobile: Subagent cards in transcript | `providers/claude/message_card.dart` |
| 15 | Mobile: Notification → session navigation | `screens/dashboard_screen.dart` |
| 16 | `helios setup-resurrect` | `cmd/helios/main.go` |
| 17 | Daemon startup recovery | `daemon/daemon.go` |

## Out of Scope (Future)

- Per-session notification preferences
- Session search / filtering / grouping
- Multi-provider session support (hermes, codex) — model is ready, just needs parsers
- Session cost tracking
- Session sharing between devices
