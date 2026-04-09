# Flow Diagrams

## Flow 1: Create Session

```
User: helios new "fix auth bug" --provider claude
    |
    v
CLI sends: POST /api/sessions
    { title: "fix auth bug", provider: "claude", working_dir: "/home/user/app" }
    |
    v
Daemon Session Manager:
    +-- Assign session ID #3
    +-- Look up provider: "claude"
    +-- Ask Claude Provider: start command?
    |     → command: "claude"
    |     → hooks: inject HTTP hook config into .claude/settings.local.json
    +-- tmux Client: create window in helios session
    |     → tmux new-window -t helios -n "s3-fix-auth-bug"
    +-- tmux Client: run command in window
    |     → tmux send-keys -t helios:s3-fix-auth-bug "claude" Enter
    +-- State: save session to sessions.json
    +-- SSE: broadcast { type: "session_created", id: 3 }
    |
    v
CLI receives: 201 { id: 3, title: "fix auth bug", status: "active" }
TUI receives SSE: updates session list
```

## Flow 2: Create Session with Non-Claude Provider

```
User: helios new "refactor db" --provider aider
    |
    v
Daemon Session Manager:
    +-- Assign session ID #4
    +-- Look up provider: "aider"
    +-- Ask Aider Provider: start command?
    |     → command: "aider --model claude-3.5-sonnet"
    |     → hooks: NONE (aider has no hook system)
    +-- tmux Client: create window
    |     → tmux new-window -t helios -n "s4-refactor-db"
    +-- tmux Client: run command
    |     → tmux send-keys "aider --model claude-3.5-sonnet" Enter
    +-- Start pane scraper goroutine for session #4
    |     → polls tmux pane content every 2s
    |     → matches against aider's prompt/permission patterns
    +-- State: save session (capabilities: no resume, no hooks)
    +-- SSE: broadcast { type: "session_created", id: 4 }
```

## Flow 3: Send Message to Session

```
User (from Telegram): /send 3 add unit tests for auth module
    |
    v
Telegram Plugin (inside daemon, native call):
    → Session Manager: send message to session #3
    |
    v
Session Manager:
    +-- Look up session #3 (provider: claude)
    +-- Check status: active? YES
    +-- Ask Claude Provider: is session waiting for input?
    |     +-- Check hook state: last event was "Stop" → waiting for input
    |     +-- (fallback) capture pane, check for prompt pattern
    |     → YES
    +-- tmux Client: send keys
    |     → tmux send-keys -t helios:s3-fix-auth-bug "add unit tests for auth module" Enter
    +-- SSE: broadcast { type: "message_sent", session: 3 }
    |
    v
Telegram Plugin: reply "OK Message sent to session #3"
TUI receives SSE: could show activity indicator

--- same flow for aider session ---

Session Manager:
    +-- Look up session #4 (provider: aider)
    +-- Ask Aider Provider: is session waiting for input?
    |     +-- Pane scraper: last capture ends with ">>> " → waiting
    |     → YES
    +-- tmux send-keys: same mechanism, universal
```

## Flow 4: Permission Request (Claude — Hook-Based)

```
Claude (in tmux pane): hits permission prompt for Bash tool
    |
    v
Claude fires HTTP hook:
    POST localhost:7655/hooks/permission
    {
      session_id: "claude-abc",
      tool_name: "Bash",
      tool_input: { command: "rm -rf dist/" },
      cwd: "/home/user/app"
    }
    |
    v
Daemon Hook Handler:
    +-- Map claude session_id "claude-abc" → helios session #3
    +-- Check auto-approve mode for session #3
    |     Mode: "auto-safe", Tool: "Bash" → NOT in safe set → needs approval
    +-- Create notification
    |     { id: "notif-007", type: "permission", session: 3, tool: "Bash",
    |       detail: "rm -rf dist/" }
    +-- Save to notification state
    +-- Fan out to channels:
    |     +-- Desktop: osascript notification (if terminal unfocused)
    |     +-- ntfy: POST to ntfy.sh/helios-kamrul (with [Approve][Deny] actions)
    |     +-- Telegram: send message with inline keyboard buttons
    +-- SSE: broadcast { type: "notification", notification: {...} }
    +-- HOLD the HTTP response open (block — don't reply to Claude yet)
    |
    ... waiting for user decision from any client ...
```

## Flow 5: Permission Approval (from any client)

