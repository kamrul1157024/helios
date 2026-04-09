# Design Decisions

## Decided

### Language: Go

**Why**: bubbletea is the best TUI framework available. Go compiles to a single binary, fast startup, good concurrency model for managing multiple sessions + socket listener. Community has strong tmux tooling.

**Alternatives considered**: Rust (ratatui is great but bubbletea's component model is more ergonomic for this use case).

### TUI Framework: bubbletea + lipgloss + bubbles

- **bubbletea**: Elm-architecture TUI framework, composable models
- **lipgloss**: Styling (colors, borders, padding)
- **bubbles**: Pre-built components (text input, list, viewport)

### State Storage: JSON Files

**Why**: Simple, human-readable, debuggable. We expect < 100 sessions. No need for SQLite overhead.

**Location**: `~/.claude-tmux/sessions.json` and `~/.claude-tmux/notifications.json`

### tmux Integration: Shell Out

**Why**: tmux CLI is a stable, well-documented API. No Go library needed. Parsing tmux output is straightforward.

**Commands used**:
- `tmux new-session -d -s <name>` -- create detached session
- `tmux send-keys -t <name> "command" Enter` -- run command in session
- `tmux kill-session -t <name>` -- kill session
- `tmux list-sessions` -- list sessions
- `tmux capture-pane -t <name> -p` -- capture pane content (for preview)

### Session IDs: Auto-increment Integers

**Why**: Easy to type. `claude-tmux attach 3` is much faster than `claude-tmux attach abc123-def456`. UUIDs are overkill for a local tool.

### Keybinding Prefix: ^a

**Why**: tmux convention. Users already have muscle memory for `^a` or `^b` as prefix keys. We use `^a` to avoid conflicting with default tmux `^b`.

**Note**: Should be configurable in case user already remapped tmux to `^a`.

### Hook Config Location: .claude/settings.local.json

**Why**: Per-project, local-only (not committed to git), exactly what we need. We can merge our hooks with any existing hooks in the file.

### IPC: Unix Domain Socket

**Why**: When `claude-tmux notify` is called by a hook, it needs to tell the running TUI process about the notification. A unix socket at `~/.claude-tmux/claude-tmux.sock` provides low-latency, reliable IPC. Fallback to writing to notifications.json if socket unavailable.

## Open Questions

### How to Capture Claude Session ID

When we suspend a session, we need to save Claude's session ID so we can `--resume` it later. Options:

1. **Parse Claude's output** -- look for session ID in the terminal output
2. **Read Claude's state files** -- check `~/.claude/` for session state
3. **Use SessionStart hook** -- capture session_id from the hook input JSON (it includes `session_id`)

Option 3 is cleanest -- we already have hooks infrastructure.

### Sidebar Visibility Default

Should the sidebar be visible by default or hidden? Leaning visible since session management is the core feature, but when you're deep in a session you might want full-width terminal.

Decision: **Visible by default**, `^a b` to toggle.

### Notification Sound

Should we play a sound on notification? Leaning toward terminal bell only (`\a`), which respects user's terminal sound settings.

Decision: **Terminal bell by default**, configurable.

### Config File

Should claude-tmux have its own config file? Leaning yes for:
- Custom keybinding prefix
- Notification preferences (OS notification on/off, bell on/off)
- Default sidebar width
- Auto-suspend after N minutes of inactivity

Location: `~/.claude-tmux/config.yaml`

### Multiple Projects with Same Settings.local.json

If two sessions are in the same project directory, they share `.claude/settings.local.json`. We need to handle this:

- Option A: Use unique hook commands per session, both coexist in the file
- Option B: Use a session-specific env var that the hook script reads
- Option C: Use the global `~/.claude/settings.json` instead

Leaning toward **Option A** -- each session adds its own matcher entries with its session ID embedded in the command.
