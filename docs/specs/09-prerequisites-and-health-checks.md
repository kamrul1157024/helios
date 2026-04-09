# Prerequisites & Health Checks

## Startup Checks

When `claude-tmux` launches, it must verify all dependencies before proceeding. Fail fast with clear error messages.

### Check Order

```
claude-tmux
    |
    1. tmux installed?
    |     $ tmux -V
    |     FAIL: "tmux not found. Install: brew install tmux (macOS) or apt install tmux (Linux)"
    |     OK:   "tmux 3.4" -> parse version, check >= 3.0
    |
    2. tmux version compatible?
    |     FAIL: "tmux 2.9 found, requires >= 3.0. Upgrade: brew upgrade tmux"
    |     OK:   continue
    |
    3. claude CLI installed?
    |     $ claude --version
    |     FAIL: "claude CLI not found. Install: npm install -g @anthropic-ai/claude-code"
    |     OK:   "claude 1.x.x" -> store version
    |
    4. claude --resume supported?
    |     $ claude --help | grep resume
    |     FAIL: "claude CLI version does not support --resume. Upgrade: npm update -g @anthropic-ai/claude-code"
    |     OK:   continue
    |
    5. tmux server running?
    |     $ tmux list-sessions 2>&1
    |     NOT RUNNING: auto-start server (tmux starts on first session creation anyway)
    |     OK:   continue
    |
    6. tmux-resurrect plugin installed? (optional, warn only)
    |     Check: ~/.tmux/plugins/tmux-resurrect/ exists
    |     MISSING: "WARNING: tmux-resurrect not installed. Sessions will not survive terminal kill."
    |              "Install: see docs/research/10-tmux-resurrect-integration.md"
    |     OK:   continue
    |
    7. State directory exists?
    |     ~/.claude-tmux/
    |     MISSING: create it
    |     OK:   load state
    |
    All checks passed -> launch TUI or execute command
```

### Check Output (TUI mode)

If any required check fails, don't launch TUI. Print error and exit:

```
$ claude-tmux

claude-tmux: preflight checks

  [x] tmux .................. 3.4
  [x] claude CLI ........... 1.0.16
  [x] claude --resume ...... supported
  [ ] tmux-resurrect ....... not installed (optional)
  [x] state directory ...... ~/.claude-tmux/

WARNING: tmux-resurrect not installed. Sessions won't survive terminal kill.
         Run: claude-tmux setup-resurrect

Starting claude-tmux...
```

If required check fails:

```
$ claude-tmux

claude-tmux: preflight checks

  [!] tmux .................. NOT FOUND

ERROR: tmux is required but not installed.
       Install: brew install tmux (macOS) or apt install tmux (Linux)
```

### Check Output (CLI mode)

```
$ claude-tmux doctor

claude-tmux: health check

  [x] tmux .................. 3.4
  [x] claude CLI ........... 1.0.16
  [x] claude --resume ...... supported
  [x] tmux-resurrect ....... installed (v4.0.0)
  [x] tmux-continuum ....... installed (v3.1.0)
  [x] state directory ...... ~/.claude-tmux/
  [x] socket ............... ~/.claude-tmux/claude-tmux.sock (listening)
  [x] sessions ............. 4 tracked (2 active, 2 suspended)

All checks passed.
```

## `claude-tmux doctor` Command

A dedicated health check command that validates everything in detail:

```bash
claude-tmux doctor          # run all checks
claude-tmux doctor --fix    # attempt to auto-fix issues (create dirs, install plugins)
```

### --fix Behavior

| Issue                        | Auto-fix action                                    |
|------------------------------|----------------------------------------------------|
| State directory missing      | `mkdir -p ~/.claude-tmux`                          |
| tmux-resurrect not installed | Prompt to install via TPM or manual clone          |
| tmux-continuum not installed | Prompt to install via TPM                          |
| tmux.conf missing settings   | Append required lines (with user confirmation)     |
| Stale socket file            | Remove `~/.claude-tmux/claude-tmux.sock`           |
| Orphaned tmux sessions       | List and offer to clean up `claude-tmux-*` sessions|

## Required vs Optional Dependencies

| Dependency        | Required | Min Version | Purpose                          |
|-------------------|----------|-------------|----------------------------------|
| tmux              | YES      | 3.0         | Session backend                  |
| claude CLI        | YES      | --          | Claude Code sessions             |
| claude --resume   | YES      | --          | Suspend/resume feature           |
| tmux-resurrect    | NO       | --          | Survive terminal kill            |
| tmux-continuum    | NO       | --          | Auto-save/restore sessions       |
| TPM               | NO       | --          | Plugin manager for tmux          |