```
--- Option A: User approves from TUI ---
TUI sends: POST /api/notifications/notif-007/approve

--- Option B: User approves from Telegram ---
User taps [Approve] button
Telegram plugin calls daemon Action Router directly (native, no HTTP)

--- Option C: User approves from remote browser (via SSH tunnel) ---
Browser sends: POST localhost:7654/api/notifications/notif-007/approve

--- All three arrive at the same place ---
    |
    v
Daemon Action Router:
    +-- Idempotency check: notification notif-007 still pending? YES
    +-- Mark as resolved: status → approved, source → "telegram"
    +-- Respond to the HELD HTTP hook connection:
    |     {
    |       hookSpecificOutput: {
    |         hookEventName: "PermissionRequest",
    |         decision: { behavior: "allow" }
    |       }
    |     }
    +-- SSE: broadcast { type: "notification_resolved", id: "notif-007", action: "approved" }
    +-- Notify channels: "Permission approved for session #3 (via telegram)"
    |
    v
Claude receives HTTP response → executes Bash tool
All clients update UI (notification disappears from pending)
```

## Flow 6: Permission Detection (Aider — Pane Scraping)

```
Aider (in tmux pane): asks user to allow file edit
    Pane content: "Allow edits to src/db.py? (Y/n)"
    |
    v
Pane Scraper (goroutine, runs every 2s):
    +-- tmux capture-pane -t helios:s4-refactor-db -p
    +-- Match against Aider Provider's permission patterns
    +-- Pattern "^Allow .* to" matched!
    +-- Create notification
    |     { id: "notif-008", type: "permission", session: 4,
    |       tool: "file_edit", detail: "src/db.py" }
    +-- Fan out to channels (same as Claude flow)
    +-- SSE: broadcast notification
    |
    ... waiting for user decision ...
    |
    v
User approves (from any client)
    |
    v
Daemon:
    +-- Aider has no hook to respond to
    +-- Instead: send keystroke to approve
    |     → tmux send-keys -t helios:s4-refactor-db "y" Enter
    +-- Resolve notification
    +-- SSE: broadcast resolved
```

## Flow 7: Suspend and Resume

```
User: helios suspend 3
    |
    v
CLI sends: POST /api/sessions/3/suspend
    |
    v
Daemon Session Manager:
    +-- Look up session #3 (provider: claude)
    +-- Ask Claude Provider: capture resume ID
    |     → resume_id: "claude-abc-123" (captured from SessionStart hook)
    +-- Ask Claude Provider: graceful stop
    |     → send "/exit" to pane
    |     → wait for process to exit
    +-- Claude Provider: remove hooks from .claude/settings.local.json
    +-- tmux Client: kill window
    |     → tmux kill-window -t helios:s3-fix-auth-bug
    +-- State: update { status: "suspended", resume_id: "claude-abc-123" }
    +-- SSE: broadcast { type: "session_suspended", id: 3 }
    |
    v
CLI: "Session #3 suspended"

--- later ---

User: helios resume 3
    |
    v
CLI sends: POST /api/sessions/3/resume
    |
    v
Daemon Session Manager:
    +-- Load session #3: resume_id = "claude-abc-123", provider = "claude"
    +-- Ask Claude Provider: resume command?
    |     → command: "claude --resume claude-abc-123"
    +-- tmux Client: create new window
    |     → tmux new-window -t helios -n "s3-fix-auth-bug"
    +-- Claude Provider: re-inject hooks config
    +-- tmux Client: run resume command
    |     → tmux send-keys "claude --resume claude-abc-123" Enter
    +-- State: update { status: "active" }
    +-- SSE: broadcast { type: "session_resumed", id: 3 }
    |
    v
CLI: "Session #3 resumed"
Claude picks up conversation where it left off
```

## Flow 8: Suspend Non-Resumable Provider (Aider)

```
User: helios suspend 4
    |
    v
Daemon Session Manager:
    +-- Look up session #4 (provider: aider)
    +-- Ask Aider Provider: supports resume?
    |     → NO
    +-- Warn user: "Aider does not support resume. Suspending will lose session context."
    |
    v
CLI: "WARNING: Aider sessions cannot be resumed. Session will start fresh on resume. Continue? [y/N]"
    |
    v
User confirms
    |
    v
Daemon:
    +-- Stop pane scraper goroutine
    +-- Graceful stop: send Ctrl+C, then Ctrl+D
    +-- Kill tmux window
    +-- State: { status: "suspended", resume_id: null }
    |
    v
Resume later → starts a fresh aider session (no conversation history)
```

## Flow 9: Crash Recovery

```
T=0: Terminal killed / system crash
    +-- tmux server dies
    +-- All AI processes die
    +-- sessions.json on disk (intact)
    +-- tmux-resurrect last save on disk (saved by continuum every 5min)

T=1: User opens new terminal
    +-- tmux server auto-starts
    +-- tmux-continuum triggers auto-restore
    +-- tmux-resurrect restores helios session + window layouts
    +-- Windows are empty (AI processes not running)

T=2: User runs: helios daemon start
    |
    v
Daemon Startup Recovery:
    +-- Load sessions.json
    +-- For each session marked "active":
    |     |
    |     +-- tmux window exists? (restored by resurrect)
    |     |     +-- AI process alive? → OK, nothing to do
    |     |     +-- AI process dead? → ask provider to resume
    |     |           Claude Provider:
    |     |             resume_id exists → run "claude --resume <id>"
    |     |             re-inject hooks
    |     |           Aider Provider:
    |     |             no resume support → run fresh "aider" (context lost)
    |     |
    |     +-- tmux window missing? (resurrect failed or not installed)
    |           +-- Create new window
    |           +-- Ask provider to resume (same as above)
    |
    +-- For each session marked "suspended":
    |     → Nothing to do (already saved)
    |
    +-- Start pane scrapers for non-hook providers
    +-- Ready
    |
    v
All resumable sessions restored with conversation history intact.
Non-resumable sessions start fresh.
Data loss: zero for Claude (conversation is server-side).
```

