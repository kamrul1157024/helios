# UI Improvements Roadmap

## v0.1 — Must Have

### Session Grouping by Project Directory

Group sessions by working directory in the sidebar instead of a flat list:

```
|  SESSIONS                        |
|  ------------------------------  |
|  ~/workspace/opal-app            |
|    * #1 refactor-auth            |
|    * #2 fix-api-bug              |
|                                  |
|  ~/workspace/other-project       |
|    S #3 write-tests              |
|    o #4 explore-code             |
```

### Search / Filter Sessions

When you have 20+ sessions, scrolling is painful. Press `/` to filter:

```
|  / auth_______________   |
|  ----------------------  |
|  * #1 refactor-auth      |
|  * #7 auth-migration     |
|       (2 of 12 sessions) |
```

### Richer Status Bar

Show current session metadata:

```
| #1 refactor-auth | ~/workspace/opal-app | 47 turns | 12m active | 47% ctx |
```

### Live Resource Indicators

Show context window usage per session:

```
|  * #1 refactor-auth   ==.. 47% ctx |
|  * #2 fix-api-bug     =... 12% ctx |
|  S #3 write-tests     --- suspended |
```

## v0.1 Stretch

### Session Preview on Select

Before opening a tab, show last few lines of conversation in a preview pane below the sidebar:

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

### Notification Badges on Background Sessions

When a background session finishes or errors, show badges:

```
|  * #2 fix-api-bug  ! 1  |   <- needs attention
|  * #5 run-tests    +    |   <- completed
```

## v0.2

### Split Panes

Side-by-side Claude sessions -- one doing research, one implementing:

```
| +--#1 refactor-auth------+--#2 fix-api-----------+ |
| |                         |                        | |
| |  claude session 1       |  claude session 2      | |
| |                         |                        | |
| +-------------------------+------------------------+ |
```

`^a |` for vertical split, `^a -` for horizontal.

### Pinned Sessions

Pin important sessions to the top of the sidebar:

```
|  PINNED                  |
|    P #1 refactor-auth    |
|  ----------------------  |
|  RECENT                  |
|    * #2 fix-api-bug      |
|    S #3 write-tests      |
```

### Tab Drag-to-Reorder

Drag tabs to reorder them. Keyboard: `^a <` and `^a >` to move current tab left/right.

## Impact vs Effort Matrix

| Feature              | Impact | Effort | Version |
|----------------------|--------|--------|---------|
| Search/filter        | High   | Low    | v0.1    |
| Richer status bar    | High   | Low    | v0.1    |
| Resource indicators  | High   | Medium | v0.1    |
| Session grouping     | Medium | Low    | v0.1    |
| Notification badges  | High   | Medium | v0.1s   |
| Preview pane         | Medium | Medium | v0.1s   |
| Split panes          | High   | High   | v0.2    |
| Pinned sessions      | Low    | Low    | v0.2    |
| Tab reorder          | Low    | Low    | v0.2    |
