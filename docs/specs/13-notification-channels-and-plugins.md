# Notification Channels & Plugin System

## Overview

Notification delivery is handled by **channel plugins**. Each plugin handles one delivery channel (ntfy, Slack, Telegram, etc.) and optionally supports **bidirectional interaction** (approve/deny from the phone) and **inbound commands** (send messages, create sessions remotely).

The core daemon doesn't know about Slack or ntfy. It only knows the channel interface.

## Architecture

```
Daemon (Channel Manager)
    |
    +-- Delivery Engine
    |     |
    |     +-- calls each registered channel plugin
    |     |
    |     +-- Channel: Desktop (built-in, always active)
    |     +-- Channel: ntfy plugin
    |     +-- Channel: slack plugin
    |     +-- Channel: telegram plugin
    |     +-- Channel: custom-webhook plugin
    |     +-- Channel: ... (user-built)
    |
    +-- Action Router
          |
          +-- receives actions (approve/deny) from ANY channel
          +-- routes action to the correct session's pending permission
          +-- ensures idempotency (same action from two channels = one execution)
```

Note: TUI is NOT a channel plugin. TUI is a client that talks to the daemon over HTTP, same as any other client.

## Three-Tier Interface Hierarchy

Channels implement whichever tier matches their capabilities:

### Tier 1: NotificationChannel (send-only)

Every channel must implement this. Minimum: receive a notification and deliver it.

- `Info()` — plugin metadata (name, display name, version, description)
- `Init(config)` — called once at startup with channel-specific config
- `Send(notification)` — deliver a notification
- `Capabilities()` — declare what this channel supports (send, actions, rich format, sound, priority)
- `Close()` — cleanup on shutdown

Examples: desktop, discord webhook, generic webhook

### Tier 2: InteractiveChannel (send + actions)

For channels that support approve/deny from the remote side.

- Everything from Tier 1, plus:
- `ListenForActions(callback)` — start listening for user actions. Runs in its own goroutine. When user taps "Approve" on their phone, call the callback.

Examples: ntfy (via response topics), telegram (inline keyboards)

### Tier 3: CommandableChannel (send + actions + commands)

For channels that support full inbound commands (send messages to sessions, create sessions, list status).

- Everything from Tier 2, plus:
- `ListenForCommands(callback)` — listen for inbound user commands
- `SendResult(result)` — send command result back to user

Examples: telegram (bot commands), slack (slash commands + threads)

The daemon uses type checking at runtime to see which tier a plugin implements.

## Notification Data Model

A notification contains:

- **ID** — unique identifier
- **Session ID** — which claud session this is about
- **Session Title** — human-readable session name
- **Type** — permission, done, error, idle, input
- **Tool** — tool name (for permission type, e.g., "Bash", "Write")
- **Detail** — command string, file path, error message, etc.
- **Timestamp** — when the event happened

## Action Data Model

An action contains:

- **Notification ID** — which notification this responds to
- **Type** — approve, deny, dismiss
- **Source** — which channel sent this action (for logging/display)

## Command Data Model

A command contains:

- **Type** — send_message, new_session, list_sessions, get_status, suspend, resume
- **Session ID** — target session (0 if not session-specific)
- **Body** — message text, session title, etc.
- **Source** — which channel sent this command

## Capability Flags

Channels declare capabilities via bitflags:

- **CapSend** — can send notifications
- **CapActions** — can receive approve/deny actions
- **CapCommands** — can receive inbound commands
- **CapRichFormat** — supports markdown/formatting
- **CapSound** — can play notification sound
- **CapPriority** — supports priority levels

## Plugin Registration

For v0.1, plugins are compiled into the binary. Registration via Go's `init()` mechanism. Each plugin registers itself with the channel registry at import time.

Future versions could support external plugin loading (subprocess plugins communicating via stdin/stdout JSON, similar to MCP protocol).

## Configuration

### ~/.claud/config.yaml

