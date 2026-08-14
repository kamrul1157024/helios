# Spec: Hand desktop notifications to the desktop app

Supersedes `desktop-notification-service.md`, which put notification delivery in
a detached Go process shelling out to `terminal-notifier` / `notify-send`.

## Goal

Delete the Go notifier. The desktop app becomes the only thing that raises
desktop notifications, and it gains an approval HUD so a blocking request can be
answered where it lands instead of only opening the session that asked.

## Why

The Go notifier can announce but never act. It shells out to a binary most macOS
users do not have (`terminal-notifier`), so on a stock machine the whole feature
is a no-op with a line in `~/.helios/logs/desktop-notif.log`. It also cannot take
a notification back down: answer a permission on the phone and the banner sits
there offering to approve something already approved.

The desktop app has all of that already — dedupe, retract on
`notification_resolved`, tray badge, click-to-session — and it holds the SSE
stream and the device key needed to act. Two delivery paths for one event is one
too many, and the weaker one is the one running by default.

## Decisions

**No fallback.** With the notifier gone, a machine with no desktop app running
raises no desktop notifications. The phone and the TUI are unaffected. A daemon
on a headless box has no desktop to notify in the first place; the alternative —
keeping the Go path alive but gated on desktop-client presence — buys that case
at the cost of maintaining both paths forever.

**HUD window, not OS notification actions.** Electron's notification `actions`
and `hasReply` are macOS-only and want a signed app; `electron-builder.yml` sets
`identity: null`, so buttons would not render on the shipped DMG, and Linux gets
nothing either way. The app draws its own approval window instead: same
behaviour on every platform, no signing dependency, and room for a command or
diff preview that a banner has no space for.

Native notifications stay for the informational types (`claude.done`,
`claude.error`) — nothing to act on, so a banner is the right size.

## Removals (Go)

| Location | What goes |
|---|---|
| `internal/tui/desktop_notif.go` | whole file — SSE subscriber, `terminal-notifier`/`notify-send` exec, `notifLog` |
| `internal/tui/start.go` | `RunNotifier`, the `notifyBin` fields on both models |
| `internal/tui/view.go` | the "enable in System Settings → terminal-notifier" hints and the `brew install terminal-notifier` prerequisite row |
| `cmd/helios/main.go` | `handleNotify`, the `notify` case, `spawnNotifier`, `stopNotifier`, the call from `handleStop` |
| `internal/tui/notification_settings.go` | whole file — the `desktop.notify.*` screen, now writing keys nothing reads |
| `internal/tui/start.go` | `screenNotificationSettings` and its `notifSettings*` state, plus the settings-menu row that opens it |
| `README.md` | the notifications bullet and the `terminal-notifier` prerequisite line |

The `desktop.notify.*` rows can stay in the settings table; they are inert and
deleting rows costs a migration for nothing.

Kept: `GET /internal/events` and `/internal/settings` — the TUI uses both via
`internal/tui/client.go` for other settings.

### Migration

Users are running a detached `helios notify` from a previous install; nothing in
the new binary will ever kill it, so it would keep firing forever alongside the
app. On daemon start, if `~/.helios/notify.pid` exists, signal the pid and remove
the file. One-shot cleanup, no config flag.

## Additions (desktop app)

### Settings — device-local, like the phone

The `desktop.notify.*` keys in the daemon existed because the thing raising the
notification ran on the host. Once the app owns delivery, whether *this machine*
makes noise is a property of this machine, so the app follows the mobile model
rather than reading host settings: a three-host user should not get three
different answers to "do permissions alert me," and the phone already ignores
those keys entirely.

Stored next to `hosts.json` in `app.getPath('userData')`, same
read-file/write-file shape as `HostRegistry`.

Semantics match `mobile/lib/screens/notification_settings_screen.dart`: the
toggles control *alerting*, not existence. A silenced type still gets its tray
entry, its badge count, and its HUD card — it just does not make a sound.
Nothing that blocks an agent can be configured into invisibility.

- per-type toggles for all seven types, grouped "Action required"
  (`claude.permission`, `claude.question`, `claude.elicitation.form`,
  `claude.elicitation.url`, `claude.trust`) and informational
  (`claude.done`, `claude.error`)
- a global sound toggle, the desktop counterpart of the phone's sound/vibration
  pair
- reset to defaults; every type defaults on

The daemon-side keys and the TUI screen that writes them go away with the
notifier — see Removals.

### Routing by type

`Notifier.present()` in `desktop/src/main/notify.ts` gains a split:

- blocking types (`claude.permission`, `claude.question`, `claude.trust`,
  `claude.elicitation.*`) → HUD window, plus the existing tray entry
- everything else → native notification, as today

The tray badge and `seed()` reconciliation are unchanged and cover both.

### The HUD

Everything in this section that concerns window behaviour was measured in a
throwaway Electron app on macOS 15 / Electron 33 before being written down; see
"Spike findings" at the end.

A second `BrowserWindow`, created lazily on the first blocking notification:

- frameless, transparent, `hasShadow: false` (the cards draw their own, so the
  window shadow does not outline the transparent region), `alwaysOnTop` at
  `screen-saver` level, `skipTaskbar`, `visibleOnAllWorkspaces` with
  `visibleOnFullScreen`, `resizable: false`, `acceptFirstMouse: true` so the
  click that focuses the HUD is also the click that presses the button
- shown with `showInactive()` — an approval must not steal focus from an editor
  mid-keystroke
- anchored top-right of the work area of the display holding the cursor
  (`screen.getDisplayNearestPoint(screen.getCursorScreenPoint()).workArea`), so
  the menu bar is cleared without hardcoding its height
