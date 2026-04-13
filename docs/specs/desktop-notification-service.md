# Spec: Desktop Notification Service

## Goal

Unified desktop notification service that replaces the current `osascript` approach and removes the unused VAPID/web push system. All notification delivery goes through a single Go service that calls native OS binaries — `terminal-notifier` on macOS and `notify-send` on Linux. Desktop notifications support click-to-session (opens the tmux pane for the notification's session), sound control, and per-type alert settings stored on the host. Settings are exposed via a TUI settings screen.

No Node.js dependency. No child process. Pure Go calling native binaries.

---

## Architecture

```
                      ┌─────────────────────────────────┐
                      │     NotificationService         │
                      │     (internal/notify/)          │
                      │                                 │
                      │  .Send(id, type, title, body,   │
                      │        sessionID, paneID)       │
                      │  .Available() bool              │
                      │  .Status() ServiceStatus        │
                      │                                 │
                      │  Reads settings from DB:        │
                      │    desktop.notify.enabled        │
                      │    desktop.notify.sound          │
                      │    desktop.notify.alert.*        │
                      │                                 │
                      │  Detects parent terminal on     │
                      │  startup for -sender flag       │
                      │                                 │
                      └──────────┬──────────────────────┘
                                 │
                        runtime.GOOS switch
                       ┌─────────┴─────────┐
                       │                   │
                  darwin              linux
                       │                   │
              terminal-notifier      notify-send
              (exec.Command)         (exec.Command)
```

---

## VAPID / Web Push Removal

The VAPID/web push system is unused — the mobile app uses SSE, not web push. Remove it entirely.

### Files to delete

| File | Reason |
|---|---|
| `internal/push/vapid.go` | Unused VAPID key generation/loading |
| `internal/push/sender.go` | Unused web push sender |
| `internal/server/push.go` | Unused push subscribe/unsubscribe/vapid API handlers |

### Files to modify (removal)

| File | Change |
|---|---|
| `internal/daemon/daemon.go` | Remove `push.LoadOrGenerateVAPID`, `push.NewSender`, remove `pusher` param from `NewShared` |
| `internal/server/server.go` | Remove `Pusher *push.Sender` from `Shared`, remove `pusher` param from `NewShared`, remove push API routes |
| `internal/server/hooks.go` | Remove `Pusher.SendToAll` from `Push` callback |
| `internal/server/pane_watcher.go` | Remove `shared.Pusher.SendToAll` call |
| `internal/store/store.go` | Keep `push_subscriptions` table for now (no migration, just unused) |
| `go.mod` / `go.sum` | Remove `github.com/SherClockHolmes/webpush-go` dependency |

---

## macOS: terminal-notifier

### Install

```
brew install terminal-notifier
```

### Flags used

```
terminal-notifier \
  -title "Helios" \
  -subtitle "Permission Request" \
  -message "Claude needs permission: Bash" \
  -sound default \
  -sender com.apple.Terminal \
  -group notif-abc123 \
  -execute "helios attach %42"
```

| Flag | Purpose |
|---|---|
| `-title` | "Helios" |
| `-subtitle` | Notification type label (e.g. "Permission Request") |
| `-message` | Detail text |
| `-sound NAME` | `default` for system sound, or specific name (`Ping`, `Glass`, etc.), omitted when sound disabled |
| `-sender ID` | Bundle ID of the parent terminal — notification shows under that app's name + icon |
| `-group ID` | Notification ID — replaces previous notification with same group |
| `-execute COMMAND` | Shell command to run on click — `helios attach <paneID>` |
| `-ignoreDnD` | Optional, for blocking notification types |

### Parent terminal detection

On daemon startup, detect which terminal app launched `helios start`:

```go
func detectTerminalBundleID() string {
    ppid := os.Getppid()
    // Walk up the process tree to find the terminal app
    // macOS: use ps -p <ppid> -o comm= to get process name
    // Map process name → bundle ID
    //   Terminal     → com.apple.Terminal
    //   iTerm2       → com.googlecode.iterm2
    //   WezTerm      → com.github.wez.wezterm
    //   Alacritty    → org.alacritty
    //   kitty        → net.kovidgoyal.kitty
    // Fallback: com.apple.Terminal
}
```

The detected bundle ID is stored on the service and passed as `-sender` on every notification. This makes the notification show under and focus the user's actual terminal.

### Click-to-session

`-execute "helios attach <paneID>"` runs when the user clicks the notification. The `helios attach` command (already exists via `tmux.Client.Attach`) does:

1. `tmux select-window -t <paneID>`
2. `tmux select-pane -t <paneID>`
3. `tmux attach-session -t helios` (if not already attached)

The `-sender` flag also brings the terminal to the foreground, so the user lands directly in the right tmux pane.

---

## Linux: notify-send

### Install

Preinstalled on most desktop environments (GNOME, KDE, XFCE).

### Command

```
notify-send "Helios — Permission Request" "Claude needs permission: Bash" \
  --urgency=critical \
  --app-name=Helios
```

| Flag | Purpose |
|---|---|
| `--urgency` | `critical` for blocking types, `normal` for informational |
| `--app-name` | "Helios" |
| `--icon` | Optional, path to helios icon |
| `--expire-time` | Auto-dismiss milliseconds (0 = persistent) |

### Click-to-session (Linux)

`notify-send` supports `--action` on GNOME 42+:

```
notify-send "Helios" "Permission Request" --action="open=Open Session"
```

When clicked, prints the action name to stdout. However this requires the command to block waiting for the action — not practical from a fire-and-forget `exec.Command`.

**Fallback:** On Linux, notification click doesn't navigate to the session. The notification is informational only. Users use the TUI or mobile app to navigate. This is acceptable since Linux desktop notification support varies widely.

---

## Go Service: internal/notify/service.go

### ServiceStatus

```go
type ServiceStatus struct {
    Available    bool   // binary found (terminal-notifier or notify-send)
    Binary       string // path to the binary
    Platform     string // "darwin" or "linux"
    InstallHint  string // e.g. "brew install terminal-notifier"
}
```

### Service

```go
type Service struct {
    db             *store.Store
    tmux           *tmux.Client
    binary         string // resolved path to terminal-notifier or notify-send
    platform       string
    senderBundleID string // macOS only: parent terminal bundle ID
    available      bool
}
```

### Methods

```go
// New creates a new notification service.
// Detects platform, resolves binary, detects parent terminal.
func New(db *store.Store, tmux *tmux.Client) *Service

// Available returns true if the notification binary is found.
func (s *Service) Available() bool

// Status returns the current readiness status.
func (s *Service) Status() ServiceStatus

// CheckStatus is a static check (no service instance needed).
// Used by the TUI during initial status check.
func CheckStatus() ServiceStatus

// Send sends a desktop notification if enabled and the type is alert-enabled.
// Checks settings from DB before sending.
func (s *Service) Send(id, notifType, title, body, sessionID, paneID string)
```

No Start/Stop needed — no child process to manage. Each `Send` is a fire-and-forget `exec.Command`.

### Settings keys (stored in DB via settings table)

```
desktop.notify.enabled              bool  (default: true)
desktop.notify.sound                bool  (default: true)
desktop.notify.alert.permission     bool  (default: true)
desktop.notify.alert.question       bool  (default: true)
desktop.notify.alert.elicitation    bool  (default: true)
desktop.notify.alert.done           bool  (default: true)
desktop.notify.alert.error          bool  (default: true)
```

### Send logic

```go
func (s *Service) Send(id, notifType, title, body, sessionID, paneID string) {
    if !s.available {
        return
    }

    // 1. Check desktop.notify.enabled
    if !s.isEnabled() {
        return
    }

    // 2. Check desktop.notify.alert.<type>
    if !s.isAlertEnabled(notifType) {
        return
    }

    // 3. Determine sound
    sound := s.isSoundEnabled()

    // 4. Dispatch to platform
    switch s.platform {
    case "darwin":
        s.sendDarwin(id, notifType, title, body, sound, paneID)
    case "linux":
        s.sendLinux(notifType, title, body)
    }
}

func (s *Service) sendDarwin(id, notifType, title, body string, sound bool, paneID string) {
    args := []string{
        "-title", "Helios",
        "-subtitle", notifTypeLabel(notifType),
        "-message", body,
        "-group", id,
        "-sender", s.senderBundleID,
    }
    if sound {
        args = append(args, "-sound", "default")
    }
    if paneID != "" {
        // helios attach selects the pane and attaches to the tmux session
        exe, _ := os.Executable()
        args = append(args, "-execute", fmt.Sprintf("%s attach %s", exe, paneID))
    }
    exec.Command(s.binary, args...).Run()
}

func (s *Service) sendLinux(notifType, title, body string) {
    urgency := "normal"
    if isBlockingType(notifType) {
        urgency = "critical"
    }
    exec.Command(s.binary,
        fmt.Sprintf("Helios — %s", notifTypeLabel(notifType)),
        body,
        "--urgency="+urgency,
        "--app-name=Helios",
    ).Run()
}
```

### Type labels

```go
func notifTypeLabel(notifType string) string {
    switch notifType {
    case "claude.permission":
        return "Permission Request"
    case "claude.question":
        return "Question"
    case "claude.elicitation.form":
        return "Input Requested"
    case "claude.elicitation.url":
        return "Authentication Required"
    case "claude.done":
        return "Session Completed"
    case "claude.error":
        return "Session Error"
    default:
        return "Notification"
    }
}

func isBlockingType(notifType string) bool {
    return notifType == "claude.permission" ||
        notifType == "claude.question" ||
        strings.HasPrefix(notifType, "claude.elicitation")
}
```

---

## Daemon Integration (internal/daemon/daemon.go)

### Startup

```go
// Replace VAPID/pusher initialization with:
notifService := notify.New(db, shared.Tmux)
shared.DesktopNotifier = notifService

if notifService.Available() {
    log.Printf("desktop notifications: %s", notifService.Status().Binary)
} else {
    log.Printf("desktop notifications: unavailable (%s)", notifService.Status().InstallHint)
}
```

No Start/Stop lifecycle — service is stateless.

### Hook context wiring (internal/server/hooks.go)

Replace the current `Push` callback:

```go
// BEFORE
Push: func(notifType, id, title, body string) {
    go sendDesktopNotification(body)
    if s.shared.Pusher != nil {
        go s.shared.Pusher.SendToAll(push.PushPayload{...})
    }
},

// AFTER
Push: func(notifType, id, title, body, sessionID, paneID string) {
    if s.shared.DesktopNotifier != nil {
        go s.shared.DesktopNotifier.Send(id, notifType, title, body, sessionID, paneID)
    }
},
```

`sendDesktopNotification` function is deleted entirely.

### Push signature change

`HookContext.Push` gains `sessionID` and `paneID` parameters:

```go
// BEFORE
Push func(notifType, id, title, body string)

// AFTER
Push func(notifType, id, title, body, sessionID, paneID string)
```

All `ctx.Push(...)` call sites in hooks.go updated to pass session and pane info.

---

## TUI: Start Command Integration

### Files

- `internal/tui/start.go` — model, screens enum, Update logic, key handling
- `internal/tui/view.go` — all View rendering (main dashboard at `viewMain()` line 351)

### Status check (start.go)

Add to `statusCheckDone` struct:

```go
type statusCheckDone struct {
    // ... existing fields ...
    desktopNotif notify.ServiceStatus
}
```

In `checkStatus()` function:

```go
result.desktopNotif = notify.CheckStatus()
```

### Main screen display (view.go viewMain)

After the tunnel status line (line 378) and before the devices section (line 382), add:

```go
if m.desktopNotif.Available {
    b.WriteString(check(fmt.Sprintf("Desktop notifications (%s)", filepath.Base(m.desktopNotif.Binary))))
} else {
    b.WriteString(cross(fmt.Sprintf("Desktop notifications — %s", m.desktopNotif.InstallHint)))
}
```

### Help bar (view.go line 484)

Update from:
```go
b.WriteString(helpStyle.Render("  t change tunnel  q quit"))
```
To:
```go
b.WriteString(helpStyle.Render("  t change tunnel  n notifications  q quit"))
```

### New screen enum (start.go)

Add to the `screen` const block:

```go
screenNotificationSettings  // desktop notification alert settings
```

### Key binding (start.go handleKey)

In the `screenMain` case, add `n`:

```go
case "n":
    if m.screen == screenMain {
        m.screen = screenNotificationSettings
        return m, loadNotificationSettings(m.client)
    }
```

### Screen routing (view.go View)

Add case in `View()`:

```go
case screenNotificationSettings:
    return m.viewNotificationSettings()
```

---

## TUI: Notification Settings Screen

### New file: internal/tui/notification_settings.go

Contains the `viewNotificationSettings()` method on `StartModel` and related Update handling.

### State on StartModel (start.go)

```go
// Notification settings
notifSettingsCursor  int
notifSettingsValues  map[string]bool  // key → enabled
```

### Screen layout

```
  helios

  ─── Desktop Notifications ─────────────────────

    Desktop notifications      [ON ]

    Sound                      [ON ]

  ─── Alert types ───────────────────────────────

    Permission requests        [ON ]
    Questions                  [ON ]
    Elicitation                [ON ]
    Session completed          [ON ]
    Session error              [ON ]

  ─────────────────────────────────────────────────

    r reset defaults   q back
```

### Navigation

- `j`/`k` or Up/Down to move cursor between the 7 toggles
- Enter or Space to toggle the focused item
- `r` to reset all to defaults
- `q` or Esc to go back to main screen

### Toggle list order

```go
var notifSettingsKeys = []struct {
    key   string
    label string
}{
    {"desktop.notify.enabled", "Desktop notifications"},
    {"desktop.notify.sound", "Sound"},
    {"desktop.notify.alert.permission", "Permission requests"},
    {"desktop.notify.alert.question", "Questions"},
    {"desktop.notify.alert.elicitation", "Elicitation"},
    {"desktop.notify.alert.done", "Session completed"},
    {"desktop.notify.alert.error", "Session error"},
}
```

### Behavior

- On screen entry: reads all 7 settings from DB via internal API
- Each toggle immediately writes to DB via internal API `PUT /internal/settings`
- If binary is not available, show status + install hint at the top; toggles still functional (configure for when it becomes available)
- Visual: cursor row highlighted with `selectedStyle`, active toggles in `checkStyle`, inactive in `crossStyle`

---

## Shared State Changes

### server.Shared

```go
type Shared struct {
    DB              *store.Store
    Mgr             *notifications.Manager
    SSE             *SSEBroadcaster
    Tmux            *tmux.Client
    PendingPanes    *PendingPaneMap
    Reporter        *reporter.Reporter
    DesktopNotifier *notify.Service       // new
    // Pusher removed
}
```

---

## Files to Create

| File | Description |
|---|---|
| `internal/notify/service.go` | Service: Send, settings, platform dispatch, terminal detection |
| `internal/tui/notification_settings.go` | TUI settings screen (bubbletea model) |

## Files to Delete

| File | Reason |
|---|---|
| `internal/push/vapid.go` | Unused VAPID key management |
| `internal/push/sender.go` | Unused web push sender |
| `internal/server/push.go` | Unused push API handlers |

## Files to Modify

| File | Change |
|---|---|
| `internal/daemon/daemon.go` | Remove VAPID/pusher, create notify service |
| `internal/server/server.go` | Remove `Pusher` from `Shared`, add `DesktopNotifier`, remove push routes, remove `pusher` from `NewShared` |
| `internal/server/hooks.go` | Delete `sendDesktopNotification`, replace `Push` callback with notify service, update signature |
| `internal/provider/registry.go` | Update `Push` signature on `HookContext` to include sessionID + paneID |
| `internal/provider/claude/hooks.go` | Update all `ctx.Push(...)` calls to pass sessionID + paneID |
| `internal/server/pane_watcher.go` | Replace `sendDesktopNotification` + `Pusher.SendToAll` with notify service |
| `internal/tui/start.go` | Add `screenNotificationSettings`, `n` keybinding, `desktopNotif` to status check + model |
| `internal/tui/view.go` | Add desktop notification status in `viewMain`, add `viewNotificationSettings` routing, update help bar |
| `go.mod` / `go.sum` | Remove `github.com/SherClockHolmes/webpush-go` |

---

## Out of Scope

- Windows support
- Mobile notification settings (already implemented, stays on-device)
- Per-session mute
- Notification grouping/batching
- Sound picker (specific sound name selection — future enhancement)
- Linux click-to-session (notify-send action support varies by DE)
