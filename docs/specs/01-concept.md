# claude-tmux: Concept & Vision

## Problem Statement

When running multiple Claude Code sessions, there is no way to:

1. **See all sessions at a glance** — you lose track of what's running where
2. **Get notified when Claude needs you** — permission prompts, plan mode output, task completion, and errors go unnoticed when you're in another tab/window
3. **Manage session lifecycle** — no suspend/resume to free memory, no quick switching, no session organization
4. **Multitask across sessions** — no tabs, no split panes, no way to run research and implementation in parallel

## What is claude-tmux?

A TUI application that uses tmux as the backend to manage multiple Claude Code sessions with:

- **Session management**: create, list, switch, suspend, resume, kill
- **Tabs**: open multiple sessions as tabs, quick-switch between them
- **Notifications**: hooks-powered alerts when any session needs attention
- **Suspend/Resume**: save conversation state, free resources, pick back up later
- **Session metadata**: title, status, working directory, turn count, context usage

## Core Concept

Each Claude Code instance runs inside a tmux session managed by `claude-tmux`. The TUI provides a sidebar for session inventory, a tab bar for active views, and a notification system powered by Claude Code hooks.

```
claude-tmux (TUI)
    |
    +-- tmux session "claude-tmux-1" --> claude (refactor-auth)
    +-- tmux session "claude-tmux-2" --> claude (fix-api-bug)
    +-- tmux session "claude-tmux-3" --> claude --resume abc (suspended/resumed)
```

## Key Design Principles

1. **Keyboard-first, mouse-supported** — everything is accessible via keybindings, mouse is a convenience layer
2. **Zero-config notifications** — hooks are injected automatically when sessions are created
3. **tmux-native** — leverages tmux as the session backend, no reinventing the wheel
4. **Non-destructive suspend** — uses Claude Code's `--resume` flag to restore sessions
5. **Single binary** — Go binary, easy to install and distribute