## Flow 10: Remote Access via SSH Tunnel

```
You're on your phone with Termius (SSH client)
    |
    v
Create SSH tunnel:
    ssh -L 7654:localhost:7654 user@workstation
    |
    v
Now localhost:7654 on your phone → workstation's daemon
    |
    +-- GET /api/sessions → list all sessions
    |     Response: [
    |       { id: 1, title: "refactor-auth", provider: "claude", status: "active" },
    |       { id: 4, title: "refactor-db", provider: "aider", status: "active" }
    |     ]
    |
    +-- GET /api/events → SSE stream for real-time updates
    |     event: notification
    |     data: { id: "notif-007", type: "permission", session: 3, tool: "Bash" }
    |
    +-- POST /api/notifications/notif-007/approve → approve permission
    |     Response: { success: true }
    |
    +-- POST /api/sessions/3/message → send message to Claude
    |     { message: "add unit tests" }
    |     Response: { success: true }
    |
    +-- POST /api/sessions → create new session
    |     { title: "hotfix", provider: "claude", initial_message: "fix the NPE" }
    |     Response: { id: 5, status: "active" }
    |
    v
Full control. No public endpoint. No webhook. Just SSH.
```

## Flow 11: Auto-Approve Decision

```
Claude fires permission hook for session #3
    |
    v
Daemon Hook Handler:
    +-- Look up session #3 auto-approve mode
    |
    +-- Mode: "ask"
    |     → create notification, hold HTTP, wait for user
    |
    +-- Mode: "auto"
    |     → immediately respond with { behavior: "allow" }
    |     → log: "[auto] Bash: npm run test"
    |     → no notification created
    |
    +-- Mode: "auto-safe"
    |     +-- Tool is "Read"? → auto-approve, log, respond
    |     +-- Tool is "Bash"? → NOT in safe set → create notification, wait
    |
    +-- Mode: "auto-read"
    |     +-- Tool is "Grep"? → auto-approve
    |     +-- Tool is "Write"? → NOT read-only → create notification, wait
    |
    +-- Mode: "custom"
          +-- Look up per-tool rule for this session
          +-- Tool "Bash" rule = "ask" → create notification, wait
          +-- Tool "Read" rule = "auto" → auto-approve, log, respond
```

## Flow 12: Batch Approve from Notification Page

```
TUI: User is on notification page, sees 5 pending permissions
    |
    v
User presses Space on 3 notifications to select them
User presses 'B' for batch action
User selects 'Approve selected'
    |
    v
TUI sends 3 HTTP requests (or single batch request):
    POST /api/notifications/batch
    { notification_ids: ["notif-007", "notif-008", "notif-009"], action: "approve" }
    |
    v
Daemon Action Router:
    +-- For each notification:
    |     +-- Idempotency check
    |     +-- Resolve: respond to held hook connection (or send keystroke for pane-based)
    |     +-- Update state
    +-- SSE: broadcast 3 resolved events
    +-- Channels: "3 permissions approved for sessions #3, #4"
    |
    v
TUI: notifications move from PENDING to COMPLETED
All 3 Claude/Aider sessions continue executing
```

## Flow 13: Channel Notification Fan-Out

```
New notification created in daemon (from any source)
    |
    v
Channel Manager:
    +-- Check external_filter config:
    |     permission: true, error: true, done: false, idle: false
    |     This notification type: "permission" → YES, send to external channels
    |
    +-- Desktop channel (built-in):
    |     +-- Is terminal focused?
    |     |     YES → skip (user can see TUI)
    |     |     NO  → osascript notification
    |     |           "helios: Session #3 needs permission — Bash: rm -rf dist/"
    |
    +-- ntfy channel (plugin):
    |     +-- POST to ntfy.sh/helios-kamrul
    |     +-- Headers: Title, Priority (from priority_map: permission → high)
    |     +-- Body: "Session #3 — Permission: Bash — rm -rf dist/"
    |
    +-- Telegram channel (plugin):
    |     +-- Send message via bot API
    |     +-- Include inline keyboard: [Approve] [Deny]
    |     +-- Chat ID from config
    |
    +-- SSE stream:
          +-- broadcast to all connected HTTP clients
          +-- event: notification, data: { ... }
```
