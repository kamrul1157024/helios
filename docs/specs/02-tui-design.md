# TUI Design

## Main Layout

The TUI has four regions: sidebar, tab bar, main pane, and status bar.

```
+--------------------+----------------------------------------------+
|  claude-tmux v0.1  |                              [?] Help [q] Quit|
+--------------------+----------------------------------------------+
|  SESSIONS          | +--#1 refactor-auth--+--#2 fix-api x--+--+--+ |
|  -----------------  | |                                          | |
|  ~/workspace/opal   | |                                          | |
|  * #1 refactor-auth | |                                          | |
|    active . 3m      | |    Active Claude Code Session #1          | |
|  * #2 fix-api    !  | |                                          | |
|    error . 1m ago   | |    (embedded tmux pane)                   | |
|                     | |                                          | |
|  ~/other-project    | |                                          | |
|  * #3 tests      L  | |                                          | |
|    permission . 2m  | |                                          | |
|  S #4 explore       | |                                          | |
|    suspended . 3h   | |                                          | |
|                     | +------------------------------------------+ |
|                     |  #1 refactor-auth | ~/opal-app | 47 turns    |
+--------------------+----------------------------------------------+
| ^a n:new  ^a s:suspend  ^a r:resume  ^a k:kill  ^a 1-9:tab  ^a l |
+-------------------------------------------------------------------+

Legend:  * active   S suspended   o dead
Badges: ! error    L permission  ~ idle   + done
```

## Sidebar

- **Grouped by working directory** — sessions from the same project are visually grouped
- **Status indicators**: `*` active, `S` suspended, `o` dead/exited
- **Notification badges** shown inline next to session title
- **Mouse**: click to select, scroll wheel to navigate
- **Keyboard**: `j/k` to navigate, `Enter` to open as tab
- **Resizable**: drag the border or `^a H/L` to resize
- **Search/filter**: press `/` to filter sessions by name

### Search Mode

```
|  / auth______________ |
|  -----------------     |
|  * #1 refactor-auth   |
|  * #7 auth-migration  |
|       (2 of 12)       |
```

## Tab Bar

- Tabs are **views** into sessions — you can have 5 active sessions but only 2 open as tabs
- **Close button**: `x` on each tab, or `^a w`
- **Switch**: click tab, `^a 1-9`, or `^a [/]` for prev/next
- **New tab**: `+` button at end of tab bar, or `^a n`
- **Badges**: notification badges appear on tabs too

```
+--#1 refactor-auth--+--#2 fix-api !--+--#3 tests L--+--+--+
```

## Main Pane

- Embedded tmux pane showing the active Claude Code session
- Full terminal emulation — everything Claude outputs is visible
- No chrome or wrapper — raw terminal output

## Status Bar

- Single row at the bottom
- Shows all keybinding hints (no duplication elsewhere)
- Shows notification counter: `^a !:notifications(3)`
- Shows current session metadata: name, directory, turn count

## Resource Indicators (v0.1 stretch)

Show context window usage per session in the sidebar:

```
|  * #1 refactor-auth   ==.. 47% ctx |
|  * #2 fix-api-bug     =... 12% ctx |
|  S #3 write-tests     --- suspended |
```

## Session Preview (v0.2)

Before opening a tab, show last few lines of conversation:

```
+--------------------+
|  PREVIEW #2        |
|  ----------------  |
|  > fix the 500     |
|    error on /api   |
|  claude: Found the |
|    issue in auth.. |
+--------------------+
```

## Split Panes (v0.2)

Side-by-side Claude sessions:

```
| +--#1 refactor-auth------+--#2 fix-api-----------+ |
| |                         |                        | |
| |  claude session 1       |  claude session 2      | |
| |                         |                        | |
| +-------------------------+------------------------+ |
```

`^a |` for vertical split, `^a -` for horizontal.

## Mouse Support

| Action              | Mouse              | Keyboard equivalent |
|---------------------|--------------------|---------------------|
| Switch tab          | click tab header   | `^a 1-9`, `^a [/]` |
| Close tab           | click `x` on tab   | `^a w`              |
| Select session      | click row          | `j/k` + `Enter`     |
| Resize sidebar      | drag border        | `^a H/L`            |
| Scroll session list | scroll wheel       | `j/k`               |
| Context menu        | right-click session| `s/r/k`             |