- height driven by content up to a cap, measured from the card stack's
  `getBoundingClientRect().bottom` plus the trailing gap — **not**
  `document.body.scrollHeight`, which under-reports by the collapsed bottom
  margin and clips the last card. `body { overflow: hidden }` too, or a
  scrollbar sliver draws over the transparent edge
- past the cap the stack scrolls inside the window rather than growing
- one window, a stack of cards: a second request while one is up appends rather
  than opening a second window
- hidden when the last card resolves, never destroyed: a hide/show cycle
  measured 1–3 ms, so the next request is instant and the renderer keeps its
  warm state

### Control parity with the phone

The HUD hosts the same cards the approvals panel does, not a reduced
approve/deny bar. The phone can answer anything from the notification list, and
an approval surface that can only say yes or no would send the user to the main
window for exactly the requests that are hardest to judge.

That means all six types, with their full controls:

| Type | Controls |
|---|---|
| `claude.permission` | approve, deny, edit tool input before approving, quick-rule checkbox from `permission_suggestions` (`apply_permission`) |
| `claude.question` | the option list, multi-select where the payload says so, free-text answer |
| `claude.trust` | trust, deny |
| `claude.elicitation.form` | the generated fields, submit, decline |
| `claude.elicitation.url` | open the URL, accept, decline |
| `claude.error` | whatever `ErrorCard` offers today |

This is reuse, not reimplementation: the cards in `approvals.tsx` already cover
every type the mobile `card_registry` does, including edit-before-approve and
quick rules.

Keyboard, when the HUD has focus, as accelerators over the same actions — not a
second control surface: `y` approve, `a` approve with the first suggested rule,
`n` deny, `o` open the session in the main window, `esc` dismiss the HUD without
answering (the request stays pending — dismissing a banner is not a decision).
Types with input — question, form — take `esc` and `o` only; the rest needs the
card.

Focus is the catch. A window shown with `showInactive()` never receives real
keystrokes, so the shortcuts only exist after the user clicks the HUD — which
makes them near-useless, since the click could have hit the button. Give the
HUD a `globalShortcut` (default `Cmd/Ctrl+Shift+A`, "answer") that focuses it
and hands the keyboard over; the spike showed `window.focus()` alone is not
always enough, and `app.focus({ steal: true })` before it is what actually
brings the window forward.

### Reuse, not a second implementation

The action bodies are type-specific and already correct in
`desktop/src/renderer/components/approvals.tsx`; the daemon rejects a
`{action:'approve'}` sent to a question. The card components move to a shared
module that both the panel and the HUD import, so there is one place that knows
what a `claude.elicitation.form` response looks like.

Build: a third renderer entry in `desktop/build.mjs` (`src/renderer/hud.tsx` →
`dist/renderer/hud.js`, with its own `hud.html`), same preload, same sandbox.

### IPC

| Channel | Direction | Payload |
|---|---|---|
| `hud:present` | main → HUD | `{ hostId, notification }` |
| `hud:retract` | main → HUD | `{ hostId, notificationId }` — answered elsewhere |
| `hud:resize` | HUD → main | content height, so the window tracks the stack |
| `hud:dismiss` | HUD → main | user pressed `esc`; hide the window |
| `hud:activate` | HUD → main | open the session in the main window |

Actions themselves go through the existing `api:call` allow-list
(`notificationAction`), so the HUD needs no new privileges.

## Testing

- Go: assert the `notify` command is gone and that daemon start reaps a stale
  `notify.pid` (write a pid for a live sleep, start, expect it signalled).
- Desktop: unit-test the type split (blocking → HUD, informational → native) and
  the sound decision, both as pure functions over a notification plus a prefs
  map, the way `Notifier.seed()` is already testable. Assert that a silenced
  type still produces a tray entry and a HUD card — silence must not become
  suppression.
- Manual: raise a permission from a real session with the app closed (expect
  nothing), with the app running (HUD, answer it, session unblocks), then answer
  the same one from the phone (HUD card disappears).

## Spike findings

A throwaway Electron app (macOS 15, Electron 33.4, the version in
`desktop/node_modules`) built the window described above with two stubbed
permission cards. What it settled:

| Question | Result |
|---|---|
| Does `showInactive()` steal focus? | No. The frontmost app was unchanged before and after, `isFocused()` false. The premise holds. |
| Always-on-top | `setAlwaysOnTop(true, 'screen-saver')` held; `visibleOnAllWorkspaces(true, { visibleOnFullScreen: true })` accepted. |
| Content-driven resize | Works, and the window tracked 171 px → 349 px as a second card appeared. Two bugs: `scrollHeight` misses the collapsed bottom margin, clipping the last card, and `body` scrolls unless told not to, drawing a scrollbar over the transparent edge. |
| Re-show cost | 0.9 ms and 2.6 ms across runs. Hiding beats destroying by enough that the window should just live for the session. |
| Can the app focus the HUD on demand? | Not with `window.focus()` alone — it returned `isFocused() === false`. `app.focus({ steal: true })` first, then `window.focus()`, worked. This is why the keyboard path needs a global shortcut rather than "press y when it appears". |
| Transparency and layout | Rounded cards over a transparent background, CSS shadow, no window chrome. Render is what the mockup promised. |

Also worth recording for whoever picks this up: the terminal here has no Screen
Recording permission, so `screencapture` returns wallpaper with every window
missing. `webContents.capturePage()` photographs the window from inside the
process and needs no permission — that is how the HUD was inspected, and it is
the only way an agent can check this UI without the user granting anything.

Still unmeasured: behaviour over a fullscreen app, a cursor on a secondary
display, and all of Linux.

## Out of scope

- Signing the desktop app, and the native notification actions that would unlock
- Windows support: the HUD is portable, but nothing else here is tested there
- Changing the notification model, the action API, or the hooks
