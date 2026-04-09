# AI Provider Interface

## Overview

Helios is not tied to Claude. It manages AI coding sessions generically. The AI backend is a **provider plugin** — a native Go interface implementation compiled into the binary.

Each provider knows how to start, stop, resume, and interact with a specific AI CLI tool. The daemon doesn't know what "claude" or "aider" is. It only knows the provider interface.

## Architecture

```
Helios Daemon
    |
    +-- Session Manager
    |     |
    |     +-- uses provider interface for all AI-specific operations
    |
    +-- Provider Registry
          |
          +-- Claude Provider    → wraps `claude` CLI
          +-- Codex Provider     → wraps `codex` CLI
          +-- Aider Provider     → wraps `aider` CLI
          +-- Gemini Provider    → wraps `gemini` CLI
          +-- Custom Provider    → user-defined
```

## What a Provider Must Define

### Identity

- **Name** — unique identifier (e.g., "claude", "aider", "codex")
- **Display name** — human-readable (e.g., "Claude Code", "Aider", "OpenAI Codex")
- **Version** — provider plugin version
- **CLI command** — what binary it wraps (e.g., "claude", "aider")

### Lifecycle

- **Start command** — how to start a new session. Returns the full command + args to run inside a tmux window.
- **Resume command** — how to resume a suspended session given a resume ID. Returns command + args, or indicates "not supported."
- **Stop method** — how to gracefully stop a session. Could be sending a specific command (e.g., `/exit`), a signal (SIGTERM), or keystroke (Ctrl+C).
- **Health check** — how to detect if the AI process is still alive in the tmux pane.

### Input Detection

- **Is waiting for input?** — how to detect if the AI is at its prompt, ready for user input (vs. mid-response). Providers choose their mechanism:
  - **Hook-based** (claude) — hooks fire events that track state
  - **Pane scraping** (aider, codex) — read tmux pane, match prompt pattern
  - **Process state** (generic) — check if process is blocked on stdin

### Event Detection

Providers declare which events they can detect and how:

- **Completion** — AI finished responding. Hook-based or pane pattern match.
- **Error** — AI hit an error. Hook-based or pane pattern match.
- **Permission request** — AI needs user approval for a tool call. Hook-based or pane pattern match.
- **Session ID capture** — how to capture the session/resume ID for suspend/resume.

### Capabilities

Providers declare what they support. The daemon adapts its behavior based on this:

| Capability | Claude | Aider | Codex | Generic |
|------------|--------|-------|-------|---------|
| Resume sessions | Yes | No | No | No |
| Native hooks (HTTP) | Yes | No | No | No |
| Permission system | Yes | Yes (file edits) | No | No |
| Auto-approve | Yes (via hooks) | Partial (pane) | No | No |
| Session ID capture | Yes (via hooks) | No | No | No |
| Completion detection | Yes (hooks) | Yes (pane) | Yes (pane) | Yes (pane) |
| Error detection | Yes (hooks) | Yes (pane) | Yes (pane) | Yes (pane) |

## Detection Mechanisms

### Hook-Based (Claude)

Claude has a native HTTP hook system. The Claude provider:

1. On session create: injects hook config into `.claude/settings.local.json`
2. Hooks point to daemon's hook API (localhost:7655)
3. Daemon receives structured events — permission requests, stop, errors
4. On session kill/suspend: removes hook config

This is the most reliable mechanism — structured data, no parsing.

### Pane Scraping (Aider, Codex, others)

For providers without hooks, helios falls back to reading tmux pane content:

1. Daemon starts a scraper goroutine per session
2. Scraper runs `tmux capture-pane` every N seconds (configurable, default 2s)
3. Matches last few lines against provider-defined patterns

Pattern examples:

| Provider | Pattern | Detected as |
|----------|---------|-------------|
| Aider | `>>> ` at end of pane | Waiting for input |
| Aider | `Allow creation of` or `Allow edits to` | Permission prompt |
| Aider | Process exited | Done or error |
| Codex | `>` prompt at end | Waiting for input |
| Generic | Process not running | Done |

Pane scraping is less reliable than hooks (timing issues, false matches) but works for ANY terminal AI tool without requiring the tool to support plugins.

### Hybrid

A provider can use hooks for some events and pane scraping for others. The Claude provider uses hooks for everything but could fall back to pane scraping if hooks fail.

## Provider Configuration

### ~/.helios/config.yaml

```yaml
providers:
  claude:
    enabled: true
    command: "claude"
    # Claude-specific settings
    hooks_port: 7655

  aider:
    enabled: true
    command: "aider"
    default_args: ["--model", "claude-3.5-sonnet"]
    # Pane scraping patterns
    prompt_pattern: "^>>> $"
    permission_patterns:
      - "^Allow creation of"
      - "^Allow edits to"
    scrape_interval_ms: 2000

  codex:
    enabled: false
    command: "codex"
    prompt_pattern: "^> $"
    scrape_interval_ms: 2000

  custom:
    enabled: false
    command: "/path/to/my-ai-tool"
    prompt_pattern: "^\\$ $"
    scrape_interval_ms: 3000

# Default provider when --provider flag is omitted
default_provider: "claude"
```

## Provider Registration

Compiled into the binary via Go's init() mechanism. Each provider registers itself with the provider registry at import time. The registry holds all known providers. At startup, daemon loads config and initializes only the enabled providers.

## Session Creation with Provider

```
helios new "fix auth bug"                          → uses default provider (claude)
helios new "fix auth bug" --provider aider          → uses aider
helios new "fix auth bug" --provider codex          → uses codex
```

The session stores which provider it uses. Suspend/resume uses the same provider.

## Provider-Agnostic Session State

```
Session #3:
    title: "fix auth bug"
    provider: "claude"              ← which provider manages this session
    status: "active"
    resume_id: "claude-abc-123"     ← provider-specific, opaque to daemon
    tmux_window: "helios:s3-fix-auth"
    working_dir: "/home/user/app"
    capabilities:                   ← what this session supports
      resume: true
      hooks: true
      permissions: true
      auto_approve: true
```

## What the Daemon Delegates vs Owns

### Daemon owns (provider-agnostic)

- Session CRUD and state management
- tmux window lifecycle
- Notification fan-out to channels
- HTTP API + SSE
- Action routing (approve/deny)
- Auto-approve logic (mode checking)
- Message sending (tmux send-keys — universal)

### Provider handles (AI-specific)

- Start command construction
- Resume command construction
- Graceful stop method
- Hook installation/removal (if supported)
- Event detection strategy (hooks vs pane scraping)
- Prompt pattern definition
- Permission pattern definition
- Session/resume ID capture
- Capability declaration
