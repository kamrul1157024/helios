# Claude Code Hooks Reference

Source: https://code.claude.com/docs/en/hooks

This document captures the Claude Code hooks API as researched for the claude-tmux notification system.

## Hook Events (Complete List)

| Event               | When it fires                     | Can block? |
|---------------------|-----------------------------------|-----------|
| `SessionStart`      | Session begins/resumes            | No        |
| `InstructionsLoaded` | CLAUDE.md or rules files loaded  | No        |
| `UserPromptSubmit`   | User submits prompt              | Yes       |
| `PreToolUse`         | Before tool executes             | Yes       |
| `PermissionRequest`  | Permission dialog appears        | Yes       |
| `PermissionDenied`   | Auto mode denies tool            | No        |
| `PostToolUse`        | Tool succeeds                    | No        |
| `PostToolUseFailure` | Tool fails                       | No        |
| `Notification`       | Notification sent                | No        |
| `SubagentStart`      | Subagent spawned                 | No        |
| `SubagentStop`       | Subagent finishes                | Yes       |
| `TaskCreated`        | Task created via TaskCreate      | Yes       |
| `TaskCompleted`      | Task marked completed            | Yes       |
| `Stop`               | Claude finishes responding       | Yes       |
| `StopFailure`        | Turn ends due to API error       | No        |
| `TeammateIdle`       | Agent team teammate going idle   | Yes       |
| `ConfigChange`       | Config file changes              | Yes       |
| `CwdChanged`         | Working directory changes        | No        |
| `FileChanged`        | Watched file changes             | No        |
| `PreCompact`         | Before context compaction        | No        |
| `PostCompact`        | After compaction completes       | No        |
| `Elicitation`        | MCP server requests user input   | Yes       |
| `ElicitationResult`  | User responds to MCP elicitation | Yes       |
| `WorktreeCreate`     | Worktree created                 | Yes       |
| `WorktreeRemove`     | Worktree removed                 | No        |
| `SessionEnd`         | Session terminates               | No        |

## Hook Configuration Locations

```
~/.claude/settings.json              -> All projects (local only)
.claude/settings.json                -> Single project (shareable)
.claude/settings.local.json          -> Single project (local only)  <-- we use this
Managed policy settings              -> Organization-wide
Plugin hooks/hooks.json              -> When plugin enabled
Skill/Agent frontmatter              -> While component active
```

## JSON Configuration Format

```json
{
  "hooks": {
    "EventName": [
      {
        "matcher": "regex_pattern_or_*",
        "hooks": [
          {
            "type": "command",
            "command": "path/to/script.sh",
            "timeout": 600,
            "statusMessage": "Custom message"
          }
        ]
      }
    ]
  }
}
```

## Hook Handler Types

### Command Hook (what we use)

```json
{
  "type": "command",
  "command": "claude-tmux notify --session 2 --type permission",
  "async": false,
  "shell": "bash"
}
```

### HTTP Hook

```json
{
  "type": "http",
  "url": "http://localhost:8080/hook",
  "headers": { "Authorization": "Bearer $TOKEN" },
  "allowedEnvVars": ["TOKEN"]
}
```

### Prompt Hook

```json
{
  "type": "prompt",
  "prompt": "Evaluate this: $ARGUMENTS",
  "model": "fast_model"
}
```

## Environment Variables Available to Hooks

```bash
CLAUDE_PROJECT_DIR       # Project root
CLAUDE_PLUGIN_ROOT       # Plugin installation directory
CLAUDE_PLUGIN_DATA       # Plugin persistent data directory
CLAUDE_ENV_FILE          # Write export statements to persist env vars
CLAUDE_CODE_REMOTE       # "true" in web environments
```

## Common Input Fields (stdin JSON)

```json
{
  "session_id": "abc123",
  "transcript_path": "/path/to/transcript.jsonl",
  "cwd": "/current/working/dir",
  "permission_mode": "default|plan|acceptEdits|auto|dontAsk|bypassPermissions",
  "hook_event_name": "EventName"
}
```

## Exit Codes

| Exit Code | Behavior                                                  |
|-----------|-----------------------------------------------------------|
| 0         | Success. Parse stdout for JSON output                     |
| 2         | Blocking error. stderr fed to Claude. Effect varies by event |
| Other     | Non-blocking error. Show notice, continue execution       |

## Notification Event Matcher Values

| Matcher               | Meaning                        |
|-----------------------|--------------------------------|
| `permission_prompt`   | Permission dialog appeared     |
| `idle_prompt`         | Claude idle, waiting for input |
| `auth_success`        | Authentication succeeded       |
| `elicitation_dialog`  | MCP elicitation dialog         |

## StopFailure Matcher Values

| Matcher                 | Meaning                    |
|-------------------------|----------------------------|
| `rate_limit`            | API rate limit hit         |
| `authentication_failed` | Auth failure               |
| `billing_error`         | Billing issue              |
| `invalid_request`       | Bad request                |
| `server_error`          | Server-side error          |
| `max_output_tokens`     | Output too long            |
| `unknown`               | Unknown error              |

## Events Relevant to claude-tmux

### Notification (matcher: permission_prompt | idle_prompt | elicitation_dialog)

Fires when Claude sends a notification to the user. This is the primary hook for detecting when Claude needs attention.

### Stop (no matcher)

Fires when Claude finishes responding. Useful for "task done" notifications.

### StopFailure (matcher: error type)

Fires when a turn ends due to an error. Useful for error notifications.

### PermissionRequest (matcher: tool name)

Fires when permission dialog appears. Input includes `tool_name`, `tool_input`, and `permission_suggestions`. Can be used to show what tool is requesting permission.

### SessionEnd (matcher: end reason)

Fires when session terminates. Useful for detecting unexpected session death.

### PreToolUse / PostToolUse

Could be used for activity indicators (showing what Claude is currently doing) but not needed for v0.1 notifications.
