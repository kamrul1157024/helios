# Notification System

## The Core Problem

When Claude Code is running in a tmux session you're not looking at, you miss:

1. **Permission prompts** — Claude is blocked waiting for approval
2. **Plan mode output** — Claude finished planning, needs your go-ahead
3. **Task completion** — Claude finished its work
4. **Errors** — rate limits, API failures, etc.

Without notifications, you context-switch back to find Claude's been idle for 10 minutes.

## Solution: Claude Code Hooks

Claude Code has a hooks system that fires shell commands on specific events. `claude-tmux` auto-injects hooks into each session that call back to the `claude-tmux notify` CLI command.

## Hook Configuration (auto-injected per session)

```json
{
  "hooks": {
    "Notification": [
      {
        "matcher": "permission_prompt|idle_prompt|elicitation_dialog",
        "hooks": [
          {
            "type": "command",
            "command": "claude-tmux notify --session $SESSION_ID --type awaiting-input"
          }
        ]
      }
    ],
    "Stop": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "command",
            "command": "claude-tmux notify --session $SESSION_ID --type done"
          }
        ]
      }
    ],
    "StopFailure": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "command",
            "command": "claude-tmux notify --session $SESSION_ID --type error"
          }
        ]
      }
    ],
    "PermissionRequest": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "command",
            "command": "claude-tmux notify --session $SESSION_ID --type permission"
          }
        ]
      }
    ]
  }
}
```

## Hook Events Mapped to Notifications

| Hook Event        | Matcher              | Badge      | Meaning                          |
|-------------------|----------------------|------------|----------------------------------|
| `Notification`    | `permission_prompt`  | `L PERMIT` | Claude needs permission approval |
| `Notification`    | `idle_prompt`        | `~ IDLE`   | Claude is idle, waiting for input|
| `Notification`    | `elicitation_dialog` | `? INPUT`  | MCP server wants user input      |
| `Stop`            | `*`                  | `+ DONE`   | Claude finished its response     |
| `StopFailure`     | `rate_limit`         | `! RATE`   | Hit API rate limit               |
| `StopFailure`     | `*`                  | `! ERROR`  | Something broke                  |
| `PermissionRequest`| `*`                 | `L PERMIT` | Permission dialog shown          |

## Three Notification Layers

### Layer 1: In-TUI Toast (bottom-right, auto-dismiss)

When a notification fires and you're on a different tab, a toast pops up at the bottom-right of the TUI. Auto-dismisses after 5 seconds.

```
|                                                                     |
|                                                                     |
|                                          +------------------------+ |
|                                          | L #3 tests             | |
|                                          | Permission: Bash       | |
|                                          | [Enter] go  [x] dismiss| |
|                                          +------------------------+ |
+---------------------------------------------------------------------+
| ^a n:new  ^a !:notifications(3)  ^a 1-9:tab                        |
+---------------------------------------------------------------------+
```

- **Enter** on the toast -> switches to that session's tab
- **x** or wait 5s -> dismisses toast, badge stays in sidebar
- Toasts stack (max 3 visible), newest on top

### Layer 2: Notification Panel (toggle with ^a !)

A slide-out panel from the right edge. Chronological feed, newest on top.

```
+--------------------+----------------------------------------------+
|  SESSIONS          | +--#1 refactor-auth--+--#2 fix x--+--+--+    |
|  -----------------  | |                          +--NOTIFICATIONS-+|
|  ~/workspace/opal   | |                          |               ||
|  * #1 refactor-auth | |                          | L #3 tests    ||
|    active . 3m      | |   Active Claude          |   Permission   ||
|  * #2 fix-api    !  | |   Session #1             |   Bash: rm -rf ||
|    error . 1m ago   | |                          |   2m ago       ||
|                     | |   (embedded tmux         | ---------------||
|  ~/other-project    | |    pane)                  | ! #2 fix-api   ||
|  * #3 tests   L     | |                          |   StopFailure  ||
|    permission . 2m  | |                          |   rate_limit   ||
|  S #4 explore       | |                          |   5m ago       ||
|    suspended . 3h   | |                          | ---------------||
|                     | |                          | + #1 refactor  ||
|                     | |                          |   Done         ||
|                     | |                          |   12m ago      ||
|                     | +--------------------------|               ||
|                     |  #1 refactor | 47t         +---------------+|
+--------------------+----------------------------------------------+
| ^a n:new  ^a !:notifications  ^a 1-9:tab  ^a s:suspend  ^a k:kill|
+---------------------------------------------------------------------+
```

### Layer 3: OS-Level Notification (when terminal not focused)

When the terminal isn't focused at all, fire a macOS native notification:

```
+------------------------------+
| claude-tmux                   |
| L #3 write-tests needs        |
| permission: Bash              |
|                               |
| Click to switch               |
+------------------------------+
```

Implementation: `osascript -e 'display notification "..." with title "claude-tmux"'`

### Layer 4: tmux Window Highlight

If the user is using tmux outside of the TUI, highlight the claude-tmux window in the tmux status bar:

```bash
tmux set-window-option -t claude-tmux-2 window-status-style "bg=red"
```

## Notification Card Anatomy

Each notification in the panel is a card:

```
+---------------------------+
| L #3 write-tests          |  <- icon + session ID + title
|   Permission requested     |  <- notification type
|   Bash: npm run test       |  <- context (tool name + input preview)
|   2m ago          [-> Go]  |  <- timestamp + action
+---------------------------+
```

| Icon | Type            | Context shown                    |
|------|-----------------|----------------------------------|
| L    | Permission      | tool name + input preview        |
| ~    | Idle/waiting    | last message from Claude         |
| +    | Done            | turn count                       |
| !    | Error           | error type (rate_limit, etc)     |
| ?    | Input needed    | MCP elicitation question         |

## Notification Lifecycle

```
Hook fires (Claude Code event)
    |
    v
claude-tmux notify --session 2 --type permission
    |
    +---> 1. Update session state (badge in sidebar + tab)
    |
    +---> 2. Show toast (bottom-right, 5s auto-dismiss)
    |
    +---> 3. tmux window highlight
    |
    +---> 4. macOS notification (if terminal unfocused)
    |
    +---> 5. Terminal bell (optional)
    
User sees notification
    |
    v
User switches to session tab
    |
    v
Badge auto-clears (you're looking at it now)
```

## Auto-Clear Logic

When a user switches to a session (via tab click, sidebar select, or toast Enter), all badges for that session are cleared automatically. No manual dismiss needed for the session-level state.

## Badge Counter in Status Bar

The status bar always shows unread notification count:

```
| ^a n:new  ^a !:notifications(3)  ^a 1-9:tab  ^a s:suspend        |
```

The `(3)` goes away as you address each session.

## Hook Installer

When `claude-tmux` creates a session, it writes hooks config to `.claude/settings.local.json` in the project directory. Hooks point back to `claude-tmux notify` CLI command, passing the session ID.

This is zero-config from the user's perspective.

### Install Flow

```
claude-tmux new "refactor auth"
    |
    +---> 1. Generate session ID (e.g., 3)
    +---> 2. Write hooks to .claude/settings.local.json
    +---> 3. Create tmux session "claude-tmux-3"
    +---> 4. Launch `claude` inside the tmux session
    +---> 5. Open as tab in TUI
```

### Cleanup

When a session is killed, hooks are removed from settings.local.json.
When a session is suspended, hooks are removed (re-injected on resume).
