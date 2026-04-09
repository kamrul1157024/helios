# Daemon Architecture

## The Core Insight

There are two distinct things:

1. **Daemon** — the brain. Manages tmux sessions, handles Claude hooks, runs the HTTP API, routes notifications, executes commands. Always running in the background.

2. **Clients** — the eyes and hands. TUI, Telegram bot, Slack bot, browser UI, CLI commands. They all talk to the daemon over HTTP. They are interchangeable and optional.

The daemon is the product. Clients are just interfaces.

## Component Separation

```
                    Clients (interchangeable)
                    ========================
                    
    TUI App         Telegram Bot      Slack Bot       CLI
    (bubbletea)     (channel plugin)  (channel plugin) (cobra)
        |                |                |              |
        +-------+--------+-------+--------+------+------+
                |                |               |
                v                v               v
            HTTP API         HTTP API         HTTP API
                |                |               |
        +-------+----------------+---------------+------+
        |                                               |
        |              DAEMON (claud)                    |
        |                                               |
        |   +-- HTTP Server (API)                       |
        |   |     sessions, notifications, commands     |
        |   |                                           |
        |   +-- Session Manager                         |
        |   |     create, suspend, resume, kill          |
        |   |     send message (via tmux send-keys)     |
        |   |                                           |
        |   +-- Notification Manager                    |
        |   |     receive from hooks, fan-out to clients|
        |   |     action routing (approve/deny)         |
        |   |                                           |
        |   +-- Hook Handler                            |
        |   |     receives hook calls from Claude       |
        |   |     translates to notifications/state     |
        |   |                                           |
        |   +-- Channel Manager                         |
        |   |     loads channel plugins                 |
        |   |     routes notifications to channels      |
        |   |     receives actions/commands from channels|
        |   |                                           |
        |   +-- tmux Client                             |
        |         talks to tmux server                  |
        |         create/kill sessions, send-keys       |
        |                                               |
        +-----------------------------------------------+
                         |
                         v
                    tmux server
                    (claude sessions running inside)
```

## What the Daemon Owns

- All session state (sessions.json)
- All notification state
- The HTTP API (single source of truth)
- Hook registration and handling
- tmux lifecycle management
- Channel plugin lifecycle

## What Clients Do

- Render UI (TUI, browser, mobile)
- Send HTTP requests to daemon
- Receive events via SSE/WebSocket from daemon
- They hold NO state of their own (stateless clients)

## Daemon Lifecycle

```
claud daemon start
    |
    +-- Preflight checks (tmux, claude CLI)
    +-- Load config (~/.claud/config.yaml)
    +-- Load session state (~/.claud/sessions.json)
    +-- Initialize channel plugins
    +-- Start HTTP server (localhost:7654)
    +-- Start hook HTTP handler (localhost:7655, for Claude hooks)
    +-- Reconcile sessions (check tmux state vs saved state)
    +-- Ready
    |
    ... running ...
    |
    +-- claud daemon stop
         +-- Graceful shutdown
         +-- Save state
         +-- Close channel plugins
         +-- Stop HTTP servers
```

## Hook Integration

Claude hooks call the daemon's hook endpoint directly (no shell scripts, no unix sockets):

```json
{
  "hooks": {
    "PermissionRequest": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "http",
            "url": "http://localhost:7655/hooks/permission",
            "timeout": 300
          }
        ]
      }
    ],
    "Stop": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "http",
            "url": "http://localhost:7655/hooks/stop"
          }
        ]
      }
    ],
    "Notification": [
      {
        "matcher": "permission_prompt|idle_prompt|elicitation_dialog",
        "hooks": [
          {
            "type": "http",
            "url": "http://localhost:7655/hooks/notification"
          }
        ]
      }
    ],
    "StopFailure": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "http",
            "url": "http://localhost:7655/hooks/stop-failure"
          }
        ]
      }
    ],
    "SessionEnd": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "http",
            "url": "http://localhost:7655/hooks/session-end"
          }
        ]
      }
    ]
  }
}
```

Using HTTP hooks instead of command hooks is cleaner — no shell scripts, the daemon handles everything directly, and the hook can block (wait for approval) by holding the HTTP response.

### Permission Approval via HTTP Hook

When Claude hits a permission prompt, the HTTP hook calls the daemon. The daemon holds the HTTP connection open until the user approves or denies from ANY client:

```
Claude: PermissionRequest hook fires
    |
    v
HTTP POST to daemon: localhost:7655/hooks/permission
    |
    v
Daemon:
    1. Creates notification (pending)
    2. Fans out to all channels (TUI badge, phone push, etc.)
    3. Holds the HTTP response open (blocking)
    4. Waits for action from any client
    |
    ... user approves from Telegram ...
    |
    v
Daemon:
    5. Returns HTTP response with approval JSON
    |
    v
Claude: receives approval, executes tool
```

## Why This Is Better

- **No shell scripts** — hooks are HTTP calls to the daemon directly
- **No unix socket IPC** — everything is HTTP
- **Stateless clients** — TUI crash? just restart it, daemon still running
- **Multiple clients simultaneously** — TUI + Telegram + browser all see the same state
- **Headless operation** — run daemon only, control entirely from Telegram
- **Testable** — HTTP API is easy to test, mock, and document

## Daemon as Systemd/Launchd Service

The daemon should be runnable as a background service:

```bash
# macOS launchd
claud daemon install    # installs LaunchAgent plist
claud daemon uninstall  # removes it

# Linux systemd
claud daemon install    # installs user systemd unit
claud daemon uninstall  # removes it

# Manual
claud daemon start      # foreground
claud daemon start -d   # background (daemonize)
claud daemon stop       # stop running daemon
claud daemon status     # is it running?
```
