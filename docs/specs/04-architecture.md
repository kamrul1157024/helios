# Architecture

## High-Level Components

```
claude-tmux (Go binary)
    |
    +-- TUI Layer (bubbletea)
    |     +-- Sidebar        -- full session list, grouped by dir, badges
    |     +-- Tab Bar        -- open sessions as tabs, quick switch
    |     +-- Main Pane      -- embedded tmux pane for active tab
    |     +-- Toast Overlay  -- bottom-right notification toasts
    |     +-- Notif Panel    -- slide-out notification feed (^a !)
    |     +-- Status Bar     -- keybindings, notification counter
    |
    +-- Session Manager
    |     +-- create(title, dir)  -- spawns tmux session, runs claude
    |     +-- list()              -- queries tmux + local state
    |     +-- attach(id)          -- open in tab + focus
    |     +-- suspend(id)         -- save session_id, kill tmux, free memory
    |     +-- resume(id)          -- claude --resume <session_id>, open tab
    |     +-- kill(id)            -- terminate tmux session, cleanup
    |     +-- rename(id, title)   -- update session title
    |
    +-- Tab Manager
    |     +-- open(session_id)    -- add tab, attach tmux pane
    |     +-- close(tab_idx)      -- detach pane, remove tab
    |     +-- focus(tab_idx)      -- switch visible pane, clear badges
    |     +-- reorder(from, to)   -- move tabs (v0.2)
    |
    +-- Notification Manager
    |     +-- notify(session, type, context)  -- CLI entrypoint for hooks
    |     +-- subscribe()                     -- TUI listens via unix socket/file watch
    |     +-- clear(session)                  -- auto-clear on tab focus
    |     +-- delivery:
    |           +-- TUI badge update (sidebar + tab)
    |           +-- Toast popup (5s auto-dismiss)
    |           +-- tmux window highlight
    |           +-- macOS notification (osascript)
    |           +-- Terminal bell (optional)
    |
    +-- Hook Installer
    |     +-- inject(session_id, project_dir)  -- write hooks to settings.local.json
    |     +-- remove(session_id)               -- cleanup on kill/suspend
    |
    +-- State Store (~/.claude-tmux/)
    |     +-- sessions.json                    -- persistent session state
    |     +-- notifications.json               -- notification queue
    |
    +-- tmux Backend
          +-- all sessions prefixed: claude-tmux-{id}
          +-- uses tmux CLI (shell out, no library)
```

## Technology Choices

| Decision           | Choice            | Rationale                                         |
|--------------------|-------------------|----------------------------------------------------|
| Language           | Go                | bubbletea is excellent, fast compile, single binary|
| TUI framework      | bubbletea         | Best Go TUI lib, active community, composable      |
| State storage      | JSON files        | Simple, human-readable, < 100 sessions expected    |
| tmux integration   | shell out to CLI  | tmux CLI is stable API, fewer deps                 |
| Session IDs        | auto-increment    | Easy to type: `attach 3` vs UUID                   |
| Keybindings        | vim-style + ^a    | j/k navigate, ^a prefix for actions (tmux-like)    |
| IPC (hooks -> TUI) | Unix socket       | Low latency, reliable, works cross-process         |

## Session State Machine

```
                  create
                    |
                    v
    +--------+  suspend  +------------+
    | ACTIVE | --------> | SUSPENDED  |
    +--------+           +------------+
        |                     |
        |   kill         resume |
        |                     |
        v                     v
    +--------+           +--------+
    |  DEAD  |           | ACTIVE |
    +--------+           +--------+
```

## State File Schema

### ~/.claude-tmux/sessions.json

```json
{
  "next_id": 5,
  "sessions": [
    {
      "id": 1,
      "title": "refactor-auth",
      "status": "active",
      "tmux_session": "claude-tmux-1",
      "claude_session_id": "abc123-def456",
      "working_dir": "/Users/user/workspace/opal-app",
      "created_at": "2026-04-09T10:23:00Z",
      "last_active_at": "2026-04-09T10:35:00Z",
      "turn_count": 47,
      "notifications": []
    },
    {
      "id": 3,
      "title": "write-tests",
      "status": "suspended",
      "tmux_session": null,
      "claude_session_id": "xyz789-uvw012",
      "working_dir": "/Users/user/workspace/other-project",
      "created_at": "2026-04-09T08:00:00Z",
      "last_active_at": "2026-04-09T09:15:00Z",
      "turn_count": 89,
      "notifications": []
    }
  ]
}
```

### ~/.claude-tmux/notifications.json

```json
{
  "notifications": [
    {
      "id": "notif-001",
      "session_id": 3,
      "type": "permission",
      "context": "Bash: npm run test",
      "timestamp": "2026-04-09T10:33:00Z",
      "read": false
    }
  ]
}
```

## IPC: Hook -> TUI Communication

When a hook fires, it runs `claude-tmux notify --session 2 --type permission`. This CLI command needs to communicate with the running TUI process.

### Option: Unix Domain Socket

```
claude-tmux (TUI) listens on ~/.claude-tmux/claude-tmux.sock

claude-tmux notify --session 2 --type permission
    |
    +---> connects to ~/.claude-tmux/claude-tmux.sock
    +---> sends JSON: {"session_id": 2, "type": "permission", "context": "..."}
    +---> TUI receives, updates state, shows toast
```

Pros: low latency, bidirectional, clean
Cons: need socket lifecycle management

### Fallback: File Watch

If the socket isn't available (TUI not running), write to notifications.json and let the TUI pick it up on next launch via fsnotify/polling.

## Directory Structure

```
claude-tmux/
+-- cmd/
|   +-- root.go          -- CLI root command
|   +-- tui.go           -- default: launch TUI
|   +-- new.go           -- claude-tmux new "title"
|   +-- ls.go            -- claude-tmux ls
|   +-- attach.go        -- claude-tmux attach <id>
|   +-- suspend.go       -- claude-tmux suspend <id>
|   +-- resume.go        -- claude-tmux resume <id>
|   +-- kill.go          -- claude-tmux kill <id>
|   +-- rename.go        -- claude-tmux rename <id> "title"
|   +-- notify.go        -- claude-tmux notify (called by hooks)
+-- internal/
|   +-- tui/
|   |   +-- app.go       -- main bubbletea model
|   |   +-- sidebar.go   -- sidebar component
|   |   +-- tabs.go      -- tab bar component
|   |   +-- pane.go      -- main pane (tmux embed)
|   |   +-- toast.go     -- toast overlay
|   |   +-- panel.go     -- notification panel
|   |   +-- statusbar.go -- status bar
|   |   +-- styles.go    -- lipgloss styles
|   +-- session/
|   |   +-- manager.go   -- session CRUD
|   |   +-- state.go     -- state file read/write
|   +-- notification/
|   |   +-- manager.go   -- notification handling
|   |   +-- socket.go    -- unix socket server
|   |   +-- delivery.go  -- toast, OS notif, bell
|   +-- hooks/
|   |   +-- installer.go -- inject/remove hook configs
|   +-- tmux/
|       +-- client.go    -- tmux CLI wrapper
+-- docs/
|   +-- research/        -- this research folder
+-- go.mod
+-- go.sum
+-- main.go
+-- README.md
```
