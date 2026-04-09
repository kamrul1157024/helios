# Per-Session Auto-Approve

## Overview

Auto-approve lets you skip permission prompts for a specific session. Useful when you trust what a session is doing and don't want to be interrupted — e.g., a test-runner session or a well-scoped refactoring task.

This is **per-session**, not global. Session #1 can be locked down while session #3 runs in full auto-approve.

## Auto-Approve Modes

| Mode            | Behavior                                              |
|-----------------|-------------------------------------------------------|
| `ask` (default) | All permissions go through notification system        |
| `auto`          | Auto-approve everything for this session              |
| `auto-read`     | Auto-approve read-only tools (Read, Glob, Grep, WebFetch, WebSearch), ask for writes |
| `auto-safe`     | Auto-approve safe tools (Read, Glob, Grep, Write, Edit), ask for Bash and destructive ops |
| `custom`        | Define per-tool rules                                 |

## Setting Auto-Approve

### From Sidebar (press `m` for mode)

```
+----------------------------------+
| Session #3: write-tests          |
|                                  |
| Auto-approve mode:               |
|                                  |
|   (*) ask       — ask everything |
|   ( ) auto      — approve all    |
|   ( ) auto-read — reads only     |
|   ( ) auto-safe — reads + writes |
|   ( ) custom    — per-tool rules |
|                                  |
| [Enter] select  [Esc] cancel     |
+----------------------------------+
```

### From Notification Page

When approving a permission, option to set auto-approve:

```
+-------------------------------------------+
| Approve permission?                       |
|                                           |
| Session: #3 write-tests                   |
| Tool: Bash                                |
| Command: npm run test --coverage          |
|                                           |
| [a] Approve once                          |
| [A] Approve + auto-approve this session   |
| [d] Deny                                  |
| [Esc] Cancel                              |
+-------------------------------------------+
```

### From CLI

```bash
claud auto-approve 3 auto        # full auto for session 3
claud auto-approve 3 auto-safe   # safe tools only
claud auto-approve 3 ask         # back to manual
```

### From Session Creation

```bash
claud new "run all tests" --auto-approve auto
claud new "explore codebase" --auto-approve auto-read
```

### From Remote Channels

```
/auto 3 auto-safe     (Telegram)
```

## Custom Per-Tool Rules

For `custom` mode, define which tools auto-approve and which require manual approval:

```
+-------------------------------------------+
| Custom Auto-Approve: #3 write-tests       |
|                                           |
| Tool            | Rule                    |
| ----------------+------------------------ |
| Read            | [x] auto-approve        |
| Glob            | [x] auto-approve        |
| Grep            | [x] auto-approve        |
| Edit            | [x] auto-approve        |
| Write           | [x] auto-approve        |
| Bash            | [ ] ask (manual)        |
| WebFetch        | [ ] ask (manual)        |
| Agent           | [x] auto-approve        |
|                                           |
| [Enter] save  [Esc] cancel               |
+-------------------------------------------+
```

## How It Works

When a permission request arrives at the daemon via Claude's HTTP hook, the daemon checks the session's auto-approve mode before creating a notification:

1. Look up the session's auto-approve mode
2. If `auto` — respond to hook immediately with approval, no notification
3. If `auto-read` / `auto-safe` — check if tool matches the approved set
4. If tool matches — auto-approve, no notification
5. If tool doesn't match or mode is `ask` — create pending notification, hold hook connection, wait for user decision
6. If `custom` — check the per-tool rules for this session

Auto-approved actions are logged for audit.

## Auto-Approve Indicator in Sidebar

Show the current mode next to the session:

```
|  SESSIONS                                |
|  --------------------------------------  |
|  ~/workspace/opal-app                    |
|  * #1 refactor-auth        [ask]         |
|    active . 3m                           |
|  * #2 fix-api              [auto]        |
|    active . 12m                          |
|  * #3 write-tests          [safe]        |
|    active . 1m                           |
```

Also shown in tab headers.

## Safety

### Warning for Full Auto Mode

Switching to `auto` shows a warning dialog explaining that ALL tool calls including destructive Bash commands will execute without review. Suggests `auto-safe` as a safer alternative.

### Auto-Approve Timeout (v0.2)

Auto-approve can have a time limit — after N minutes, revert to `ask` mode:

```bash
claud auto-approve 3 auto --timeout 30m
```

### Activity Log

All auto-approved actions are logged so you can review what happened:

```bash
claud log 3    # show auto-approved actions for session 3
```

```
Session #3 write-tests — auto-approve log (auto-safe mode)

  10:23:01  [auto] Read: src/utils/helpers.ts
  10:23:03  [auto] Grep: pattern "describe" in tests/
  10:23:05  [auto] Edit: tests/helpers.test.ts
  10:23:08  [auto] Write: tests/new-test.ts
  10:23:15  [ASK]  Bash: npm run test    <- manual approval
  10:23:22  [auto] Read: test-output.log
```
