# Remote Commands via Channels

## Overview

Channels are not just notification delivery — they are **bidirectional control planes**. From any channel (phone, Slack, Telegram), you can:

1. **Send a message to a running session** — type a prompt to Claude from your phone
2. **Create a new session** — start a new Claude task remotely
3. **Approve/deny permissions** — already designed in spec 11-13
4. **Query status** — list sessions, check what's running
5. **Suspend/resume** — manage session lifecycle

This turns claud into a remotely controllable Claude fleet manager. You can use Claude without any TUI at all — entirely from Telegram if you want.

## Command Types

| Command | Description | Needs session ID? |
|---------|-------------|-------------------|
| `send_message` | Send a prompt to a running Claude session | Yes |
| `new_session` | Create a new session with optional initial message | No |
| `list_sessions` | Get all sessions and their status | No |
| `get_status` | Get details of a specific session | Yes |
| `suspend` | Suspend a session | Yes |
| `resume` | Resume a suspended session | Yes |

## Command Syntax by Channel

### Telegram Bot

```
/list                              → list all sessions
/status 3                          → status of session #3
/new refactor auth ~/workspace/app → create new session
/send 3 fix the failing test       → send message to session #3
/suspend 2                         → suspend session #2
/resume 2                          → resume session #2
```

Bot responds inline:

```
User: /list

Bot:  Sessions:
      * #1 refactor-auth    active   47t  3m ago
      * #2 fix-api          active   12t  12m ago
      S #3 write-tests      suspend  89t  1h ago

User: /send 1 now add unit tests for the auth module

Bot:  OK Message sent to session #1 (refactor-auth)
```

### Slack

Slash commands or natural message parsing in a dedicated channel:

```
/claude list
/claude new "refactor auth" ~/workspace/app
/claude send 3 fix the failing test
/claude status 3
```

Or thread-based — each session gets a Slack thread. Reply in thread = send message to that session.

### ntfy

Primarily push-only (no rich input). But can publish JSON to a command topic for basic operations. Limited UX — ntfy is better for notifications + simple approve/deny.

### Generic Webhook

The daemon's HTTP API is the webhook. Any custom integration can POST commands to it directly. This enables building custom UIs, mobile apps, or browser extensions on top.

## Send Message to Session

The most interesting command. How does a remote message get typed into a running Claude session?

**Answer: `tmux send-keys`**

```
Channel receives: /send 3 fix the failing test
    |
    v
Channel plugin calls daemon HTTP API:
    POST /api/sessions/3/message
    {"message": "fix the failing test"}
    |
    v
Daemon Session Manager:
    1. Look up session #3
    2. Check: is session active?
    3. Check: is Claude waiting for input? (not mid-response)
    4. Execute: tmux send-keys -t claud-3 "fix the failing test" Enter
    |
    v
Claude receives the prompt and starts working
```

This works because Claude Code's prompt is just a terminal readline waiting for text. `tmux send-keys` is the universal input method.

## Detecting "Waiting for Input"

Before sending a message, the daemon must verify Claude is at its prompt (not mid-response). Injecting text while Claude is outputting would corrupt the session.

**Two signals combined:**

1. **Hook-based state tracking** — `Stop` hook fires when Claude finishes a response (mark session as "waiting"). `UserPromptSubmit` hook fires when user sends prompt (mark as "processing"). This is the primary signal.

2. **tmux pane capture** — as a fallback, capture the last line of the tmux pane and check if it matches Claude's input prompt pattern. Verify the hook-based signal.

If the session is busy, the command returns an error: "Session #3 is busy processing, try again later."

## Create Session Remotely

```
Channel: /new refactor auth ~/workspace/app
    |
    v
Daemon:
    1. Create tmux session
    2. Inject hooks config
    3. Launch claude in the tmux pane
    4. Optionally send initial message
    5. Return session ID to channel
```

With initial message:

```
Channel: /new "fix login bug" ~/workspace/app fix the login timeout in auth.py
    |
    v
Creates session #4, sends "fix the login timeout in auth.py" as the first prompt.
```

## Security Considerations

### Authentication

Anyone who knows your Telegram bot or has access to your Slack channel could send commands. Each channel plugin supports:

- **Allowed users list** — only accept commands from specific user IDs
- **Auth tokens** — for webhook-based channels

### Command Whitelist

Global config to restrict which commands are allowed remotely:

```yaml
remote_commands:
  enabled: true
  allowed_commands:
    - list_sessions
    - get_status
    - send_message
    - suspend
    # - new_session    # disabled remotely
    # - resume         # disabled remotely
```

### Confirmation Flow (optional safety net)

When `require_confirmation: true` in config, remote commands that modify state show a notification in ALL other clients before executing. The first client to approve/deny determines the outcome. Can be disabled for trusted channels.

## Channel Capability Matrix (Updated)

| Channel   | Send | Actions | Commands | Notes                           |
|-----------|------|---------|----------|---------------------------------|
| Desktop   | Yes  | No      | No       | OS-native, one-way              |
| ntfy      | Yes  | Yes     | Limited  | Actions via topics, no rich cmd |
| Slack     | Yes  | v0.2    | Yes      | Slash commands + threads        |
| Telegram  | Yes  | Yes     | Yes      | Bot commands, inline keyboards  |
| Discord   | Yes  | No      | v0.2     | Webhook only for now            |
| Webhook   | Yes  | Depends | Yes      | Via daemon HTTP API directly    |

## Full Remote Session Example (Telegram)

```
You're on your phone at lunch.

Notification:
  claud bot:
  L Session #3 (write-tests) needs permission
  Tool: Bash
  Command: npm run test --coverage
  [Approve] [Deny]

You tap [Approve].

  claud bot:
  OK Permission approved for session #3

A minute later:

  claud bot:
  OK Session #3 (write-tests) — Done (92 turns)

You reply:

  You: /send 3 now refactor the test helpers into a shared utils file

  claud bot:
  OK Message sent to session #3 (write-tests)
  Claude is working...

Five minutes later:

  claud bot:
  OK Session #3 (write-tests) — Done (98 turns)

You check status:

  You: /list

  claud bot:
  Sessions:
  * #1 refactor-auth    active   47t  1h ago
  S #2 fix-api          suspend  12t  2h ago
  * #3 write-tests      active   98t  just now

You create a new task:

  You: /new "hotfix prod" ~/workspace/app fix the null pointer in user_service.go line 42

  claud bot:
  OK Created session #5 (hotfix prod)
  Claude is working...
```

No TUI needed. Full Claude fleet management from your phone.
