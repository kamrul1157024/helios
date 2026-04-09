# tmux-resurrect & continuum Integration

## The Problem

If the terminal is killed (crash, reboot, accidental close), all tmux sessions are destroyed. The Claude Code processes inside them die. Without intervention:

- Active sessions are lost
- Unsaved conversation context is gone
- The user has to manually resume each session from the claude session ID (if they remember it)

## Solution: tmux-resurrect + tmux-continuum

### tmux-resurrect

[tmux-resurrect](https://github.com/tmux-plugins/tmux-resurrect) saves and restores tmux sessions, windows, and panes — including the running programs inside them.

What it saves:
- All tmux sessions, windows, pane layouts
- Current working directory of each pane
- Running program in each pane (configurable)
- Pane contents / scrollback (optional)

What it does NOT save:
- The in-memory state of running programs (like Claude's conversation context)

### tmux-continuum

[tmux-continuum](https://github.com/tmux-plugins/tmux-continuum) auto-saves tmux-resurrect state every N minutes and auto-restores on tmux server start.

Together: tmux-resurrect saves the layout, tmux-continuum makes it automatic.

## How claude-tmux Uses This

### The Combined Strategy

```
Terminal kill / system reboot
    |
    v
tmux-resurrect restores:
    - tmux sessions (claude-tmux-1, claude-tmux-2, etc.)
    - pane layouts
    - working directories
    |
    v
BUT claude processes inside panes are dead
    |
    v
claude-tmux detects dead sessions on startup:
    1. Reads ~/.claude-tmux/sessions.json (still on disk)
    2. Checks each "active" session's tmux pane
    3. If pane exists but claude process is dead -> mark as "needs-restart"
    4. Auto-resume: run `claude --resume <session_id>` in each pane
    |
    v
All sessions restored with conversation context intact
```

### Startup Recovery Flow

```
claude-tmux starts
    |
    +-- Load sessions.json
    |
    +-- For each session with status "active":
    |     |
    |     +-- tmux session exists?
    |     |     YES -> check if claude process is running in pane
    |     |            |
    |     |            +-- Process alive? -> OK, nothing to do
    |     |            +-- Process dead?  -> run `claude --resume <session_id>`
    |     |
    |     +-- tmux session missing?
    |           |
    |           +-- Create new tmux session
    |           +-- Run `claude --resume <session_id>`
    |           +-- Re-inject hooks
    |
    +-- For each session with status "suspended":
    |     -> Nothing to do, already saved
    |
    +-- Launch TUI
```

### Session Recovery States

```
+-------------------+--------------------+--------------------------+
| sessions.json     | tmux state         | Action                   |
+-------------------+--------------------+--------------------------+
| active            | pane alive, claude | None (healthy)           |
|                   | running            |                          |
+-------------------+--------------------+--------------------------+
| active            | pane alive, claude | Resume claude in pane    |
|                   | dead               | (tmux-resurrect case)    |
+-------------------+--------------------+--------------------------+
| active            | tmux session gone  | Recreate tmux session +  |
|                   |                    | resume claude            |
+-------------------+--------------------+--------------------------+
| suspended         | (no tmux session)  | None (expected)          |
+-------------------+--------------------+--------------------------+
| dead              | (no tmux session)  | None (expected)          |
+-------------------+--------------------+--------------------------+
```

## Installation

### Option 1: TPM (tmux Plugin Manager) — Recommended

```bash
# 1. Install TPM if not already installed
git clone https://github.com/tmux-plugins/tpm ~/.tmux/plugins/tpm

# 2. Add to ~/.tmux.conf
cat >> ~/.tmux.conf << 'EOF'

# tmux plugin manager
set -g @plugin 'tmux-plugins/tpm'

# save/restore tmux sessions
set -g @plugin 'tmux-plugins/tmux-resurrect'
set -g @plugin 'tmux-plugins/tmux-continuum'

# resurrect settings
set -g @resurrect-capture-pane-contents 'on'
set -g @resurrect-processes 'claude'

# continuum settings — auto-save every 5 minutes, auto-restore on start
set -g @continuum-save-interval '5'
set -g @continuum-restore 'on'

# Initialize TPM (keep this line at the very bottom)
run '~/.tmux/plugins/tpm/tpm'
EOF

# 3. Reload tmux config
tmux source-file ~/.tmux.conf

# 4. Install plugins (inside tmux, press prefix + I)
# Or from CLI:
~/.tmux/plugins/tpm/bin/install_plugins
```

### Option 2: Manual Install (no TPM)

```bash
# resurrect
git clone https://github.com/tmux-plugins/tmux-resurrect ~/.tmux/plugins/tmux-resurrect

# continuum
git clone https://github.com/tmux-plugins/tmux-continuum ~/.tmux/plugins/tmux-continuum

# Add to ~/.tmux.conf
cat >> ~/.tmux.conf << 'EOF'

# resurrect
run-shell ~/.tmux/plugins/tmux-resurrect/resurrect.tmux
set -g @resurrect-capture-pane-contents 'on'
set -g @resurrect-processes 'claude'

# continuum
run-shell ~/.tmux/plugins/tmux-continuum/continuum.tmux
set -g @continuum-save-interval '5'
set -g @continuum-restore 'on'
EOF

tmux source-file ~/.tmux.conf
```

### Option 3: `claude-tmux setup-resurrect` (automated)

```bash
claude-tmux setup-resurrect
```

This command:
1. Checks if TPM is installed, installs if not
2. Adds plugin entries to ~/.tmux.conf (with user confirmation)
3. Configures resurrect to capture claude processes
4. Configures continuum for auto-save (5 min interval)
5. Installs plugins via TPM
6. Reloads tmux config

```
$ claude-tmux setup-resurrect

tmux-resurrect setup
--------------------

This will configure tmux to save and restore sessions automatically,
so your Claude sessions survive terminal kills and reboots.

Steps:
  1. Install TPM (tmux plugin manager)
  2. Add tmux-resurrect plugin
  3. Add tmux-continuum plugin (auto-save every 5 min)
  4. Configure to capture claude processes
  5. Reload tmux config

Changes will be appended to ~/.tmux.conf

Proceed? [Y/n] y

  [x] TPM installed at ~/.tmux/plugins/tpm
  [x] tmux-resurrect added to ~/.tmux.conf
  [x] tmux-continuum added to ~/.tmux.conf
  [x] Resurrect configured to capture 'claude' processes
  [x] Continuum auto-save interval: 5 minutes
  [x] Continuum auto-restore: enabled
  [x] Plugins installed
  [x] tmux config reloaded

Done! Your Claude sessions will now survive terminal kills.
Manual save: prefix + Ctrl-s
Manual restore: prefix + Ctrl-r
```

## Key tmux-resurrect Settings for claude-tmux

```bash
# Capture pane contents so we can show session preview
set -g @resurrect-capture-pane-contents 'on'

# Tell resurrect to restore claude processes
# This means when a pane had 'claude' running, resurrect will re-run it
set -g @resurrect-processes 'claude'

# BUT: we don't want resurrect to blindly re-run 'claude'
# We want it to run 'claude --resume <session_id>'
# Solution: use resurrect's process restoration hooks
# OR: let resurrect restore panes empty, and let claude-tmux handle resume
```

## The Resurrect vs claude-tmux Resume Problem

tmux-resurrect can restore processes, but it runs them with the original command. If Claude was started with `claude`, resurrect will re-run `claude` — starting a NEW session, not resuming the old one.

### Solution: Let claude-tmux Handle Resume

Configure resurrect to NOT auto-restore claude processes:

```bash
# Don't let resurrect restart claude — we'll do it ourselves
set -g @resurrect-processes 'false'
# OR more selectively, exclude claude:
# set -g @resurrect-processes '~claude'
```

Instead, resurrect restores the tmux panes (empty), and claude-tmux's startup recovery logic detects dead panes and runs `claude --resume <session_id>` with the correct session ID from sessions.json.

This is the cleanest approach:
1. **tmux-resurrect**: restores tmux sessions + pane layouts
2. **tmux-continuum**: auto-saves every 5 minutes
3. **claude-tmux**: detects empty/dead panes on startup, resumes claude with correct session IDs

### Recovery Timeline

```
T=0: Terminal killed / system crash
     - tmux server dies
     - All claude processes die
     - sessions.json on disk (intact)
     - tmux-resurrect last save on disk (intact, saved by continuum)

T=1: User opens new terminal, tmux server auto-starts
     - tmux-continuum triggers auto-restore
     - tmux-resurrect restores sessions + pane layouts
     - Panes are empty (claude not running)

T=2: User runs `claude-tmux`
     - Reads sessions.json: finds sessions marked "active"
     - Checks tmux panes: exist but empty
     - For each: runs `claude --resume <session_id>` in the pane
     - Re-injects hooks
     - All sessions restored with full conversation history

Total downtime: however long it takes user to reopen terminal
Data loss: zero (Claude's conversation state is server-side)
```

## Manual Save / Restore

Even without continuum, users can manually save/restore:

```
prefix + Ctrl-s    — save all sessions (tmux-resurrect)
prefix + Ctrl-r    — restore all sessions (tmux-resurrect)
```
