# Notification Page

## Overview

A dedicated full-screen page (not just a slide-out panel) for managing all notifications across all sessions. This is where you triage, batch-approve, and configure per-session behavior.

Toggle with `^a !` — replaces the main pane entirely, like a separate view.

## Full-Screen Layout

```
+---------------------------------------------------------------------+
| claud                                          NOTIFICATIONS    [Esc]|
+---------------------------------------------------------------------+
| Filter: [all v]  [permission v]  [done v]  [error v]    / search    |
+---------------------------------------------------------------------+
|                                                                     |
| PENDING (3)                            [A] Approve All  [B] Batch   |
| ---------------------------------------------------------------    |
|                                                                     |
| [ ] L #3 write-tests                                     2m ago    |
|     Permission: Bash                                                |
|     Command: npm run test --coverage                                |
|     [a] approve  [d] deny  [v] view session                        |
|                                                                     |
| [ ] L #3 write-tests                                     2m ago    |
|     Permission: Write                                               |
|     File: src/utils/helpers.ts                                      |
|     [a] approve  [d] deny  [v] view session                        |
|                                                                     |
| [ ] L #2 fix-api                                          5m ago    |
|     Permission: Bash                                                |
|     Command: docker exec opal-mysql mysql -uroot ...                |
|     [a] approve  [d] deny  [v] view session                        |
|                                                                     |
| COMPLETED (12)                                                      |
| ---------------------------------------------------------------    |
|                                                                     |
|   + #1 refactor-auth                                     12m ago    |
|     Done — 47 turns                                                 |
|                                                                     |
|   + #5 run-tests                                         20m ago    |
|     Done — 8 turns                                                  |
|                                                                     |
|   ! #2 fix-api                                           30m ago    |
|     Error: rate_limit                                               |
|                                                                     |
| DISMISSED (5)                                          [C] Clear    |
| ---------------------------------------------------------------    |
|   ...                                                               |
|                                                                     |
+---------------------------------------------------------------------+
| j/k:nav  a:approve  d:deny  Space:select  A:approve-all  B:batch   |
+---------------------------------------------------------------------+
```

## Notification Categories

### Pending — Needs Action

Notifications that require user input. These are the critical ones.

| Type       | Icon | What's Waiting                    | Actions Available        |
|------------|------|-----------------------------------|--------------------------|
| Permission | L    | Tool needs approval               | approve, deny, view      |
| Idle       | ~    | Claude waiting for user input     | view (switch to session) |
| Input      | ?    | MCP elicitation dialog            | view (switch to session) |

### Completed — Informational

| Type       | Icon | What Happened                     | Actions Available        |
|------------|------|-----------------------------------|--------------------------|
| Done       | +    | Claude finished responding        | view, dismiss            |
| Error      | !    | Rate limit, API error, etc.       | view, dismiss            |

### Dismissed — Archive

Previously seen notifications, kept for reference. `[C] Clear` to purge.

## Batch Operations

### Select Multiple

Use `Space` to toggle selection checkbox on individual notifications. Then press `B` for batch action menu:

```
+---------------------------+
| Batch Action (2 selected) |
|                           |
| [a] Approve selected      |
| [d] Deny selected         |
| [x] Dismiss selected      |
+---------------------------+
```

### Approve All

Press `A` to approve ALL pending permission notifications at once. Shows confirmation dialog listing everything that will be approved.

### Session-Scoped Batch

Press `S` to approve/deny all pending notifications from a specific session.

## Filter Bar

Top of the page has filter chips. Toggle with number keys:

- `1` — toggle all
- `2` — toggle permission only
- `3` — toggle done only
- `4` — toggle error only
- `/` — search by session name, command, file path

## How Approve/Deny Works

The daemon receives permission requests from Claude via HTTP hooks. The HTTP connection is held open (blocking) until the user makes a decision from ANY client — TUI notification page, Telegram, Slack, etc.

When the user approves from the notification page:

```
User presses 'a' on a permission notification
    |
    v
TUI sends HTTP to daemon:
    POST /api/notifications/notif-001/approve
    |
    v
Daemon Action Router:
    1. Idempotency: not already resolved? OK
    2. Respond to the blocking Claude hook HTTP connection with approval
    3. Update notification state: pending -> approved
    4. Broadcast to all channels: "Permission approved"
    |
    v
Claude Code receives approval, executes tool
```

This works identically regardless of which client sends the approve — TUI, Telegram, Slack. They all hit the same daemon HTTP endpoint.