```yaml
notifications:
  # Desktop notifications (built-in)
  desktop:
    enabled: true
    sound: true
    only_when_unfocused: true

  # Channel plugins
  channels:
    ntfy:
      enabled: true
      topic: "claud-kamrul"
      server: "https://ntfy.sh"
      token: ""
      priority_map:
        permission: "high"
        error: "high"
        done: "default"
        idle: "low"

    slack:
      enabled: false
      webhook_url: "${SLACK_CLAUDE_WEBHOOK}"
      channel: "#claude-notifications"

    telegram:
      enabled: false
      bot_token: "${TELEGRAM_BOT_TOKEN}"
      chat_id: "123456789"
      commands:
        enabled: true
        allowed_users:
          - 123456789

    discord:
      enabled: false
      webhook_url: "${DISCORD_WEBHOOK_URL}"

    webhook:
      enabled: false
      url: "https://my-server.com/claud/notify"
      headers:
        Authorization: "Bearer ${WEBHOOK_TOKEN}"

  # Which notification types go to external channels
  # (desktop always gets everything)
  external_filter:
    permission: true
    error: true
    done: false
    idle: false
    input: true

  # Remote command security
  remote_commands:
    enabled: true
    require_confirmation: true
    allowed_commands:
      - list_sessions
      - get_status
      - send_message
      - suspend
```

Environment variables (`${VAR}`) are expanded at config load time. Secrets should never be hardcoded.

## Notification Flow

```
Claude Code hook fires (HTTP to daemon)
    |
    v
Daemon Notification Manager
    |
    +---> Desktop (built-in): osascript / notify-send (if terminal unfocused)
    |
    +---> For each enabled channel plugin:
            |
            +---> Check external_filter: is this type enabled? 
            +---> Call plugin.Send(notification)
            |
            +---> ntfy: POST to ntfy.sh topic
            +---> slack: POST to webhook
            +---> telegram: send via bot API
```

## Bidirectional Action Flow

```
Phone notification arrives:
    "Session #3 needs permission: Bash — npm run test"
    [Approve] [Deny]

User taps [Approve]

Channel plugin receives action via its listen mechanism

Plugin calls ActionCallback with the action

Action Router in daemon:
    1. Validate: is notification still pending?
    2. Idempotency check: already acted on? (skip if yes)
    3. Route: respond to the blocking HTTP hook for this session
    4. Update notification state: pending -> approved
    5. Notify all channels: "Permission approved (via ntfy)"
```

## Channel Lifecycle

```
Daemon starts
    |
    +-- Load config.yaml
    +-- For each enabled channel:
    |     +-- Call plugin.Init(config)
    |     +-- If error: log warning, disable channel, continue
    |     +-- If InteractiveChannel: start ListenForActions goroutine
    |     +-- If CommandableChannel: start ListenForCommands goroutine
    |
    +-- Running...
    |
    +-- Daemon shutdown
          +-- Cancel context (stops listener goroutines)
          +-- Call plugin.Close() on each channel
```

## Channel Status Command

```
$ claud channels

Channel          Status      Capabilities              Last Used
-------          ------      ------------              ---------
desktop          active      send, sound               2m ago
ntfy             active      send, actions, sound      5m ago
slack            disabled    --                        never
telegram         disabled    --                        never
webhook          disabled    --                        never

$ claud channels test ntfy

Sending test notification via ntfy...
  Topic: claud-kamrul
  Server: https://ntfy.sh
  Result: OK (200)

Check your phone for the notification.
```

## Channel Capability Matrix

| Channel   | Built-in? | Send | Actions | Commands | Notes                    |
|-----------|-----------|------|---------|----------|--------------------------|
| Desktop   | Yes       | Yes  | No      | No       | macOS/Linux native       |
| ntfy      | Plugin    | Yes  | Yes     | Limited  | Best for mobile push     |
| Slack     | Plugin    | Yes  | v0.2    | Yes      | Slash commands + threads |
| Telegram  | Plugin    | Yes  | Yes     | Yes      | Bot commands, keyboards  |
| Discord   | Plugin    | Yes  | No      | v0.2     | Webhook only             |
| Pushover  | Plugin    | Yes  | No      | No       | Paid                     |
| Webhook   | Plugin    | Yes  | Depends | Depends  | User-defined endpoint    |

## ntfy Action Mechanism

ntfy supports action buttons on notifications. Two approaches:

1. **Callback URL** — ntfy sends POST to a public URL when button is tapped. Requires exposing an endpoint.
2. **Response topic** — send notifications to `claud-kamrul`, listen for actions on `claud-kamrul-actions`. Plugin polls the actions topic. No public endpoint needed.

Option 2 is preferred for local-only setups.

## Future: External Plugin Loading (v0.3+)

For v0.1, all plugins are compiled in. Future versions could support:

1. **Subprocess plugins** — plugin is a separate binary, communicates via stdin/stdout JSON (like MCP protocol)
2. **WASM plugins** — sandboxed, portable
3. **Go plugin system** — load .so/.dylib at runtime

The interface stays the same — only the loading mechanism changes.
