# CLI Interface

## Commands

### TUI Mode (default)

```bash
claude-tmux                        # opens the full TUI
```

### Direct Commands (scriptable)

```bash
claude-tmux new "refactor auth"    # create session + attach
claude-tmux new "fix bug" -d /path # create in specific directory
claude-tmux ls                     # list all sessions
claude-tmux attach 2               # switch to session #2
claude-tmux suspend 1              # save state, free resources
claude-tmux resume 3               # restore suspended session
claude-tmux kill 4                 # terminate session
claude-tmux rename 2 "new title"   # rename a session
claude-tmux notify                 # internal: called by hooks
```

### Session List Output

```
$ claude-tmux ls

 ID  Title               Status       Turns   Last Active   Directory
  1  refactor-auth       * active        47   3m ago        ~/workspace/opal-app
  2  fix-api-bug         * active        12   12m ago       ~/workspace/opal-app
  3  write-tests         S suspended     89   1h ago        ~/workspace/other
  4  explore-codebase    S suspended    201   3h ago        ~/workspace/other
```

## Keybindings (TUI Mode)

### Navigation

| Key          | Action                          |
|--------------|---------------------------------|
| `j` / `k`    | Navigate session list up/down   |
| `Enter`      | Open selected session as tab    |
| `/`          | Filter/search sessions          |
| `Esc`        | Close search, close panel       |
| `Tab`        | Toggle focus: sidebar <-> pane  |

### Session Management (^a prefix)

| Key          | Action                          |
|--------------|---------------------------------|
| `^a n`       | New session (prompts for title)  |
| `^a s`       | Suspend current session          |
| `^a r`       | Resume selected suspended session|
| `^a k`       | Kill selected session            |
| `^a R`       | Rename selected session          |

### Tab Management (^a prefix)

| Key          | Action                          |
|--------------|---------------------------------|
| `^a 1-9`     | Switch to tab by number          |
| `^a [`       | Previous tab                     |
| `^a ]`       | Next tab                         |
| `^a w`       | Close current tab                |

### Notifications

| Key          | Action                          |
|--------------|---------------------------------|
| `^a !`       | Toggle notification panel        |

### Layout

| Key          | Action                          |
|--------------|---------------------------------|
| `^a H`       | Shrink sidebar                   |
| `^a L`       | Expand sidebar                   |
| `^a b`       | Toggle sidebar visibility        |

### Global

| Key          | Action                          |
|--------------|---------------------------------|
| `?`          | Show help                        |
| `q`          | Quit claude-tmux                 |

## Suspend / Resume Flow

### Suspend

```
claude-tmux suspend 1
    |
    1. Capture claude's session ID (from claude process / session file)
    2. Store { session_id, title, working_dir } in state DB
    3. Gracefully exit claude in the tmux pane
    4. Kill the tmux session (frees all memory)
    5. Remove hooks from settings.local.json
    6. Update status -> suspended
```

### Resume

```
claude-tmux resume 3
    |
    1. Look up stored session_id from state DB
    2. Create fresh tmux session
    3. Re-inject hooks into settings.local.json
    4. Run `claude --resume <session_id>` in the tmux pane
    5. Claude picks up conversation where it left off
    6. Update status -> active
    7. Open as tab in TUI
```
